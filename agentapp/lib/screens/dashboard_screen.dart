import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/health_provider.dart';
import '../theme/agent_status_theme.dart';

List<Map<String, String>> getDefaultModels(String provider) {
  final defaultModels = {
    'claude': [
      {'id': 'claude-sonnet-4-6', 'name': 'Sonnet 4.6'},
      {'id': 'claude-opus-4-6', 'name': 'Opus 4.6'},
      {'id': 'claude-haiku-4-5-20251001', 'name': 'Haiku 4.5'},
    ],
    'claude-bedrock': [
      {'id': 'anthropic.claude-3-5-sonnet-20241022-v2:0', 'name': 'Claude 3.5 Sonnet (Bedrock)'},
      {'id': 'anthropic.claude-3-5-haiku-20241022-v1:0', 'name': 'Claude 3.5 Haiku (Bedrock)'},
    ],
    'claude-vertex': [
      {'id': 'claude-3-5-sonnet@20241022', 'name': 'Claude 3.5 Sonnet (Vertex)'},
      {'id': 'claude-3-5-haiku@20241022', 'name': 'Claude 3.5 Haiku (Vertex)'},
    ],
  };

  return defaultModels[provider] ?? defaultModels['claude']!;
}

class SessionCandidate {
  final int? pid;
  final String provider;
  final String workDir;
  final String? sessionId;
  final String? terminal;
  final String attachMode;
  final bool isReadOnly;
  final String readOnlyReason;
  final String? projectName;

  const SessionCandidate({
    required this.pid,
    required this.provider,
    required this.workDir,
    this.sessionId,
    this.terminal,
    this.attachMode = '',
    this.isReadOnly = false,
    this.readOnlyReason = '',
    this.projectName,
  });

  factory SessionCandidate.fromJson(Map<String, dynamic> json) =>
      SessionCandidate(
        pid: (json['pid'] as num?)?.toInt(),
        provider: (json['provider'] as String? ?? 'unknown').toLowerCase(),
        workDir: json['workDir'] as String? ?? '',
        sessionId:
            json['sessionId'] as String? ??
            json['session'] as String? ??
            _sessionIdFromPath(json['sessionFile'] as String?),
        terminal: json['terminal'] as String?,
        attachMode: json['attachMode'] as String? ?? '',
        isReadOnly: json['readOnly'] as bool? ?? false,
        readOnlyReason: json['readOnlyReason'] as String? ?? '',
        projectName: json['projectName'] as String?,
      );

  static String? _sessionIdFromPath(String? path) {
    if (path == null || path.isEmpty) return null;
    final normalized = path.replaceAll('\\', '/');
    final name = normalized.split('/').last;
    if (name.endsWith('.jsonl')) return name.substring(0, name.length - 6);
    if (name.endsWith('.json')) return name.substring(0, name.length - 5);
    return null;
  }
}

bool isLiveSessionCandidate(SessionCandidate s) => (s.pid ?? 0) > 0;

bool isWritableAttachSession(SessionCandidate s) {
  return isLiveSessionCandidate(s) && s.attachMode == 'tmux' && !s.isReadOnly;
}

int sessionCandidateSortPriority(SessionCandidate s) {
  if (isWritableAttachSession(s)) return 0;
  if (isLiveSessionCandidate(s) && !s.isReadOnly) return 1;
  if (isLiveSessionCandidate(s)) return 2;
  return 3;
}

SessionCandidate? pickPreferredAutoAttachCandidate(
  Iterable<SessionCandidate> sessions,
) {
  final writable = sessions.where(isWritableAttachSession).toList();
  if (writable.isEmpty) return null;
  writable.sort((a, b) => (b.pid ?? 0).compareTo(a.pid ?? 0));
  return writable.first;
}

List<SessionCandidate> parseSessionCandidates(dynamic result) {
  final rawAttachable = result is List
      ? result
      : (result is Map<String, dynamic>
            ? ((result['attachable'] as List?) ??
                  (result['processes'] as List?) ??
                  const [])
            : const []);

  final rawClaude = result is Map<String, dynamic>
      ? (result['claudeFiles'] as List?) ?? const []
      : const [];

  final rawOpencode = result is Map<String, dynamic>
      ? (result['opencodeFiles'] as List?) ?? const []
      : const [];

  final attachable = rawAttachable.whereType<Map>().map(
    (e) => SessionCandidate.fromJson(Map<String, dynamic>.from(e)),
  );

  final claudeFiles = rawClaude.whereType<Map>().map((e) {
    final m = Map<String, dynamic>.from(e);
    return SessionCandidate(
      pid: null,
      provider: 'claude',
      workDir: m['workDir'] as String? ?? '',
      sessionId: m['id'] as String?,
      terminal: null,
      attachMode: '',
      isReadOnly: false,
      readOnlyReason: '',
    );
  });

  // Include OpenCode file sessions - these can be resumed even without a running process
  final opencodeFiles = rawOpencode.whereType<Map>().map((e) {
    final m = Map<String, dynamic>.from(e);
    final sessionId = m['id'] as String? ?? '';
    return SessionCandidate(
      pid: null,
      provider: 'opencode',
      workDir: '',
      sessionId: sessionId,
      terminal: null,
      attachMode: '',
      isReadOnly: false,
      readOnlyReason: '',
      projectName: sessionId.length > 6 ? sessionId.substring(sessionId.length - 6) : sessionId,
    );
  });

  return [...attachable, ...claudeFiles, ...opencodeFiles];
}

String managedVisibilityKey(AgentModel agent) {
  final provider = agent.provider.toLowerCase();
  final sessionId = (agent.sessionId ?? '').trim().toLowerCase();
  if (sessionId.isNotEmpty) {
    return '$provider|session:$sessionId';
  }
  return '$provider|agent:${agent.id.toLowerCase()}';
}

class OpencodeFileCandidate {
  final String id;
  final String name;

  const OpencodeFileCandidate({required this.id, required this.name});

  factory OpencodeFileCandidate.fromJson(Map<String, dynamic> json) =>
      OpencodeFileCandidate(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? (json['id'] as String? ?? ''),
      );
}

List<OpencodeFileCandidate> parseOpencodeFiles(dynamic result) {
  final rawList = result is List
      ? result
      : (result is Map<String, dynamic>
            ? (result['opencodeFiles'] as List?) ?? const []
            : const []);

  return rawList
      .whereType<Map>()
      .map((e) => OpencodeFileCandidate.fromJson(Map<String, dynamic>.from(e)))
      .where((e) => e.id.isNotEmpty)
      .toList();
}

class DashboardScreen extends ConsumerStatefulWidget {
  const DashboardScreen({super.key});

  @override
  ConsumerState<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends ConsumerState<DashboardScreen> {
  Timer? _refreshTimer;
  bool _wsConnected = true;

  @override
  void initState() {
    super.initState();
    _setupEventListener();
    _startAutoRefresh();
    // Listen for connection state changes
    final notifier = ref.read(connectionProvider.notifier);
    notifier.onStateChanged.listen((state) {
      if (!mounted) return;
      setState(() {
        _wsConnected = state == WsConnectionState.connected;
      });
      // Reconnect successful — refresh all data immediately
      if (state == WsConnectionState.connected) {
        _refreshAllNodes();
        // Re-subscribe to events on the new connection
        final client = ref.read(connectionProvider);
        if (client != null) {
          client.onEvent((event) {
            ref.read(nodesProvider.notifier).handleEvent(event);
            ref.read(conversationProvider.notifier).handleEvent(event);
          });
          // Register auto-refresh for newly discovered agents
          ref.read(nodesProvider.notifier).onAgentsRefresh = (nodeId) async {
            try {
              final ar = await client.call('agent.list', {'nodeId': nodeId});
              final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
              ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
            } catch (_) {}
          };
        }
      }
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    super.dispose();
  }

  void _startAutoRefresh() {
    // Refresh every 10 seconds (was 3s, increased for slower networks)
    _refreshTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _refreshAllNodes();
    });
    // Initial refresh
    Future.delayed(const Duration(milliseconds: 100), _refreshAllNodes);
  }

  Future<void> _refreshAllNodes() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('node.list', {});
      final nodes = (result is List
          ? result
          : (result['nodes'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadNodes(nodes);

      for (final n in nodes) {
        final nodeId = (n as Map<String, dynamic>)['id'] as String;
        await _refreshNodeAgents(nodeId);
      }
    } catch (e) {
      debugPrint('Auto refresh error: $e');
    }
  }

  void _setupEventListener() {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    client.onEvent((event) {
      if (event.method == 'node.status_changed') {
        final params = event.params as Map<String, dynamic>;
        final nodeId = params['nodeId'] as String;
        final status = params['status'] as String;

        // When a node becomes connected, refresh its agents
        if (status == 'connected') {
          _refreshNodeAgents(nodeId);
        }
      }
    });
  }

  Future<void> _refreshNodeAgents(String nodeId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('agent.list', {'nodeId': nodeId});
      final agents = (result is List
          ? result
          : (result['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
    } catch (e) {
      debugPrint('Failed to refresh agents for node $nodeId: $e');
    }
  }

  Future<void> _discoverNodes(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => const AlertDialog(
        title: Text('发现节点'),
        content: SizedBox(
          height: 100,
          child: Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                CircularProgressIndicator(),
                SizedBox(height: 16),
                Text('正在扫描 SSH 配置...'),
              ],
            ),
          ),
        ),
      ),
    );

    try {
      final result = await client.call(
        'node.discover',
        {},
        timeout: const Duration(seconds: 15),
      );
      if (!context.mounted) return;
      Navigator.pop(context);

      final scanned = (result['scanned'] as num?)?.toInt() ?? 0;
      final found = (result['found'] as List?) ?? [];

      if (found.isEmpty) {
        if (!context.mounted) return;
        showDialog(
          context: context,
          builder: (ctx) => AlertDialog(
            title: const Text('发现节点'),
            content: Text('扫描了 $scanned 个 SSH 主机，未发现运行 agentd 的节点。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('确定'),
              ),
            ],
          ),
        );
        return;
      }

      // Show discovered nodes
      if (!context.mounted) return;
      final added = await showDialog<List<String>>(
        context: context,
        builder: (ctx) => StatefulBuilder(
          builder: (ctx, setState) {
            final selected = <String>{};
            for (final node in found) {
              selected.add(node['id'] as String);
            }

            return AlertDialog(
              title: Text('发现 ${found.length} 个节点'),
              content: SizedBox(
                width: double.maxFinite,
                child: ListView.builder(
                  shrinkWrap: true,
                  itemCount: found.length,
                  itemBuilder: (_, i) {
                    final node = found[i] as Map<String, dynamic>;
                    final id = node['id'] as String;
                    return CheckboxListTile(
                      title: Text(node['name'] as String),
                      subtitle: Text(node['host'] as String),
                      value: selected.contains(id),
                      onChanged: (v) {
                        setState(() {
                          if (v == true) {
                            selected.add(id);
                          } else {
                            selected.remove(id);
                          }
                        });
                      },
                    );
                  },
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx),
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(ctx, selected.toList()),
                  child: const Text('添加选中'),
                ),
              ],
            );
          },
        ),
      );

      // Add selected nodes
      if (added != null && added.isNotEmpty && context.mounted) {
        for (final id in added) {
          final node = found.firstWhere((n) => n['id'] == id) as Map<String, dynamic>;
          try {
            await client.call('node.add', {
              'id': node['id'],
              'name': node['name'],
              'host': node['host'],
              'sshAlias': node['sshAlias'],
              'sshPort': node['sshPort'],
              'agentdPort': 7373,
              'token': client.token,
            });
          } catch (e) {
            debugPrint('Failed to add node ${node['name']}: $e');
          }
        }
        // Refresh node list
        _refreshAllNodes();
      }
    } catch (e) {
      if (!context.mounted) return;
      Navigator.pop(context);
      showDialog(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('错误'),
          content: Text('扫描失败: $e'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('确定'),
            ),
          ],
        ),
      );
    }
  }

  Future<void> _restartGateway(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重启 Gateway'),
        content: const Text('确定要重启 Gateway 吗？WebSocket 连接将会断开并在重启完成后自动恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('gateway.restart', {});
      if (!mounted) return;
      messenger.showSnackBar(
        const SnackBar(content: Text('Gateway 正在重启…')),
      );
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('重启 Gateway 失败: $e')),
      );
    }
  }

  Future<void> _showAddNodeDialog(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final nameCtrl = TextEditingController();
    final hostCtrl = TextEditingController();
    final aliasCtrl = TextEditingController();
    final tokenCtrl = TextEditingController();
    final dirCtrl = TextEditingController(text: r'$HOME/bin');
    bool deployNow = true;

    final client = ref.read(connectionProvider);
    if (client == null) return;
    // Default token to the current connection token for convenience
    tokenCtrl.text = client.token;

    final added = await showDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          title: const Text('添加远程节点'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称',
                    hintText: 'ws',
                  ),
                ),
                TextField(
                  controller: hostCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Host',
                    hintText: '192.168.1.10',
                  ),
                ),
                TextField(
                  controller: aliasCtrl,
                  decoration: const InputDecoration(
                    labelText: 'SSH 别名 (可选)',
                    hintText: 'ssh config 中的别名',
                  ),
                ),
                TextField(
                  controller: tokenCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Token',
                    hintText: 'agentd 认证 token',
                  ),
                ),
                TextField(
                  controller: dirCtrl,
                  decoration: const InputDecoration(
                    labelText: '远程目录',
                    hintText: r'$HOME/bin',
                  ),
                ),
                const SizedBox(height: 8),
                CheckboxListTile(
                  title: const Text('立即部署 agentd'),
                  value: deployNow,
                  onChanged: (v) => setState(() => deployNow = v ?? true),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );

    if (added != true) return;
    final name = nameCtrl.text.trim();
    final host = hostCtrl.text.trim();
    if (host.isEmpty) return;

    try {
      final addResult = await client.call('node.add', {
        'name': name.isEmpty ? host : name,
        'host': host,
        'sshAlias': aliasCtrl.text.trim(),
        'token': tokenCtrl.text.trim(),
      });
      final nodeId = (addResult is Map<String, dynamic>
          ? addResult['nodeId'] as String?
          : null);
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('节点已添加: ${name.isEmpty ? host : name}')),
      );
      if (deployNow && nodeId != null) {
        await client.call('node.deploy', {
          'nodeId': nodeId,
          'remoteDir': dirCtrl.text.trim(),
        });
        if (!mounted) return;
        messenger.showSnackBar(
          const SnackBar(content: Text('部署已启动')),
        );
      }
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('添加/部署失败: $e')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final nodes = nodeState.nodeList;

    return Scaffold(
      appBar: AppBar(
        title: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('仪表盘'),
            if (!_wsConnected) ...[
              const SizedBox(width: 8),
              SizedBox(
                width: 12,
                height: 12,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ],
          ],
        ),
        actions: [
          // Health status indicator
          const _HealthIndicator(),
          IconButton(
            icon: const Icon(Icons.search),
            tooltip: '发现节点',
            onPressed: () => _discoverNodes(context, ref),
          ),
          IconButton(
            icon: const Icon(Icons.add_circle_outline),
            tooltip: '添加节点',
            onPressed: () => _showAddNodeDialog(ref),
          ),
          IconButton(
            icon: const Icon(Icons.restart_alt),
            tooltip: '重启网关',
            onPressed: _wsConnected ? () => _restartGateway(ref) : null,
          ),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: nodes.isEmpty
                ? const Center(
                    child: Text('暂无节点', style: TextStyle(color: Colors.grey)),
                  )
                : ListView.builder(
                    itemCount: nodes.length,
                    padding: const EdgeInsets.all(12),
                    itemBuilder: (_, i) => NodeCard(node: nodes[i]),
                  ),
          ),
        ],
      ),
    );
  }
}

class NodeCard extends ConsumerStatefulWidget {
  final NodeModel node;
  const NodeCard({super.key, required this.node});

  @override
  ConsumerState<NodeCard> createState() => _NodeCardState();
}

class _NodeCardState extends ConsumerState<NodeCard> {
  Color get _statusColor {
    switch (widget.node.status) {
      case NodeStatus.connected:
        return Colors.green;
      case NodeStatus.connecting:
      case NodeStatus.deploying:
        return Colors.orange;
      case NodeStatus.error:
        return Colors.red;
      case NodeStatus.disconnected:
        return Colors.grey;
    }
  }


  String get _statusLabel {
    switch (widget.node.status) {
      case NodeStatus.connected:
        return '已连接';
      case NodeStatus.connecting:
        return '连接中…';
      case NodeStatus.deploying:
        return '部署中…';
      case NodeStatus.error:
        return '错误';
      case NodeStatus.disconnected:
        return '未连接';
    }
  }

  @override
  Widget build(BuildContext context) {
    final agents = ref.watch(nodesProvider).agentsFor(widget.node.id);
    final nodeDisplay = widget.node.isLocal ? widget.node.name : widget.node.name;
    final isRemote = !widget.node.isLocal;
    final nodeReady = widget.node.status == NodeStatus.connected;

    // Choose icon based on local/remote status
    final nodeIcon = widget.node.isLocal ? Icons.computer : Icons.cloud;

    int statusPriority(AgentStatus s) {
      switch (s) {
        case AgentStatus.working:
          return 0;
        case AgentStatus.starting:
          return 1;
        case AgentStatus.idle:
          return 2;
        case AgentStatus.stopped:
          return 3;
        case AgentStatus.crashed:
          return 4;
      }
    }

    // Only show active agents. Stopped/crashed agents are hidden from the main list
    // and should be managed through the session manager instead.
    final bySignature = <String, AgentModel>{};
    for (final a in agents) {
      final isActive = a.status == AgentStatus.working ||
          a.status == AgentStatus.starting ||
          a.status == AgentStatus.idle;
      if (!isActive) continue;
      final key = managedVisibilityKey(a);
      final existing = bySignature[key];
      if (existing == null ||
          statusPriority(a.status) < statusPriority(existing.status)) {
        bySignature[key] = a;
      }
    }

    final visibleAgents = bySignature.values.toList()
      ..sort((a, b) {
        final pa = statusPriority(a.status);
        final pb = statusPriority(b.status);
        if (pa != pb) return pa.compareTo(pb);
        return a.name.toLowerCase().compareTo(b.name.toLowerCase());
      });

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ListTile(
            leading: Icon(nodeIcon, color: _statusColor),
            title: Text(
              nodeDisplay,
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
            subtitle: Text('${widget.node.location.displayLocation}  ·  $_statusLabel'),
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                IconButton(
                  icon: const Icon(Icons.edit, size: 18),
                  tooltip: '重命名节点',
                  onPressed: () => _renameNode(context, ref),
                ),
                Icon(Icons.circle, color: _statusColor, size: 12),
              ],
            ),
          ),
          if (visibleAgents.isNotEmpty) const Divider(height: 1),
          ...visibleAgents.map(
            (a) => AgentRow(agent: a, nodeId: widget.node.id),
          ),
          if (visibleAgents.isEmpty)
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('暂无活跃会话', style: TextStyle(color: Colors.grey)),
            ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                if (isRemote &&
                    (widget.node.status == NodeStatus.disconnected ||
                        widget.node.status == NodeStatus.error)) ...[
                  TextButton.icon(
                    onPressed: () => _connectNode(context, ref),
                    icon: const Icon(Icons.link, size: 18),
                    label: const Text('连接'),
                  ),
                  TextButton.icon(
                    onPressed: () => _deployNode(ref),
                    icon: const Icon(Icons.cloud_upload, size: 18),
                    label: const Text('部署'),
                  ),
                ],
                if (isRemote && widget.node.status == NodeStatus.connecting)
                  TextButton.icon(
                    onPressed: null,
                    icon: const Icon(Icons.sync, size: 18),
                    label: const Text('连接中…'),
                  ),
                if (isRemote && widget.node.status == NodeStatus.connected)
                  TextButton.icon(
                    onPressed: () => _restartNode(context, ref),
                    icon: const Icon(Icons.restart_alt, size: 18),
                    label: const Text('重启节点'),
                  ),
                if (isRemote && widget.node.status == NodeStatus.deploying)
                  TextButton.icon(
                    onPressed: null,
                    icon: const Icon(Icons.sync, size: 18),
                    label: const Text('重启中…'),
                  ),
                TextButton.icon(
                  onPressed: nodeReady
                      ? () => _showCreateAgentDialog(context, ref)
                      : null,
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('新建 Agent'),
                ),
                TextButton.icon(
                  onPressed: nodeReady
                      ? () => _showSessionManager(context, ref)
                      : null,
                  icon: const Icon(Icons.manage_search, size: 18),
                  label: const Text('管理会话'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _connectNode(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      await client.call('node.connect', {'nodeId': widget.node.id});
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('正在连接 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('连接节点失败: $e')),
      );
    }
  }

  Future<void> _restartNode(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      await client.call('node.restart', {'nodeId': widget.node.id});
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('正在重启 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('重启节点失败: $e')),
      );
    }
  }

  Future<void> _deployNode(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final dirCtrl = TextEditingController(text: r'$HOME/bin');
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('部署 ${widget.node.name}'),
        content: TextField(
          controller: dirCtrl,
          decoration: const InputDecoration(
            labelText: '远程目录',
            hintText: r'$HOME/bin',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('部署'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await client.call('node.deploy', {
        'nodeId': widget.node.id,
        'remoteDir': dirCtrl.text.trim(),
      });
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('正在部署 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('部署失败: $e')),
      );
    }
  }

  Future<void> _refreshAgents(WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final result = await client.call('agent.list', {'nodeId': widget.node.id});
    final agents = (result is List
        ? result
        : (result['agents'] as List?) ?? []);
    ref.read(nodesProvider.notifier).loadAgents(widget.node.id, agents);
  }

  Future<void> _renameNode(BuildContext context, WidgetRef ref) async {
    final controller = TextEditingController(text: widget.node.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名节点'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '输入新名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (newName != null && newName.isNotEmpty) {
      final client = ref.read(connectionProvider);
      if (client == null) return;
      try {
        await client.call('node.rename', {
          'nodeId': widget.node.id,
          'name': newName,
        });
        ref.read(nodesProvider.notifier).renameNode(widget.node.id, newName);
      } catch (e) {
        debugPrint('rename node error: $e');
      }
    }
  }

  void _showCreateAgentDialog(BuildContext context, WidgetRef ref) {
    final cwdCtrl = TextEditingController();
    final nameCtrl = TextEditingController();
    final sessionCtrl = TextEditingController();
    final modelCtrl = TextEditingController();
    String provider = 'claude';
    final defaultModels = getDefaultModels(provider);
    if (defaultModels.isNotEmpty) {
      modelCtrl.text = defaultModels.first['id'] ?? '';
    }

    // Fetch home directory from agentd
    Future<void> fetchHomeDir() async {
      final client = ref.read(connectionProvider);
      if (client == null) return;
      try {
        final result = await client.call('system.info', {'nodeId': widget.node.id});
        if (result is Map<String, dynamic>) {
          final homeDir = result['homeDir'] as String?;
          if (homeDir != null && homeDir.isNotEmpty) {
            cwdCtrl.text = homeDir;
          }
        }
      } catch (e) {
        debugPrint('Failed to fetch home dir: $e');
      }
    }

    // Fetch home directory when dialog opens
    fetchHomeDir();

    showDialog(
      context: context,
      builder: (_) => StatefulBuilder(
        builder: (context, setState) => AlertDialog(
          title: Text(
            '在 ${(widget.node.host.isNotEmpty && widget.node.host != 'localhost' && widget.node.host != '127.0.0.1') ? widget.node.host : widget.node.name} 上新建 Agent',
          ),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称',
                    hintText: 'claude-1',
                  ),
                ),
                TextField(
                  controller: cwdCtrl,
                  decoration: const InputDecoration(
                    labelText: '工作目录',
                    hintText: '/home/user/proj',
                  ),
                ),
                const SizedBox(height: 8),
                SegmentedButton<String>(
                  segments: const [
                    ButtonSegment(value: 'claude', label: Text('Claude')),
                    ButtonSegment(value: 'opencode', label: Text('OpenCode')),
                  ],
                  selected: {provider},
                  onSelectionChanged: (s) => setState(() {
                    provider = s.first;
                    final models = getDefaultModels(provider);
                    modelCtrl.text = models.first['id'] ?? '';
                  }),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: sessionCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Session ID (可选)',
                    hintText: 'ses_xxx / uuid',
                    helperText: '留空创建新 session',
                  ),
                ),
                if (provider == 'claude') ...[
                  const SizedBox(height: 8),
                  TextField(
                    controller: modelCtrl,
                    decoration: InputDecoration(
                      labelText: 'Model (可选)',
                      hintText: defaultModels.first['name'] ?? defaultModels.first['id'] ?? '',
                    ),
                  ),
                ],
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                Navigator.pop(context);
                final client = ref.read(connectionProvider);
                if (client == null) return;

                final params = <String, dynamic>{
                  'nodeId': widget.node.id,
                  'name': nameCtrl.text.trim(),
                  'workDir': cwdCtrl.text.trim(),
                  'provider': provider,
                };
                final sessionId = sessionCtrl.text.trim();
                final model = modelCtrl.text.trim();
                if (sessionId.isNotEmpty) params['sessionId'] = sessionId;
                if (provider == 'claude' && model.isNotEmpty) {
                  params['model'] = model;
                }

                try {
                  await client.call('session.create', params);
                  await _refreshAgents(ref);
                } catch (e) {
                  debugPrint('Create session error: $e');
                }
              },
              child: const Text('创建'),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _showSessionManager(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    List<AgentModel> managedAgents = [];
    List<SessionCandidate> sessions = [];
    List<OpencodeFileCandidate> opencodeFiles = [];
    bool autoAttachDone = false;

    int managedPriority(AgentModel a) {
      switch (a.status) {
        case AgentStatus.working:
          return 0;
        case AgentStatus.starting:
          return 1;
        case AgentStatus.idle:
          return 2;
        case AgentStatus.stopped:
          return 3;
        case AgentStatus.crashed:
          return 4;
      }
    }

    List<AgentModel> normalizeManaged(List<AgentModel> input) {
      final byName = <String, AgentModel>{};
      for (final a in input) {
        // Filter logic must match NodeCard.build():
        // - Always show working/starting/idle agents
        // - For stopped agents: only show if hasHistory AND has sessionId (resumable)
        // - crashed agents are hidden entirely
        final hasResumeCapability = a.sessionId != null && a.sessionId!.isNotEmpty;
        final isActive = a.status == AgentStatus.working ||
            a.status == AgentStatus.starting ||
            a.status == AgentStatus.idle;
        final isResumableStopped = a.status == AgentStatus.stopped &&
            a.hasHistory && hasResumeCapability;
        if (!isActive && !isResumableStopped) {
          continue;
        }
        final key = managedVisibilityKey(a);
        final existing = byName[key];
        if (existing == null ||
            managedPriority(a) < managedPriority(existing)) {
          byName[key] = a;
        }
      }
      final list = byName.values.toList();
      list.sort((a, b) {
        final pa = managedPriority(a);
        final pb = managedPriority(b);
        if (pa != pb) return pa.compareTo(pb);
        final an = a.name.toLowerCase();
        final bn = b.name.toLowerCase();
        return an.compareTo(bn);
      });
      return list;
    }

    String sigFromManaged(AgentModel a) {
      final lower = a.provider.toLowerCase();

      // Priority 1: sessionId (most reliable for matching)
      if (a.sessionId != null && a.sessionId!.isNotEmpty) {
        return '$lower|${a.sessionId!.toLowerCase()}';
      }

      // Priority 2: PID from attached agent name
      final nameLower = a.name.toLowerCase();
      final attachedPrefix = '$lower-attached-';
      if (nameLower.startsWith(attachedPrefix)) {
        final pid = nameLower.substring(attachedPrefix.length);
        if (pid.isNotEmpty) {
          return '$lower|pid:$pid';
        }
      }

      // Priority 3: sessionId in name (for opencode)
      if (lower == 'opencode' && nameLower.contains('ses_')) {
        return '$lower|$nameLower';
      }

      return '$lower|${a.id.toLowerCase()}';
    }

    String sigFromCandidate(SessionCandidate s) {
      final lower = s.provider.toLowerCase();
      if (s.sessionId != null && s.sessionId!.isNotEmpty) {
        return '$lower|${s.sessionId!.toLowerCase()}';
      }
      if (s.pid != null && s.pid! > 0) {
        return '$lower|pid:${s.pid}';
      }
      return '$lower|${s.workDir.toLowerCase()}';
    }

    // Check if a session candidate matches a managed agent
    // Uses sessionId as primary key, PID as fallback
    bool managedContains(SessionCandidate s, List<AgentModel> managed) {
      final candidateSig = sigFromCandidate(s);
      final sigs = managed.map(sigFromManaged).toSet();
      return sigs.contains(candidateSig);
    }

    String statusText(AgentStatus status) {
      switch (status) {
        case AgentStatus.working:
          return 'Running';
        case AgentStatus.starting:
          return 'Starting';
        case AgentStatus.idle:
          return 'Standby';
        case AgentStatus.stopped:
          return 'Stopped';
        case AgentStatus.crashed:
          return 'Crashed';
      }
    }

    String sessionActionLabel(SessionCandidate s) {
      return (s.pid != null && s.pid! > 0) ? '附加' : '恢复';
    }

    String sessionStateBadge(SessionCandidate s) {
      if (s.pid == null || s.pid! <= 0) return 'RESUME';
      if (s.isReadOnly) return 'READ ONLY';
      if (s.attachMode == 'tmux') return 'WRITABLE';
      return 'ATTACH';
    }

    Color sessionStateColor(SessionCandidate s) {
      if (s.pid == null || s.pid! <= 0) return Colors.blueGrey;
      if (s.isReadOnly) return Colors.orange;
      if (s.attachMode == 'tmux') return Colors.blue;
      return Colors.green;
    }

    String sessionConfirmTitle(SessionCandidate s) {
      return sessionActionLabel(s) == '恢复' ? '恢复会话' : '附加会话';
    }

    String sessionConfirmMessage(SessionCandidate s) {
      final target = '${s.provider} ${s.sessionId ?? 'pid:${s.pid ?? 0}'}';
      if (sessionActionLabel(s) == '恢复') {
        return '将从 $target 恢复为托管会话，是否继续？';
      }
      if (s.isReadOnly && s.readOnlyReason.trim().isNotEmpty) {
        return '将附加到 $target。\n\n注意：该会话当前只读。\n原因：${s.readOnlyReason.trim()}\n\n是否继续？';
      }
      if (s.attachMode == 'tmux') {
        return '将附加到 $target，并通过 tmux 转发输入，是否继续？';
      }
      return '将附加到 $target，是否继续？';
    }

    Future<bool> confirmAction(
      BuildContext ctx,
      String title,
      String content,
    ) async {
      final ok = await showDialog<bool>(
        context: ctx,
        builder: (_) => AlertDialog(
          title: Text(title),
          content: Text(content),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('确认'),
            ),
          ],
        ),
      );
      return ok ?? false;
    }

    Future<List<SessionCandidate>> visibleSessions(
      List<SessionCandidate> all,
      List<AgentModel> managed,
    ) async {
      final unique = <String, SessionCandidate>{};
      for (final s in all) {
        unique[sigFromCandidate(s)] = s;
      }
      final list = unique.values
          .where((s) => (s.sessionId ?? '').isNotEmpty || ((s.pid ?? 0) > 0))
          .where((s) => !managedContains(s, managed))
          .toList();
      list.sort((a, b) {
        final pa = sessionCandidateSortPriority(a);
        final pb = sessionCandidateSortPriority(b);
        if (pa != pb) return pa.compareTo(pb);
        final ap = a.pid ?? 0;
        final bp = b.pid ?? 0;
        if (ap != bp) return bp.compareTo(ap);
        final as = (a.sessionId ?? '').toLowerCase();
        final bs = (b.sessionId ?? '').toLowerCase();
        return as.compareTo(bs);
      });
      return list;
    }

    Future<void> applyCatalog(
      Map<String, dynamic> map,
      StateSetter setState,
    ) async {
      final fetchedManaged = normalizeManaged(
        ((map['managed'] as List?) ?? const [])
            .whereType<Map>()
            .map(
              (e) => AgentModel.fromJson({
                ...Map<String, dynamic>.from(e),
                'nodeId': widget.node.id,
              }),
            )
            .toList(),
      );
      final fetchedSessions = await visibleSessions(
        parseSessionCandidates(map),
        fetchedManaged,
      );

      setState(() {
        managedAgents = fetchedManaged;
        sessions = fetchedSessions;
        opencodeFiles = const [];
      });
    }

    Future<void> fetchCatalog(
      StateSetter setState,
      BuildContext ctx, {
      bool runAutoAttach = false,
    }) async {
      try {
        final result = await client.call('session.catalog', {
          'nodeId': widget.node.id,
        });
        final map = result is Map
            ? Map<String, dynamic>.from(result)
            : <String, dynamic>{};

        final fetchedManaged = normalizeManaged(
          ((map['managed'] as List?) ?? const [])
              .whereType<Map>()
              .map(
                (e) => AgentModel.fromJson({
                  ...Map<String, dynamic>.from(e),
                  'nodeId': widget.node.id,
                }),
              )
              .toList(),
        );

        final fetchedSessions = await visibleSessions(
          parseSessionCandidates(map),
          fetchedManaged,
        );

        if (!ctx.mounted) return;

        if (runAutoAttach && !autoAttachDone) {
          autoAttachDone = true;

          final candidate = pickPreferredAutoAttachCandidate(fetchedSessions);
          if (candidate != null) {
            final err = await _attachSessionCandidate(
              ref,
              candidate,
              refreshAgents: false,
            );
            if (err != null && ctx.mounted) {
              ScaffoldMessenger.of(ctx).showSnackBar(
                SnackBar(content: Text('自动附加失败：$err')),
              );
            }

            final refreshResult = await client.call('session.catalog', {
              'nodeId': widget.node.id,
            });
            final refreshMap = refreshResult is Map
                ? Map<String, dynamic>.from(refreshResult)
                : <String, dynamic>{};

            if (!ctx.mounted) return;
            await applyCatalog(refreshMap, setState);
            return;
          }
        }

        if (!ctx.mounted) return;
        await applyCatalog(map, setState);
      } catch (e) {
        debugPrint('session manager load error: $e');
      }
    }

    try {
      final result = await client.call('session.catalog', {
        'nodeId': widget.node.id,
      });
      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      managedAgents = normalizeManaged(
        ((map['managed'] as List?) ?? const [])
            .whereType<Map>()
            .map(
              (e) => AgentModel.fromJson({
                ...Map<String, dynamic>.from(e),
                'nodeId': widget.node.id,
              }),
            )
            .toList(),
      );
      sessions = await visibleSessions(
        parseSessionCandidates(map),
        managedAgents,
      );
      opencodeFiles = const [];
    } catch (e) {
      debugPrint('session manager init error: $e');
    }

    if (!context.mounted) return;

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) {
          if (!autoAttachDone) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (!ctx.mounted || autoAttachDone) return;
              fetchCatalog(setState, ctx, runAutoAttach: true);
            });
          }
          return AlertDialog(
            title: Text(
              '${(widget.node.host.isNotEmpty && widget.node.host != 'localhost' && widget.node.host != '127.0.0.1') ? widget.node.host : widget.node.name} 会话管理',
            ),
            content: SizedBox(
              width: double.maxFinite,
              child:
                  (managedAgents.isEmpty &&
                      sessions.isEmpty &&
                      opencodeFiles.isEmpty)
                  ? const Padding(
                      padding: EdgeInsets.symmetric(vertical: 16),
                      child: Text('暂无会话', style: TextStyle(color: Colors.grey)),
                    )
                  : SingleChildScrollView(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (managedAgents.isNotEmpty) ...[
                            const Text(
                              '已管理会话',
                              style: TextStyle(fontWeight: FontWeight.w600),
                            ),
                            const SizedBox(height: 8),
                            ...managedAgents.map((a) {
                              final running =
                                  a.status == AgentStatus.working ||
                                  a.status == AgentStatus.starting;
                              return ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: const Icon(Icons.smart_toy, size: 18),
                                title: Row(
                                  children: [
                                    Expanded(
                                      child: Text(
                                        a.name.isEmpty ? a.id : a.name,
                                      ),
                                    ),
                                    if (running)
                                      Container(
                                        padding: const EdgeInsets.symmetric(
                                          horizontal: 6,
                                          vertical: 2,
                                        ),
                                        decoration: BoxDecoration(
                                          color: Colors.blue.withValues(
                                            alpha: 0.12,
                                          ),
                                          borderRadius: BorderRadius.circular(
                                            10,
                                          ),
                                        ),
                                        child: const Text(
                                          'RUNNING',
                                          style: TextStyle(
                                            fontSize: 10,
                                            color: Colors.blue,
                                          ),
                                        ),
                                      ),
                                  ],
                                ),
                                subtitle: Text(
                                  '${a.provider} · ${a.workDir} · ${statusText(a.status)}',
                                ),
                                trailing: const Text('进入'),
                                onTap: () {
                                  Navigator.pop(ctx);
                                  context.push(
                                    '/agent/${widget.node.id}/${a.id}',
                                  );
                                },
                              );
                            }),
                          ],
                          if (managedAgents.isNotEmpty && sessions.isNotEmpty)
                            const Padding(
                              padding: EdgeInsets.symmetric(vertical: 8),
                              child: Divider(height: 1),
                            ),
                          if (sessions.isNotEmpty) ...[
                            const Text(
                              '可接管 / 可恢复会话',
                              style: TextStyle(fontWeight: FontWeight.w600),
                            ),
                            const SizedBox(height: 8),
                            ...sessions.map((s) {
                              final secondaryParts = [
                                if (s.sessionId != null &&
                                    s.sessionId!.isNotEmpty)
                                  s.sessionId!,
                                if (s.pid != null && s.pid! > 0)
                                  'PID ${s.pid}',
                                if (s.terminal != null &&
                                    s.terminal!.isNotEmpty)
                                  s.terminal!,
                                if (s.pid != null && s.pid! > 0)
                                  (s.attachMode == 'tmux'
                                      ? '可交互'
                                      : (s.isReadOnly ? '只读' : '可附加')),
                                if (s.readOnlyReason.trim().isNotEmpty)
                                  s.readOnlyReason.trim(),
                              ];
                              final secondary = secondaryParts.join(' · ');
                              final badgeColor = sessionStateColor(s);
                              final actionLabel = sessionActionLabel(s);
                              // 优先使用 projectName，fallback 到 provider + PID/sessionId
                              final titleText = s.projectName != null && s.projectName!.isNotEmpty
                                  ? '${s.projectName} (${s.provider})'
                                  : (s.pid != null && s.pid! > 0)
                                      ? '${s.provider}  PID ${s.pid ?? 0}'
                                      : '${s.provider}  ${s.sessionId ?? '历史会话'}';

                              return ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: Icon(
                                  s.provider == 'opencode'
                                      ? Icons.terminal
                                      : Icons.smart_toy,
                                  size: 18,
                                ),
                                title: Row(
                                  children: [
                                    Expanded(child: Text(titleText)),
                                    Container(
                                      padding: const EdgeInsets.symmetric(
                                        horizontal: 6,
                                        vertical: 2,
                                      ),
                                      decoration: BoxDecoration(
                                        color: badgeColor.withValues(alpha: 0.12),
                                        borderRadius: BorderRadius.circular(10),
                                      ),
                                      child: Text(
                                        sessionStateBadge(s),
                                        style: TextStyle(
                                          fontSize: 10,
                                          color: badgeColor,
                                        ),
                                      ),
                                    ),
                                  ],
                                ),
                                subtitle: secondary.isEmpty
                                    ? null
                                    : Text(secondary),
                                trailing: TextButton(
                                  onPressed: () async {
                                    final ok = await confirmAction(
                                      ctx,
                                      sessionConfirmTitle(s),
                                      sessionConfirmMessage(s),
                                    );
                                    if (!ok || !ctx.mounted) return;
                                    final err = await _attachSessionCandidate(
                                      ref,
                                      s,
                                    );
                                    if (!ctx.mounted) return;
                                    ScaffoldMessenger.of(ctx).showSnackBar(
                                      SnackBar(
                                        content: Text(
                                          err == null
                                              ? '$actionLabel成功'
                                              : '$actionLabel失败: $err',
                                        ),
                                      ),
                                    );
                                    await fetchCatalog(setState, ctx);
                                  },
                                  child: Text(actionLabel),
                                ),
                              );
                            }),
                          ],
                          if (opencodeFiles.isNotEmpty) ...[
                            const Padding(
                              padding: EdgeInsets.symmetric(vertical: 8),
                              child: Divider(height: 1),
                            ),
                            const Text(
                              'OpenCode 历史会话文件',
                              style: TextStyle(fontWeight: FontWeight.w600),
                            ),
                            const SizedBox(height: 8),
                            ...opencodeFiles.map(
                              (f) => ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: const Icon(Icons.history, size: 18),
                                title: Text(f.name),
                                subtitle: const Text('不一定在运行，可恢复为托管会话'),
                                trailing: TextButton(
                                  onPressed: () async {
                                    final ok = await confirmAction(
                                      ctx,
                                      '恢复会话',
                                      '将从历史文件恢复 ${f.name}，是否继续？',
                                    );
                                    if (!ok || !ctx.mounted) return;
                                    final err = await _restoreOpencodeFile(
                                      ref,
                                      f.id,
                                    );
                                    if (!ctx.mounted) return;
                                    ScaffoldMessenger.of(ctx).showSnackBar(
                                      SnackBar(
                                        content: Text(
                                          err == null ? '恢复成功' : '恢复失败: $err',
                                        ),
                                      ),
                                    );
                                    await fetchCatalog(setState, ctx);
                                  },
                                  child: const Text('恢复'),
                                ),
                              ),
                            ),
                          ],
                        ],
                      ),
                    ),
            ),
            actions: [
              TextButton(
                onPressed: () => fetchCatalog(setState, ctx),
                child: const Text('刷新'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('关闭'),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<String?> _attachSessionCandidate(
    WidgetRef ref,
    SessionCandidate candidate, {
    bool refreshAgents = true,
  }) async {
    final client = ref.read(connectionProvider);
    if (client == null) return 'not connected';

    final params = <String, dynamic>{
      'nodeId': widget.node.id,
      'provider': candidate.provider,
      'name': '${candidate.provider}-attached',
    };

    if (candidate.pid != null && candidate.pid! > 0) {
      params['pid'] = candidate.pid;
      if (candidate.workDir.isNotEmpty) {
        params['workDir'] = candidate.workDir;
      }
    } else if (candidate.sessionId != null && candidate.sessionId!.isNotEmpty) {
      params['sessionId'] = candidate.sessionId;
    }

    if (!params.containsKey('sessionId') && !params.containsKey('pid')) {
      return 'missing sessionId/pid';
    }

    try {
      await client.call('session.attach', params);
      if (refreshAgents) {
        await _refreshAgents(ref);
      }
      return null;
    } catch (e) {
      debugPrint('session.attach error: $e');
      return e.toString();
    }
  }

  Future<String?> _restoreOpencodeFile(WidgetRef ref, String sessionId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return 'not connected';
    try {
      await client.call('session.create', {
        'nodeId': widget.node.id,
        'provider': 'opencode',
        'sessionId': sessionId,
        'name': 'opencode-$sessionId',
        'workDir': '',
      });
      await _refreshAgents(ref);
      return null;
    } catch (e) {
      debugPrint('opencode restore error: $e');
      return e.toString();
    }
  }
}

class AgentRow extends ConsumerStatefulWidget {
  final AgentModel agent;
  final String nodeId;
  const AgentRow({super.key, required this.agent, required this.nodeId});

  @override
  ConsumerState<AgentRow> createState() => _AgentRowState();
}

class _AgentRowState extends ConsumerState<AgentRow> {
  Color get _statusColor => AgentStatusTheme.getColor(widget.agent.status);

  String get _statusLabel {
    switch (widget.agent.status) {
      case AgentStatus.working:
        return 'Working';
      case AgentStatus.idle:
        return 'Standby';
      case AgentStatus.starting:
        return 'Starting…';
      case AgentStatus.stopped:
        return 'Stopped';
      case AgentStatus.crashed:
        return 'Crashed';
    }
  }

  Future<void> _renameAgent() async {
    final controller = TextEditingController(text: widget.agent.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名 Agent'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '输入新名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (newName == null || newName.isEmpty) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('agent.rename', {
        'nodeId': widget.nodeId,
        'agentId': widget.agent.id,
        'name': newName,
      });
      ref.read(nodesProvider.notifier).renameAgent(widget.nodeId, widget.agent.id, newName);
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('已重命名为 $newName')),
        );
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('重命名失败: $e')),
        );
      }
    }
  }

  PopupMenuItem<String> _detailMenuItem(IconData icon, String text) {
    return PopupMenuItem<String>(
      enabled: false,
      child: Row(
        children: [
          Icon(icon, size: 16, color: Theme.of(context).colorScheme.onSurfaceVariant),
          const SizedBox(width: 8),
          Expanded(
            child: Text(
              text,
              style: TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final agent = widget.agent;
    final displayTitle = agent.projectName != null && agent.projectName!.isNotEmpty
        ? '${agent.projectName} (${agent.provider})'
        : agent.name;

    final subtitleText = '${agent.provider} · ${_statusLabel}';

    final attentionChips = <Widget>[];
    if ((agent.providerWriteMode ?? '') == 'read_only') {
      attentionChips.add(_InfoChip(label: '只读', color: Colors.orange));
    }
    if (agent.status == AgentStatus.crashed) {
      attentionChips.add(_InfoChip(label: '异常', color: Colors.red));
    }

    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 24, vertical: 2),
      leading: Icon(Icons.smart_toy, color: _statusColor, size: 20),
      title: Text(displayTitle),
      subtitle: Row(
        children: [
          Expanded(
            child: Text(
              subtitleText,
              style: TextStyle(color: _statusColor, fontSize: 12),
            ),
          ),
          if (attentionChips.isNotEmpty)
            Row(
              mainAxisSize: MainAxisSize.min,
              children: attentionChips,
            ),
        ],
      ),
      trailing: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (agent.status == AgentStatus.working)
            const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          PopupMenuButton<String>(
            icon: const Icon(Icons.more_vert, size: 18),
            onSelected: (value) {
              if (value == 'rename') _renameAgent();
            },
            itemBuilder: (_) {
              final items = <PopupMenuEntry<String>>[
                const PopupMenuItem(
                  value: 'rename',
                  child: Row(
                    children: [
                      Icon(Icons.edit, size: 18),
                      SizedBox(width: 8),
                      Text('重命名'),
                    ],
                  ),
                ),
              ];

              final details = <PopupMenuEntry<String>>[];
              if ((agent.runtimeState ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.memory,
                  'Runtime: ${_runtimeStateLabel(agent.runtimeState)}',
                ));
              }
              if ((agent.sessionState ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.chat_bubble_outline,
                  'Session: ${_sessionStateLabel(agent.sessionState)}',
                ));
              }
              if ((agent.sessionControl ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.gamepad_outlined,
                  'Control: ${_sessionControlLabel(agent.sessionControl)}',
                ));
              }
              if ((agent.providerState ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.settings,
                  'Provider: ${_providerStateLabel(agent.providerState)}',
                ));
              }
              if ((agent.providerScope ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.account_tree,
                  'Scope: ${agent.providerScope == 'inherited' ? '继承 Root' : agent.providerScope!}',
                ));
              }
              if ((agent.providerWriteMode ?? '').isNotEmpty) {
                details.add(_detailMenuItem(
                  agent.providerWriteMode == 'read_only'
                      ? Icons.lock_outline
                      : Icons.edit_note,
                  'Mode: ${agent.providerWriteMode == 'read_only' ? 'Provider 只读' : 'Provider 可切换'}',
                ));
              }
              if ((agent.sessionStateReason ?? '').trim().isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.info_outline,
                  agent.sessionStateReason!.trim(),
                ));
              }
              if ((agent.providerReadOnlyReason ?? '').trim().isNotEmpty) {
                details.add(_detailMenuItem(
                  Icons.info_outline,
                  agent.providerReadOnlyReason!.trim(),
                ));
              }

              if (details.isNotEmpty) {
                items.add(const PopupMenuDivider());
                items.addAll(details);
              }

              return items;
            },
          ),
        ],
      ),
      onTap: () => context.push('/agent/${widget.nodeId}/${agent.id}'),
    );
  }
}

class _InfoChip extends StatelessWidget {
  final String label;
  final Color color;

  const _InfoChip({required this.label, required this.color});

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.12),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        label,
        style: TextStyle(fontSize: 10, color: color, fontWeight: FontWeight.w600),
      ),
    );
  }
}

String _runtimeStateLabel(String? value) {
  switch (value) {
    case 'live':
      return '运行中';
    case 'exited':
      return '已退出';
    case 'stopped':
      return '已停止';
    case 'crashed':
      return '异常退出';
    case 'starting':
      return '启动中';
    default:
      return value ?? '';
  }
}

String _sessionStateLabel(String? value) {
  switch (value) {
    case 'active':
      return '会话活跃';
    case 'standby':
      return '会话待机';
    case 'resumable':
      return '可恢复';
    case 'missing':
      return '会话缺失';
    case 'broken':
      return '会话异常';
    case 'none':
      return '无会话';
    default:
      return value ?? '';
  }
}

String _providerStateLabel(String? value) {
  switch (value) {
    case 'synced':
      return 'Provider 已同步';
    case 'drifted':
      return 'Provider 漂移';
    case 'unknown':
      return 'Provider 未知';
    default:
      return value ?? '';
  }
}

String _sessionControlLabel(String? value) {
  switch (value) {
    case 'managed':
      return '已托管';
    case 'attachable':
      return '可附加';
    case 'rebindable':
      return '可重绑';
    case 'read_only':
      return '只读';
    case 'unavailable':
      return '不可接管';
    default:
      return value ?? '';
  }
}

class _HealthIndicator extends ConsumerWidget {
  const _HealthIndicator();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final health = ref.watch(healthProvider);

    final status = health?.status ?? HealthStatus.unknown;
    final color = switch (status) {
      HealthStatus.healthy => Colors.green,
      HealthStatus.degraded => Colors.orange,
      HealthStatus.down => Colors.red,
      HealthStatus.unknown => Colors.grey,
    };

    final label = switch (status) {
      HealthStatus.healthy => 'Healthy',
      HealthStatus.degraded => 'Degraded',
      HealthStatus.down => 'Down',
      HealthStatus.unknown => 'Checking...',
    };

    final nodeDetails = <String>[];
    if (health?.nodes.isNotEmpty ?? false) {
      for (final entry in health!.nodes.entries) {
        final n = entry.value;
        final latencyStr = n.latencyMs > 0 ? '${n.latencyMs}ms' : '';
        final errStr = n.error != null ? ' (${n.error})' : '';
        nodeDetails.add('${entry.key}: ${n.status}$latencyStr$errStr');
      }
    }

    final tooltip = nodeDetails.isNotEmpty
        ? '$label\n${nodeDetails.join('\n')}'
        : label;

    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: Tooltip(
        message: tooltip,
        child: Icon(Icons.circle, color: color, size: 14),
      ),
    );
  }
}
