import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_text_styles.dart';

void main() {
  group('AppTextStyles', () {
    const expectedFamily = 'Noto Sans SC';

    void verify(TextStyle style, double size, FontWeight weight, double height) {
      expect(style.fontFamily, expectedFamily,
          reason: 'fontFamily must be the local Noto Sans SC');
      expect(style.fontSize, size);
      expect(style.fontWeight, weight);
      expect(style.height, height);
    }

    test('displayLarge: 28 / 1.25 / w700', () {
      verify(AppTextStyles.displayLarge, 28, FontWeight.w700, 1.25);
    });

    test('titleLarge: 24 / 1.25 / w700', () {
      verify(AppTextStyles.titleLarge, 24, FontWeight.w700, 1.25);
    });

    test('titleMedium: 20 / 1.3 / w500', () {
      verify(AppTextStyles.titleMedium, 20, FontWeight.w500, 1.3);
    });

    test('bodyLarge: 18 / 1.4 / w400', () {
      verify(AppTextStyles.bodyLarge, 18, FontWeight.w400, 1.4);
    });

    test('bodyMedium: 16 / 1.4 / w400', () {
      verify(AppTextStyles.bodyMedium, 16, FontWeight.w400, 1.4);
    });

    test('bodySmall: 14 / 1.4 / w400', () {
      verify(AppTextStyles.bodySmall, 14, FontWeight.w400, 1.4);
    });

    test('labelSmall: 13 / 1.3 / w500', () {
      verify(AppTextStyles.labelSmall, 13, FontWeight.w500, 1.3);
    });

    test('caption: 12 / 1.3 / w400', () {
      verify(AppTextStyles.caption, 12, FontWeight.w400, 1.3);
    });

    test('all styles use Noto Sans SC family', () {
      final all = <TextStyle>[
        AppTextStyles.displayLarge,
        AppTextStyles.titleLarge,
        AppTextStyles.titleMedium,
        AppTextStyles.bodyLarge,
        AppTextStyles.bodyMedium,
        AppTextStyles.bodySmall,
        AppTextStyles.labelSmall,
        AppTextStyles.caption,
      ];
      for (final s in all) {
        expect(s.fontFamily, expectedFamily);
      }
    });
  });
}
