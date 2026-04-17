import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:mobile_scanner/mobile_scanner.dart';
import 'dashboard_screen.dart';
import '../models/connection_config.dart';
import '../providers/connection_provider.dart';
import '../providers/nodes_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/health_provider.dart';

/// Pick the best saved config to auto-reconnect.
/// Prefers [lastUrl] if present, otherwise the first saved config.
ConnectionConfig? pickAutoReconnectConfig(
  List<ConnectionConfig> saved,
  String? lastUrl,
) {
  if (saved.isEmpty) return null;
  if (lastUrl != null) {
    try {
      return saved.firstWhere((c) => c.url == lastUrl);
    } catch (_) {}
  }
  return saved.first;
}

class ConnectionsScreen extends ConsumerStatefulWidget {
  const ConnectionsScreen({super.key});

  @override
  ConsumerState<ConnectionsScreen> createState() => _ConnectionsScreenState();
}

class _ConnectionsScreenState extends ConsumerState<ConnectionsScreen>
    with WidgetsBindingObserver {
  static const _platform = MethodChannel('com.phonetalk.agentapp/install');

  List<ConnectionConfig> _saved = [];
  bool _connecting = false;
  String? _connectingUrl;
  String? _connectStatus;
  bool _connectStatusIsError = false;
  ConnectionConfig? _lastFailedConfig;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _loadSaved();
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      _tryAutoReconnect();
    }
  }

  Future<void> _loadSaved() async {
    final store = ref.read(connectionStoreProvider);
    final configs = await store.load();
    if (mounted) setState(() => _saved = configs);
    if (configs.isNotEmpty) {
      _tryAutoReconnect();
    }
    await _checkLaunchExtras();
  }

  Future<void> _checkLaunchExtras() async {
    try {
      final extras = await _platform.invokeMethod<Map<dynamic, dynamic>>('getLaunchExtras');
      final url = extras?['url'] as String? ?? '';
      final token = extras?['token'] as String? ?? '';
      if (url.isNotEmpty && token.isNotEmpty && mounted) {
        await _connect(ConnectionConfig(url: url, token: token));
      }
    } on PlatformException catch (e) {
      debugPrint('getLaunchExtras error: ${e.message}');
    }
  }

  Future<void> _tryAutoReconnect() async {
    final client = ref.read(connectionProvider);
    if (client != null && client.isConnected) return;
    if (_connecting) return;
    final cfg = await _pickAutoReconnectConfig();
    if (cfg != null) {
      await _connect(cfg);
    }
  }

  Future<ConnectionConfig?> _pickAutoReconnectConfig() async {
    if (_saved.isEmpty) return null;
    final store = ref.read(connectionStoreProvider);
    final lastUrl = await store.getLastUsedUrl();
    return pickAutoReconnectConfig(_saved, lastUrl);
  }

  Future<void> _deleteConnection(int index) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: const Text('删除连接'),
        content: const Text('确定要删除这个连接吗？'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
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

  Future<void> _scanQrCode() async {
    final result = await Navigator.push<String>(
      context,
      MaterialPageRoute(builder: (_) => const _QrScannerPage()),
    );
    if (result == null || !mounted) return;

    // Parse QR: format is "ws://URL|TOKEN" or "ws://URL\nTOKEN"
    String url;
    String token;
    if (result.contains('|')) {
      final parts = result.split('|');
      url = parts[0].trim();
      token = parts.sublist(1).join('|').trim();
    } else if (result.contains('\n')) {
      final parts = result.split('\n');
      url = parts[0].trim();
      token = parts.sublist(1).join('\n').trim();
    } else {
      url = result.trim();
      token = '';
    }

    if (url.isEmpty) return;

    if (token.isNotEmpty) {
      // Direct connect if both URL and token are present.
      final ok = await _connect(ConnectionConfig(url: url, token: token));
      if (!ok && mounted) {
        showModalBottomSheet(
          context: context,
          isScrollControlled: true,
          builder: (_) => _AddConnectionSheet(
            initialUrl: url,
            initialToken: token,
            onConnect: _connect,
          ),
        );
      }
    } else {
      // Show pre-filled edit sheet so user can modify URL or fill token.
      if (!mounted) return;
      showModalBottomSheet(
        context: context,
        isScrollControlled: true,
        builder: (_) => _AddConnectionSheet(
          initialUrl: url,
          initialToken: token,
          onConnect: _connect,
        ),
      );
    }
  }

  Future<bool> _connect(ConnectionConfig cfg) async {
    if (_connecting) return false;
    if (mounted) {
      setState(() {
        _connecting = true;
        _connectingUrl = cfg.url;
        _connectStatus = '连接中…';
        _connectStatusIsError = false;
        _lastFailedConfig = null;
      });
    }

    try {
      await ref.read(connectionProvider.notifier).connect(cfg);

      // Load initial data
      final client = ref.read(connectionProvider);
      if (client == null) {
        throw Exception('连接未建立');
      }

      final result = await client.call('node.list', {});
      final nodes = (result is List
          ? result
          : (result['nodes'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadNodes(nodes);

      // Subscribe to events
      client.onEvent((event) {
        ref.read(nodesProvider.notifier).handleEvent(event);
        ref.read(conversationProvider.notifier).handleEvent(event);
      });

      // Register auto-refresh callback for newly discovered agents
      ref.read(nodesProvider.notifier).onAgentsRefresh = (nodeId) async {
        try {
          final ar = await client.call('agent.list', {'nodeId': nodeId});
          final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
          ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
        } catch (_) {}
      };

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

      if (mounted) {
        setState(() {
          _connecting = false;
          _connectingUrl = null;
          _connectStatus = '连接成功';
          _connectStatusIsError = false;
        });
        context.go('/dashboard');
      }
      return true;
    } catch (e) {
      if (!mounted) return false;
      setState(() {
        _connecting = false;
        _connectingUrl = null;
        _connectStatus = _friendlyConnectError(e, cfg);
        _connectStatusIsError = true;
        _lastFailedConfig = cfg;
      });
      return false;
    }
  }

  String _friendlyConnectError(Object error, ConnectionConfig cfg) {
    final raw = error.toString();
    final lower = raw.toLowerCase();

    if (lower.contains('401') ||
        lower.contains('unauthorized') ||
        lower.contains('forbidden') ||
        lower.contains('403')) {
      return '连接失败：Token 验证不通过（401/403）。请检查 token 是否正确。';
    }

    if (lower.contains('failed host lookup') ||
        lower.contains('nodename nor servname provided') ||
        lower.contains('no route to host') ||
        lower.contains('network is unreachable')) {
      return '连接失败：主机不可达（类似 ping 不通）。请检查 Tailscale 是否在线、IP 是否正确。';
    }

    if (lower.contains('connection refused') ||
        lower.contains('actively refused') ||
        lower.contains('connection reset')) {
      return '连接失败：端口未开放或服务未启动。请检查 8080 端口监听和 agentgw/agentd 进程。';
    }

    if ((lower.contains('websocket') && lower.contains('404')) ||
        lower.contains('404 not found') ||
        lower.contains('not upgraded to websocket')) {
      return '连接失败：WebSocket 路径错误。请确认 URL 以 /api/v1/ws 结尾。';
    }

    if (error is TimeoutException ||
        lower.contains('handshake timeout') ||
        lower.contains('timed out')) {
      return '连接失败：连接超时。通常是网络不通或端口无响应，请先检查连通性和端口。';
    }

    return '连接失败：$raw';
  }

  Future<void> _autoAttachLatestSessions(String nodeId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('session.catalog', {'nodeId': nodeId});
      final sessions = parseSessionCandidates(result);
      final candidate = pickPreferredAutoAttachCandidate(sessions);
      if (candidate == null) return;

      final provider = candidate.provider;
      final pid = candidate.pid ?? 0;
      if (pid <= 0) return;

      debugPrint('Auto-attaching writable session: $provider PID $pid');

      final params = <String, dynamic>{
        'nodeId': nodeId,
        'provider': provider,
        'pid': pid,
        'workDir': candidate.workDir,
        'name': '$provider-attached-$pid',
      };

      await client.call('session.attach', params);

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
          Consumer(
            builder: (context, ref, _) {
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
            },
          ),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: Column(
        children: [
          if (_connectStatus != null)
            MaterialBanner(
              backgroundColor: _connectStatusIsError
                  ? Theme.of(context).colorScheme.errorContainer
                  : Theme.of(context).colorScheme.primaryContainer,
              content: Text(
                _connectStatus!,
                style: TextStyle(
                  color: _connectStatusIsError
                      ? Theme.of(context).colorScheme.onErrorContainer
                      : Theme.of(context).colorScheme.onPrimaryContainer,
                ),
              ),
              leading: _connecting
                  ? const SizedBox(
                      width: 16,
                      height: 16,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : Icon(
                      _connectStatusIsError
                          ? Icons.error_outline
                          : Icons.check_circle_outline,
                    ),
              actions: [
                if (_connectStatusIsError && _lastFailedConfig != null)
                  TextButton(
                    onPressed: () {
                      setState(() {
                        _connectStatus = null;
                        _connectStatusIsError = false;
                      });
                      showModalBottomSheet(
                        context: context,
                        isScrollControlled: true,
                        builder: (_) => _AddConnectionSheet(
                          initialUrl: _lastFailedConfig!.url,
                          initialToken: _lastFailedConfig!.token,
                          onConnect: _connect,
                        ),
                      );
                    },
                    child: const Text('编辑'),
                  ),
                TextButton(
                  onPressed: () {
                    setState(() {
                      _connectStatus = null;
                      _connectStatusIsError = false;
                    });
                  },
                  child: const Text('关闭'),
                ),
              ],
            ),
          Expanded(
            child: _saved.isEmpty
                ? Center(
                    child: Column(
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        const Icon(Icons.cable, size: 64, color: Colors.grey),
                        const SizedBox(height: 16),
                        const Text(
                          '无连接',
                          style: TextStyle(fontSize: 18, color: Colors.grey),
                        ),
                        const SizedBox(height: 16),
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            ElevatedButton.icon(
                              onPressed: _showAddSheet,
                              icon: const Icon(Icons.add),
                              label: const Text('手动添加'),
                            ),
                            const SizedBox(width: 12),
                            ElevatedButton.icon(
                              onPressed: _connecting ? null : _scanQrCode,
                              icon: const Icon(Icons.qr_code_scanner),
                              label: const Text('扫码连接'),
                            ),
                          ],
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
                            final isThisConnecting =
                                _connecting && _connectingUrl == cfg.url;
                            return ListTile(
                              leading: isThisConnecting
                                  ? const SizedBox(
                                      width: 20,
                                      height: 20,
                                      child: CircularProgressIndicator(
                                        strokeWidth: 2,
                                      ),
                                    )
                                  : const Icon(Icons.hub),
                              title: Text(cfg.url),
                              subtitle: Text(
                                '${cfg.token.substring(0, cfg.token.length > 8 ? 8 : cfg.token.length)}...',
                              ),
                              trailing: Row(
                                mainAxisSize: MainAxisSize.min,
                                children: [
                                  IconButton(
                                    icon: const Icon(Icons.edit, size: 20),
                                    onPressed: _connecting
                                        ? null
                                        : () => _showEditSheet(cfg, i),
                                    tooltip: '编辑',
                                  ),
                                  IconButton(
                                    icon: const Icon(
                                      Icons.delete,
                                      size: 20,
                                      color: Colors.red,
                                    ),
                                    onPressed: _connecting
                                        ? null
                                        : () => _deleteConnection(i),
                                    tooltip: '删除',
                                  ),
                                  const Icon(Icons.chevron_right),
                                ],
                              ),
                              onTap: _connecting ? null : () => _connect(cfg),
                            );
                          },
                        ),
                      ),
                      const Divider(height: 1),
                      Padding(
                        padding: const EdgeInsets.all(12),
                        child: Row(
                          children: [
                            Expanded(
                              child: ElevatedButton.icon(
                                onPressed: _connecting ? null : _showAddSheet,
                                icon: const Icon(Icons.add),
                                label: const Text('新建连接'),
                                style: ElevatedButton.styleFrom(
                                  minimumSize: const Size.fromHeight(48),
                                ),
                              ),
                            ),
                            const SizedBox(width: 8),
                            ElevatedButton.icon(
                              onPressed: _connecting ? null : _scanQrCode,
                              icon: const Icon(Icons.qr_code_scanner),
                              label: const Text('扫码'),
                              style: ElevatedButton.styleFrom(
                                minimumSize: const Size.fromHeight(48),
                              ),
                            ),
                          ],
                        ),
                      ),
                    ],
                  ),
          ),
        ],
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: _connecting ? null : _scanQrCode,
        tooltip: '扫码连接',
        child: const Icon(Icons.qr_code_scanner),
      ),
    );
  }
}

class _AddConnectionSheet extends StatefulWidget {
  final Future<void> Function(ConnectionConfig) onConnect;
  final String initialUrl;
  final String initialToken;

  const _AddConnectionSheet({
    required this.onConnect,
    this.initialUrl = 'ws://',
    this.initialToken = '',
  });

  @override
  State<_AddConnectionSheet> createState() => _AddConnectionSheetState();
}

class _AddConnectionSheetState extends State<_AddConnectionSheet> {
  late final _urlCtrl = TextEditingController(text: widget.initialUrl);
  late final _tokenCtrl = TextEditingController(text: widget.initialToken);
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
          const Text(
            '添加连接',
            style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold),
          ),
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
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
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
          const Text(
            '编辑连接',
            style: TextStyle(fontSize: 20, fontWeight: FontWeight.bold),
          ),
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
                ? const SizedBox(
                    height: 20,
                    width: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  )
                : const Text('保存'),
          ),
        ],
      ),
    );
  }
}

/// QR Scanner page using mobile_scanner.
class _QrScannerPage extends StatefulWidget {
  const _QrScannerPage();

  @override
  State<_QrScannerPage> createState() => _QrScannerPageState();
}

class _QrScannerPageState extends State<_QrScannerPage> {
  final MobileScannerController _controller = MobileScannerController();
  bool _processed = false;

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('扫描二维码')),
      body: Stack(
        children: [
          MobileScanner(
            controller: _controller,
            onDetect: (capture) {
              if (_processed) return;
              for (final barcode in capture.barcodes) {
                if (barcode.rawValue != null) {
                  _processed = true;
                  _controller.stop();
                  Navigator.pop(context, barcode.rawValue);
                  return;
                }
              }
            },
          ),
          // Scanner overlay
          Center(
            child: Container(
              width: 250,
              height: 250,
              decoration: BoxDecoration(
                border: Border.all(color: Colors.white, width: 3),
                borderRadius: BorderRadius.circular(12),
              ),
            ),
          ),
          Positioned(
            bottom: 80,
            left: 0,
            right: 0,
            child: Text(
              '将二维码对准框内',
              textAlign: TextAlign.center,
              style: TextStyle(
                color: Colors.white,
                fontSize: 16,
                shadows: [Shadow(blurRadius: 4, color: Colors.black.withOpacity(0.8))],
              ),
            ),
          ),
        ],
      ),
    );
  }
}