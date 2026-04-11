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
    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    final key = (nodeId, agentId);
    final existing = List<MessageModel>.from(state[key] ?? []);
    final role = (params['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant;
    final text = params['text'] as String? ?? '';
    final isPartial = params['partial'] as bool? ?? false;
    final isFinal = params['final'] as bool? ?? false;

    // For streaming assistant messages, update the last message if it's also streaming
    if (isPartial && role == MessageRole.assistant) {
      if (existing.isNotEmpty && existing.last.role == MessageRole.assistant) {
        // Append to the last assistant message
        final last = existing.last;
        existing[existing.length - 1] = MessageModel(
          nodeId: nodeId,
          agentId: agentId,
          role: role,
          text: last.text + text,
          seq: last.seq,
        );
        state = {...state, key: existing};
        return;
      }
    }

    // For final message with full content, replace any partial streaming message
    if (isFinal && role == MessageRole.assistant) {
      if (existing.isNotEmpty && existing.last.role == MessageRole.assistant) {
        // Replace the last assistant message with the final full text
        final last = existing.last;
        existing[existing.length - 1] = MessageModel(
          nodeId: nodeId,
          agentId: agentId,
          role: role,
          text: text,
          seq: last.seq,
        );
        state = {...state, key: existing};
        return;
      }
    }

    // Default: create a new message
    final seq = (params['seq'] as num?)?.toInt() ??
        (existing.isEmpty ? 0 : (existing.last.seq + 1));
    final msg = MessageModel(
      nodeId: nodeId,
      agentId: agentId,
      role: role,
      text: text,
      seq: seq,
    );
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
