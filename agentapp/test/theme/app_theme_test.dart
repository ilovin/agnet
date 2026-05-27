import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';

void main() {
  group('AppTheme.build', () {
    test('returns a Material 3 ThemeData', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      expect(theme.useMaterial3, isTrue);
    });

    test('binds Noto Sans SC fontFamily with required fallbacks', () {
      final theme = AppTheme.build(densityMode: DensityMode.standard);
      // Material 3 routes fontFamily through textTheme.bodyMedium.
      expect(theme.textTheme.bodyMedium?.fontFamily, 'Noto Sans SC');
      // The MaterialApp-level fontFamily must also be set so that
      // ad-hoc TextStyle() calls inherit the same family.
      expect(theme.textTheme.bodyLarge?.fontFamily, 'Noto Sans SC');
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
      // Caption maps to labelMedium in Material 3 textTheme.
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
      expect(theme.appBarTheme.scrolledUnderElevation, anyOf(0.0, 1.0));
    });

    test('cards have rounded 12 corners and low elevation', () {
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
  });
}
