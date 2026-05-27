import 'package:flutter/material.dart';

import 'app_colors.dart';
import 'app_text_styles.dart';
import 'density_mode.dart';

/// Builds a [ThemeData] from the design tokens defined in
/// `app_colors.dart`, `app_text_styles.dart`, `app_spacing.dart`,
/// and `density_mode.dart`.
///
/// All text styles route through locally bundled families
/// (`Source Han Sans CN`, `Noto Sans SC`, `JetBrainsMono`); no remote /
/// `google_fonts` source is referenced.
///
/// The "mission control" palette is applied to both [Brightness] modes:
/// signal blue (`AppColors.accent`) is the [ColorScheme.primary] in
/// either mode; only the surface family changes (warm off-white in
/// light mode, ink near-black in dark mode).
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
  ///
  /// The palette is fixed by the design tokens in [AppColors] — there is
  /// no seed colour to override.
  static ThemeData build({
    required DensityMode densityMode,
    Brightness brightness = Brightness.light,
  }) {
    final isDark = brightness == Brightness.dark;
    final colorScheme = _buildColorScheme(brightness);
    final textTheme = _buildTextTheme(densityMode, colorScheme);

    final scaffoldBackground = isDark ? AppColors.ink : AppColors.surface;
    final cardBackground = isDark ? AppColors.inkElev : Colors.white;

    return ThemeData(
      useMaterial3: true,
      colorScheme: colorScheme,
      brightness: brightness,
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFamilyFallback,
      scaffoldBackgroundColor: scaffoldBackground,
      textTheme: textTheme,
      appBarTheme: AppBarTheme(
        elevation: 0,
        scrolledUnderElevation: 0,
        backgroundColor: scaffoldBackground,
        foregroundColor: colorScheme.onSurface,
        surfaceTintColor: Colors.transparent,
        titleTextStyle: textTheme.titleMedium?.copyWith(
          color: colorScheme.onSurface,
          fontWeight: FontWeight.w600,
        ),
        shape: const Border(
          bottom: BorderSide(
            color: AppColors.hairline,
            width: 1,
          ),
        ),
      ),
      cardTheme: CardThemeData(
        elevation: isDark ? 0 : 1,
        color: cardBackground,
        surfaceTintColor: Colors.transparent,
        shape: RoundedRectangleBorder(
          side: const BorderSide(color: AppColors.hairline, width: 1),
          borderRadius: BorderRadius.circular(12),
        ),
        clipBehavior: Clip.antiAlias,
      ),
      dividerTheme: const DividerThemeData(
        color: AppColors.hairline,
        thickness: 1,
        space: 1,
      ),
      listTileTheme: ListTileThemeData(
        iconColor: colorScheme.onSurfaceVariant,
        textColor: colorScheme.onSurface,
      ),
      textSelectionTheme: TextSelectionThemeData(
        selectionColor: AppColors.accent.withValues(alpha: 0.50),
        cursorColor: AppColors.accent,
        selectionHandleColor: AppColors.accent,
      ),
    );
  }

  static ColorScheme _buildColorScheme(Brightness brightness) {
    if (brightness == Brightness.dark) {
      return const ColorScheme(
        brightness: Brightness.dark,
        primary: AppColors.accent,
        onPrimary: Color(0xFF06141B),
        secondary: AppColors.data,
        onSecondary: Color(0xFF003733),
        tertiary: AppColors.warn,
        onTertiary: Color(0xFF2A1B00),
        error: AppColors.error,
        onError: Color(0xFFFFE6E8),
        surface: AppColors.ink,
        onSurface: Color(0xFFFAFAF7),
        surfaceContainerHighest: AppColors.inkElev,
        onSurfaceVariant: Color(0xFFB6BBC2),
        outline: Color(0xFF3A4049),
        outlineVariant: AppColors.hairline,
      );
    }
    return const ColorScheme(
      brightness: Brightness.light,
      primary: AppColors.accent,
      onPrimary: Colors.white,
      secondary: AppColors.data,
      onSecondary: Color(0xFF003733),
      tertiary: AppColors.warn,
      onTertiary: Color(0xFF2A1B00),
      error: AppColors.error,
      onError: Colors.white,
      surface: AppColors.surface,
      onSurface: AppColors.ink,
      surfaceContainerHighest: Color(0xFFEFEFEA),
      onSurfaceVariant: Color(0xFF4A4F57),
      outline: Color(0xFFC8C8C0),
      outlineVariant: AppColors.hairline,
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
