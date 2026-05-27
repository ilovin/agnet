import 'package:agentapp/providers/unread_provider.dart';
import 'package:agentapp/services/ws_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  group('UnreadNotifier', () {
    late ProviderContainer container;
    late UnreadNotifier notifier;

    setUp(() {
      SharedPreferences.setMockInitialValues({});
      container = ProviderContainer();
      notifier = container.read(unreadProvider.notifier);
    });

    tearDown(() => container.dispose());

    test('counts assistant conversation.message events', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'hello',
        },
      ));

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(1));
    });

    test('counts assistant conversation.message_update events', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'hello again',
          'msg_id': 'm1',
        },
      ));

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(1));
    });

    test('events without sessionId fall back to empty sentinel', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'role': 'assistant',
          'text': 'hello',
        },
      ));

      expect(container.read(unreadProvider)[('n1', 'a1', '')], equals(1));
    });

    test('counts isolated per session', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'hello',
        },
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's2',
          'role': 'assistant',
          'text': 'next',
        },
      ));
      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(1));
      expect(container.read(unreadProvider)[('n1', 'a1', 's2')], equals(1));
    });

    test('ignores tool and permission assistant events', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'kind': 'tool_use',
          'text': 'tool call',
        },
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'kind': 'permission_request',
          'text': 'allow?',
        },
      ));

      expect(container.read(unreadProvider), isEmpty);
    });

    test('ignores non-assistant events', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'user',
          'text': 'ping',
        },
      ));

      expect(container.read(unreadProvider), isEmpty);
    });

    test('markAsRead removes unread count for one conversation', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'hello',
        },
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message',
        params: {
          'nodeId': 'n1',
          'agentId': 'a2',
          'sessionId': 's2',
          'role': 'assistant',
          'text': 'hello 2',
        },
      ));

      notifier.markAsRead('n1', 'a1', 's1');

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], isNull);
      expect(container.read(unreadProvider)[('n1', 'a2', 's2')], equals(1));
    });

    test('deduplicates conversation.message_update by msg_id', () {
      // Simulate streaming: three updates for the same msg_id
      for (var i = 0; i < 3; i++) {
        notifier.handleEvent(WsMessage(
          method: 'conversation.message_update',
          params: {
            'nodeId': 'n1',
            'agentId': 'a1',
            'sessionId': 's1',
            'role': 'assistant',
            'text': 'chunk $i',
            'msg_id': 'm1',
          },
        ));
      }

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(1));
    });

    test('counts different msg_ids separately', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'first',
          'msg_id': 'm1',
        },
      ));
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'second',
          'msg_id': 'm2',
        },
      ));

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(2));
    });

    test('does not re-count same msg_id after markAsRead', () {
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'first',
          'msg_id': 'm1',
        },
      ));
      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(1));

      // User reads the conversation
      notifier.markAsRead('n1', 'a1', 's1');
      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], isNull);

      // Late duplicate update for the same msg_id should not re-count
      notifier.handleEvent(WsMessage(
        method: 'conversation.message_update',
        params: {
          'nodeId': 'n1',
          'agentId': 'a1',
          'sessionId': 's1',
          'role': 'assistant',
          'text': 'first again',
          'msg_id': 'm1',
        },
      ));
      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], isNull);
    });

    test('counts message_update without msg_id every time', () {
      // Fallback: no msg_id means no deduplication
      for (var i = 0; i < 3; i++) {
        notifier.handleEvent(WsMessage(
          method: 'conversation.message_update',
          params: {
            'nodeId': 'n1',
            'agentId': 'a1',
            'sessionId': 's1',
            'role': 'assistant',
            'text': 'chunk $i',
          },
        ));
      }

      expect(container.read(unreadProvider)[('n1', 'a1', 's1')], equals(3));
    });
  });
}
