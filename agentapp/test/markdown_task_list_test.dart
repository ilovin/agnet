// Test that GFM Markdown task lists (`- [ ] foo` / `- [x] bar`) render with
// real checkbox glyphs (not raw `[ ]`/`[x]` text and not tofu).
//
// Background: messages from Claude / Codex frequently contain task lists.
// Previously, the renderer either:
//   1. left the literal `[ ]` / `[x]` brackets visible, or
//   2. emitted tofu (.notdef boxes) when the chosen font lacked the glyph
//      under Flutter Web CanvasKit.
//
// Fix: route any text that contains a task-list marker through `MarkdownBody`,
// supply an explicit `checkboxBuilder` that renders Unicode ballot-box
// characters (U+2610 ☐ / U+2611 ☑) backed by `Noto Sans Symbols 2` — the font
// is already bundled in pubspec.yaml and is verified to cover both code
// points (see commit 759c87a).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/screens/agent_detail_screen.dart';

Future<void> _pumpMarkdown(WidgetTester tester, String text) async {
  await tester.pumpWidget(
    ProviderScope(
      child: MaterialApp(
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

/// Walk the rendered widget tree and collect every visible `String` that
/// originated from a `Text` / `Text.rich` widget.
List<String> _collectVisibleStrings(WidgetTester tester) {
  final out = <String>[];
  for (final element in find.byType(Text).evaluate()) {
    final widget = element.widget as Text;
    if (widget.data != null) {
      out.add(widget.data!);
    } else if (widget.textSpan != null) {
      widget.textSpan!.visitChildren((span) {
        if (span is TextSpan && span.text != null) {
          out.add(span.text!);
        }
        return true;
      });
    }
  }
  return out;
}

/// Walk the rendered tree and return the styles attached to any Text widget
/// whose visible content contains [needle].
List<TextStyle> _stylesContaining(WidgetTester tester, String needle) {
  final styles = <TextStyle>[];
  for (final element in find.byType(Text).evaluate()) {
    final widget = element.widget as Text;
    if (widget.data != null && widget.data!.contains(needle)) {
      if (widget.style != null) styles.add(widget.style!);
    } else if (widget.textSpan != null) {
      widget.textSpan!.visitChildren((span) {
        if (span is TextSpan && span.text != null && span.text!.contains(needle)) {
          if (span.style != null) styles.add(span.style!);
        }
        return true;
      });
    }
  }
  return styles;
}

void main() {
  group('MarkdownContent task list rendering', () {
    testWidgets(
        'renders `- [ ] foo` / `- [x] bar` as ☐/☑ with Symbols 2 fallback (no tofu, no raw brackets)',
        (WidgetTester tester) async {
      const input = '- [ ] todo unchecked\n- [x] todo done';
      await _pumpMarkdown(tester, input);

      final visible = _collectVisibleStrings(tester).join('|');

      // 1. The list-item bodies must still be visible.
      expect(visible, contains('todo unchecked'),
          reason: 'unchecked task body must remain readable');
      expect(visible, contains('todo done'),
          reason: 'checked task body must remain readable');

      // 2. We must NOT leave the GFM source markers `[ ]` / `[x]` in the
      //    visible text — that would mean the task-list extension is off and
      //    users see raw markdown.
      expect(visible.contains('[ ]'), isFalse,
          reason: 'raw `[ ]` marker must not survive into rendered output');
      expect(visible.contains('[x]'), isFalse,
          reason: 'raw `[x]` marker must not survive into rendered output');

      // 3. Checkbox glyphs must be present. Accept either the canonical
      //    ballot-box pair (☐/☑) or the geometric square pair (□/■). What
      //    matters is that the user sees a checkbox — not tofu.
      final hasUnchecked = visible.contains('☐') || visible.contains('□');
      final hasChecked = visible.contains('☑') || visible.contains('■');
      expect(hasUnchecked, isTrue,
          reason: 'unchecked checkbox glyph (U+2610 ☐ or U+25A1 □) must be drawn');
      expect(hasChecked, isTrue,
          reason: 'checked checkbox glyph (U+2611 ☑ or U+25A0 ■) must be drawn');

      // 4. Crucially: every checkbox glyph MUST declare a font fallback
      //    chain that includes a font we know covers the code point. The
      //    project bundles `Noto Sans Symbols 2` exactly for this purpose,
      //    so the fallback chain MUST include it. Without this assertion
      //    the previous tofu regression could trivially come back.
      const checkboxGlyphs = ['☐', '☑', '□', '■'];
      for (final g in checkboxGlyphs) {
        if (!visible.contains(g)) continue;
        final styles = _stylesContaining(tester, g);
        expect(styles, isNotEmpty,
            reason: 'checkbox glyph $g must carry an explicit TextStyle');
        for (final s in styles) {
          final fallback = s.fontFamilyFallback;
          expect(fallback, isNotNull,
              reason:
                  'checkbox glyph must declare fontFamilyFallback so the '
                  'glyph never falls back to a font without the code point');
          expect(fallback, contains('Noto Sans Symbols 2'),
              reason:
                  'checkbox glyph fallback must include Noto Sans Symbols 2 '
                  '— the bundled font that covers U+2610-2612 and U+25A0-25A1');
        }
      }
    });

    testWidgets(
        'task list is recognised even without other GFM markers (no headings, no fenced code)',
        (WidgetTester tester) async {
      // Regression guard: previous heuristic only routed text through
      // MarkdownBody when it contained ``` / # / |. A pure task list missed
      // that path entirely and the brackets leaked into the UI.
      const input = '- [ ] just a checkbox, nothing fancy';
      await _pumpMarkdown(tester, input);

      final visible = _collectVisibleStrings(tester).join('|');
      expect(visible, contains('just a checkbox, nothing fancy'));
      expect(visible.contains('[ ]'), isFalse,
          reason:
              'pure task-list message must still be parsed by MarkdownBody, '
              'not rendered as raw inline text');
    });
  });
}
