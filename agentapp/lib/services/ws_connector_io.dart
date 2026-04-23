import 'dart:developer' as dev;
import 'dart:io';

import 'package:flutter/foundation.dart' show defaultTargetPlatform, TargetPlatform;
import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/io.dart';

import 'native_ws_channel.dart';

Future<WebSocketChannel> platformConnect(
  Uri uri, {
  Map<String, dynamic>? headers,
}) async {
  if (defaultTargetPlatform == TargetPlatform.android && uri.scheme == 'wss') {
    dev.log('[ws_connector] using native OkHttp WebSocket for $uri');
    Object? nativeError;
    try {
      return await NativeWebSocketChannel.connect(uri.toString());
    } catch (e) {
      nativeError = e;
      dev.log('[ws_connector] native ws failed, falling back to dart: $e');
    }

    try {
      return await _connectWithDart(uri, headers: headers);
    } catch (e) {
      throw Exception('native ws failed: $nativeError; dart ws failed: $e');
    }
  }

  return _connectWithDart(uri, headers: headers);
}

Future<WebSocketChannel> _connectWithDart(Uri uri, {Map<String, dynamic>? headers}) async {
  dev.log('[ws_connector] connecting to $uri');

  final client = HttpClient();
  client.badCertificateCallback = (cert, host, port) => true;

  client.findProxy = (url) {
    final env = Platform.environment;
    final proxy = env['https_proxy'] ?? env['HTTPS_PROXY'] ??
                  env['http_proxy'] ?? env['HTTP_PROXY'];
    if (proxy != null && proxy.isNotEmpty) {
      dev.log('[ws_connector] using proxy from env: $proxy');
      if (proxy.startsWith('http://') || proxy.startsWith('https://')) {
        return 'PROXY ${proxy.replaceFirst(RegExp(r'^https?://'), '')}';
      }
      return 'PROXY $proxy';
    }
    return 'DIRECT';
  };

  final ws = await WebSocket.connect(
    uri.toString(),
    customClient: client,
    headers: headers?.cast<String, dynamic>(),
  );
  dev.log('[ws_connector] WebSocket connected');
  return IOWebSocketChannel(ws);
}
