import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_colors.dart';
import 'package:agentapp/widgets/app_bar/connection_status_indicator.dart';

Widget _wrap(Widget child) => MaterialApp(home: Scaffold(body: child));

Color? _firstDotColor(WidgetTester tester) {
  // Scan all Container widgets that have a circle BoxDecoration; return the
  // first such fill colour. The indicator emits exactly one of these.
  for (final c in tester.widgetList<Container>(find.byType(Container))) {
    final dec = c.decoration;
    if (dec is BoxDecoration && dec.shape == BoxShape.circle) {
      return dec.color;
    }
  }
  return null;
}

void main() {
  group('ConnectionStatusDot', () {
    testWidgets('connected uses accent colour', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.connected),
      ));
      expect(_firstDotColor(tester), AppColors.accent);
    });

    testWidgets('reconnecting uses warn amber', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.reconnecting),
      ));
      expect(_firstDotColor(tester), AppColors.warn);
      // Drain pulse animation before tearDown.
      await tester.pump(const Duration(milliseconds: 100));
    });

    testWidgets('disconnected uses error red', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.disconnected),
      ));
      expect(_firstDotColor(tester), AppColors.error);
    });

    testWidgets('unknown uses muted grey', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.unknown),
      ));
      expect(_firstDotColor(tester), const Color(0xFF6B7280));
    });

    testWidgets('renders at requested size', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(
          status: ConnectionStatus.connected,
          size: 16,
        ),
      ));
      final root = tester.widget<SizedBox>(
        find.byKey(const ValueKey('connection-status-dot-root')),
      );
      expect(root.width, 16);
      expect(root.height, 16);
    });

    testWidgets('reconnecting wraps dot in a FadeTransition', (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.reconnecting),
      ));
      // FadeTransition is added inside the Semantics subtree; assert one
      // exists *under* our root (MaterialApp itself emits unrelated
      // FadeTransitions for routing).
      final fadesUnderDot = find.descendant(
        of: find.byKey(const ValueKey('connection-status-dot-root')),
        matching: find.byType(FadeTransition),
      );
      expect(fadesUnderDot, findsOneWidget);
      await tester.pump(const Duration(milliseconds: 100));
    });

    testWidgets('non-reconnecting states have no FadeTransition under the dot',
        (tester) async {
      await tester.pumpWidget(_wrap(
        const ConnectionStatusDot(status: ConnectionStatus.connected),
      ));
      final fadesUnderDot = find.descendant(
        of: find.byKey(const ValueKey('connection-status-dot-root')),
        matching: find.byType(FadeTransition),
      );
      expect(fadesUnderDot, findsNothing);
    });
  });
}
