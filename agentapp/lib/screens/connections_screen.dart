import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../models/connection_config.dart';
import '../providers/connection_provider.dart';
import '../providers/nodes_provider.dart';
import '../providers/conversation_provider.dart';

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
      url: 'ws://localhost:8080',
      token: 'testtoken123',
    );
    await _connect(defaultCfg);
  }

  Future<void> _loadSaved() async {
    final store = ref.read(connectionStoreProvider);
    final configs = await store.load();
    if (mounted) setState(() => _saved = configs);
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
      } catch (_) {}
    }
    if (mounted) context.go('/dashboard');
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Agent Manager'),
        actions: [
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
          : ListView.builder(
              itemCount: _saved.length,
              itemBuilder: (_, i) {
                final cfg = _saved[i];
                return ListTile(
                  leading: const Icon(Icons.hub),
                  title: Text(cfg.url),
                  subtitle: const Text('点击连接'),
                  trailing: const Icon(Icons.chevron_right),
                  onTap: () => _connect(cfg),
                );
              },
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
