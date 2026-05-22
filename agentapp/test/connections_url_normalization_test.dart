import 'package:agentapp/screens/connections_screen.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  group('normalizeGatewayWsUrl', () {
    test('appends /ws when ws url path is empty', () {
      expect(
        normalizeGatewayWsUrl('ws://localhost:7374'),
        equals('ws://localhost:7374/ws'),
      );
    });

    test('appends /ws when wss url path is slash only', () {
      expect(
        normalizeGatewayWsUrl('wss://example.com/'),
        equals('wss://example.com/ws'),
      );
    });

    test('keeps url unchanged when path is already non-root', () {
      expect(
        normalizeGatewayWsUrl('ws://localhost:7374/ws/custom'),
        equals('ws://localhost:7374/ws/custom'),
      );
    });

    test('keeps non-ws schemes unchanged', () {
      expect(
        normalizeGatewayWsUrl('http://localhost:7374'),
        equals('http://localhost:7374'),
      );
    });
  });
}
