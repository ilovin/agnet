import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:stream_channel/stream_channel.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'package:agentapp/services/ws_client.dart';

Future<void> flushAsync([int turns = 6]) async {
  for (var i = 0; i < turns; i++) {
    await Future<void>.delayed(Duration.zero);
  }
}

class FakeReconnectTimer implements Timer {
  FakeReconnectTimer(this._callback);

  final void Function() _callback;
  bool _isActive = true;

  void fire() {
    if (!_isActive) return;
    _isActive = false;
    _callback();
  }

  @override
  void cancel() {
    _isActive = false;
  }

  @override
  bool get isActive => _isActive;

  @override
  int get tick => 0;
}

class FakeWebSocketSink implements WebSocketSink {
  FakeWebSocketSink(this._messages, this._onClose);

  final List<dynamic> _messages;
  final Future<void> Function(int? closeCode, String? closeReason) _onClose;
  final Completer<void> _done = Completer<void>();
  bool _closed = false;

  @override
  void add(dynamic event) {
    if (_closed) {
      throw StateError('sink is closed');
    }
    _messages.add(event);
  }

  @override
  Future<void> addStream(Stream<dynamic> stream) async {
    await for (final event in stream) {
      add(event);
    }
  }

  @override
  void addError(Object error, [StackTrace? stackTrace]) {
    if (_closed) {
      throw StateError('sink is closed');
    }
  }

  @override
  Future<void> close([int? closeCode, String? closeReason]) async {
    if (_closed) return;
    _closed = true;
    await _onClose(closeCode, closeReason);
    if (!_done.isCompleted) {
      _done.complete();
    }
  }

  @override
  Future<void> get done => _done.future;
}

class FakeWebSocketChannel with StreamChannelMixin<dynamic> implements WebSocketChannel {
  FakeWebSocketChannel({Future<void>? ready}) : ready = ready ?? Future.value() {
    sink = FakeWebSocketSink(sentMessages, (closeCode, closeReason) async {
      this.closeCode = closeCode;
      this.closeReason = closeReason;
      await _controller.close();
    });
  }

  final StreamController<dynamic> _controller = StreamController<dynamic>();
  final List<dynamic> sentMessages = [];

  @override
  int? closeCode;

  @override
  String? closeReason;

  @override
  String? protocol;

  @override
  final Future<void> ready;

  @override
  late final FakeWebSocketSink sink;

  @override
  Stream<dynamic> get stream => _controller.stream;

  Future<void> disconnect() => _controller.close();

  void emit(dynamic event) {
    _controller.add(event);
  }
}

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
      final backoff = ReconnectBackoff(maxSeconds: 30, maxJitterMs: 0);
      expect(backoff.next().inSeconds, equals(1));
      expect(backoff.next().inSeconds, equals(2));
      expect(backoff.next().inSeconds, equals(4));
      expect(backoff.next().inSeconds, equals(8));
      expect(backoff.next().inSeconds, equals(16));
      expect(backoff.next().inSeconds, equals(30)); // capped
      expect(backoff.next().inSeconds, equals(30)); // stays capped
    });

    test('reset restarts from 1', () {
      final backoff = ReconnectBackoff(maxSeconds: 30, maxJitterMs: 0);
      backoff.next();
      backoff.next();
      backoff.reset();
      expect(backoff.next().inSeconds, equals(1));
    });
  });

  group('WsClient reconnect', () {
    test('keeps retrying after a failed reconnect attempt', () async {
      final firstChannel = FakeWebSocketChannel();
      final thirdChannel = FakeWebSocketChannel();
      final timers = <FakeReconnectTimer>[];
      final delays = <Duration>[];
      final states = <bool>[];
      var connectCount = 0;

      final client = WsClient(
        url: 'ws://example.com/ws',
        token: 'secret-token',
        channelConnector: (uri, {headers}) async {
          expect(uri.queryParameters['token'], equals('secret-token'));
          connectCount++;
          if (connectCount == 1) return firstChannel;
          if (connectCount == 2) {
            throw StateError('handshake failed');
          }
          return thirdChannel;
        },
        backoff: ReconnectBackoff(maxJitterMs: 0),
        reconnectTimerFactory: (delay, callback) {
          delays.add(delay);
          final timer = FakeReconnectTimer(callback);
          timers.add(timer);
          return timer;
        },
      );
      addTearDown(client.dispose);

      client.onConnectionChanged.listen(states.add);

      await client.connect();
      await flushAsync();
      expect(connectCount, equals(1));
      expect(states, equals([true]));

      await firstChannel.disconnect();
      await flushAsync();
      expect(states, equals([true, false]));
      expect(delays, equals([const Duration(seconds: 1)]));
      expect(timers, hasLength(1));

      timers.removeAt(0).fire();
      await flushAsync();
      expect(connectCount, equals(2));
      expect(states, equals([true, false]));
      expect(delays, equals([
        const Duration(seconds: 1),
        const Duration(seconds: 2),
      ]));
      expect(timers, hasLength(1));

      timers.removeAt(0).fire();
      await flushAsync();
      expect(connectCount, equals(3));
      expect(states, equals([true, false, true]));
      expect(client.isConnected, isTrue);
    });

    test('dispose cancels a pending reconnect timer', () async {
      final firstChannel = FakeWebSocketChannel();
      final timers = <FakeReconnectTimer>[];
      var connectCount = 0;

      final client = WsClient(
        url: 'ws://example.com/ws',
        token: 'secret-token',
        channelConnector: (_, {headers}) async {
          connectCount++;
          return firstChannel;
        },
        backoff: ReconnectBackoff(maxJitterMs: 0),
        reconnectTimerFactory: (delay, callback) {
          final timer = FakeReconnectTimer(callback);
          timers.add(timer);
          return timer;
        },
      );

      await client.connect();
      await firstChannel.disconnect();
      await flushAsync();

      expect(timers, hasLength(1));
      expect(timers.single.isActive, isTrue);

      client.dispose();
      expect(timers.single.isActive, isFalse);

      timers.single.fire();
      await flushAsync();
      expect(connectCount, equals(1));
    });
  });
}
