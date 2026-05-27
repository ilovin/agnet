import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_text_styles.dart';

void main() {
  group('AppTextStyles', () {
    const bodyFamily = 'Noto Sans SC';
    const displayFamily = 'Source Han Sans CN';
    const monoFamily = 'JetBrainsMono';

    void verify(
      TextStyle style,
      double size,
      FontWeight weight,
      double height,
      String expectedFamily,
    ) {
      expect(style.fontFamily, expectedFamily);
      expect(style.fontSize, size);
      expect(style.fontWeight, weight);
      expect(style.height, height);
    }

    test('displayLarge: 28 / display family / w900', () {
      verify(
        AppTextStyles.displayLarge,
        28,
        FontWeight.w900,
        1.2,
        displayFamily,
      );
    });

    test('titleLarge: 24 / display family / w900', () {
      verify(
        AppTextStyles.titleLarge,
        24,
        FontWeight.w900,
        1.25,
        displayFamily,
      );
    });

    test('titleMedium uses body family', () {
      verify(
        AppTextStyles.titleMedium,
        20,
        FontWeight.w500,
        1.3,
        bodyFamily,
      );
    });

    test('bodyLarge: 18 / 1.4 / w400', () {
      verify(AppTextStyles.bodyLarge, 18, FontWeight.w400, 1.4, bodyFamily);
    });

    test('bodyMedium: 16 / 1.4 / w400', () {
      verify(AppTextStyles.bodyMedium, 16, FontWeight.w400, 1.4, bodyFamily);
    });

    test('bodySmall: 14 / 1.4 / w400', () {
      verify(AppTextStyles.bodySmall, 14, FontWeight.w400, 1.4, bodyFamily);
    });

    test('labelSmall: 13 / 1.3 / w500', () {
      verify(AppTextStyles.labelSmall, 13, FontWeight.w500, 1.3, bodyFamily);
    });

    test('caption: 12 / 1.3 / w400', () {
      verify(AppTextStyles.caption, 12, FontWeight.w400, 1.3, bodyFamily);
    });

    test('mono uses JetBrainsMono family', () {
      expect(AppTextStyles.mono.fontFamily, monoFamily);
      expect(AppTextStyles.monoLarge.fontFamily, monoFamily);
    });

    test('display family is Source Han Sans CN', () {
      expect(AppTextStyles.displayFontFamily, displayFamily);
    });

    test('mono family is JetBrainsMono', () {
      expect(AppTextStyles.monoFontFamily, monoFamily);
    });

    test('body styles use Noto Sans SC family', () {
      final bodyStyles = <TextStyle>[
        AppTextStyles.titleMedium,
        AppTextStyles.bodyLarge,
        AppTextStyles.bodyMedium,
        AppTextStyles.bodySmall,
        AppTextStyles.labelSmall,
        AppTextStyles.caption,
      ];
      for (final s in bodyStyles) {
        expect(s.fontFamily, bodyFamily);
      }
    });

    test('display styles use Source Han Sans CN family', () {
      final displayStyles = <TextStyle>[
        AppTextStyles.displayLarge,
        AppTextStyles.titleLarge,
      ];
      for (final s in displayStyles) {
        expect(s.fontFamily, displayFamily);
      }
    });
  });
}
