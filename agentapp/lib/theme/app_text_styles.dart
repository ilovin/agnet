import 'package:flutter/material.dart';

/// Centralised typography scale (8-step Major Second 1.125x).
///
/// All styles are bound to the locally bundled `Noto Sans SC` family —
/// no external CDN/google_fonts package is used.
///
/// Use these constants directly when you need a specific style outside
/// of a [Theme]-aware context, or read via `Theme.of(context).textTheme`
/// (see `app_theme.dart` which maps these into Material's textTheme).
class AppTextStyles {
  AppTextStyles._();

  static const String fontFamily = 'Noto Sans SC';

  // ── Display ──────────────────────────────────────────────────────────
  static const TextStyle displayLarge = TextStyle(
    fontFamily: fontFamily,
    fontSize: 28,
    fontWeight: FontWeight.w700,
    height: 1.25,
  );

  // ── Titles ───────────────────────────────────────────────────────────
  static const TextStyle titleLarge = TextStyle(
    fontFamily: fontFamily,
    fontSize: 24,
    fontWeight: FontWeight.w700,
    height: 1.25,
  );

  static const TextStyle titleMedium = TextStyle(
    fontFamily: fontFamily,
    fontSize: 20,
    fontWeight: FontWeight.w500,
    height: 1.3,
  );

  // ── Body ─────────────────────────────────────────────────────────────
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
}
