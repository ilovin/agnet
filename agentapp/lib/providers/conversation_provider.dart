import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/message_model.dart';
import '../services/ws_client.dart';

/// Key: (nodeId, agentId, sessionId).
///
/// `sessionId` is the third component so cached conversation state is
/// scoped per session. When the underlying agent rotates its
/// `sessionId` (e.g. resume to a new session), old entries become
/// orphaned naturally and new entries land under a fresh key — no
/// cross-session bleed-through. An empty string is allowed and used as
/// a sentinel when the session id is not yet known (callers should
/// avoid writing to an unknown-session key when a real id is available).
typedef ConversationKey = (String, String, String);

class ConversationNotifier extends StateNotifier<Map<ConversationKey, List<MessageModel>>> {
  ConversationNotifier() : super(const {});

  /// Load history from [conversation.history] response.
  void loadHistory(
    String nodeId,
    String agentId,
    String sessionId,
    List<dynamic> rawMessages,
  ) {
    final key = (nodeId, agentId, sessionId);
    final messages = rawMessages
        .map((m) => MessageModel.fromJson(m as Map<String, dynamic>))
        .toList()
      ..sort((a, b) => a.seq.compareTo(b.seq));
    state = {...state, key: messages};
  }

  void mergeHistory(
    String nodeId,
    String agentId,
    String sessionId,
    List<dynamic> rawMessages,
  ) {
    final key = (nodeId, agentId, sessionId);
    final merged = <int, MessageModel>{
      for (final message in state[key] ?? const <MessageModel>[]) message.seq: message,
    };
    for (final raw in rawMessages) {
      final message = MessageModel.fromJson(raw as Map<String, dynamic>);
      final local = merged[message.seq];
      if (local != null) {
        final localTs = local.timestamp;
        final rpcTs = message.timestamp;
        if (localTs != null && rpcTs != null) {
          // Both have timestamps: newer wins, equal timestamps preserve local (WS-updated)
          if (localTs < rpcTs) {
            merged[message.seq] = message;
          }
          // else: local >= rpcTs => keep local
        } else if (localTs == null && rpcTs == null) {
          // Neither has timestamp: RPC wins (latest snapshot, no WS update involved)
          merged[message.seq] = message;
        }
        // else: one has timestamp, the other doesn't => keep local
        // (local with timestamp but RPC without: local is WS-enriched)
        // (local without timestamp but RPC with: local is WS-updated text)
      } else {
        // New message from RPC
        merged[message.seq] = message;
      }
    }
    final messages = merged.values.toList()
      ..sort((a, b) => a.seq.compareTo(b.seq));
    state = {...state, key: messages};
  }

  /// Handle push events: [conversation.message], [conversation.message_update], and [conversation.cleared].
  ///
  /// `sessionId` is read from `event.params['sessionId']` when present
  /// (e.g. `conversation.cleared` carries it from the backend). For
  /// other events that lack the field today, the empty-string sentinel
  /// is used so messages are still bucketed but stay separated from
  /// session-bound entries written by `loadHistory`/`mergeHistory`.
  void handleEvent(WsMessage event) {
    if (event.method == 'conversation.cleared') {
      final params = event.params as Map<String, dynamic>?;
      if (params != null) {
        final nodeId = params['nodeId'] as String? ?? '';
        final agentId = params['agentId'] as String? ?? '';
        final sessionId = params['sessionId'] as String? ?? '';
        clear(nodeId, agentId, sessionId);
      }
      return;
    }
    if (event.method == 'conversation.message_update') {
      _handleMessageUpdate(event);
      return;
    }
    if (event.method != 'conversation.message') return;
    final params = event.params as Map<String, dynamic>;
    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    final sessionId = params['sessionId'] as String? ?? '';
    final key = (nodeId, agentId, sessionId);
    final existing = List<MessageModel>.from(state[key] ?? []);
    final role = (params['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant;
    final text = params['text'] as String? ?? '';
    final isPartial = params['partial'] as bool? ?? false;
    final isFinal = params['final'] as bool? ?? false;
    final msgId = params['msg_id'] as String? ?? '';

    // For streaming assistant messages, update the last message if it's also streaming
    if (isPartial && role == MessageRole.assistant) {
      if (existing.isNotEmpty && existing.last.role == MessageRole.assistant) {
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

    final seq = (params['seq'] as num?)?.toInt() ??
        (existing.isEmpty ? 0 : (existing.last.seq + 1));
    final msg = MessageModel(
      nodeId: nodeId,
      agentId: agentId,
      role: role,
      text: text,
      seq: seq,
      msgId: msgId,
    );
    _appendMessage(msg, sessionId);
  }

  void _handleMessageUpdate(WsMessage event) {
    final params = event.params as Map<String, dynamic>;
    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    final sessionId = params['sessionId'] as String? ?? '';
    final msgId = params['msg_id'] as String? ?? '';
    final newText = params['text'] as String? ?? '';
    final newSeq = (params['seq'] as num?)?.toInt() ?? 0;
    final key = (nodeId, agentId, sessionId);
    final existing = List<MessageModel>.from(state[key] ?? []);

    // Find message by msg_id and update its text
    bool updated = false;
    for (int i = 0; i < existing.length; i++) {
      if (existing[i].msgId == msgId && msgId.isNotEmpty) {
        existing[i] = existing[i].copyWith(text: newText, seq: newSeq);
        updated = true;
        break;
      }
    }

    if (updated) {
      state = {...state, key: existing};
    }
  }

  void _appendMessage(MessageModel msg, String sessionId) {
    final key = (msg.nodeId, msg.agentId, sessionId);
    final existing = List<MessageModel>.from(state[key] ?? []);
    // Deduplicate by seq
    if (existing.any((m) => m.seq == msg.seq)) return;
    existing.add(msg);
    existing.sort((a, b) => a.seq.compareTo(b.seq));
    state = {...state, key: existing};
  }

  List<MessageModel> messagesFor(
    String nodeId,
    String agentId,
    String sessionId,
  ) =>
      state[(nodeId, agentId, sessionId)] ?? [];

  void clear(String nodeId, String agentId, String sessionId) {
    final key = (nodeId, agentId, sessionId);
    if (!state.containsKey(key)) return;
    state = {...state}..remove(key);
  }
}

final conversationProvider =
    StateNotifierProvider<ConversationNotifier, Map<ConversationKey, List<MessageModel>>>(
  (_) => ConversationNotifier(),
);
