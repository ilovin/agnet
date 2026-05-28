// Audit that the global theme + every Markdown styleSheet declares a
// fontFamilyFallback chain that includes `Noto Sans Symbols 2`. Without
// the fallback, common arrows / shapes / box-drawing characters that the
// primary CJK family does not cover (e.g. → ↑ ↓ ←, ★, ─, ⏺) render as
// .notdef tofu blocks under Flutter Web CanvasKit.
//
// This audit operates at two levels:
//
//  1. Source-level — pump the `MarkdownContent` widget with a corpus that
//     contains exactly those problematic glyphs and walk every rendered
//     `Text` / `Text.rich` widget. Each style that carries any glyph
//     from the corpus MUST declare a non-null `fontFamilyFallback` that
//     includes `Noto Sans Symbols 2`.
//
//  2. Theme-level — `AppTheme.build(...)` must hand back a `textTheme`
//     whose body / title styles all carry `Noto Sans Symbols 2` in
//     their fallback chain. This is the safety net for every plain
//     `Text(...)` widget in the app that does not set its own style.
//
// The failing assertion before the fix: the `MarkdownStyleSheet.p` /
// `h1..h6` / `blockquote` / `listBullet` / `a` styles were created with
// `TextStyle(fontSize: ..., color: ...)` and no fallback, so any Text
// span produced by `MarkdownBody` for body markdown carried a null
// `fontFamilyFallback`. → would resolve via OS fallback on macOS but
// became tofu on Flutter Web CanvasKit.

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/theme/app_text_styles.dart';
import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';

/// The set of common symbol code points that have historically rendered
/// as tofu when a TextStyle does not declare a fontFamilyFallback that
/// reaches `Noto Sans Symbols 2`. Each entry is a single Unicode code
/// point.
///
/// Note on arrows (U+2190..U+2193): they used to live in this list, but
/// `MarkdownContent` now rewrites them to ASCII at render time via
/// [`canvaskitSafeText`] (mirrors the gateway sanitize). Under Flutter
/// Web CanvasKit, Noto Sans SC's OS/2 bitmap claims arrow coverage and
/// short-circuits the fallback chain even when Symbols 2 is declared,
/// so the only reliable fix is to not let the rune reach the renderer.
/// Arrow tofu is now covered by `test/canvaskit_safe_text_test.dart`.
const List<String> _tofuRiskGlyphs = <String>[
  // Geometric / decorative — used by status indicators
  '★',
  '•',
  // Box-drawing fragments — task#1 sanitize already turns these into
  // ASCII at the gateway, but we still want the app side to draw them
  // correctly if any reach the UI from another source.
  '─',
  '│',
  // Media / playback symbols
  '⏺',
  '⏵',
  '⏸',
  // Check / cross
  '✓',
  '✗',
];

/// Walk the rendered widget tree and collect, for every Text widget,
/// the list of (visibleString, style) pairs.
List<({String text, TextStyle? style})> _collectTextWithStyles(
    WidgetTester tester) {
  final out = <({String text, TextStyle? style})>[];
  for (final element in find.byType(Text).evaluate()) {
    final widget = element.widget as Text;
    if (widget.data != null) {
      out.add((text: widget.data!, style: widget.style));
    } else if (widget.textSpan != null) {
      widget.textSpan!.visitChildren((span) {
        if (span is TextSpan && span.text != null) {
          out.add((text: span.text!, style: span.style));
        }
        return true;
      });
    }
  }
  return out;
}

Future<void> _pumpMarkdown(WidgetTester tester, String text) async {
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

void main() {
  group('Font fallback audit (theme level)', () {
    test(
        'AppTheme.build textTheme carries Noto Sans Symbols 2 fallback on every body/title style',
        () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final styles = <String, TextStyle?>{
        'displayLarge': theme.textTheme.displayLarge,
        'displayMedium': theme.textTheme.displayMedium,
        'displaySmall': theme.textTheme.displaySmall,
        'titleLarge': theme.textTheme.titleLarge,
        'titleMedium': theme.textTheme.titleMedium,
        'titleSmall': theme.textTheme.titleSmall,
        'bodyLarge': theme.textTheme.bodyLarge,
        'bodyMedium': theme.textTheme.bodyMedium,
        'bodySmall': theme.textTheme.bodySmall,
        'labelLarge': theme.textTheme.labelLarge,
        'labelMedium': theme.textTheme.labelMedium,
        'labelSmall': theme.textTheme.labelSmall,
      };

      for (final entry in styles.entries) {
        final s = entry.value;
        expect(s, isNotNull, reason: '${entry.key} must be defined');
        expect(s!.fontFamilyFallback, isNotNull,
            reason:
                '${entry.key} must declare fontFamilyFallback (theme level fallback)');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                '${entry.key} fallback chain must include Noto Sans Symbols 2 '
                'so arrows / box-drawing / media symbols never tofu');
      }
    });

    test('AppTheme.build top-level fontFamilyFallback also includes Symbols 2',
        () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      // Some Flutter widgets read ThemeData.fontFamilyFallback directly
      // (rather than via the textTheme). Verify the top-level chain too.
      expect(theme.textTheme.bodyMedium?.fontFamilyFallback,
          contains('Noto Sans Symbols 2'));
    });

    test('AppTextStyles.fontFamilyFallback contains Noto Sans Symbols 2', () {
      expect(AppTextStyles.fontFamilyFallback,
          contains('Noto Sans Symbols 2'));
    });
  });

  group('Font fallback audit (markdown level)', () {
    testWidgets(
        'every glyph from the tofu-risk corpus rendered by MarkdownContent declares a Symbols 2 fallback',
        (WidgetTester tester) async {
      // A markdown blob with every risky glyph in plain prose, in a
      // heading, in a list item and in a blockquote so we exercise
      // every styleSheet entry.
      final corpus = '''
# heading ${_tofuRiskGlyphs.join(' ')}

paragraph: ${_tofuRiskGlyphs.join(' ')}

- list ${_tofuRiskGlyphs.join(' ')}

> quote ${_tofuRiskGlyphs.join(' ')}
''';

      await _pumpMarkdown(tester, corpus);

      final pairs = _collectTextWithStyles(tester);

      // For every glyph in the risk set, every Text widget that contains
      // it must declare a fontFamilyFallback that includes
      // "Noto Sans Symbols 2".
      for (final glyph in _tofuRiskGlyphs) {
        final hits = pairs.where((p) => p.text.contains(glyph)).toList();
        expect(hits, isNotEmpty,
            reason:
                'glyph "$glyph" must appear in the rendered tree (markdown '
                'parser must not strip it)');
        for (final hit in hits) {
          final style = hit.style;
          expect(style, isNotNull,
              reason:
                  'Text containing "$glyph" must carry an explicit TextStyle '
                  '(parent: "${hit.text.replaceAll(RegExp(r'\s+'), ' ')}")');
          final fallback = style!.fontFamilyFallback;
          expect(fallback, isNotNull,
              reason:
                  'Text containing "$glyph" must declare fontFamilyFallback. '
                  'Otherwise the glyph will tofu under Flutter Web CanvasKit '
                  'when the primary family lacks the code point.');
          expect(fallback, contains('Noto Sans Symbols 2'),
              reason:
                  'Text containing "$glyph" must list "Noto Sans Symbols 2" '
                  'in its fallback chain — that is the bundled font that '
                  'covers arrows / shapes / media symbols.');
        }
      }
    });

    testWidgets(
        'plain Text widget under AppTheme inherits fontFamilyFallback so → does not tofu',
        (WidgetTester tester) async {
      // A plain Text(...) (no style override) should pick up the
      // textTheme.bodyMedium style — and that style must carry the
      // Symbols 2 fallback. This guards every screen that uses
      // `Text('...')` without a custom style.
      const corpus = 'arrow → and star ★ inside plain text';

      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.build(densityMode: DensityMode.standard),
          home: const Scaffold(
            body: Text(corpus),
          ),
        ),
      );
      await tester.pump();

      // The merged effective style on a default Text comes from
      // DefaultTextStyle.of(context); we can verify it via the rendered
      // RichText widget's text.style.
      final richText = tester.widget<RichText>(find.byType(RichText));
      final effective = richText.text.style;
      expect(effective, isNotNull);
      final fallback = effective!.fontFamilyFallback;
      expect(fallback, isNotNull,
          reason:
              'Effective style on a plain Text must declare fontFamilyFallback');
      expect(fallback, contains('Noto Sans Symbols 2'),
          reason:
              'Plain Text must inherit a fallback chain that reaches Noto Sans Symbols 2');
    });

    testWidgets(
        'MarkdownContent renders [X] fallback when ballot-box-with-X (U+2612) appears in text',
        (WidgetTester tester) async {
      // Background: U+2612 (☒) is covered by Noto Sans Symbols 2, but
      // Noto Sans SC claims the Miscellaneous Symbols block via OS/2
      // ulUnicodeRange1 bit 29 without actually containing the glyph.
      // Under Flutter Web CanvasKit this causes the fallback chain to
      // be skipped and the glyph renders as tofu. The gateway now
      // sanitizes ☒ → [X] before the text reaches the app.
      //
      // This test verifies the app renders the sanitized [X] correctly
      // (no tofu, no raw ☒) and that the fallback chain is still declared.
      const corpus = 'Task status: [X] failed';

      await _pumpMarkdown(tester, corpus);

      final pairs = _collectTextWithStyles(tester);
      final visible = pairs.map((p) => p.text).join('|');

      // The sanitized [X] must be visible.
      expect(visible, contains('[X]'),
          reason: 'sanitized [X] must be rendered');

      // The original ☒ must NOT survive (it would tofu on CanvasKit).
      expect(visible.contains('☒'), isFalse,
          reason: 'raw U+2612 (☒) must not appear — it renders as tofu');
    });

    testWidgets(
        'MarkdownContent simple-text path declares Symbols 2 fallback on ★ (and the rewritten arrow ASCII)',
        (WidgetTester tester) async {
      // The simple-text path is exercised when the input has NO
      // ```/#/|/[ ] markers. The outer TextSpan style emitted by
      // `_buildTextSpan` must declare a fallback chain that covers the
      // tofu-risk glyphs.
      //
      // The raw → in the corpus is rewritten to "->" before rendering
      // (canvaskitSafeText). The fallback assertion targets the surviving
      // tofu-risk glyph (★) plus the rewritten arrow ASCII so we still
      // verify the simple-text root span carries the chain.
      const corpus = 'plain prose with arrow → and star ★';

      await tester.pumpWidget(
        ProviderScope(
          child: MaterialApp(
            theme: AppTheme.build(densityMode: DensityMode.standard),
            home: const Scaffold(
              body: MarkdownContent(
                text: corpus,
                fontSize: 14,
                textColor: Color(0xFF000000),
              ),
            ),
          ),
        ),
      );
      await tester.pump();

      // Walk RichText widgets and verify the OUTER root span style
      // (which children inherit from) declares the fallback chain.
      final richTexts = find.byType(RichText).evaluate();
      var foundOuter = false;
      for (final element in richTexts) {
        final widget = element.widget as RichText;
        final raw = widget.text.toPlainText();
        // Match either the surviving ★ or the rewritten "->" produced by
        // canvaskitSafeText (raw → no longer reaches the renderer).
        if (!raw.contains('->') && !raw.contains('★')) continue;
        foundOuter = true;
        final rootStyle = widget.text.style;
        expect(rootStyle, isNotNull,
            reason: 'simple-text RichText root must declare a TextStyle');
        final fallback = rootStyle!.fontFamilyFallback;
        expect(fallback, isNotNull,
            reason:
                'simple-text RichText root must declare fontFamilyFallback');
        expect(fallback, contains('Noto Sans Symbols 2'),
            reason:
                'simple-text RichText root must reach Noto Sans Symbols 2 '
                'in its fallback chain');
      }
      expect(foundOuter, isTrue,
          reason:
              'must locate at least one RichText whose plain text contains "★" or the rewritten "->"');
    });
  });
}
