import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/services/ws_client.dart';

void main() {
  group('WsClient.buildRequest', () {
    test('produces valid JSON-RPC 2.0 request map', () {
      final req = WsClient.buildRequest(42, 'agent.list', {'nodeId': 'n1'});
      expect(req['jsonrpc'], equals('2.0'));
      expect(req['id'], equals(42));
      expect(req['method'], equals('agent.list'));
      expect(req['params'], equals({'nodeId': 'n1'}));
    });
  });

  group('WsClient.parseMessage', () {
    test('parses a successful response', () {
      final msg = WsClient.parseMessage(
          '{"jsonrpc":"2.0","id":1,"result":{"agents":[]}}');
      expect(msg.id, equals(1));
      expect(msg.result, equals({'agents': []}));
      expect(msg.error, isNull);
      expect(msg.method, isNull);
    });

    test('parses a push event', () {
      final msg = WsClient.parseMessage(
          '{"jsonrpc":"2.0","method":"agent.status_changed","params":{"agentId":"a1","status":"working"}}');
      expect(msg.method, equals('agent.status_changed'));
      expect(msg.id, isNull);
      expect(msg.params['agentId'], equals('a1'));
    });

    test('parses an error response', () {
      final msg = WsClient.parseMessage(
          '{"jsonrpc":"2.0","id":2,"error":{"code":-32600,"message":"Invalid Request"}}');
      expect(msg.id, equals(2));
      expect(msg.error, isNotNull);
      expect(msg.error['code'], equals(-32600));
    });
  });

  group('ReconnectBackoff', () {
    test('doubles delay each call, capped at maxSeconds', () {
      final backoff = ReconnectBackoff(maxSeconds: 30);
      expect(backoff.next().inSeconds, equals(1));
      expect(backoff.next().inSeconds, equals(2));
      expect(backoff.next().inSeconds, equals(4));
      expect(backoff.next().inSeconds, equals(8));
      expect(backoff.next().inSeconds, equals(16));
      expect(backoff.next().inSeconds, equals(30)); // capped
      expect(backoff.next().inSeconds, equals(30)); // stays capped
    });

    test('reset restarts from 1', () {
      final backoff = ReconnectBackoff(maxSeconds: 30);
      backoff.next();
      backoff.next();
      backoff.reset();
      expect(backoff.next().inSeconds, equals(1));
    });
  });
}
