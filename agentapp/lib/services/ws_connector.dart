import 'package:web_socket_channel/web_socket_channel.dart';

import 'ws_connector_stub.dart'
    if (dart.library.io) 'ws_connector_io.dart'
    if (dart.library.html) 'ws_connector_web.dart';

Future<WebSocketChannel> connect(Uri uri, {Map<String, dynamic>? headers}) =>
    platformConnect(uri, headers: headers);
