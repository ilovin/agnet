// Tests for `canvaskitSafeText`. Verifies the rune rewrites mirror
// `agentgw/internal/ws/sanitize.go` so tofu-prone characters (arrows,
// ☒) consistently reach the user as ASCII regardless of the path they
// travelled (live broadcast → already sanitized at the gateway;
// `conversation.history` proxy → unsanitized, the case this helper
// covers).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';
import 'package:agentapp/utils/canvaskit_safe_text.dart';

void main() {
  group('canvaskitSafeText (rune-level)', () {
    test('returns input unchanged when pure ASCII', () {
      expect(canvaskitSafeText('hello world'), 'hello world');
      expect(canvaskitSafeText(''), '');
      expect(canvaskitSafeText('abc 123 -> xyz'), 'abc 123 -> xyz');
    });

    test('returns input unchanged when no tofu-prone runes present', () {
      // CJK characters must pass through unchanged.
      expect(canvaskitSafeText('你好世界'), '你好世界');
      // Star, dot, box-drawing — handled by the gateway separately.
      expect(canvaskitSafeText('★ • ─ │'), '★ • ─ │');
      // Emoji untouched.
      expect(canvaskitSafeText('🚀 done'), '🚀 done');
    });

    test('rewrites U+2192 → "->"', () {
      expect(canvaskitSafeText('login → dashboard'), 'login -> dashboard');
    });

    test('rewrites U+2190 ← to "<-"', () {
      expect(canvaskitSafeText('back ← here'), 'back <- here');
    });

    test('rewrites U+2191 ↑ to "^"', () {
      expect(canvaskitSafeText('next ↑ row'), 'next ^ row');
    });

    test('rewrites U+2193 ↓ to "v"', () {
      expect(canvaskitSafeText('drop ↓ down'), 'drop v down');
    });

    test('rewrites U+2612 ☒ to "[X]"', () {
      expect(canvaskitSafeText('task ☒ failed'), 'task [X] failed');
    });

    test('rewrites every arrow in a mixed string in one pass', () {
      expect(canvaskitSafeText('↑↓←→'), '^v<-->');
    });

    test('preserves CJK around a rewrite', () {
      // The rewrite must not corrupt surrounding multi-byte characters.
      expect(canvaskitSafeText('登录 → 仪表盘'), '登录 -> 仪表盘');
    });

    test('handles the same arrow appearing multiple times', () {
      expect(canvaskitSafeText('a → b → c'), 'a -> b -> c');
    });
  });

  group('MarkdownContent applies canvaskitSafeText to its input', () {
    Future<void> pump(WidgetTester tester, String text) async {
      await tester.pumpWidget(
        ProviderScope(
          child: MaterialApp(
            theme: AppTheme.build(densityMode: DensityMode.standard),
            home: Scaffold(
              body: MarkdownContent(
                text: text,
                fontSize: 14,
                textColor: const Color(0xFF000000),
              ),
            ),
          ),
        ),
      );
      await tester.pump();
    }

    /// Collect the visible plain-text from every Text / RichText in the
    /// rendered tree. We use this to verify the user-visible content
    /// after the runtime rewrite, regardless of whether MarkdownContent
    /// took the simple-text path or the MarkdownBody path.
    String collectVisibleText(WidgetTester tester) {
      final buf = StringBuffer();
      for (final element in find.byType(Text).evaluate()) {
        final widget = element.widget as Text;
        if (widget.data != null) {
          buf.write(widget.data);
        } else if (widget.textSpan != null) {
          widget.textSpan!.visitChildren((span) {
            if (span is TextSpan && span.text != null) {
              buf.write(span.text);
            }
            return true;
          });
        }
        buf.write('\n');
      }
      for (final element in find.byType(RichText).evaluate()) {
        final widget = element.widget as RichText;
        buf.write(widget.text.toPlainText());
        buf.write('\n');
      }
      return buf.toString();
    }

    testWidgets(
        'simple-text path: → in plain prose is rewritten to ->', (tester) async {
      await pump(tester, 'login → dashboard');
      final visible = collectVisibleText(tester);

      expect(visible.contains('->'), isTrue,
          reason: 'arrow must be rewritten to ASCII for CanvasKit safety');
      expect(visible.contains('→'), isFalse,
          reason: 'raw → must not survive — it tofu under CanvasKit');
    });

    testWidgets(
        'MarkdownBody path: → inside a heading is rewritten to ->',
        (tester) async {
      // The hasComplexMarkdown branch is taken when there is a `# heading`.
      await pump(tester, '# Step 1 → Step 2\n\nbody → text');
      final visible = collectVisibleText(tester);

      expect(visible.contains('->'), isTrue);
      expect(visible.contains('→'), isFalse,
          reason: 'arrows in markdown headings/paragraphs must be rewritten');
    });

    testWidgets('MarkdownBody path: ☒ ballot box rewritten to [X]',
        (tester) async {
      // Force complex-markdown branch via a fence so we exercise the
      // MarkdownBody path explicitly.
      await pump(tester, '```\nsome code\n```\n\nstatus: ☒ failed');
      final visible = collectVisibleText(tester);

      expect(visible.contains('[X]'), isTrue,
          reason: '☒ must be rewritten to [X] (matches gateway sanitize)');
      expect(visible.contains('☒'), isFalse,
          reason: 'raw ☒ must not survive — it tofu under CanvasKit');
    });

    testWidgets('CJK content is not corrupted by the rewrite',
        (tester) async {
      await pump(tester, '登录 → 仪表盘');
      final visible = collectVisibleText(tester);

      expect(visible.contains('登录'), isTrue);
      expect(visible.contains('仪表盘'), isTrue);
      expect(visible.contains('->'), isTrue);
    });

    testWidgets('all four arrow code points rewritten in markdown body',
        (tester) async {
      await pump(tester, '# h\n\n↑ up, ↓ down, ← back, → forward');
      final visible = collectVisibleText(tester);

      expect(visible.contains('^'), isTrue);
      expect(visible.contains('v'), isTrue);
      expect(visible.contains('<-'), isTrue);
      expect(visible.contains('->'), isTrue);
      // None of the raw runes should remain.
      for (final raw in ['↑', '↓', '←', '→']) {
        expect(visible.contains(raw), isFalse,
            reason: 'raw $raw must not survive in rendered markdown');
      }
    });
  });
}
