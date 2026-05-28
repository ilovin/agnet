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

  /// Theme-level font-family fallback. Mirrors
  /// [AppTextStyles.fontFamilyFallback] so that **every** TextStyle that
  /// originates from this theme — explicit (`AppTextStyles.bodyMedium`)
  /// or implicit (`Text('...')` inheriting `DefaultTextStyle`) —
  /// resolves uncovered glyphs (arrows / shapes / box-drawing / emoji)
  /// through the same chain.
  ///
  /// In particular `Noto Sans Symbols 2` is bundled in pubspec.yaml and
  /// covers U+2190..U+21FF (arrows incl. `→`), U+25A0..U+25FF (geometric
  /// shapes incl. `★`), U+2600..U+27BF (misc symbols incl. `✓`/`⏺`).
  /// Without this fallback those glyphs render as .notdef tofu under
  /// Flutter Web CanvasKit.
  static const _fontFamilyFallback = AppTextStyles.fontFamilyFallback;

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
    // Apply the shared fallback chain on every entry so that any widget
    // that looks up `Theme.of(context).textTheme.bodyMedium` (or any
    // other tier) is guaranteed to carry the Symbols 2 / Color Emoji
    // fallback — not just the ones that were authored by hand in
    // [AppTextStyles].
    final textTheme = _buildTextTheme(densityMode, colorScheme)
        .apply(fontFamilyFallback: _fontFamilyFallback);

    final scaffoldBackground = isDark ? AppColors.bgDark : AppColors.bgLight;
    final cardBackground = isDark ? AppColors.elevDark : AppColors.elevLight;
    final hairlineColor = isDark ? AppColors.borderDark : AppColors.borderLight;
    final accent = isDark ? AppColors.accentDark : AppColors.accentLight;

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
        shape: Border(
          bottom: BorderSide(
            color: hairlineColor,
            width: 1,
          ),
        ),
      ),
      cardTheme: CardThemeData(
        elevation: isDark ? 0 : 1,
        color: cardBackground,
        surfaceTintColor: Colors.transparent,
        shape: RoundedRectangleBorder(
          side: BorderSide(color: hairlineColor, width: 1),
          borderRadius: BorderRadius.circular(12),
        ),
        clipBehavior: Clip.antiAlias,
      ),
      dividerTheme: DividerThemeData(
        color: hairlineColor,
        thickness: 1,
        space: 1,
      ),
      listTileTheme: ListTileThemeData(
        iconColor: colorScheme.onSurfaceVariant,
        textColor: colorScheme.onSurface,
      ),
      textSelectionTheme: TextSelectionThemeData(
        selectionColor: accent.withValues(alpha: 0.50),
        cursorColor: accent,
        selectionHandleColor: accent,
      ),
    );
  }

  static ColorScheme _buildColorScheme(Brightness brightness) {
    if (brightness == Brightness.dark) {
      return const ColorScheme(
        brightness: Brightness.dark,
        primary: AppColors.accentDark,
        onPrimary: AppColors.onAccent,
        primaryContainer: AppColors.accentContainerDark,
        onPrimaryContainer: AppColors.onAccentContainerDark,
        secondary: AppColors.data,
        onSecondary: Color(0xFF003733),
        secondaryContainer: Color(0xFF153B38),
        onSecondaryContainer: Color(0xFFD7F2EF),
        tertiary: AppColors.warn,
        onTertiary: Color(0xFF2A1B00),
        tertiaryContainer: Color(0xFF3A2A00),
        onTertiaryContainer: Color(0xFFFFE6B5),
        error: AppColors.error,
        onError: Color(0xFFFFE6E8),
        errorContainer: Color(0xFF4A1419),
        onErrorContainer: Color(0xFFFFD9DD),
        surface: AppColors.bgDark,
        onSurface: AppColors.textDark,
        surfaceContainerHighest: AppColors.elevDark,
        onSurfaceVariant: AppColors.mutedDark,
        outline: AppColors.borderDark,
        outlineVariant: AppColors.borderDark,
      );
    }
    return const ColorScheme(
      brightness: Brightness.light,
      primary: AppColors.accentLight,
      onPrimary: AppColors.onAccent,
      primaryContainer: AppColors.accentContainerLight,
      onPrimaryContainer: AppColors.onAccentContainerLight,
      secondary: AppColors.data,
      onSecondary: Color(0xFF003733),
      secondaryContainer: Color(0xFFCFEEEA),
      onSecondaryContainer: Color(0xFF002824),
      tertiary: AppColors.warn,
      onTertiary: Color(0xFF2A1B00),
      tertiaryContainer: Color(0xFFFFE7B0),
      onTertiaryContainer: Color(0xFF2A1B00),
      error: AppColors.error,
      onError: Colors.white,
      errorContainer: Color(0xFFFFD9DD),
      onErrorContainer: Color(0xFF410006),
      surface: AppColors.bgLight,
      onSurface: AppColors.textLight,
      surfaceContainerHighest: AppColors.elevLight,
      onSurfaceVariant: AppColors.mutedLight,
      outline: AppColors.borderLight,
      outlineVariant: AppColors.borderLight,
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
