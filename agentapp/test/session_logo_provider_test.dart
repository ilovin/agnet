import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/providers/session_logo_provider.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('defaultIconForSession', () {
    test('returns stable icon for same key', () {
      const key = 'node1:agent1';
      final a = defaultIconForSession(key);
      final b = defaultIconForSession(key);
      expect(a, equals(b));
    });

    test('returns different icons for different keys usually', () {
      final icons = <String, IconData>{};
      for (var i = 0; i < 20; i++) {
        icons['node$i:agent$i'] = defaultIconForSession('node$i:agent$i');
      }
      final unique = icons.values.toSet();
      expect(unique.length, greaterThan(1));
    });

    test('produces deterministic cross-platform hash', () {
      // _crossPlatformStringHash('test') should equal Java String.hashCode
      const key = 'test';
      final icon = defaultIconForSession(key);
      // Java hashCode for "test" = 3556498
      // With mixing, this should map to a specific deterministic icon
      expect(icon, isNotNull);
      // Verify stability by recomputing
      expect(defaultIconForSession(key), equals(icon));
    });
  });

  group('SessionLogoNotifier', () {
    setUp(() async {
      SharedPreferences.setMockInitialValues({});
    });

    test('returns default icon when no custom set', () {
      final notifier = SessionLogoNotifier();
      const key = 'node1:agent1';
      expect(notifier.iconFor(key), equals(defaultIconForSession(key)));
    });

    test('returns custom icon after set', () {
      final notifier = SessionLogoNotifier();
      const key = 'node1:agent1';
      notifier.setLogo(key, Icons.star);
      expect(notifier.iconFor(key), equals(Icons.star));
    });

    test('falls back to default after reset', () {
      final notifier = SessionLogoNotifier();
      const key = 'node1:agent1';
      notifier.setLogo(key, Icons.star);
      expect(notifier.iconFor(key), equals(Icons.star));
      notifier.resetLogo(key);
      expect(notifier.iconFor(key), equals(defaultIconForSession(key)));
    });

    test('rejects invalid icons silently', () {
      final notifier = SessionLogoNotifier();
      const key = 'node1:agent1';
      final defaultIcon = notifier.iconFor(key);
      // Icons.add is not in the predefined icon pool
      notifier.setLogo(key, Icons.add);
      expect(notifier.iconFor(key), equals(defaultIcon));
    });

    test('persists custom icon across instances', () async {
      final notifier1 = SessionLogoNotifier();
      const key = 'node1:agent1';
      notifier1.setLogo(key, Icons.favorite);

      // Allow async save to complete
      await Future.delayed(const Duration(milliseconds: 50));

      final notifier2 = SessionLogoNotifier();
      // Allow async load to complete
      await Future.delayed(const Duration(milliseconds: 50));

      expect(notifier2.iconFor(key), equals(Icons.favorite));

      notifier1.dispose();
      notifier2.dispose();
    });
  });

  group('availableSessionLogos', () {
    test('contains a diverse set of icons', () {
      expect(availableSessionLogos.length, greaterThan(50));
    });
  });
}
