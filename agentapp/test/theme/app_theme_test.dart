import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_colors.dart';
import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';

void main() {
  group('AppTheme.build', () {
    test('returns a Material 3 ThemeData', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.useMaterial3, isTrue);
    });

    test('binds Noto Sans SC body fontFamily', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.textTheme.bodyMedium?.fontFamily, 'Noto Sans SC');
      expect(theme.textTheme.bodyLarge?.fontFamily, 'Noto Sans SC');
    });

    test('binds Source Han Sans CN to display tier', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.textTheme.displayLarge?.fontFamily, 'Source Han Sans CN');
      expect(theme.textTheme.titleLarge?.fontFamily, 'Source Han Sans CN');
    });

    test('textTheme matches AppTextStyles at standard density', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.textTheme.displayLarge?.fontSize, 28);
      expect(theme.textTheme.titleLarge?.fontSize, 24);
      expect(theme.textTheme.titleMedium?.fontSize, 20);
      expect(theme.textTheme.bodyLarge?.fontSize, 18);
      expect(theme.textTheme.bodyMedium?.fontSize, 16);
      expect(theme.textTheme.bodySmall?.fontSize, 14);
      expect(theme.textTheme.labelSmall?.fontSize, 13);
      expect(theme.textTheme.labelMedium?.fontSize, 12);
    });

    test('compact density scales text by 0.92', () {
      final theme = AppTheme.build(densityMode: DensityMode.compact);
      expect(theme.textTheme.bodyMedium?.fontSize, closeTo(16 * 0.92, 0.001));
      expect(theme.textTheme.titleLarge?.fontSize, closeTo(24 * 0.92, 0.001));
    });

    test('comfortable density scales text by 1.08', () {
      final theme = AppTheme.build(densityMode: DensityMode.comfortable);
      expect(theme.textTheme.bodyMedium?.fontSize, closeTo(16 * 1.08, 0.001));
      expect(theme.textTheme.titleLarge?.fontSize, closeTo(24 * 1.08, 0.001));
    });

    test('appBar has flat (no shadow) styling', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.appBarTheme.elevation, 0);
      expect(theme.appBarTheme.scrolledUnderElevation, 0);
    });

    test('cards have rounded 12 corners', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final shape = theme.cardTheme.shape;
      expect(shape, isA<RoundedRectangleBorder>());
      final radius = (shape as RoundedRectangleBorder).borderRadius
          .resolve(TextDirection.ltr);
      expect(radius.topLeft.x, 12);
      expect(theme.cardTheme.elevation, lessThanOrEqualTo(1.0));
    });

    test('dark variant returns dark brightness', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.brightness, Brightness.dark);
    });

    test('light mode primary color is signal accent', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.colorScheme.primary, AppColors.accent);
    });

    test('dark mode primary color is signal accent', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.colorScheme.primary, AppColors.accent);
    });

    test('light mode scaffold background is warm off-white surface', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.scaffoldBackgroundColor, AppColors.surface);
    });

    test('dark mode scaffold background is ink', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.scaffoldBackgroundColor, AppColors.ink);
    });

    test('dark mode card background is inkElev', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.cardTheme.color, AppColors.inkElev);
    });

    test('appBar shape uses hairline divider colour', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      final shape = theme.appBarTheme.shape;
      expect(shape, isA<Border>());
      final border = shape as Border;
      expect(border.bottom.color, AppColors.hairline);
      expect(border.bottom.width, 1);
    });

    test('divider theme uses hairline accent', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.dividerTheme.color, AppColors.hairline);
    });
  });

  group('AppColors tokens', () {
    test('hairline is accent at ~12% alpha', () {
      // Approximate check: alpha component should be small but nonzero.
      final hairlineAlpha = AppColors.hairline.a;
      expect(hairlineAlpha, lessThan(0.20));
      expect(hairlineAlpha, greaterThan(0.0));
    });

    test('accent is signal orange', () {
      expect(AppColors.accent, const Color(0xFFFF6B35));
    });

    test('ink is near-black with cool tint', () {
      expect(AppColors.ink, const Color(0xFF0E1116));
    });
  });
}
