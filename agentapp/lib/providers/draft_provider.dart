import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Key: (nodeId, agentId)
typedef DraftKey = (String, String);

class DraftNotifier extends StateNotifier<Map<DraftKey, String>> {
  DraftNotifier() : super(const {});

  void setDraft(String nodeId, String agentId, String text) {
    final key = (nodeId, agentId);
    state = {...state, key: text};
  }

  String getDraft(String nodeId, String agentId) {
    return state[(nodeId, agentId)] ?? '';
  }

  void clearDraft(String nodeId, String agentId) {
    final key = (nodeId, agentId);
    if (state.containsKey(key)) {
      final next = Map<DraftKey, String>.from(state);
      next.remove(key);
      state = next;
    }
  }
}

final draftProvider = StateNotifierProvider<DraftNotifier, Map<DraftKey, String>>(
  (ref) => DraftNotifier(),
);
