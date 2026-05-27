import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/widgets/composer/composer_plus_button.dart';

Widget _wrap(Widget child) => MaterialApp(
      home: Scaffold(body: Center(child: child)),
    );

void main() {
  group('ComposerPlusButton', () {
    testWidgets('renders a + IconButton', (tester) async {
      await tester.pumpWidget(_wrap(
        ComposerPlusButton(
          onPickImage: () {},
          onShowSpecialKeys: () {},
        ),
      ));
      expect(find.byKey(const Key('composer-plus-button')), findsOneWidget);
      expect(find.byIcon(Icons.add), findsOneWidget);
    });

    testWidgets('disabled when both callbacks are null', (tester) async {
      await tester.pumpWidget(_wrap(
        const ComposerPlusButton(
          onPickImage: null,
          onShowSpecialKeys: null,
        ),
      ));
      final btn = tester.widget<IconButton>(
        find.byKey(const Key('composer-plus-button')),
      );
      expect(btn.onPressed, isNull);
    });

    testWidgets('tapping opens a bottom sheet with two grid items',
        (tester) async {
      await tester.pumpWidget(_wrap(
        ComposerPlusButton(
          onPickImage: () {},
          onShowSpecialKeys: () {},
        ),
      ));
      await tester.tap(find.byKey(const Key('composer-plus-button')));
      await tester.pumpAndSettle();

      expect(find.byKey(const Key('composer-plus-image')), findsOneWidget);
      expect(find.byKey(const Key('composer-plus-special')), findsOneWidget);
      expect(find.text('图片'), findsOneWidget);
      expect(find.text('特殊按键'), findsOneWidget);
    });

    testWidgets('image cell triggers the pickImage callback', (tester) async {
      var pickedImage = 0;
      var pressedSpecial = 0;
      await tester.pumpWidget(_wrap(
        ComposerPlusButton(
          onPickImage: () => pickedImage++,
          onShowSpecialKeys: () => pressedSpecial++,
        ),
      ));
      await tester.tap(find.byKey(const Key('composer-plus-button')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('composer-plus-image')));
      await tester.pumpAndSettle();

      expect(pickedImage, 1);
      expect(pressedSpecial, 0);
    });

    testWidgets('special-keys cell triggers the special-keys callback',
        (tester) async {
      var pickedImage = 0;
      var pressedSpecial = 0;
      await tester.pumpWidget(_wrap(
        ComposerPlusButton(
          onPickImage: () => pickedImage++,
          onShowSpecialKeys: () => pressedSpecial++,
        ),
      ));
      await tester.tap(find.byKey(const Key('composer-plus-button')));
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(const Key('composer-plus-special')));
      await tester.pumpAndSettle();

      expect(pressedSpecial, 1);
      expect(pickedImage, 0);
    });

    testWidgets('rotates icon while sheet is visible', (tester) async {
      await tester.pumpWidget(_wrap(
        ComposerPlusButton(
          onPickImage: () {},
          onShowSpecialKeys: () {},
        ),
      ));
      // Before tap: AnimatedRotation present (turns: 0).
      final preTap = tester.widget<AnimatedRotation>(
        find.byType(AnimatedRotation),
      );
      expect(preTap.turns, 0.0);

      await tester.tap(find.byKey(const Key('composer-plus-button')));
      // Pump intermediate frame so the rotation widget rebuilds with the
      // new turn value.
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 50));

      final mid = tester.widget<AnimatedRotation>(
        find.byType(AnimatedRotation),
      );
      expect(mid.turns, 0.125);

      // Dismiss sheet to allow tearDown.
      await tester.tapAt(const Offset(10, 10));
      await tester.pumpAndSettle();
    });
  });
}
