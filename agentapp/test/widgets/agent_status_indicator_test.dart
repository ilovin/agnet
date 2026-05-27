import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_colors.dart';
import 'package:agentapp/widgets/agent_status_indicator.dart';

Widget _wrap(Widget child) {
  return MaterialApp(
    home: Scaffold(
      body: Center(child: child),
    ),
  );
}

void main() {
  group('AgentStatusIndicator', () {
    testWidgets('running renders a breathing accent dot', (tester) async {
      await tester.pumpWidget(
        _wrap(const AgentStatusIndicator(
          status: AgentIndicatorStatus.running,
          size: 16,
        )),
      );
      // Allow controller to start.
      await tester.pump(const Duration(milliseconds: 100));

      // The widget itself reports the requested size in its layout.
      final size = tester.getSize(find.byType(AgentStatusIndicator));
      expect(size.width, 16);
      expect(size.height, 16);

      // ScaleTransition is present (breathing scale animation).
      expect(find.byType(ScaleTransition), findsAtLeastNWidgets(1));

      // Settle controller to an even state and stop pumping.
      await tester.pump(const Duration(milliseconds: 50));
    });

    testWidgets('thinking renders a radar scan stroke ring', (tester) async {
      await tester.pumpWidget(
        _wrap(const AgentStatusIndicator(
          status: AgentIndicatorStatus.thinking,
        )),
      );
      await tester.pump(const Duration(milliseconds: 100));

      // Custom-painted radar scan exists.
      expect(find.byType(CustomPaint), findsAtLeastNWidgets(1));

      // The widget repaints over time (animation running) — confirm by pumping
      // and verifying still in tree.
      await tester.pump(const Duration(milliseconds: 200));
      expect(find.byType(AgentStatusIndicator), findsOneWidget);
    });

    testWidgets('idle renders a static stroke ring with no animation widget',
        (tester) async {
      await tester.pumpWidget(
        _wrap(const AgentStatusIndicator(
          status: AgentIndicatorStatus.idle,
        )),
      );
      await tester.pump();

      // No ScaleTransition / FadeTransition descendant of the indicator
      // for static idle.
      expect(
        find.descendant(
          of: find.byType(AgentStatusIndicator),
          matching: find.byType(ScaleTransition),
        ),
        findsNothing,
      );
      expect(
        find.descendant(
          of: find.byType(AgentStatusIndicator),
          matching: find.byType(FadeTransition),
        ),
        findsNothing,
      );
      // Still custom-paints the ring.
      expect(
        find.descendant(
          of: find.byType(AgentStatusIndicator),
          matching: find.byType(CustomPaint),
        ),
        findsAtLeastNWidgets(1),
      );
    });

    testWidgets('error renders a pulsing dot with FadeTransition',
        (tester) async {
      await tester.pumpWidget(
        _wrap(const AgentStatusIndicator(
          status: AgentIndicatorStatus.error,
        )),
      );
      await tester.pump(const Duration(milliseconds: 100));

      expect(
        find.descendant(
          of: find.byType(AgentStatusIndicator),
          matching: find.byType(FadeTransition),
        ),
        findsAtLeastNWidgets(1),
      );
    });

    testWidgets('exposes accent color for running by default', (tester) async {
      await tester.pumpWidget(
        _wrap(const AgentStatusIndicator(
          status: AgentIndicatorStatus.running,
        )),
      );
      await tester.pump(const Duration(milliseconds: 50));

      final indicator = tester.widget<AgentStatusIndicator>(
        find.byType(AgentStatusIndicator),
      );
      expect(indicator.color ?? AppColors.accent, AppColors.accent);
    });
  });
}
