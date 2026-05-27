import 'package:flutter/material.dart';

/// Centralised typography scale (8-step Major Second 1.125x).
///
/// Three font families are used:
///
/// - **Display** (`Source Han Sans CN` Heavy) — high-impact display headings.
///   Carries the "mission control" personality on app bars, hero titles,
///   and empty-state labels.
/// - **Body** (`Noto Sans SC`) — every-day reading text. The default family
///   inherited by Material's textTheme.
/// - **Mono** (`JetBrainsMono`) — used for data columns: PIDs, sessionIds,
///   timestamps, ports, file paths.
///
/// All families are bundled locally — no `google_fonts` / CDN dependency.
class AppTextStyles {
  AppTextStyles._();

  /// Body / default UI text.
  static const String fontFamily = 'Noto Sans SC';

  /// Display (Heavy / high-impact) typography.
  static const String displayFontFamily = 'Source Han Sans CN';

  /// Monospace data-column typography.
  static const String monoFontFamily = 'JetBrainsMono';

  // ── Display (Source Han Sans CN Heavy) ───────────────────────────────
  static const TextStyle displayLarge = TextStyle(
    fontFamily: displayFontFamily,
    fontSize: 28,
    fontWeight: FontWeight.w900,
    height: 1.2,
    letterSpacing: -0.5,
  );

  // ── Titles ───────────────────────────────────────────────────────────
  static const TextStyle titleLarge = TextStyle(
    fontFamily: displayFontFamily,
    fontSize: 24,
    fontWeight: FontWeight.w900,
    height: 1.25,
    letterSpacing: -0.3,
  );

  static const TextStyle titleMedium = TextStyle(
    fontFamily: fontFamily,
    fontSize: 20,
    fontWeight: FontWeight.w500,
    height: 1.3,
  );

  // ── Body (Noto Sans SC) ──────────────────────────────────────────────
  static const TextStyle bodyLarge = TextStyle(
    fontFamily: fontFamily,
    fontSize: 18,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );

  static const TextStyle bodyMedium = TextStyle(
    fontFamily: fontFamily,
    fontSize: 16,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );

  static const TextStyle bodySmall = TextStyle(
    fontFamily: fontFamily,
    fontSize: 14,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );

  // ── Labels / captions ────────────────────────────────────────────────
  static const TextStyle labelSmall = TextStyle(
    fontFamily: fontFamily,
    fontSize: 13,
    fontWeight: FontWeight.w500,
    height: 1.3,
  );

  static const TextStyle caption = TextStyle(
    fontFamily: fontFamily,
    fontSize: 12,
    fontWeight: FontWeight.w400,
    height: 1.3,
  );

  // ── Mono (JetBrainsMono) ─────────────────────────────────────────────
  /// Default mono style — for inline PIDs, sessionIds, timestamps, ports.
  static const TextStyle mono = TextStyle(
    fontFamily: monoFontFamily,
    fontSize: 13,
    fontWeight: FontWeight.w400,
    height: 1.35,
  );

  /// Larger mono variant for prominent data displays (file paths, code).
  static const TextStyle monoLarge = TextStyle(
    fontFamily: monoFontFamily,
    fontSize: 14,
    fontWeight: FontWeight.w400,
    height: 1.4,
  );
}
