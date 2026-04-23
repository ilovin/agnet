import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/ws_client.dart';
import 'conversation_provider.dart';

class UnreadNotifier extends StateNotifier<Map<ConversationKey, int>> {
  UnreadNotifier() : super(const {});

  void handleEvent(WsMessage event) {
    if (event.method != 'conversation.message') return;
    final params = event.params as Map<String, dynamic>?;
    if (params == null) return;

    final role = params['role'] as String?;
    if (role != 'assistant') return;

    // Skip tool calls and tool results
    final kind = params['kind'] as String?;
    if (kind == 'tool_use' || kind == 'tool_result') return;

    // Skip permission requests
    if (kind == 'permission_request') return;

    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    if (nodeId.isEmpty || agentId.isEmpty) return;

    final key = (nodeId, agentId);
    state = {...state, key: (state[key] ?? 0) + 1};
  }

  void markAsRead(String nodeId, String agentId) {
    final key = (nodeId, agentId);
    if (!state.containsKey(key)) return;
    final next = Map<ConversationKey, int>.from(state);
    next.remove(key);
    state = next;
  }

  void clear() => state = const {};
}

final unreadProvider =
    StateNotifierProvider<UnreadNotifier, Map<ConversationKey, int>>(
  (_) => UnreadNotifier(),
);

class UnreadSettingNotifier extends StateNotifier<bool> {
  UnreadSettingNotifier() : super(true) {
    _load();
  }

  static const _key = 'unread_badge_enabled';

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    state = prefs.getBool(_key) ?? true;
  }

  Future<void> set(bool enabled) async {
    state = enabled;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(_key, enabled);
  }
}

final unreadSettingProvider =
    StateNotifierProvider<UnreadSettingNotifier, bool>(
  (_) => UnreadSettingNotifier(),
);
