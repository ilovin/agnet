import 'dart:async';
import 'dart:convert';
import 'dart:developer' as dev;
import 'dart:math';

import 'package:web_socket_channel/web_socket_channel.dart';

import 'ws_connector.dart' as ws_connector;

/// A single JSON-RPC message received from the server.
/// Can be a response (has id + result/error) or a push event (has method, no id).
class WsMessage {
  final dynamic id; // null for events
  final String? method; // set for push events
  final dynamic result; // set for successful responses
  final dynamic error; // set for error responses
  final dynamic params; // set for push events

  const WsMessage({this.id, this.method, this.result, this.error, this.params});
}

/// Exponential backoff calculator with jitter.
/// Sequence: 1s, 2s, 4s, 8s, 16s, 30s, 30s...
class ReconnectBackoff {
  final int maxSeconds;
  final int initialSeconds;
  final int maxJitterMs;
  int _current;

  ReconnectBackoff({
    this.maxSeconds = 30,
    this.initialSeconds = 1,
    this.maxJitterMs = 500,
  }) : _current = initialSeconds;

  Duration next() {
    final base = Duration(seconds: _current);
    _current = _current == 0 ? 0 : min(_current * 2, maxSeconds);
    final jitter = maxJitterMs <= 0
        ? Duration.zero
        : Duration(milliseconds: Random().nextInt(maxJitterMs + 1));
    return base + jitter;
  }

  void reset() => _current = initialSeconds;
}

typedef EventCallback = void Function(WsMessage event);
typedef ChannelConnector = Future<WebSocketChannel> Function(Uri uri, {Map<String, dynamic>? headers});
typedef ReconnectTimerFactory = Timer Function(
  Duration delay,
  void Function() callback,
);

Timer _defaultReconnectTimerFactory(
  Duration delay,
  void Function() callback,
) => Timer(delay, callback);

/// JSON-RPC 2.0 WebSocket client with automatic exponential-backoff reconnection.
class WsClient {
  final String url;
  final String token;
  final ChannelConnector _channelConnector;
  final ReconnectBackoff _backoff;
  final ReconnectTimerFactory _reconnectTimerFactory;

  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  bool _disposed = false;
  bool _reconnecting = false; // single-flight reconnect guard
  bool _manualDisconnect = false;
  Timer? _reconnectTimer;

  int _nextId = 1;
  final Map<int, Completer<dynamic>> _pending = {};
  final List<EventCallback> _eventListeners = [];

  // Event-driven keepalive: only ping when stale before sending
  Timer? _pingTimeout;
  DateTime _lastReceivedAt = DateTime.now();
  static const _staleThreshold = Duration(seconds: 60);
  static const _pingTimeoutDuration = Duration(seconds: 10);

  // Connection state stream
  final _connectedCtrl = StreamController<bool>.broadcast();
  Stream<bool> get onConnectionChanged => _connectedCtrl.stream;
  bool _connected = false;
  bool get isConnected => _connected;

  // Reconnecting state stream
  final _reconnectingCtrl = StreamController<bool>.broadcast();
  Stream<bool> get onReconnecting => _reconnectingCtrl.stream;

  WsClient({
    required this.url,
    required this.token,
    ChannelConnector? channelConnector,
    ReconnectBackoff? backoff,
    ReconnectTimerFactory? reconnectTimerFactory,
  }) : _channelConnector = channelConnector ?? _defaultConnector,
       _backoff = backoff ?? ReconnectBackoff(),
       _reconnectTimerFactory = reconnectTimerFactory ?? _defaultReconnectTimerFactory;

  static Future<WebSocketChannel> _defaultConnector(Uri uri, {Map<String, dynamic>? headers}) {
    return ws_connector.connect(uri, headers: headers);
  }

  /// Resolve domain to IP for wss:// on port 8443 to bypass SNI-based DPI blocking.
  /// On web this is a no-op (browser handles DNS/SNI).
  static Future<Uri> _resolveIfNeeded(Uri uri) async {
    return uri;
  }

  /// Connect and start listening. Reconnects automatically on disconnect.
  Future<void> connect({Duration timeout = const Duration(seconds: 15)}) async {
    if (_disposed) return;
    if (_connected && _channel != null) return;

    _manualDisconnect = false;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    try {
      var uri = Uri.parse(url);
      // Resolve domain to IP for port 8443 to bypass SNI-based DPI blocking
      uri = await _resolveIfNeeded(uri);
      // Pass token via Authorization header (IO) and query parameter (web).
      // Browsers cannot set custom headers on WebSocket connections, so the
      // query param is required for the web build.
      final headers = {'Authorization': 'Bearer $token'};
      if (!uri.queryParameters.containsKey('token')) {
        uri = uri.replace(
          queryParameters: {...uri.queryParameters, 'token': token},
        );
      }
      final channel = await _channelConnector(uri, headers: headers);
      _channel = channel;
      await channel.ready.timeout(
        timeout,
        onTimeout: () {
          throw TimeoutException('WebSocket handshake timeout');
        },
      );
      _connected = true;
      _connectedCtrl.add(true);
      _reconnectingCtrl.add(false);
      _backoff.reset();
      _reconnecting = false;

      _lastReceivedAt = DateTime.now();

      _sub = channel.stream.listen(
        _onData,
        onError: (err) {
          dev.log('[WsClient] stream error: $err');
          _scheduleReconnect();
        },
        onDone: () {
          dev.log('[WsClient] stream done, connected=$_connected');
          _scheduleReconnect();
        },
        cancelOnError: true,
      );
    } catch (e) {
      dev.log('[WsClient] connect error: $e (url=$url)');
      _reconnecting = false;
      _scheduleReconnect();
      // Rethrow so callers know the initial connection failed.
      rethrow;
    }
  }

  void _onData(dynamic raw) {
    dev.log('[WsClient] onData: ${raw.runtimeType} len=${raw is String ? raw.length : '?'}');
    _lastReceivedAt = DateTime.now();
    WsMessage msg;
    try {
      msg = parseMessage(raw as String);
    } catch (e) {
      dev.log('[WsClient] parse error: $e, raw=$raw');
      return;
    }

    // Push event: has method, no id
    if (msg.method != null) {
      for (final cb in List.of(_eventListeners)) {
        cb(msg);
      }
      return;
    }

    // Response: has id
    if (msg.id != null) {
      final id = (msg.id as num).toInt();
      final completer = _pending.remove(id);
      if (completer == null) return;
      if (msg.error != null) {
        completer.completeError(msg.error!);
      } else {
        completer.complete(msg.result);
      }
    }
  }

  void _scheduleReconnect() {
    if (_disposed || _manualDisconnect) return;
    if (_reconnecting) return; // single-flight guard
    _reconnecting = true;
    final delay = _backoff.next();
    dev.log('[WsClient] scheduling reconnect in ${delay.inSeconds}s');

    _teardownTransport();
    _reconnectingCtrl.add(true);

    _reconnectTimer?.cancel();
    _reconnectTimer = _reconnectTimerFactory(delay, () {
      if (!_disposed && !_manualDisconnect) {
        Future<void>(() async {
          try {
            await connect();
          } catch (_) {}
        });
      }
    });
  }

  void _teardownTransport() {
    _stopPingTimeout();
    if (_connected) {
      _connected = false;
      _connectedCtrl.add(false);
    } else {
      _connected = false;
    }
    _sub?.cancel();
    _sub = null;
    try {
      _channel?.sink.close();
    } catch (_) {}
    _channel = null;

    // Fail all pending RPC calls immediately
    _failAllPending('disconnected');
  }

  /// Fail all pending RPC completers with an error.
  void _failAllPending(String reason) {
    final pending = Map<int, Completer<dynamic>>.from(_pending);
    _pending.clear();
    for (final completer in pending.values) {
      if (!completer.isCompleted) {
        completer.completeError(reason);
      }
    }
  }

  // ── Event-driven keepalive ─────────────────────────────────────────────

  void _stopPingTimeout() {
    _pingTimeout?.cancel();
    _pingTimeout = null;
  }

  bool get _isStale =>
      DateTime.now().difference(_lastReceivedAt) > _staleThreshold;

  Future<void> _ensureAlive() async {
    if (!_connected || _channel == null) return;
    if (!_isStale) return;
    final id = _nextId++;
    final completer = Completer<dynamic>();
    _pending[id] = completer;
    final raw = jsonEncode(buildRequest(id, 'rpc.ping', {}));
    _channel?.sink.add(raw);

    _pingTimeout?.cancel();
    _pingTimeout = Timer(_pingTimeoutDuration, () {
      _pending.remove(id);
      if (!completer.isCompleted) {
        completer.completeError('ping timeout');
      }
      _scheduleReconnect();
    });

    try {
      await completer.future;
      _pingTimeout?.cancel();
      _pingTimeout = null;
    } catch (_) {}
  }

  /// Register a callback for server push events.
  void onEvent(EventCallback cb) => _eventListeners.add(cb);

  /// Send a JSON-RPC request and return the result (or throw on error).
  Future<dynamic> call(
    String method,
    Map<String, dynamic> params, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    await _ensureAlive();
    final id = _nextId++;
    final completer = Completer<dynamic>();
    _pending[id] = completer;
    final raw = jsonEncode(buildRequest(id, method, params));
    _channel?.sink.add(raw);
    return completer.future.timeout(
      timeout,
      onTimeout: () {
        _pending.remove(id);
        throw TimeoutException('RPC timeout: $method');
      },
    );
  }

  /// Fire-and-forget send.
  void send(String method, Map<String, dynamic> params) {
    final id = _nextId++;
    final raw = jsonEncode(buildRequest(id, method, params));
    _channel?.sink.add(raw);
  }

  void dispose() {
    _disposed = true;
    _manualDisconnect = true;
    _reconnectTimer?.cancel();
    _reconnectTimer = null;
    _teardownTransport();
    _connectedCtrl.close();
    _reconnectingCtrl.close();
  }

  // ── Static helpers (testable without network) ────────────────────────────

  /// Build a JSON-RPC 2.0 request map.
  static Map<String, dynamic> buildRequest(
    int id,
    String method,
    Map<String, dynamic> params,
  ) => {'jsonrpc': '2.0', 'id': id, 'method': method, 'params': params};

  /// Parse a raw JSON string into a [WsMessage].
  static WsMessage parseMessage(String raw) {
    final map = jsonDecode(raw) as Map<String, dynamic>;
    return WsMessage(
      id: map['id'],
      method: map['method'] as String?,
      result: map['result'],
      error: map['error'],
      params: map['params'],
    );
  }
}
