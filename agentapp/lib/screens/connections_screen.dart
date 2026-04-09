import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../models/connection_config.dart';
import '../providers/connection_provider.dart';
import '../providers/nodes_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/health_provider.dart';

class ConnectionsScreen extends ConsumerStatefulWidget {
  const ConnectionsScreen({super.key});

  @override
  ConsumerState<ConnectionsScreen> createState() => _ConnectionsScreenState();
}

class _ConnectionsScreenState extends ConsumerState<ConnectionsScreen> {
  List<ConnectionConfig> _saved = [];
  bool _autoConnected = false;

  @override
  void initState() {
    super.initState();
    _loadSaved();
    // Auto-connect to default gateway after a short delay
    Future.delayed(const Duration(milliseconds: 500), () {
      if (mounted && !_autoConnected) {
        _autoConnectDefault();
      }
    });
  }

  Future<void> _autoConnectDefault() async {
    _autoConnected = true;
    // Default connection: localhost gateway with demo token
    const defaultCfg = ConnectionConfig(
      url: 'ws://localhost:8080/ws',
      token: 'testtoken123',
    );
    await _connect(defaultCfg);
  }

  Future<void> _loadSaved() async {
    final store = ref.read(connectionStoreProvider);
    final configs = await store.load();
    if (mounted) setState(() => _saved = configs);
  }

  Future<void> _deleteConnection(int index) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('删除连接'),
        content: const Text('确定要删除这个连接吗？'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('取消')),
          FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('删除')),
        ],
      ),
    );
    if (confirmed != true) return;

    final store = ref.read(connectionStoreProvider);
    final newList = List<ConnectionConfig>.from(_saved)..removeAt(index);
    await store.save(newList);
    if (mounted) setState(() => _saved = newList);
  }

  void _showEditSheet(ConnectionConfig config, int index) {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (_) => _EditConnectionSheet(
        config: config,
        onSave: (updated) async {
          final store = ref.read(connectionStoreProvider);
          final newList = List<ConnectionConfig>.from(_saved);
          newList[index] = updated;
          await store.save(newList);
          if (mounted) setState(() => _saved = newList);
        },
      ),
    );
  }

  void _showAddSheet() {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (_) => _AddConnectionSheet(onConnect: _connect),
    );
  }

  Future<void> _connect(ConnectionConfig cfg) async {
    await ref.read(connectionProvider.notifier).connect(cfg);
    // Load initial data
    final client = ref.read(connectionProvider);
    if (client != null) {
      try {
        final result = await client.call('node.list', {});
        final nodes = (result is List ? result : (result['nodes'] as List?) ?? []);
        ref.read(nodesProvider.notifier).loadNodes(nodes);
        // Subscribe to events
        client.onEvent((event) {
          ref.read(nodesProvider.notifier).handleEvent(event);
          ref.read(conversationProvider.notifier).handleEvent(event);
        });
        // Load agents for each node
        for (final n in nodes) {
          final nodeId = (n as Map<String, dynamic>)['id'] as String;
          try {
            final ar = await client.call('agent.list', {'nodeId': nodeId});
            final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
            ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
          } catch (_) {}
        }
        // Auto-check and attach latest sessions for each node
        for (final n in nodes) {
          final nodeId = (n as Map<String, dynamic>)['id'] as String;
          await _autoAttachLatestSessions(nodeId);
        }
      } catch (_) {}
    }
    if (mounted) context.go('/dashboard');
  }

  Future<void> _autoAttachLatestSessions(String nodeId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('session.catalog', {'nodeId': nodeId});
      final map = result is Map ? Map<String, dynamic>.from(result) : <String, dynamic>{};

      final attachable = (map['attachable'] as List?) ?? [];
      if (attachable.isEmpty) return;

      // Find sessions with both pid and sessionId (latest/active sessions)
      final candidates = attachable.where((s) {
        if (s is! Map) return false;
        final pid = s['pid'] as int?;
        final sessionId = s['session'] as String? ?? s['sessionId'] as String?;
        return pid != null && pid > 0 && sessionId != null && sessionId.isNotEmpty;
      }).toList();

      if (candidates.isEmpty) return;

      // Sort by PID (latest first, assuming higher PID = more recent)
      candidates.sort((a, b) {
        final pidA = (a as Map)['pid'] as int? ?? 0;
        final pidB = (b as Map)['pid'] as int? ?? 0;
        return pidB.compareTo(pidA);
      });

      // Auto-attach the first (latest) session
      final latest = candidates.first as Map<String, dynamic>;
      final provider = (latest['provider'] as String? ?? 'claude').toLowerCase();
      final pid = latest['pid'] as int;
      final sessionId = latest['session'] as String? ?? latest['sessionId'] as String?;
      final workDir = latest['workDir'] as String? ?? '';

      debugPrint('Auto-attaching latest session: $provider PID $pid, sessionId: $sessionId');

      await client.call('session.attach', {
        'nodeId': nodeId,
        'provider': provider,
        'pid': pid,
        'sessionId': sessionId,
        'workDir': workDir,
        'name': '$provider-attached-$pid',
      });

      // Refresh agents list
      final ar = await client.call('agent.list', {'nodeId': nodeId});
      final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);

      debugPrint('Auto-attach successful for PID $pid');
    } catch (e) {
      debugPrint('Auto-attach latest sessions error: $e');
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Agent Manager'),
        actions: [
          Consumer(builder: (context, ref, _) {
            final health = ref.watch(healthProvider);
            final status = health?.status ?? HealthStatus.unknown;
            final color = switch (status) {
              HealthStatus.healthy => Colors.green,
              HealthStatus.degraded => Colors.orange,
              HealthStatus.down => Colors.red,
              HealthStatus.unknown => Colors.grey,
            };
            return Padding(
              padding: const EdgeInsets.only(right: 8),
              child: Icon(Icons.circle, color: color, size: 14),
            );
          }),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: _saved.isEmpty
          ? Center(
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  const Icon(Icons.cable, size: 64, color: Colors.grey),
                  const SizedBox(height: 16),
                  const Text('无连接', style: TextStyle(fontSize: 18, color: Colors.grey)),
                  const SizedBox(height: 8),
                  ElevatedButton.icon(
                    onPressed: _showAddSheet,
                    icon: const Icon(Icons.add),
                    label: const Text('添加连接'),
                  ),
                ],
              ),
            )
          : Column(
              children: [
                Expanded(
                  child: ListView.builder(
                    itemCount: _saved.length,
                    itemBuilder: (_, i) {
                      final cfg = _saved[i];
                      return ListTile(
                        leading: const Icon(Icons.hub),
                        title: Text(cfg.url),
                        subtitle: Text(cfg.token.substring(0, cfg.token.length > 8 ? 8 : cfg.token.length) + '...'),
                        trailing: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            IconButton(
                              icon: const Icon(Icons.edit, size: 20),
                              onPressed: () => _showEditSheet(cfg, i),
                              tooltip: '编辑',
                            ),
                            IconButton(
                              icon: const Icon(Icons.delete, size: 20, color: Colors.red),
                              onPressed: () => _deleteConnection(i),
                              tooltip: '删除',
                            ),
                            const Icon(Icons.chevron_right),
                          ],
                        ),
                        onTap: () => _connect(cfg),
                      );
                    },
                  ),
                ),
                const Divider(height: 1),
                Padding(
                  padding: const EdgeInsets.all(12),
                  child: ElevatedButton.icon(
                    onPressed: _showAddSheet,
                    icon: const Icon(Icons.add),
                    label: const Text('新建连接'),
                    style: ElevatedButton.styleFrom(
                      minimumSize: const Size(double.infinity, 48),
                    ),
                  ),
                ),
              ],
            ),
      floatingActionButton: FloatingActionButton(
        onPressed: _showAddSheet,
        child: const Icon(Icons.add),
      ),
    );
  }
}

class _AddConnectionSheet extends StatefulWidget {
  final Future<void> Function(ConnectionConfig) onConnect;

  const _AddConnectionSheet({required this.onConnect});

  @override
  State<_AddConnectionSheet> createState() => _AddConnectionSheetState();
}

class _AddConnectionSheetState extends State<_AddConnectionSheet> {
  final _urlCtrl = TextEditingController(text: 'ws://');
  final _tokenCtrl = TextEditingController();
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _urlCtrl.dispose();
    _tokenCtrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_urlCtrl.text.trim().isEmpty || _tokenCtrl.text.trim().isEmpty) {
      setState(() => _error = '请填写 URL 和 Token');
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cfg = ConnectionConfig(
        url: _urlCtrl.text.trim(),
        token: _tokenCtrl.text.trim(),
      );
      Navigator.of(context).pop();
      await widget.onConnect(cfg);
    } catch (e) {
      setState(() {
        _error = '连接失败: $e';
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 24,
        bottom: MediaQuery.of(context).viewInsets.bottom + 24,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Text('添加连接', style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold)),
          const SizedBox(height: 16),
          TextField(
            controller: _urlCtrl,
            decoration: const InputDecoration(
              labelText: 'Gateway URL',
              hintText: 'ws://192.168.1.x:7374',
              border: OutlineInputBorder(),
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _tokenCtrl,
            decoration: const InputDecoration(
              labelText: 'Token',
              border: OutlineInputBorder(),
            ),
            obscureText: true,
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(_error!, style: const TextStyle(color: Colors.red)),
          ],
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _loading ? null : _submit,
            child: _loading
                ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                : const Text('连接'),
          ),
        ],
      ),
    );
  }
}

class _EditConnectionSheet extends StatefulWidget {
  final ConnectionConfig config;
  final Function(ConnectionConfig) onSave;

  const _EditConnectionSheet({required this.config, required this.onSave});

  @override
  State<_EditConnectionSheet> createState() => _EditConnectionSheetState();
}

class _EditConnectionSheetState extends State<_EditConnectionSheet> {
  late final _urlCtrl = TextEditingController(text: widget.config.url);
  late final _tokenCtrl = TextEditingController(text: widget.config.token);
  bool _loading = false;
  String? _error;

  @override
  void dispose() {
    _urlCtrl.dispose();
    _tokenCtrl.dispose();
    super.dispose();
  }

  Future<void> _submit() async {
    if (_urlCtrl.text.trim().isEmpty || _tokenCtrl.text.trim().isEmpty) {
      setState(() => _error = '请填写 URL 和 Token');
      return;
    }
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final cfg = ConnectionConfig(
        url: _urlCtrl.text.trim(),
        token: _tokenCtrl.text.trim(),
      );
      Navigator.of(context).pop();
      await widget.onSave(cfg);
    } catch (e) {
      setState(() {
        _error = '保存失败: $e';
        _loading = false;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.only(
        left: 16,
        right: 16,
        top: 24,
        bottom: MediaQuery.of(context).viewInsets.bottom + 24,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Text('编辑连接', style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold)),
          const SizedBox(height: 16),
          TextField(
            controller: _urlCtrl,
            decoration: const InputDecoration(
              labelText: 'Gateway URL',
              hintText: 'ws://192.168.1.x:7374',
              border: OutlineInputBorder(),
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _tokenCtrl,
            decoration: const InputDecoration(
              labelText: 'Token',
              border: OutlineInputBorder(),
            ),
            obscureText: true,
          ),
          if (_error != null) ...[
            const SizedBox(height: 8),
            Text(_error!, style: const TextStyle(color: Colors.red)),
          ],
          const SizedBox(height: 16),
          FilledButton(
            onPressed: _loading ? null : _submit,
            child: _loading
                ? const SizedBox(height: 20, width: 20, child: CircularProgressIndicator(strokeWidth: 2))
                : const Text('保存'),
          ),
        ],
      ),
    );
  }
}
