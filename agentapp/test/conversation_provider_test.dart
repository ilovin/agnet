import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/models/message_model.dart';
import 'package:agentapp/services/ws_client.dart';

void main() {
  group('ConversationNotifier', () {
    late ProviderContainer container;
    late ConversationNotifier notifier;

    setUp(() {
      container = ProviderContainer();
      notifier = container.read(conversationProvider.notifier);
    });

    tearDown(() => container.dispose());

    test('loadHistory populates messages sorted by seq', () {
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hi', 'seq': 2},
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ]);
      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(2));
      expect(msgs[0].seq, equals(1));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('mergeHistory refreshes duplicate seqs and appends newer messages', () {
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': '继续', 'seq': 1},
        {
          'nodeId': 'n1',
          'agentId': 'a1',
          'role': 'assistant',
          'text': 'interrupt',
          'seq': 2,
        },
      ]);

      notifier.mergeHistory('n1', 'a1', 's1', [
        {
          'nodeId': 'n1',
          'agentId': 'a1',
          'role': 'assistant',
          'text': '开始做界面',
          'seq': 2,
        },
        {
          'nodeId': 'n1',
          'agentId': 'a1',
          'role': 'assistant',
          'text': '最新进度',
          'seq': 3,
        },
      ]);

      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.map((m) => m.seq).toList(), equals([1, 2, 3]));
      expect(msgs[1].text, equals('开始做界面'));
      expect(msgs[2].text, equals('最新进度'));
    });

    test('handleEvent conversation.message appends new message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(1));
      expect(msgs[0].text, equals('Hello!'));
    });

    test('handleEvent without sessionId in params buckets under empty sentinel', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      // Falls back to '' sentinel until backend stamps sessionId on the event.
      expect(notifier.messagesFor('n1', 'a1', '').length, equals(1));
      // Real-session bucket stays empty.
      expect(notifier.messagesFor('n1', 'a1', 's1').length, equals(0));
    });

    test('handleEvent deduplicates by seq', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1', 's1').length, equals(1));
    });

    test('messages for different agents are isolated', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'A', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a2', 'sessionId': 's2', 'role': 'user', 'text': 'B', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1', 's1').length, equals(1));
      expect(notifier.messagesFor('n1', 'a2', 's2').length, equals(1));
    });

    test('messages for the same agent but different sessions stay isolated', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'old', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's2', 'role': 'user', 'text': 'new', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1', 's1').length, equals(1));
      expect(notifier.messagesFor('n1', 'a1', 's1')[0].text, equals('old'));
      expect(notifier.messagesFor('n1', 'a1', 's2').length, equals(1));
      expect(notifier.messagesFor('n1', 'a1', 's2')[0].text, equals('new'));
    });

    test('conversation.cleared with sessionId clears only that session bucket', () {
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'old', 'seq': 1},
      ]);
      notifier.loadHistory('n1', 'a1', 's2', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'new', 'seq': 1},
      ]);

      notifier.handleEvent(WsMessage(
        method: 'conversation.cleared',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1'},
      ));

      expect(notifier.messagesFor('n1', 'a1', 's1'), isEmpty);
      expect(notifier.messagesFor('n1', 'a1', 's2').length, equals(1));
    });

    test('handleEvent appends partial assistant messages to last message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hi', 'partial': true, 'seq': 2},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': ' there', 'partial': true, 'seq': 3},
      ));
      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(2)); // user + 1 streaming assistant
      expect(msgs[1].text, equals('Hi there'));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('handleEvent final message replaces streaming message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hi', 'partial': true, 'seq': 2},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'assistant', 'text': 'Hi there!', 'final': true, 'seq': 3},
      ));
      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(2)); // user + final assistant
      expect(msgs[1].text, equals('Hi there!'));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('handleEvent partial does not append to user messages', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': ' world', 'partial': true, 'seq': 2},
      ));
      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(2)); // user messages are not combined
      expect(msgs[0].text, equals('Hello'));
      expect(msgs[1].text, equals(' world'));
    });

    // ---- R-013: mergeHistory preserves WS-updated messages ----

    test('mergeHistory: WS message_update preserves newer text when RPC returns stale', () {
      // 1. Load initial history
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1, 'msg_id': 'm1', 'timestamp': 1000},
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'initial response', 'seq': 2, 'msg_id': 'm2', 'timestamp': 2000},
      ]);

      // 2. WS message_update updates text (simulates real-time edit)
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1',
          'msg_id': 'm2', 'text': 'updated response', 'seq': 2,
        },
      ));
      expect(notifier.messagesFor('n1', 'a1', 's1')[1].text, equals('updated response'));

      // 3. Polling RPC history returns stale text
      notifier.mergeHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1, 'msg_id': 'm1', 'timestamp': 1000},
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'initial response', 'seq': 2, 'msg_id': 'm2', 'timestamp': 2000},
      ]);

      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      // WS-updated text should be preserved since local has no timestamp on the updated message
      // (message_update does not set timestamp, so local.timestamp is null)
      // With both having timestamp=2000 on the original RPC vs null on WS update,
      // local (null timestamp) should be preserved per the tie-break rule.
      expect(msgs[1].text, equals('updated response'));
    });

    test('mergeHistory: new messages from history RPC are added', () {
      // 1. WS delivers first message
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'sessionId': 's1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1', 's1').length, equals(1));

      // 2. Polling RPC returns history with seq 1 + 2 (seq 2 is new)
      notifier.mergeHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1, 'timestamp': 1000},
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'History reply', 'seq': 2, 'timestamp': 2000},
      ]);

      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs.length, equals(2));
      expect(msgs[1].text, equals('History reply'));
      expect(msgs[1].seq, equals(2));
    });

    test('mergeHistory: local newer timestamp preserved over RPC older timestamp', () {
      // Local message has timestamp 3000 (newer), RPC returns timestamp 2000 (older)
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1, 'timestamp': 3000},
      ]);

      notifier.mergeHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Old text', 'seq': 1, 'timestamp': 2000},
      ]);

      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs[0].text, equals('Hello'), reason: 'local (newer timestamp) should win');
    });

    test('mergeHistory: RPC newer timestamp overwrites local older timestamp', () {
      // Local message has timestamp 1000 (older), RPC returns timestamp 2000 (newer)
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'old local', 'seq': 1, 'timestamp': 1000},
      ]);

      notifier.mergeHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'fresh from RPC', 'seq': 1, 'timestamp': 2000},
      ]);

      final msgs = notifier.messagesFor('n1', 'a1', 's1');
      expect(msgs[0].text, equals('fresh from RPC'), reason: 'RPC (newer timestamp) should win');
    });

    test('mergeHistory: equal or null timestamps preserves local message', () {
      // Both have timestamp 1000 — tie goes to local
      notifier.loadHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'local', 'seq': 1, 'timestamp': 1000},
      ]);

      notifier.mergeHistory('n1', 'a1', 's1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'rpc', 'seq': 1, 'timestamp': 1000},
      ]);

      expect(notifier.messagesFor('n1', 'a1', 's1')[0].text, equals('local'));
    });
  });
}
