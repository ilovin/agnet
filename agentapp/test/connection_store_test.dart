import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:agentapp/services/connection_store.dart';
import 'package:agentapp/models/connection_config.dart';

void main() {
  group('ConnectionStore', () {
    setUp(() {
      SharedPreferences.setMockInitialValues({});
    });

    test('save and load round-trips correctly', () async {
      final store = ConnectionStore();
      final configs = [
        const ConnectionConfig(url: 'ws://localhost:7374', token: 'abc'),
        const ConnectionConfig(url: 'ws://remote:7374', token: 'xyz'),
      ];
      await store.save(configs);
      final loaded = await store.load();
      expect(loaded.length, equals(2));
      expect(loaded[0].url, equals('ws://localhost:7374'));
      expect(loaded[1].token, equals('xyz'));
    });

    test('load returns empty list when nothing saved', () async {
      final store = ConnectionStore();
      final loaded = await store.load();
      expect(loaded, isEmpty);
    });

    test('clear removes saved data', () async {
      final store = ConnectionStore();
      await store.save([const ConnectionConfig(url: 'ws://x', token: 't')]);
      await store.clear();
      final loaded = await store.load();
      expect(loaded, isEmpty);
    });

    test('last used url round-trips', () async {
      final store = ConnectionStore();
      expect(await store.getLastUsedUrl(), isNull);
      await store.setLastUsedUrl('ws://a/ws');
      expect(await store.getLastUsedUrl(), equals('ws://a/ws'));
    });
  });
}
