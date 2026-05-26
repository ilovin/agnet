// ignore_for_file: invalid_use_of_visible_for_testing_member
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

void main() {
  group('_MarkdownText code span rendering', () {
    testWidgets(
        'code span text is visible: no backgroundColor, explicit color, fontFamilyFallback',
        (WidgetTester tester) async {
      const input =
          r'path `.worktrees/issue1-package` 已删除，所有改动在 main 分支上。';

      late List<InlineSpan> spans;

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Builder(builder: (context) {
              final style = TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurface,
              );
              // Call the real production function exposed @visibleForTesting
              spans = buildMarkdownSpans(input, style, context);
              return Text.rich(TextSpan(children: spans));
            }),
          ),
        ),
      );

      // Collect all TextSpans
      final allSpans = <TextSpan>[];
      void walkSpan(InlineSpan span) {
        if (span is TextSpan) {
          if (span.text != null && span.text!.isNotEmpty) {
            allSpans.add(span);
          }
          span.children?.forEach(walkSpan);
        }
      }

      for (final span in spans) {
        walkSpan(span);
      }

      final codeSpans =
          allSpans.where((s) => s.style?.fontFamily == 'monospace').toList();
      expect(codeSpans, isNotEmpty,
          reason: 'Should have at least one code span for backtick content');

      for (final cs in codeSpans) {
        final bg = cs.style?.backgroundColor;
        final fg = cs.style?.color;

        // ASSERTION 1: no backgroundColor (removed to avoid invisible text)
        expect(bg, isNull,
            reason:
                'Code span must not have backgroundColor — it caused invisible '
                'text in Flutter Web CanvasKit when monospace had no glyphs');

        // ASSERTION 2: explicit text color so it is never inherited as null
        expect(fg, isNotNull,
            reason:
                'Code span must have explicit color so text is visible on all '
                'backgrounds');

        // ASSERTION 3: fontFamilyFallback must be non-empty so CJK + ASCII
        // path characters render when monospace is unavailable (CanvasKit).
        final fallback = cs.style?.fontFamilyFallback;
        expect(fallback, isNotNull,
            reason:
                'Code span must declare fontFamilyFallback for Flutter Web '
                'CanvasKit where monospace may not be registered');
        expect(fallback, isNotEmpty,
            reason: 'fontFamilyFallback must not be empty');
        expect(fallback, contains('Noto Sans SC'),
            reason: 'fontFamilyFallback should include CJK font Noto Sans SC');
      }
    });

    testWidgets('code span content is preserved (no text loss)',
        (WidgetTester tester) async {
      const input = r'执行 `git status` 查看状态';

      late List<InlineSpan> spans;

      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Builder(builder: (context) {
              final style = TextStyle(
                fontSize: 12,
                color: Theme.of(context).colorScheme.onSurface,
              );
              spans = buildMarkdownSpans(input, style, context);
              return Text.rich(TextSpan(children: spans));
            }),
          ),
        ),
      );

      // Collect all text content
      final allTexts = <String>[];
      void walkSpan(InlineSpan span) {
        if (span is TextSpan && span.text != null) {
          allTexts.add(span.text!);
        }
      }

      for (final span in spans) {
        walkSpan(span);
      }

      final fullText = allTexts.join();
      expect(fullText, contains('git status'),
          reason: 'Code span inner text must be present in output');
    });
  });
}
