import 'dart:async';
import 'dart:developer' as dev;

import 'package:async/async.dart';
import 'package:flutter/services.dart';
import 'package:stream_channel/stream_channel.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

class NativeWebSocketChannel extends StreamChannelMixin
    implements WebSocketChannel {
  static const _methodChannel = MethodChannel('com.phonetalk.agentapp/native_ws');
  static const _eventChannel = EventChannel('com.phonetalk.agentapp/native_ws_events');

  static Stream? _sharedStream;
  static Stream _getEventStream() {
    return _sharedStream ??= _eventChannel.receiveBroadcastStream();
  }

  final int _id;
  final _readyCompleter = Completer<void>();
  final _controller = StreamChannelController<Object?>(sync: true);
  late final StreamSubscription _sub;

  @override
  int? get closeCode => _closeCode;
  int? _closeCode;

  @override
  String? get closeReason => _closeReason;
  String? _closeReason;

  @override
  String? get protocol => null;

  @override
  Future<void> get ready => _readyCompleter.future;

  @override
  Stream get stream => _controller.foreign.stream;

  @override
  late final WebSocketSink sink = _NativeSink(this, _controller.foreign.sink);

  NativeWebSocketChannel._(this._id) {
    _sub = _getEventStream().listen(_onEvent, onError: _onError);
  }

  static Future<NativeWebSocketChannel> connect(String url) async {
    final id = await _methodChannel.invokeMethod<int>('connect', {'url': url});
    if (id == null) throw Exception('native ws connect returned null');
    final channel = NativeWebSocketChannel._(id);
    return channel;
  }

  void _onEvent(dynamic event) {
    print('[NativeWSChannel] _onEvent: $event (myId=$_id)');
    if (event is! Map) return;
    final connId = event['id'];
    if (connId != _id) return;

    final type = event['type'] as String?;
    print('[NativeWSChannel] matched id=$connId type=$type');
    switch (type) {
      case 'open':
        if (!_readyCompleter.isCompleted) _readyCompleter.complete();
      case 'message':
        if (!_readyCompleter.isCompleted) _readyCompleter.complete();
        _controller.local.sink.add(event['data']);
      case 'error':
        final err = Exception(event['error'] ?? 'unknown error');
        if (!_readyCompleter.isCompleted) _readyCompleter.completeError(err);
        _controller.local.sink.addError(err);
        _controller.local.sink.close();
      case 'closed':
        _closeCode ??= 1000;
        _controller.local.sink.close();
    }
  }

  void _onError(dynamic error) {
    if (!_readyCompleter.isCompleted) _readyCompleter.completeError(error);
    _controller.local.sink.addError(error);
    _controller.local.sink.close();
  }
}

class _NativeSink extends DelegatingStreamSink implements WebSocketSink {
  final NativeWebSocketChannel _channel;

  _NativeSink(this._channel, StreamSink inner) : super(inner);

  @override
  void add(Object? event) {
    NativeWebSocketChannel._methodChannel
        .invokeMethod('send', {'id': _channel._id, 'message': event.toString()});
  }

  @override
  Future close([int? closeCode, String? closeReason]) {
    _channel._sub.cancel();
    return NativeWebSocketChannel._methodChannel
        .invokeMethod('close', {'id': _channel._id});
  }
}
