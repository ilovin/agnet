import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/connection_config.dart';
import '../services/connection_store.dart';
import '../services/ws_client.dart';

final connectionStoreProvider = Provider<ConnectionStore>((_) => ConnectionStore());

/// Holds [WsClient] lifecycle: null = disconnected.
class ConnectionNotifier extends StateNotifier<WsClient?> {
  final ConnectionStore _store;

  ConnectionNotifier(this._store) : super(null);

  /// Connect to [config], replacing any existing connection.
  Future<void> connect(ConnectionConfig config) async {
    state?.dispose();
    final client = WsClient(url: config.url, token: config.token);
    state = client;
    await client.connect();
    // Persist this connection
    final existing = await _store.load();
    final alreadySaved = existing.any((c) => c.url == config.url);
    if (!alreadySaved) {
      await _store.save([...existing, config]);
    }
  }

  void disconnect() {
    state?.dispose();
    state = null;
  }

  @override
  void dispose() {
    state?.dispose();
    super.dispose();
  }
}

final connectionProvider = StateNotifierProvider<ConnectionNotifier, WsClient?>(
  (ref) => ConnectionNotifier(ref.watch(connectionStoreProvider)),
);
