import 'dart:io';

import 'package:web_socket_channel/web_socket_channel.dart';
import 'package:web_socket_channel/io.dart';

WebSocketChannel platformConnect(Uri uri, {Map<String, dynamic>? headers}) {
  final client = HttpClient();
  client.badCertificateCallback = (_, __, ___) => true;
  return IOWebSocketChannel.connect(uri, customClient: client, headers: headers);
}
