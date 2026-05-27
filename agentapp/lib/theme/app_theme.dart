import 'package:flutter/material.dart';

import 'app_text_styles.dart';
import 'density_mode.dart';

/// Builds a [ThemeData] from the design tokens defined in
/// `app_text_styles.dart`, `app_spacing.dart`, and `density_mode.dart`.
///
/// All text styles route through the locally bundled `Noto Sans SC`
/// font (see `pubspec.yaml`); no remote font sources are referenced.
class AppTheme {
  AppTheme._();

  static const _fontFamily = AppTextStyles.fontFamily;
  static const _fontFamilyFallback = <String>[
    'Noto Sans Symbols 2',
    'Noto Color Emoji',
    'PingFang SC',
    'Microsoft YaHei',
    'sans-serif',
  ];

  /// Build a complete [ThemeData] for the given density and brightness.
  static ThemeData build({
    required DensityMode densityMode,
    Brightness brightness = Brightness.light,
    Color seedColor = Colors.indigo,
  }) {
    final colorScheme = ColorScheme.fromSeed(
      seedColor: seedColor,
      brightness: brightness,
    );

    final textTheme = _buildTextTheme(densityMode, colorScheme);

    return ThemeData(
      useMaterial3: true,
      colorScheme: colorScheme,
      brightness: brightness,
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFamilyFallback,
      textTheme: textTheme,
      appBarTheme: AppBarTheme(
        elevation: 0,
        scrolledUnderElevation: 1,
        backgroundColor: colorScheme.surface,
        foregroundColor: colorScheme.onSurface,
        surfaceTintColor: colorScheme.surfaceTint,
        titleTextStyle: textTheme.titleMedium?.copyWith(
          color: colorScheme.onSurface,
          fontWeight: FontWeight.w600,
        ),
        shape: Border(
          bottom: BorderSide(
            color: colorScheme.outlineVariant,
            width: 1,
          ),
        ),
      ),
      cardTheme: CardThemeData(
        elevation: 1,
        shape: RoundedRectangleBorder(
          side: BorderSide(color: colorScheme.outlineVariant, width: 1),
          borderRadius: BorderRadius.circular(12),
        ),
        clipBehavior: Clip.antiAlias,
      ),
      dividerTheme: DividerThemeData(
        color: colorScheme.outlineVariant,
        thickness: 1,
        space: 1,
      ),
      listTileTheme: ListTileThemeData(
        iconColor: colorScheme.onSurfaceVariant,
        textColor: colorScheme.onSurface,
      ),
      textSelectionTheme: TextSelectionThemeData(
        selectionColor: colorScheme.primary.withValues(alpha: 0.50),
        cursorColor: colorScheme.primary,
        selectionHandleColor: colorScheme.primary,
      ),
    );
  }

  static TextTheme _buildTextTheme(
    DensityMode density,
    ColorScheme colorScheme,
  ) {
    TextStyle scale(TextStyle base) {
      final size = (base.fontSize ?? 14) * density.textScale;
      return base.copyWith(fontSize: size, color: colorScheme.onSurface);
    }

    return TextTheme(
      displayLarge: scale(AppTextStyles.displayLarge),
      displayMedium: scale(AppTextStyles.titleLarge),
      displaySmall: scale(AppTextStyles.titleMedium),
      titleLarge: scale(AppTextStyles.titleLarge),
      titleMedium: scale(AppTextStyles.titleMedium),
      titleSmall: scale(AppTextStyles.bodyMedium.copyWith(
        fontWeight: FontWeight.w500,
      )),
      bodyLarge: scale(AppTextStyles.bodyLarge),
      bodyMedium: scale(AppTextStyles.bodyMedium),
      bodySmall: scale(AppTextStyles.bodySmall),
      labelLarge: scale(AppTextStyles.labelSmall.copyWith(fontSize: 14)),
      labelMedium: scale(AppTextStyles.caption),
      labelSmall: scale(AppTextStyles.labelSmall),
    );
  }
}
