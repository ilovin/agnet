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
      notifier.loadHistory('n1', 'a1', [
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hi', 'seq': 2},
        {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ]);
      final msgs = notifier.messagesFor('n1', 'a1');
      expect(msgs.length, equals(2));
      expect(msgs[0].seq, equals(1));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('handleEvent conversation.message appends new message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      final msgs = notifier.messagesFor('n1', 'a1');
      expect(msgs.length, equals(1));
      expect(msgs[0].text, equals('Hello!'));
    });

    test('handleEvent deduplicates by seq', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hello!', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1').length, equals(1));
    });

    test('messages for different agents are isolated', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'A', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a2', 'role': 'user', 'text': 'B', 'seq': 1},
      ));
      expect(notifier.messagesFor('n1', 'a1').length, equals(1));
      expect(notifier.messagesFor('n1', 'a2').length, equals(1));
    });

    test('handleEvent appends partial assistant messages to last message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hi', 'partial': true, 'seq': 2},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': ' there', 'partial': true, 'seq': 3},
      ));
      final msgs = notifier.messagesFor('n1', 'a1');
      expect(msgs.length, equals(2)); // user + 1 streaming assistant
      expect(msgs[1].text, equals('Hi there'));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('handleEvent final message replaces streaming message', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hi', 'partial': true, 'seq': 2},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'assistant', 'text': 'Hi there!', 'final': true, 'seq': 3},
      ));
      final msgs = notifier.messagesFor('n1', 'a1');
      expect(msgs.length, equals(2)); // user + final assistant
      expect(msgs[1].text, equals('Hi there!'));
      expect(msgs[1].role, equals(MessageRole.assistant));
    });

    test('handleEvent partial does not append to user messages', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': 'Hello', 'seq': 1},
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {'nodeId': 'n1', 'agentId': 'a1', 'role': 'user', 'text': ' world', 'partial': true, 'seq': 2},
      ));
      final msgs = notifier.messagesFor('n1', 'a1');
      expect(msgs.length, equals(2)); // user messages are not combined
      expect(msgs[0].text, equals('Hello'));
      expect(msgs[1].text, equals(' world'));
    });
  });
}
