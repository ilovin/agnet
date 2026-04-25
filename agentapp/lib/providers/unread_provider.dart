import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../services/ws_client.dart';
import 'conversation_provider.dart';

class UnreadNotifier extends StateNotifier<Map<ConversationKey, int>> {
  UnreadNotifier() : super(const {});

  // Track seen msg_ids per conversation to avoid duplicate counting
  // for streaming message_update events.
  final Map<ConversationKey, Set<String>> _seenMsgIds = {};

  void handleEvent(WsMessage event) {
    if (event.method != 'conversation.message' &&
        event.method != 'conversation.message_update') {
      return;
    }
    final params = event.params as Map<String, dynamic>?;
    if (params == null) return;

    final role = params['role'] as String?;
    if (role != null && role != 'assistant') return;

    // Skip tool calls and tool results
    final kind = params['kind'] as String?;
    if (kind == 'tool_use' || kind == 'tool_result') return;

    // Skip permission requests
    if (kind == 'permission_request') return;

    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    if (nodeId.isEmpty || agentId.isEmpty) return;

    final key = (nodeId, agentId);

    // Deduplicate by msg_id for message_update events
    if (event.method == 'conversation.message_update') {
      final msgId = params['msg_id'] as String?;
      if (msgId != null) {
        final seen = _seenMsgIds.putIfAbsent(key, () => <String>{});
        if (seen.contains(msgId)) return;
        seen.add(msgId);
      }
    }

    state = {...state, key: (state[key] ?? 0) + 1};
  }

  void markAsRead(String nodeId, String agentId) {
    final key = (nodeId, agentId);
    if (!state.containsKey(key)) return;
    final next = Map<ConversationKey, int>.from(state);
    next.remove(key);
    state = next;
  }

  void clear() {
    state = const {};
    _seenMsgIds.clear();
  }
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
