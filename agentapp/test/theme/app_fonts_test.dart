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
  });
}
