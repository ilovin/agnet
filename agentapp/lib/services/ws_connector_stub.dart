import 'package:web_socket_channel/web_socket_channel.dart';

Future<WebSocketChannel> platformConnect(Uri uri, {Map<String, dynamic>? headers}) =>
    throw UnsupportedError('Cannot create WebSocketChannel on this platform');
