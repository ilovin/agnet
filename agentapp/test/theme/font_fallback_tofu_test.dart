import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_text_styles.dart';
import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';

/// Characters that have historically rendered as tofu (□) in CanvasKit mode
/// because they were missing from the primary font and the fallback chain
/// was incomplete.
const _tofuProneCharacters = <String>[
  // Arrows (U+2190–U+2199 block)
  '→', // U+2192 RIGHTWARDS ARROW
  '←', // U+2190 LEFTWARDS ARROW

  // Stars & dingbats (U+2600–U+26FF block)
  '★', // U+2605 BLACK STAR
  '☐', // U+2610 BALLOT BOX
  '☑', // U+2611 BALLOT BOX WITH CHECK

  // Check marks & crosses (U+2700–U+27BF block)
  '✓', // U+2713 CHECK MARK
  '✢', // U+2722 FOUR TEARDROP-SPOKED ASTERISK
  '✳', // U+2733 EIGHT SPOKED ASTERISK
  '✶', // U+2736 SIX POINTED BLACK STAR
  '✻', // U+273B TEARDROP-SPOKED ASTERISK
  '✽', // U+273D HEAVY TEARDROP-SPOKED ASTERISK

  // Technical / media control (U+23E0–U+23FF block)
  '⏺', // U+23FA BLACK CIRCLE FOR RECORD

  // Return / erase symbols (U+2300–U+23FF block)
  '↵', // U+21B5 DOWNWARDS ARROW WITH CORNER LEFTWARDS
  '⌫', // U+232B ERASE TO THE LEFT

  // Braille patterns (U+2800–U+28FF block)
  '·',  // U+00B7 MIDDLE DOT (used as Braille blank)
  '⠁', // U+2801 BRAILLE PATTERN DOTS-1
  '⠂', // U+2802 BRAILLE PATTERN DOTS-2
  '⠃', // U+2803 BRAILLE PATTERN DOTS-12
  '⠄', // U+2804 BRAILLE PATTERN DOTS-3
  '⠆', // U+2806 BRAILLE PATTERN DOTS-23
  '⠇', // U+2807 BRAILLE PATTERN DOTS-123
  '⠏', // U+280F BRAILLE PATTERN DOTS-1234
  '⠐', // U+2810 BRAILLE PATTERN DOTS-5
];

void main() {
  group('AppTextStyles.fontFamilyFallback', () {
    test('includes Noto Sans SC as first fallback', () {
      expect(AppTextStyles.fontFamilyFallback.first, 'Noto Sans SC');
    });

    test('includes Noto Sans Symbols 2 for technical glyphs', () {
      expect(
        AppTextStyles.fontFamilyFallback,
        contains('Noto Sans Symbols 2'),
      );
    });

    test('includes Noto Color Emoji for emoji glyphs', () {
      expect(
        AppTextStyles.fontFamilyFallback,
        contains('Noto Color Emoji'),
      );
    });

    test('includes system Chinese fonts for CJK coverage', () {
      expect(
        AppTextStyles.fontFamilyFallback,
        contains('PingFang SC'),
      );
      expect(
        AppTextStyles.fontFamilyFallback,
        contains('Microsoft YaHei'),
      );
    });

    test('ends with generic sans-serif catch-all', () {
      expect(AppTextStyles.fontFamilyFallback.last, 'sans-serif');
    });

    test('all TextStyle tokens carry the full fallback chain', () {
      final styles = <TextStyle>[
        AppTextStyles.displayLarge,
        AppTextStyles.titleLarge,
        AppTextStyles.titleMedium,
        AppTextStyles.bodyLarge,
        AppTextStyles.bodyMedium,
        AppTextStyles.bodySmall,
        AppTextStyles.labelSmall,
        AppTextStyles.caption,
        AppTextStyles.mono,
        AppTextStyles.monoLarge,
      ];

      for (final style in styles) {
        expect(
          style.fontFamilyFallback,
          AppTextStyles.fontFamilyFallback,
          reason: '${style.fontFamily} style must carry full fallback chain',
        );
      }
    });
  });

  group('AppTheme fontFamilyFallback', () {
    test('ThemeData textTheme bodyMedium carries a non-empty fallback list',
        () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final fallback = theme.textTheme.bodyMedium?.fontFamilyFallback;
      expect(fallback, isNotNull);
      expect(fallback, isNotEmpty);
    });

    test('ThemeData textTheme fallback includes Noto Sans Symbols 2', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final fallback = theme.textTheme.bodyMedium?.fontFamilyFallback;
      expect(fallback, contains('Noto Sans Symbols 2'));
    });

    test('ThemeData textTheme fallback includes Noto Color Emoji', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final fallback = theme.textTheme.bodyMedium?.fontFamilyFallback;
      expect(fallback, contains('Noto Color Emoji'));
    });
  });

  group('Tofu-prone character coverage', () {
    // These tests assert that the fallback chain is long enough to cover
    // glyphs that are NOT present in the primary font.  We cannot assert
    // actual font rasterisation in a unit test, but we can assert that the
    // style system is configured to try every font that is known to hold
    // the missing code points.

    test('every known tofu-prone character has a covering font in the chain',
        () {
      // Map each character to the font in our bundle that is known to
      // contain it (verified via cmap table inspection).
      const coverageMap = <String, String>{
        // NotoSansSC-Regular.ttf
        '→': 'Noto Sans SC',
        '←': 'Noto Sans SC',
        '★': 'Noto Sans SC',
        '✓': 'Noto Sans SC',
        '✽': 'Noto Sans SC',
        '·': 'Noto Sans SC',

        // NotoSansSymbols2-Regular.ttf
        '⏺': 'Noto Sans Symbols 2',
        '☐': 'Noto Sans Symbols 2',
        '☑': 'Noto Sans Symbols 2',
        '↵': 'Noto Sans Symbols 2',
        '⌫': 'Noto Sans Symbols 2',
        '✢': 'Noto Sans Symbols 2',
        '✳': 'Noto Sans Symbols 2',
        '✶': 'Noto Sans Symbols 2',
        '✻': 'Noto Sans Symbols 2',
        '⠁': 'Noto Sans Symbols 2',
        '⠂': 'Noto Sans Symbols 2',
        '⠃': 'Noto Sans Symbols 2',
        '⠄': 'Noto Sans Symbols 2',
        '⠆': 'Noto Sans Symbols 2',
        '⠇': 'Noto Sans Symbols 2',
        '⠏': 'Noto Sans Symbols 2',
        '⠐': 'Noto Sans Symbols 2',
      };

      for (final entry in coverageMap.entries) {
        final char = entry.key;
        final coveringFont = entry.value;
        expect(
          AppTextStyles.fontFamilyFallback,
          contains(coveringFont),
          reason: 'Character "$char" needs font "$coveringFont" in fallback',
        );
      }
    });

    test('fallback chain length is at least 6 fonts', () {
      // A short chain (< 4) is a smell; the current design uses 6.
      expect(
        AppTextStyles.fontFamilyFallback.length,
        greaterThanOrEqualTo(6),
      );
    });
  });

  group('Widget-level style inheritance', () {
    testWidgets('Text with default theme does not use an empty fallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.build(densityMode: DensityMode.standard),
          home: const Scaffold(
            body: Text('→ ★ ✓ ⏺'),
          ),
        ),
      );

      final textWidget = tester.widget<Text>(find.text('→ ★ ✓ ⏺'));
      // When no explicit style is given, the Text widget inherits from
      // DefaultTextStyle which is seeded by ThemeData.  We verify the
      // theme itself has the fallback; the Text widget may not have an
      // explicit fontFamilyFallback property.
      final theme = Theme.of(tester.element(find.text('→ ★ ✓ ⏺')));
      final fallback = theme.textTheme.bodyMedium?.fontFamilyFallback;
      expect(fallback, isNotNull);
      expect(fallback, contains('Noto Sans Symbols 2'));
    });

    testWidgets('Text with AppTextStyles.bodyMedium carries full fallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.build(densityMode: DensityMode.standard),
          home: const Scaffold(
            body: Text(
              '→ ★ ✓ ⏺',
              style: AppTextStyles.bodyMedium,
            ),
          ),
        ),
      );

      final textWidget = tester.widget<Text>(find.text('→ ★ ✓ ⏺'));
      expect(
        textWidget.style?.fontFamilyFallback,
        AppTextStyles.fontFamilyFallback,
      );
    });

    testWidgets('Text with AppTextStyles.mono carries full fallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          theme: AppTheme.build(densityMode: DensityMode.standard),
          home: const Scaffold(
            body: Text(
              '→ ★ ✓ ⏺',
              style: AppTextStyles.mono,
            ),
          ),
        ),
      );

      final textWidget = tester.widget<Text>(find.text('→ ★ ✓ ⏺'));
      expect(
        textWidget.style?.fontFamilyFallback,
        AppTextStyles.fontFamilyFallback,
      );
    });
  });
}
