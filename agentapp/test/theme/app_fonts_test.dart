import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_text_styles.dart';

/// Sanity tests that the three font families used by the design system
/// are wired to the correct text-style buckets.
///
/// These guard against accidental regressions where a typography token
/// loses its family (e.g. a contributor copies bodyMedium and forgets to
/// re-set fontFamily, defaulting to the system font).
void main() {
  group('AppTextStyles font family wiring', () {
    test('display tier references Source Han Sans CN', () {
      expect(AppTextStyles.displayLarge.fontFamily, 'Source Han Sans CN');
      expect(AppTextStyles.titleLarge.fontFamily, 'Source Han Sans CN');
    });

    test('body tier references Noto Sans SC', () {
      const bodies = <TextStyle>[
        AppTextStyles.titleMedium,
        AppTextStyles.bodyLarge,
        AppTextStyles.bodyMedium,
        AppTextStyles.bodySmall,
        AppTextStyles.labelSmall,
        AppTextStyles.caption,
      ];
      for (final s in bodies) {
        expect(s.fontFamily, 'Noto Sans SC');
      }
    });

    test('mono tier references JetBrainsMono', () {
      expect(AppTextStyles.mono.fontFamily, 'JetBrainsMono');
      expect(AppTextStyles.monoLarge.fontFamily, 'JetBrainsMono');
    });

    test('display fonts are heavy weight (w900)', () {
      expect(AppTextStyles.displayLarge.fontWeight, FontWeight.w900);
      expect(AppTextStyles.titleLarge.fontWeight, FontWeight.w900);
    });

    test('display fonts use negative letter-spacing for tight set', () {
      expect(
        AppTextStyles.displayLarge.letterSpacing,
        lessThan(0),
        reason: 'display headings should be optically tight',
      );
      expect(
        AppTextStyles.titleLarge.letterSpacing,
        lessThan(0),
      );
    });

    test('every TextStyle declares fontFamilyFallback covering symbol set', () {
      // Glyphs like ←→↑↓↵⌫ are missing from JetBrainsMono and Source Han
      // Sans CN. The shared fallback chain MUST include 'Noto Sans Symbols 2'
      // so the special-keys panel does not render tofu.
      const styles = <TextStyle>[
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
      for (final s in styles) {
        expect(
          s.fontFamilyFallback,
          isNotNull,
          reason: 'fontFamilyFallback missing on a TextStyle (${s.fontFamily})',
        );
        expect(
          s.fontFamilyFallback!.contains('Noto Sans Symbols 2'),
          isTrue,
          reason:
              'fallback for ${s.fontFamily} must include Noto Sans Symbols 2',
        );
      }
    });
  });
}
