import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/message_model.dart';
import '../services/ws_client.dart';

/// Key: (nodeId, agentId)
typedef ConversationKey = (String, String);

class ConversationNotifier extends StateNotifier<Map<ConversationKey, List<MessageModel>>> {
  ConversationNotifier() : super(const {});

  /// Load history from [conversation.history] response.
  void loadHistory(String nodeId, String agentId, List<dynamic> rawMessages) {
    final key = (nodeId, agentId);
    final messages = rawMessages
        .map((m) => MessageModel.fromJson(m as Map<String, dynamic>))
        .toList()
      ..sort((a, b) => a.seq.compareTo(b.seq));
    state = {...state, key: messages};
  }

  /// Handle a [conversation.message] push event.
  void handleEvent(WsMessage event) {
    if (event.method != 'conversation.message') return;
    final params = event.params as Map<String, dynamic>;
    final msg = MessageModel.fromJson(params);
    _appendMessage(msg);
  }

  void _appendMessage(MessageModel msg) {
    final key = (msg.nodeId, msg.agentId);
    final existing = List<MessageModel>.from(state[key] ?? []);
    // Deduplicate by seq
    if (existing.any((m) => m.seq == msg.seq)) return;
    existing.add(msg);
    existing.sort((a, b) => a.seq.compareTo(b.seq));
    state = {...state, key: existing};
  }

  List<MessageModel> messagesFor(String nodeId, String agentId) =>
      state[(nodeId, agentId)] ?? [];
}

final conversationProvider =
    StateNotifierProvider<ConversationNotifier, Map<ConversationKey, List<MessageModel>>>(
  (_) => ConversationNotifier(),
);
