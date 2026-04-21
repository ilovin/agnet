import 'package:web_socket_channel/web_socket_channel.dart';

Future<WebSocketChannel> platformConnect(Uri uri, {Map<String, dynamic>? headers}) async =>
    WebSocketChannel.connect(uri);
