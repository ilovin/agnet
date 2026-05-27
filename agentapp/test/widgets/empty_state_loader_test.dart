import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/widgets/empty_states/empty_state.dart';
import 'package:agentapp/widgets/empty_states/sentinel_illustration.dart';
import 'package:agentapp/widgets/loaders/oscilloscope_loader.dart';

Widget _wrap(Widget child) => MaterialApp(
      home: Scaffold(body: Center(child: child)),
    );

void main() {
  group('SentinelIllustration', () {
    testWidgets('renders at the requested size', (tester) async {
      await tester.pumpWidget(
        _wrap(const SentinelIllustration(size: 120, animate: false)),
      );
      final size = tester.getSize(find.byType(SentinelIllustration));
      expect(size.width, 120);
      expect(size.height, 120);
    });

    testWidgets('paints something (CustomPaint child)', (tester) async {
      await tester.pumpWidget(
        _wrap(const SentinelIllustration(size: 80, animate: false)),
      );
      expect(
        find.descendant(
          of: find.byType(SentinelIllustration),
          matching: find.byType(CustomPaint),
        ),
        findsAtLeastNWidgets(1),
      );
    });
  });

  group('EmptyState', () {
    testWidgets('renders default 等待信号 caption', (tester) async {
      await tester.pumpWidget(_wrap(const EmptyState()));
      expect(find.text('等待信号...'), findsOneWidget);
    });

    testWidgets('renders custom message + sub-message', (tester) async {
      await tester.pumpWidget(
        _wrap(const EmptyState(
          message: '没有节点',
          subMessage: '点击右上角添加',
        )),
      );
      expect(find.text('没有节点'), findsOneWidget);
      expect(find.text('点击右上角添加'), findsOneWidget);
    });

    testWidgets('contains a SentinelIllustration', (tester) async {
      await tester.pumpWidget(_wrap(const EmptyState()));
      expect(find.byType(SentinelIllustration), findsOneWidget);
    });
  });

  group('OscilloscopeLoader', () {
    testWidgets('renders at the requested size', (tester) async {
      await tester.pumpWidget(_wrap(const OscilloscopeLoader()));
      final size = tester.getSize(find.byType(OscilloscopeLoader));
      expect(size.width, 60);
      expect(size.height, 24);
      // Ensure the controller advances without throwing.
      await tester.pump(const Duration(milliseconds: 100));
    });

    testWidgets('paints CustomPaint internally', (tester) async {
      await tester.pumpWidget(_wrap(const OscilloscopeLoader()));
      expect(
        find.descendant(
          of: find.byType(OscilloscopeLoader),
          matching: find.byType(CustomPaint),
        ),
        findsAtLeastNWidgets(1),
      );
      await tester.pump(const Duration(milliseconds: 100));
    });

    testWidgets('respects custom width/height', (tester) async {
      await tester.pumpWidget(
        _wrap(const OscilloscopeLoader(width: 100, height: 32)),
      );
      final size = tester.getSize(find.byType(OscilloscopeLoader));
      expect(size.width, 100);
      expect(size.height, 32);
      await tester.pump(const Duration(milliseconds: 100));
    });
  });
}
