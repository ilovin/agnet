import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';
import '../providers/health_provider.dart';

class SessionCandidate {
  final int? pid;
  final String provider;
  final String workDir;
  final String? sessionId;
  final String? terminal;
  final String attachMode;
  final bool isReadOnly;
  final String readOnlyReason;

  const SessionCandidate({
    required this.pid,
    required this.provider,
    required this.workDir,
    this.sessionId,
    this.terminal,
    this.attachMode = '',
    this.isReadOnly = false,
    this.readOnlyReason = '',
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

  return [...attachable, ...claudeFiles];
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

  @override
  void initState() {
    super.initState();
    _setupEventListener();
    _startAutoRefresh();
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    super.dispose();
  }

  void _startAutoRefresh() {
    // Refresh every 3 seconds
    _refreshTimer = Timer.periodic(const Duration(seconds: 3), (_) {
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

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final nodes = nodeState.nodeList;

    return Scaffold(
      appBar: AppBar(
        title: const Text('仪表盘'),
        actions: [
          // Health status indicator
          const _HealthIndicator(),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: nodes.isEmpty
          ? const Center(
              child: Text('暂无节点', style: TextStyle(color: Colors.grey)),
            )
          : ListView.builder(
              itemCount: nodes.length,
              padding: const EdgeInsets.all(12),
              itemBuilder: (_, i) => NodeCard(node: nodes[i]),
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
    final nodeDisplay =
        (widget.node.host.isNotEmpty &&
            widget.node.host != 'localhost' &&
            widget.node.host != '127.0.0.1')
        ? widget.node.host
        : widget.node.name;

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

    // Show active agents + stopped agents that have conversation history
    final bySignature = <String, AgentModel>{};
    for (final a in agents) {
      if (a.status == AgentStatus.stopped && !a.hasHistory) continue;
      if (a.status == AgentStatus.crashed && !a.hasHistory) continue;
      final key = '${a.provider.toLowerCase()}|${a.name.toLowerCase()}|${a.id.toLowerCase()}';
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
            leading: Icon(Icons.computer, color: _statusColor),
            title: Text(
              nodeDisplay,
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
            subtitle: Text('${widget.node.host}  ·  $_statusLabel'),
            trailing: Icon(Icons.circle, color: _statusColor, size: 12),
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
                TextButton.icon(
                  onPressed: () => _showCreateAgentDialog(context, ref),
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('新建 Agent'),
                ),
                TextButton.icon(
                  onPressed: () => _showSessionManager(context, ref),
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

  Future<void> _refreshAgents(WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final result = await client.call('agent.list', {'nodeId': widget.node.id});
    final agents = (result is List
        ? result
        : (result['agents'] as List?) ?? []);
    ref.read(nodesProvider.notifier).loadAgents(widget.node.id, agents);
  }

  void _showCreateAgentDialog(BuildContext context, WidgetRef ref) {
    final cwdCtrl = TextEditingController();
    final nameCtrl = TextEditingController();
    final sessionCtrl = TextEditingController();
    final modelCtrl = TextEditingController();
    String provider = 'claude';

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
                  onSelectionChanged: (s) => setState(() => provider = s.first),
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
                    decoration: const InputDecoration(
                      labelText: 'Model (可选)',
                      hintText: 'claude-sonnet-4-6',
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
        // Hide stopped/crashed agents unless they have conversation history
        if ((a.status == AgentStatus.stopped || a.status == AgentStatus.crashed) && !a.hasHistory) {
          continue;
        }
        final key = '${a.provider.toLowerCase()}|${a.name.toLowerCase()}|${a.id.toLowerCase()}';
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
      final nameLower = a.name.toLowerCase();
      final attachedPrefix = '$lower-attached-';
      if (nameLower.startsWith(attachedPrefix)) {
        final pid = nameLower.substring(attachedPrefix.length);
        if (pid.isNotEmpty) {
          return '$lower|pid:$pid';
        }
      }
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

    bool managedContains(SessionCandidate s, List<AgentModel> managed) {
      final sigs = managed.map(sigFromManaged).toSet();
      return sigs.contains(sigFromCandidate(s));
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
                                if (s.workDir.isNotEmpty) s.workDir,
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
                              final titleText =
                                  (s.pid != null && s.pid! > 0)
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

class AgentRow extends ConsumerWidget {
  final AgentModel agent;
  final String nodeId;
  const AgentRow({super.key, required this.agent, required this.nodeId});

  Color get _statusColor {
    switch (agent.status) {
      case AgentStatus.working:
        return Colors.blue;
      case AgentStatus.idle:
        return Colors.yellow.shade700;
      case AgentStatus.starting:
        return Colors.orange;
      case AgentStatus.stopped:
        return Colors.grey;
      case AgentStatus.crashed:
        return Colors.red;
    }
  }

  String get _statusLabel {
    switch (agent.status) {
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

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 24),
      leading: Icon(Icons.smart_toy, color: _statusColor, size: 20),
      title: Text(agent.name),
      subtitle: Text(
        '${agent.provider} · $_statusLabel',
        style: TextStyle(color: _statusColor, fontSize: 12),
      ),
      trailing: agent.status == AgentStatus.working
          ? const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : null,
      onTap: () => context.push('/agent/$nodeId/${agent.id}'),
    );
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
