import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:http/http.dart' as http;
import 'package:mobile_scanner/mobile_scanner.dart';
import 'dashboard_screen.dart';
import '../models/connection_config.dart';
import '../providers/connection_provider.dart';
import '../providers/nodes_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/unread_provider.dart';
import '../providers/health_provider.dart';
import '../theme/app_spacing.dart';
import '../widgets/app_bar/mission_control_app_bar.dart';

class ConnectionProbeResult {
  final int? statusCode;
  final String? body;
  final Object? error;

  const ConnectionProbeResult.response(this.statusCode, this.body) : error = null;
  const ConnectionProbeResult.failure(this.error)
      : statusCode = null,
        body = null;

  bool get isReachable => statusCode != null;
}

Uri connectionProbeUri(String wsUrl) {
  final uri = Uri.parse(wsUrl);
  final scheme = switch (uri.scheme) {
    'ws' => 'http',
    'wss' => 'https',
    _ => uri.scheme,
  };
  return uri.replace(scheme: scheme);
}

String normalizeGatewayWsUrl(String rawUrl) {
  final trimmed = rawUrl.trim();
  Uri uri;
  try {
    uri = Uri.parse(trimmed);
  } catch (_) {
    return trimmed;
  }

  final isWsScheme = uri.scheme == 'ws' || uri.scheme == 'wss';
  final isRootPath = uri.path.isEmpty || uri.path == '/';
  if (!isWsScheme || !isRootPath) return trimmed;

  return uri.replace(path: '/ws').toString();
}

bool shouldProbeConnectionError(Object error) {
  final lower = error.toString().toLowerCase();
  if (isDefinitiveConnectionError(error)) return false;
  return lower.contains('ws error') ||
      lower.contains('websocket error') ||
      lower.contains('closed: 1006') ||
      lower.contains('close code 1006') ||
      lower.contains('native ws') ||
      lower.contains('handshake timeout') ||
      lower.contains('timed out') ||
      error is TimeoutException;
}

bool isDefinitiveConnectionError(Object error) {
  final lower = error.toString().toLowerCase();
  return lower.contains('401') ||
      lower.contains('unauthorized') ||
      lower.contains('forbidden') ||
      lower.contains('403') ||
      lower.contains('failed host lookup') ||
      lower.contains('nodename nor servname provided') ||
      lower.contains('no route to host') ||
      lower.contains('network is unreachable') ||
      lower.contains('connection refused') ||
      lower.contains('actively refused') ||
      lower.contains('connection reset') ||
      lower.contains('404 not found') ||
      lower.contains('not upgraded to websocket');
}

String friendlyConnectError(
  Object error,
  ConnectionConfig cfg, {
  ConnectionProbeResult? probeResult,
}) {
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
    return '连接失败：无法连接到服务器。请检查网络、域名/IP、Tailscale 或代理是否在线。';
  }

  if (lower.contains('connection refused') ||
      lower.contains('actively refused') ||
      lower.contains('connection reset')) {
    return '连接失败：服务器端口未开放或服务未启动。请检查 agentgw/agentd 进程和端口监听。';
  }

  if ((lower.contains('websocket') && lower.contains('404')) ||
      lower.contains('404 not found') ||
      lower.contains('not upgraded to websocket')) {
    return '连接失败：WebSocket 路径错误。请确认 URL 使用 /ws 或远端代理的 /ws/<userId>。';
  }

  final probedMessage = friendlyConnectProbeMessage(probeResult);
  if (probedMessage != null) return probedMessage;

  if (error is TimeoutException ||
      lower.contains('handshake timeout') ||
      lower.contains('timed out')) {
    return '连接失败：连接超时。通常是网络不通或端口无响应，请先检查连通性和端口。';
  }

  return '连接失败：$raw';
}

String? friendlyConnectProbeMessage(ConnectionProbeResult? result) {
  if (result == null) return null;
  if (!result.isReachable) {
    return '连接失败：无法连接到服务器。请检查网络、域名/IP、Tailscale 或代理是否在线。';
  }

  final body = result.body?.toLowerCase() ?? '';
  final bodyJson = parseProbeBodyJson(result.body);
  final code = bodyJson?['code']?.toString().toUpperCase();
  final detail = bodyJson?['detail']?.toString();

  if (result.statusCode == 502 && (code == 'GW_OFFLINE' || body.contains('agentgw offline'))) {
    if (detail != null && detail.isNotEmpty) {
      return '连接失败：服务器可达，但 agentgw offline（$detail）。请检查网关进程或隧道是否已连接。';
    }
    return '连接失败：服务器可达，但 agentgw offline。请检查网关进程或隧道是否已连接。';
  }

  return '连接失败：服务器可达，但 WebSocket 握手失败（HTTP ${result.statusCode}）。请检查 URL 路径、代理升级配置或 token。';
}

Map<String, dynamic>? parseProbeBodyJson(String? body) {
  if (body == null || body.trim().isEmpty) return null;
  try {
    final decoded = jsonDecode(body);
    if (decoded is Map<String, dynamic>) return decoded;
    if (decoded is Map) return decoded.cast<String, dynamic>();
  } catch (_) {}
  return null;
}

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
  const ConnectionsScreen({super.key, this.probeClient});

  final http.Client? probeClient;

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
    configs.sort((a, b) => a.url.toLowerCase().compareTo(b.url.toLowerCase()));
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
    } on MissingPluginException catch (e) {
      // Web platform does not implement this method channel
      debugPrint('getLaunchExtras not available on this platform: ${e.message}');
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
        if (ref.read(unreadSettingProvider)) {
          ref.read(unreadProvider.notifier).handleEvent(event);
        }
      });

      // Register auto-refresh callback for newly discovered agents
      ref.read(nodesProvider.notifier).onAgentsRefresh = (nodeId) async {
        try {
          final ar = await client.call('agent.list', {'nodeId': nodeId});
          final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
          ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
        } catch (_) {}
      };

      // Navigate immediately; dashboard _refreshAllNodes loads agents lazily
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
      final message = await _friendlyConnectError(e, cfg);
      if (!mounted) return false;
      setState(() {
        _connecting = false;
        _connectingUrl = null;
        _connectStatus = message;
        _connectStatusIsError = true;
        _lastFailedConfig = cfg;
      });
      return false;
    }
  }

  Future<ConnectionProbeResult?> _probeConnection(ConnectionConfig cfg) async {
    final client = widget.probeClient ?? http.Client();
    final ownsClient = widget.probeClient == null;
    try {
      final uri = connectionProbeUri(cfg.url);
      final response = await client.get(
        uri,
        headers: {'Authorization': 'Bearer ${cfg.token}'},
      ).timeout(const Duration(seconds: 3));
      return ConnectionProbeResult.response(response.statusCode, response.body);
    } catch (error) {
      return ConnectionProbeResult.failure(error);
    } finally {
      if (ownsClient) {
        client.close();
      }
    }
  }

  Future<String> _friendlyConnectError(
    Object error,
    ConnectionConfig cfg,
  ) async {
    ConnectionProbeResult? probeResult;
    if (shouldProbeConnectionError(error)) {
      probeResult = await _probeConnection(cfg);
    }
    return friendlyConnectError(error, cfg, probeResult: probeResult);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: MissionControlAppBar(
        title: 'Agent Manager',
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
                        const SizedBox(height: AppSpacing.lg),
                        Text(
                          '无连接',
                          style: Theme.of(context).textTheme.bodyLarge?.copyWith(
                                color: Colors.grey,
                              ),
                        ),
                        const SizedBox(height: AppSpacing.lg),
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            ElevatedButton.icon(
                              onPressed: _showAddSheet,
                              icon: const Icon(Icons.add),
                              label: const Text('手动添加'),
                            ),
                            const SizedBox(width: AppSpacing.md),
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
                              title: Text(
                                cfg.url,
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                softWrap: false,
                              ),
                              subtitle: Text(
                                '${cfg.token.substring(0, cfg.token.length > 8 ? 8 : cfg.token.length)}...',
                                maxLines: 1,
                                overflow: TextOverflow.ellipsis,
                                softWrap: false,
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
                        padding: const EdgeInsets.all(AppSpacing.md),
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
                            const SizedBox(width: AppSpacing.sm),
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
        url: normalizeGatewayWsUrl(_urlCtrl.text),
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
        left: AppSpacing.lg,
        right: AppSpacing.lg,
        top: AppSpacing.xl,
        bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.xl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            '添加连接',
            style: Theme.of(context).textTheme.titleMedium?.copyWith(
                  fontWeight: FontWeight.bold,
                ),
          ),
          const SizedBox(height: AppSpacing.lg),
          TextField(
            controller: _urlCtrl,
            decoration: const InputDecoration(
              labelText: 'Gateway URL',
              hintText: 'ws://192.168.1.x:7374/ws',
              border: OutlineInputBorder(),
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: AppSpacing.md),
          TextField(
            controller: _tokenCtrl,
            decoration: const InputDecoration(
              labelText: 'Token',
              border: OutlineInputBorder(),
            ),
            obscureText: true,
          ),
          if (_error != null) ...[
            const SizedBox(height: AppSpacing.sm),
            Text(_error!, style: const TextStyle(color: Colors.red)),
          ],
          const SizedBox(height: AppSpacing.lg),
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
        url: normalizeGatewayWsUrl(_urlCtrl.text),
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
        left: AppSpacing.lg,
        right: AppSpacing.lg,
        top: AppSpacing.xl,
        bottom: MediaQuery.of(context).viewInsets.bottom + AppSpacing.xl,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(
            '编辑连接',
            style: Theme.of(context).textTheme.titleMedium?.copyWith(
                  fontWeight: FontWeight.bold,
                ),
          ),
          const SizedBox(height: AppSpacing.lg),
          TextField(
            controller: _urlCtrl,
            decoration: const InputDecoration(
              labelText: 'Gateway URL',
              hintText: 'ws://192.168.1.x:7374/ws',
              border: OutlineInputBorder(),
            ),
            keyboardType: TextInputType.url,
          ),
          const SizedBox(height: AppSpacing.md),
          TextField(
            controller: _tokenCtrl,
            decoration: const InputDecoration(
              labelText: 'Token',
              border: OutlineInputBorder(),
            ),
            obscureText: true,
          ),
          if (_error != null) ...[
            const SizedBox(height: AppSpacing.sm),
            Text(_error!, style: const TextStyle(color: Colors.red)),
          ],
          const SizedBox(height: AppSpacing.lg),
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
      appBar: const MissionControlAppBar(title: '扫描二维码'),
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
              style: Theme.of(context).textTheme.bodyMedium?.copyWith(
                color: Colors.white,
                shadows: [Shadow(blurRadius: 4, color: Colors.black.withOpacity(0.8))],
              ),
            ),
          ),
        ],
      ),
    );
  }
}