import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/connection_config.dart';
import '../services/connection_store.dart';
import '../services/ws_client.dart';

final connectionStoreProvider = Provider<ConnectionStore>((_) => ConnectionStore());

/// Connection state exposed to the UI.
enum WsConnectionState { disconnected, connecting, connected }

/// Holds [WsClient] lifecycle: null = disconnected.
class ConnectionNotifier extends StateNotifier<WsClient?> {
  final ConnectionStore _store;
  StreamSubscription? _connSub;
  WsConnectionState _connectionState = WsConnectionState.disconnected;
  WsConnectionState get connectionState => _connectionState;

  // Broadcast state changes so widgets can listen
  final _stateCtrl = StreamController<WsConnectionState>.broadcast();
  Stream<WsConnectionState> get onStateChanged => _stateCtrl.stream;

  ConnectionNotifier(this._store) : super(null);

  StreamSubscription<bool>? _reconnectSub;

  /// Connect to [config], replacing any existing connection.
  Future<void> connect(ConnectionConfig config) async {
    state?.dispose();
    _connSub?.cancel();
    _reconnectSub?.cancel();
    _setConnectionState(WsConnectionState.connecting);
    final client = WsClient(url: config.url, token: config.token);
    state = client;

    // Listen for connection state changes from the client
    _connSub = client.onConnectionChanged.listen((connected) {
      _setConnectionState(connected
          ? WsConnectionState.connected
          : WsConnectionState.disconnected);
    });

    // Listen for reconnecting state so UI can show "..." instead of error banners
    _reconnectSub = client.onReconnecting.listen((reconnecting) {
      if (reconnecting) {
        _setConnectionState(WsConnectionState.connecting);
      }
    });

    await client.connect();
    _setConnectionState(WsConnectionState.connected);

    // Persist this connection and mark as last used
    final existing = await _store.load();
    final alreadySaved = existing.any((c) => c.url == config.url);
    if (!alreadySaved) {
      await _store.save([...existing, config]);
    }
    await _store.setLastUsedUrl(config.url);
  }

  void _setConnectionState(WsConnectionState s) {
    if (_connectionState == s) return;
    _connectionState = s;
    _stateCtrl.add(s);
  }

  void disconnect() {
    _connSub?.cancel();
    _reconnectSub?.cancel();
    state?.dispose();
    state = null;
    _setConnectionState(WsConnectionState.disconnected);
  }

  @override
  void dispose() {
    _connSub?.cancel();
    _reconnectSub?.cancel();
    _stateCtrl.close();
    state?.dispose();
    super.dispose();
  }
}

final connectionProvider = StateNotifierProvider<ConnectionNotifier, WsClient?>(
  (ref) => ConnectionNotifier(ref.watch(connectionStoreProvider)),
);

/// Streams the current [WsConnectionState] from the active
/// [ConnectionNotifier]. Initial value comes from the notifier's snapshot;
/// subsequent changes are pushed via the broadcast stream so widgets can
/// rebuild reactively (e.g. the AppBar connection-status indicator).
final connectionStateProvider =
    StreamProvider<WsConnectionState>((ref) async* {
  final notifier = ref.watch(connectionProvider.notifier);
  yield notifier.connectionState;
  yield* notifier.onStateChanged;
});
