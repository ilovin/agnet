import 'dart:async';
import 'dart:convert';
import 'dart:math';

import 'package:web_socket_channel/web_socket_channel.dart';

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

/// Exponential backoff calculator, doubles delay each call, capped at [maxSeconds].
class ReconnectBackoff {
  final int maxSeconds;
  int _current = 1;

  ReconnectBackoff({this.maxSeconds = 30});

  Duration next() {
    final d = Duration(seconds: _current);
    _current = min(_current * 2, maxSeconds);
    return d;
  }

  void reset() => _current = 1;
}

typedef EventCallback = void Function(WsMessage event);

/// JSON-RPC 2.0 WebSocket client with automatic exponential-backoff reconnection.
class WsClient {
  final String url;
  final String token;

  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  bool _disposed = false;
  bool _reconnecting = false; // single-flight reconnect guard

  int _nextId = 1;
  final Map<int, Completer<dynamic>> _pending = {};
  final List<EventCallback> _eventListeners = [];
  final ReconnectBackoff _backoff = ReconnectBackoff();

  // Heartbeat
  Timer? _pingTimer;
  Timer? _pingTimeout;
  static const _pingInterval = Duration(seconds: 25);
  static const _pingTimeoutDuration = Duration(seconds: 10);

  // Connection state stream
  final _connectedCtrl = StreamController<bool>.broadcast();
  Stream<bool> get onConnectionChanged => _connectedCtrl.stream;
  bool _connected = false;
  bool get isConnected => _connected;

  WsClient({required this.url, required this.token});

  /// Connect and start listening. Reconnects automatically on disconnect.
  Future<void> connect({Duration timeout = const Duration(seconds: 8)}) async {
    if (_disposed) return;
    try {
      final uri = Uri.parse(url).replace(queryParameters: {'token': token});
      _channel = WebSocketChannel.connect(uri);
      await _channel!.ready.timeout(
        timeout,
        onTimeout: () {
          throw TimeoutException('WebSocket handshake timeout');
        },
      );
      _connected = true;
      _connectedCtrl.add(true);
      _backoff.reset();
      _reconnecting = false;

      _startPingTimer();

      _sub = _channel!.stream.listen(
        _onData,
        onError: (_) => _scheduleReconnect(),
        onDone: _scheduleReconnect,
        cancelOnError: true,
      );
    } catch (e) {
      _scheduleReconnect();
      rethrow;
    }
  }

  void _onData(dynamic raw) {
    WsMessage msg;
    try {
      msg = parseMessage(raw as String);
    } catch (_) {
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
    if (_disposed) return;
    if (_reconnecting) return; // single-flight guard
    _reconnecting = true;

    _stopPingTimer();
    _connected = false;
    _connectedCtrl.add(false);
    _sub?.cancel();
    _channel?.sink.close();
    _channel = null;

    // Fail all pending RPC calls immediately
    _failAllPending('disconnected');

    final delay = _backoff.next();
    Future.delayed(delay, () {
      if (!_disposed) connect();
    });
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

  // ── Heartbeat (application-level JSON-RPC ping) ─────────────────────────

  void _startPingTimer() {
    _stopPingTimer();
    _pingTimer = Timer.periodic(_pingInterval, (_) => _sendPing());
  }

  void _stopPingTimer() {
    _pingTimer?.cancel();
    _pingTimer = null;
    _pingTimeout?.cancel();
    _pingTimeout = null;
  }

  void _sendPing() {
    if (!_connected || _channel == null) return;
    final id = _nextId++;
    final completer = Completer<dynamic>();
    _pending[id] = completer;
    final raw = jsonEncode(buildRequest(id, 'rpc.ping', {}));
    _channel?.sink.add(raw);

    // Start timeout: if no response within 10s, force reconnect
    _pingTimeout?.cancel();
    _pingTimeout = Timer(_pingTimeoutDuration, () {
      _pending.remove(id);
      if (!completer.isCompleted) {
        completer.completeError('ping timeout');
      }
      _scheduleReconnect();
    });

    // On successful pong response, cancel the timeout
    completer.future.then((_) {
      _pingTimeout?.cancel();
      _pingTimeout = null;
    }).catchError((_) {
      // Error already handled by timeout or disconnect
    });
  }

  /// Register a callback for server push events.
  void onEvent(EventCallback cb) => _eventListeners.add(cb);

  /// Send a JSON-RPC request and return the result (or throw on error).
  Future<dynamic> call(
    String method,
    Map<String, dynamic> params, {
    Duration timeout = const Duration(seconds: 10),
  }) {
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
    _stopPingTimer();
    _sub?.cancel();
    _channel?.sink.close();
    _failAllPending('disposed');
    _connectedCtrl.close();
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
