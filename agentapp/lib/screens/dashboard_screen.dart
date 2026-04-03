import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';

class SessionCandidate {
  final int? pid;
  final String provider;
  final String workDir;
  final String? sessionId;
  final String? terminal;

  const SessionCandidate({
    required this.pid,
    required this.provider,
    required this.workDir,
    this.sessionId,
    this.terminal,
  });

  factory SessionCandidate.fromJson(Map<String, dynamic> json) => SessionCandidate(
        pid: (json['pid'] as num?)?.toInt(),
        provider: (json['provider'] as String? ?? 'unknown').toLowerCase(),
        workDir: json['workDir'] as String? ?? '',
        sessionId: json['session'] as String? ?? _sessionIdFromPath(json['sessionFile'] as String?),
        terminal: json['terminal'] as String?,
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

List<SessionCandidate> parseSessionCandidates(dynamic result) {
  final rawAttachable = result is List
      ? result
      : (result is Map<String, dynamic>
          ? ((result['attachable'] as List?) ?? (result['processes'] as List?) ?? const [])
          : const []);

  final rawClaude = result is Map<String, dynamic> ? (result['claudeFiles'] as List?) ?? const [] : const [];

  final attachable = rawAttachable
      .whereType<Map>()
      .map((e) => SessionCandidate.fromJson(Map<String, dynamic>.from(e)));

  final claudeFiles = rawClaude.whereType<Map>().map((e) {
    final m = Map<String, dynamic>.from(e);
    return SessionCandidate(
      pid: null,
      provider: 'claude',
      workDir: m['workDir'] as String? ?? '',
      sessionId: m['id'] as String?,
      terminal: null,
    );
  });

  return [...attachable, ...claudeFiles];
}

class OpencodeFileCandidate {
  final String id;
  final String name;

  const OpencodeFileCandidate({required this.id, required this.name});

  factory OpencodeFileCandidate.fromJson(Map<String, dynamic> json) => OpencodeFileCandidate(
        id: json['id'] as String? ?? '',
        name: json['name'] as String? ?? (json['id'] as String? ?? ''),
      );
}

List<OpencodeFileCandidate> parseOpencodeFiles(dynamic result) {
  final rawList = result is List
      ? result
      : (result is Map<String, dynamic> ? (result['opencodeFiles'] as List?) ?? const [] : const []);

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
  @override
  void initState() {
    super.initState();
    _setupEventListener();
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
      final agents = (result is List ? result : (result['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
    } catch (e) {
      debugPrint('Failed to refresh agents for node $nodeId: $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final client = ref.watch(connectionProvider);
    final nodes = nodeState.nodeList;

    return Scaffold(
      appBar: AppBar(
        title: const Text('仪表盘'),
        actions: [
          // Reconnection status indicator
          if (client != null)
            StreamBuilder<bool>(
              stream: client.onConnectionChanged,
              initialData: client.isConnected,
              builder: (_, snap) {
                final connected = snap.data ?? false;
                return Padding(
                  padding: const EdgeInsets.only(right: 12),
                  child: Icon(
                    connected ? Icons.wifi : Icons.wifi_off,
                    color: connected ? Colors.green : Colors.red,
                  ),
                );
              },
            ),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: nodes.isEmpty
          ? const Center(child: Text('暂无节点', style: TextStyle(color: Colors.grey)))
          : ListView.builder(
              itemCount: nodes.length,
              padding: const EdgeInsets.all(12),
              itemBuilder: (_, i) => NodeCard(node: nodes[i]),
            ),
    );
  }
}

class NodeCard extends ConsumerWidget {
  final NodeModel node;
  const NodeCard({super.key, required this.node});

  Color get _statusColor {
    switch (node.status) {
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
    switch (node.status) {
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
  Widget build(BuildContext context, WidgetRef ref) {
    final agents = ref.watch(nodesProvider).agentsFor(node.id);
    final nodeDisplay = (node.host.isNotEmpty && node.host != 'localhost' && node.host != '127.0.0.1') ? node.host : node.name;

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

    final activeAgents = agents
        .where((a) => a.status != AgentStatus.stopped && a.status != AgentStatus.crashed)
        .toList();

    final bySignature = <String, AgentModel>{};
    for (final a in activeAgents) {
      final key = '${a.provider.toLowerCase()}|${a.name.toLowerCase()}';
      final existing = bySignature[key];
      if (existing == null || statusPriority(a.status) < statusPriority(existing.status)) {
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
            title: Text(nodeDisplay, style: const TextStyle(fontWeight: FontWeight.bold)),
            subtitle: Text('${node.host}  ·  $_statusLabel'),
            trailing: Icon(Icons.circle, color: _statusColor, size: 12),
          ),
          if (visibleAgents.isNotEmpty) const Divider(height: 1),
          ...visibleAgents.map((a) => AgentRow(agent: a, nodeId: node.id)),
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
    final result = await client.call('agent.list', {'nodeId': node.id});
    final agents = (result is List ? result : (result['agents'] as List?) ?? []);
    ref.read(nodesProvider.notifier).loadAgents(node.id, agents);
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
          title: Text('在 ${(node.host.isNotEmpty && node.host != 'localhost' && node.host != '127.0.0.1') ? node.host : node.name} 上新建 Agent'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(labelText: '名称', hintText: 'claude-1'),
                ),
                TextField(
                  controller: cwdCtrl,
                  decoration: const InputDecoration(labelText: '工作目录', hintText: '/home/user/proj'),
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
            TextButton(onPressed: () => Navigator.pop(context), child: const Text('取消')),
            FilledButton(
              onPressed: () async {
                Navigator.pop(context);
                final client = ref.read(connectionProvider);
                if (client == null) return;

                final params = <String, dynamic>{
                  'nodeId': node.id,
                  'name': nameCtrl.text.trim(),
                  'workDir': cwdCtrl.text.trim(),
                  'provider': provider,
                };
                final sessionId = sessionCtrl.text.trim();
                final model = modelCtrl.text.trim();
                if (sessionId.isNotEmpty) params['sessionId'] = sessionId;
                if (provider == 'claude' && model.isNotEmpty) params['model'] = model;

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
        if (a.status == AgentStatus.stopped || a.status == AgentStatus.crashed) {
          continue;
        }
        final key = '${a.provider.toLowerCase()}|${a.name.toLowerCase()}';
        final existing = byName[key];
        if (existing == null || managedPriority(a) < managedPriority(existing)) {
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

    Future<bool> confirmAction(BuildContext ctx, String title, String content) async {
      final ok = await showDialog<bool>(
        context: ctx,
        builder: (_) => AlertDialog(
          title: Text(title),
          content: Text(content),
          actions: [
            TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('取消')),
            FilledButton(onPressed: () => Navigator.pop(ctx, true), child: const Text('确认')),
          ],
        ),
      );
      return ok ?? false;
    }

    Future<List<SessionCandidate>> visibleSessions(List<SessionCandidate> all, List<AgentModel> managed) async {
      final unique = <String, SessionCandidate>{};
      for (final s in all) {
        unique[sigFromCandidate(s)] = s;
      }
      final list = unique.values
          .where((s) => (s.sessionId ?? '').isNotEmpty)
          .where((s) => !managedContains(s, managed))
          .toList();
      list.sort((a, b) {
        final ap = a.pid ?? 0;
        final bp = b.pid ?? 0;
        if (ap > 0 && bp == 0) return -1;
        if (ap == 0 && bp > 0) return 1;
        final as = (a.sessionId ?? '').toLowerCase();
        final bs = (b.sessionId ?? '').toLowerCase();
        return as.compareTo(bs);
      });
      return list;
    }

    Future<void> applyCatalog(Map<String, dynamic> map, StateSetter setState) async {
      final fetchedManaged = normalizeManaged(((map['managed'] as List?) ?? const [])
          .whereType<Map>()
          .map((e) => AgentModel.fromJson({...Map<String, dynamic>.from(e), 'nodeId': node.id}))
          .toList());
      final fetchedSessions = await visibleSessions(parseSessionCandidates(map), fetchedManaged);

      setState(() {
        managedAgents = fetchedManaged;
        sessions = fetchedSessions;
        opencodeFiles = const [];
      });
    }

    Future<void> fetchCatalog(StateSetter setState, BuildContext ctx, {bool runAutoAttach = false}) async {
      try {
        final result = await client.call('session.catalog', {'nodeId': node.id});
        final map = result is Map ? Map<String, dynamic>.from(result) : <String, dynamic>{};

        final fetchedManaged = normalizeManaged(((map['managed'] as List?) ?? const [])
            .whereType<Map>()
            .map((e) => AgentModel.fromJson({...Map<String, dynamic>.from(e), 'nodeId': node.id}))
            .toList());

        final fetchedSessions = await visibleSessions(parseSessionCandidates(map), fetchedManaged);

        if (!ctx.mounted) return;

        if (runAutoAttach && !autoAttachDone && fetchedSessions.isNotEmpty) {
          autoAttachDone = true;

          final failed = <String>[];
          final autoCandidates = fetchedSessions
              .where((c) => c.pid != null && c.pid! > 0 && c.sessionId != null && c.sessionId!.isNotEmpty)
              .toList();
          for (final c in autoCandidates) {
            final err = await _attachSessionCandidate(ref, c, refreshAgents: false);
            if (err != null) {
              failed.add('${c.provider} ${c.sessionId ?? 'pid:${c.pid ?? 0}'}: $err');
            }
          }

          final refreshResult = await client.call('session.catalog', {'nodeId': node.id});
          final refreshMap = refreshResult is Map ? Map<String, dynamic>.from(refreshResult) : <String, dynamic>{};

          if (!ctx.mounted) return;
          await applyCatalog(refreshMap, setState);

          if (failed.isNotEmpty && ctx.mounted) {
            ScaffoldMessenger.of(ctx).showSnackBar(
              SnackBar(content: Text('自动附加失败 ${failed.length} 项，可手动附加。')),
            );
          }
          return;
        }

        if (!ctx.mounted) return;
        await applyCatalog(map, setState);
      } catch (e) {
        debugPrint('session manager load error: $e');
      }
    }

    try {
      final result = await client.call('session.catalog', {'nodeId': node.id});
      final map = result is Map ? Map<String, dynamic>.from(result) : <String, dynamic>{};
      managedAgents = normalizeManaged(((map['managed'] as List?) ?? const [])
          .whereType<Map>()
          .map((e) => AgentModel.fromJson({...Map<String, dynamic>.from(e), 'nodeId': node.id}))
          .toList());
      sessions = await visibleSessions(parseSessionCandidates(map), managedAgents);
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
            title: Text('${(node.host.isNotEmpty && node.host != 'localhost' && node.host != '127.0.0.1') ? node.host : node.name} 会话管理'),
            content: SizedBox(
              width: double.maxFinite,
              child: (managedAgents.isEmpty && sessions.isEmpty && opencodeFiles.isEmpty)
                  ? const Padding(
                      padding: EdgeInsets.symmetric(vertical: 16),
                      child: Text('暂无会话', style: TextStyle(color: Colors.grey)),
                    )
                  : SingleChildScrollView(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (managedAgents.isNotEmpty) ...[
                            const Text('已管理会话', style: TextStyle(fontWeight: FontWeight.w600)),
                            const SizedBox(height: 8),
                            ...managedAgents.map((a) {
                              final running = a.status == AgentStatus.working || a.status == AgentStatus.starting;
                              return ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: const Icon(Icons.smart_toy, size: 18),
                                title: Row(
                                  children: [
                                    Expanded(child: Text(a.name.isEmpty ? a.id : a.name)),
                                    if (running)
                                      Container(
                                        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                                        decoration: BoxDecoration(
                                          color: Colors.blue.withValues(alpha: 0.12),
                                          borderRadius: BorderRadius.circular(10),
                                        ),
                                        child: const Text('RUNNING', style: TextStyle(fontSize: 10, color: Colors.blue)),
                                      ),
                                  ],
                                ),
                                subtitle: Text('${a.provider} · ${a.workDir} · ${statusText(a.status)}'),
                                trailing: const Text('进入'),
                                onTap: () {
                                  Navigator.pop(ctx);
                                  context.push('/agent/${node.id}/${a.id}');
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
                            const Text('可附加会话（运行中）', style: TextStyle(fontWeight: FontWeight.w600)),
                            const SizedBox(height: 8),
                            ...sessions.map((s) {
                              final secondary = [
                                if (s.sessionId != null && s.sessionId!.isNotEmpty) s.sessionId!,
                                if (s.workDir.isNotEmpty) s.workDir,
                                if (s.terminal != null && s.terminal!.isNotEmpty) s.terminal!,
                              ].join(' · ');

                              return ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: Icon(
                                  s.provider == 'opencode' ? Icons.terminal : Icons.smart_toy,
                                  size: 18,
                                ),
                                title: Row(
                                  children: [
                                    Expanded(child: Text('${s.provider}  PID ${s.pid ?? 0}')),
                                    Container(
                                      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
                                      decoration: BoxDecoration(
                                        color: Colors.green.withValues(alpha: 0.12),
                                        borderRadius: BorderRadius.circular(10),
                                      ),
                                      child: const Text('AVAILABLE', style: TextStyle(fontSize: 10, color: Colors.green)),
                                    ),
                                  ],
                                ),
                                subtitle: secondary.isEmpty ? null : Text(secondary),
                                trailing: TextButton(
                                  onPressed: () async {
                                    final ok = await confirmAction(
                                      ctx,
                                      '附加会话',
                                      '将附加到 ${s.provider} ${s.sessionId ?? 'pid:${s.pid ?? 0}'}，是否继续？',
                                    );
                                    if (!ok || !ctx.mounted) return;
                                    final err = await _attachSessionCandidate(ref, s);
                                    if (!ctx.mounted) return;
                                    ScaffoldMessenger.of(ctx).showSnackBar(
                                      SnackBar(content: Text(err == null ? '附加成功' : '附加失败: $err')),
                                    );
                                    await fetchCatalog(setState, ctx);
                                  },
                                  child: const Text('附加'),
                                ),
                              );
                            }),
                          ],
                          if (opencodeFiles.isNotEmpty) ...[
                            const Padding(
                              padding: EdgeInsets.symmetric(vertical: 8),
                              child: Divider(height: 1),
                            ),
                            const Text('OpenCode 历史会话文件', style: TextStyle(fontWeight: FontWeight.w600)),
                            const SizedBox(height: 8),
                            ...opencodeFiles.map((f) => ListTile(
                                  dense: true,
                                  contentPadding: EdgeInsets.zero,
                                  leading: const Icon(Icons.history, size: 18),
                                  title: Text(f.name),
                                  subtitle: const Text('不一定在运行，可恢复为托管会话'),
                                  trailing: TextButton(
                                    onPressed: () async {
                                      final ok = await confirmAction(ctx, '恢复会话', '将从历史文件恢复 ${f.name}，是否继续？');
                                      if (!ok || !ctx.mounted) return;
                                      final err = await _restoreOpencodeFile(ref, f.id);
                                      if (!ctx.mounted) return;
                                      ScaffoldMessenger.of(ctx).showSnackBar(
                                        SnackBar(content: Text(err == null ? '恢复成功' : '恢复失败: $err')),
                                      );
                                      await fetchCatalog(setState, ctx);
                                    },
                                    child: const Text('恢复'),
                                  ),
                                )),
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

  Future<String?> _attachSessionCandidate(WidgetRef ref, SessionCandidate candidate, {bool refreshAgents = true}) async {
    final client = ref.read(connectionProvider);
    if (client == null) return 'not connected';

    final params = <String, dynamic>{
      'nodeId': node.id,
      'provider': candidate.provider,
      'name': '${candidate.provider}-attached',
    };

    if ((candidate.sessionId == null || candidate.sessionId!.isEmpty) && candidate.pid != null && candidate.pid! > 0) {
      params['pid'] = candidate.pid;
      if (candidate.workDir.isNotEmpty) {
        params['workDir'] = candidate.workDir;
      }
    }
    if (candidate.sessionId != null && candidate.sessionId!.isNotEmpty) {
      params['sessionId'] = candidate.sessionId;
    }

    if (!params.containsKey('sessionId')) {
      return 'missing sessionId';
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
        'nodeId': node.id,
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
      subtitle: Text('${agent.provider} · $_statusLabel', style: TextStyle(color: _statusColor, fontSize: 12)),
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
