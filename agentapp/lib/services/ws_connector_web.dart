import 'package:web_socket_channel/web_socket_channel.dart';

WebSocketChannel platformConnect(Uri uri, {Map<String, dynamic>? headers}) => WebSocketChannel.connect(uri);
