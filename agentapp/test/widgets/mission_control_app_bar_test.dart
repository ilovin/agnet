import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_colors.dart';
import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';
import 'package:agentapp/widgets/app_bar/mission_control_app_bar.dart';
import 'package:agentapp/widgets/app_bar/mission_control_mark.dart';
import 'package:agentapp/widgets/app_bar/scanning_line.dart';

Widget _wrap(Widget child, {Brightness brightness = Brightness.light}) {
  return MaterialApp(
    theme: AppTheme.build(
      densityMode: DensityMode.standard,
      brightness: brightness,
    ),
    home: Scaffold(appBar: child as PreferredSizeWidget),
  );
}

void main() {
  group('MissionControlAppBar', () {
    testWidgets('renders Agent wordmark', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: false)),
      );
      expect(find.text('Agent'), findsOneWidget);
      expect(find.text('phone-talk'), findsNothing);
    });

    testWidgets('renders MissionControlMark', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: false)),
      );
      expect(find.byType(MissionControlMark), findsOneWidget);
    });

    testWidgets('renders title when provided', (tester) async {
      await tester.pumpWidget(
        _wrap(
          const MissionControlAppBar(
            title: '仪表盘',
            showScanningLine: false,
          ),
        ),
      );
      expect(find.text('仪表盘'), findsOneWidget);
    });

    testWidgets('omits title slot when title is null', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: false)),
      );
      expect(find.text('仪表盘'), findsNothing);
    });

    testWidgets('renders actions in trailing slot', (tester) async {
      const actionKey = Key('mc-action-test');
      await tester.pumpWidget(
        _wrap(
          MissionControlAppBar(
            actions: [
              IconButton(
                key: actionKey,
                icon: const Icon(Icons.settings),
                onPressed: () {},
              ),
            ],
            showScanningLine: false,
          ),
        ),
      );
      expect(find.byKey(actionKey), findsOneWidget);
    });

    testWidgets('includes scanning line when enabled', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: true)),
      );
      expect(find.byType(ScanningLine), findsOneWidget);
      // Stop pending animation before tearDown.
      await tester.pump(const Duration(milliseconds: 50));
    });

    testWidgets('omits scanning line when disabled', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: false)),
      );
      expect(find.byType(ScanningLine), findsNothing);
    });

    testWidgets('renders 1px hairline divider at bottom', (tester) async {
      await tester.pumpWidget(
        _wrap(const MissionControlAppBar(showScanningLine: false)),
      );
      // Scan child Containers for one with height 1 and hairline colour.
      // Light brightness (default in _wrap) → borderLight token.
      bool found = false;
      tester
          .widgetList<Container>(find.byType(Container))
          .forEach((c) {
        final dec = c.decoration;
        final colour = c.color ??
            (dec is BoxDecoration ? dec.color : null);
        if (c.constraints?.maxHeight == 1 &&
            colour == AppColors.borderLight) {
          found = true;
        }
      });
      expect(found, isTrue, reason: 'expected a 1px hairline divider');
    });

    test('preferredSize advertises toolbar + scan + hairline', () {
      const bar = MissionControlAppBar(toolbarHeight: 56);
      // 56 toolbar + 1 scan + 1 hairline = 58.
      expect(bar.preferredSize.height, 58);
    });

    test('preferredSize without scanning line is toolbar + hairline', () {
      const bar = MissionControlAppBar(
        toolbarHeight: 56,
        showScanningLine: false,
      );
      expect(bar.preferredSize.height, 57);
    });
  });

  group('ScanningLine', () {
    testWidgets('static (reduced motion) variant still renders', (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: MediaQuery(
            data: const MediaQueryData(disableAnimations: true),
            child: const Scaffold(
              body: SizedBox(width: 200, child: ScanningLine()),
            ),
          ),
        ),
      );
      expect(find.byType(ScanningLine), findsOneWidget);
    });
  });

  group('MissionControlMark', () {
    testWidgets('paints something at the requested size', (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: Center(child: MissionControlMark(size: 32)),
          ),
        ),
      );
      final size = tester.getSize(find.byType(MissionControlMark));
      expect(size.width, 32);
      expect(size.height, 32);
    });
  });
}
