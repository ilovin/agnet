import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_colors.dart';
import 'package:agentapp/widgets/app_bar/bypass_indicator.dart';

Widget _wrap(Widget child) => MaterialApp(home: Scaffold(body: child));

void main() {
  group('BypassIndicator', () {
    testWidgets('renders mode label in square brackets, uppercased',
        (tester) async {
      await tester.pumpWidget(
        _wrap(const BypassIndicator(modeLabel: 'Bypass')),
      );
      expect(find.text('[BYPASS]'), findsOneWidget);
    });

    testWidgets('renders Auto / Plan / Build modes', (tester) async {
      for (final m in const ['Auto', 'Plan', 'Build']) {
        await tester.pumpWidget(_wrap(BypassIndicator(modeLabel: m)));
        expect(find.text('[${m.toUpperCase()}]'), findsOneWidget);
      }
    });

    testWidgets('invokes onTap when pressed', (tester) async {
      var taps = 0;
      await tester.pumpWidget(
        _wrap(BypassIndicator(modeLabel: 'Bypass', onTap: () => taps++)),
      );
      await tester.tap(find.text('[BYPASS]'));
      await tester.pump();
      expect(taps, 1);
    });

    testWidgets('uses accent stroke around the chip', (tester) async {
      await tester.pumpWidget(
        _wrap(const BypassIndicator(modeLabel: 'Bypass')),
      );
      // Find a Container under the indicator with accent border.
      final containers = tester.widgetList<Container>(find.descendant(
        of: find.byType(BypassIndicator),
        matching: find.byType(Container),
      ));
      bool found = false;
      for (final c in containers) {
        final dec = c.decoration;
        if (dec is BoxDecoration && dec.border != null) {
          final top = (dec.border! as Border).top;
          if (top.color == AppColors.accent) {
            found = true;
            break;
          }
        }
      }
      expect(found, isTrue, reason: 'expected accent-coloured border');
    });
  });
}
