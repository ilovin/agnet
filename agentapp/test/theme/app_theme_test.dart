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

    test('light mode primary color is GitHub light accent', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.colorScheme.primary, AppColors.accentLight);
    });

    test('dark mode primary color is GitHub dark accent', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.colorScheme.primary, AppColors.accentDark);
    });

    test('light mode scaffold background is GitHub light bg', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.scaffoldBackgroundColor, AppColors.bgLight);
    });

    test('dark mode scaffold background is GitHub dark bg', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.scaffoldBackgroundColor, AppColors.bgDark);
    });

    test('dark mode card background is GitHub dark elev', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.cardTheme.color, AppColors.elevDark);
    });

    test('light mode card background is GitHub light elev', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.cardTheme.color, AppColors.elevLight);
    });

    test('light appBar shape uses light border divider colour', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      final shape = theme.appBarTheme.shape;
      expect(shape, isA<Border>());
      final border = shape as Border;
      expect(border.bottom.color, AppColors.borderLight);
      expect(border.bottom.width, 1);
    });

    test('dark appBar shape uses dark border divider colour', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      final shape = theme.appBarTheme.shape;
      expect(shape, isA<Border>());
      final border = shape as Border;
      expect(border.bottom.color, AppColors.borderDark);
      expect(border.bottom.width, 1);
    });

    test('light divider theme uses light border', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.light,
      );
      expect(theme.dividerTheme.color, AppColors.borderLight);
    });

    test('dark divider theme uses dark border', () {
      final theme = AppTheme.build(
        densityMode: DensityMode.standard,
        brightness: Brightness.dark,
      );
      expect(theme.dividerTheme.color, AppColors.borderDark);
    });
  });

  group('AppColors tokens', () {
    test('GitHub light accent is #0969DA', () {
      expect(AppColors.accentLight, const Color(0xFF0969DA));
    });

    test('GitHub dark accent is #58A6FF', () {
      expect(AppColors.accentDark, const Color(0xFF58A6FF));
    });

    test('GitHub dark bg is near-black #0D1117', () {
      expect(AppColors.bgDark, const Color(0xFF0D1117));
    });

    test('GitHub light bg is pure white', () {
      expect(AppColors.bgLight, const Color(0xFFFFFFFF));
    });

    test('GitHub dark elev is #161B22', () {
      expect(AppColors.elevDark, const Color(0xFF161B22));
    });

    test('GitHub light elev is #F6F8FA', () {
      expect(AppColors.elevLight, const Color(0xFFF6F8FA));
    });

    test('legacy accent alias resolves to dark accent', () {
      expect(AppColors.accent, AppColors.accentDark);
    });

    test('legacy ink alias resolves to dark bg', () {
      expect(AppColors.ink, AppColors.bgDark);
    });
  });
}
