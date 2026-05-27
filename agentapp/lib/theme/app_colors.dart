import 'package:flutter/material.dart';

/// Mission-control / "哨所" palette.
///
/// Centralised colour tokens for the BOLD 70/30 redesign. All colours are
/// defined as compile-time constants (no remote / dynamic values) so the
/// theme can be evaluated at startup without async work.
///
/// Naming intent:
/// - [ink]        — primary dark surface (a near-black with cool tint).
/// - [inkElev]    — slightly elevated dark surface (cards, sheets).
/// - [surface]    — light-mode background, warm off-white.
/// - [accent]     — signal blue (station-radio tone). Used for CTAs, running state, hairlines.
/// - [data]       — secondary data colour (cyan/teal).
/// - [warn]       — non-fatal warning amber.
/// - [error]      — fatal / error red.
/// - [hairline]   — accent at 12% alpha, used for 1px dividers / outlines.
class AppColors {
  AppColors._();

  /// Primary dark background (near-black, cool tint).
  static const Color ink = Color(0xFF0E1116);

  /// Elevated dark surface (cards, sheets) on top of [ink].
  static const Color inkElev = Color(0xFF1A1F26);

  /// Light-mode background, warm off-white (米白偏暖).
  static const Color surface = Color(0xFFFAFAF7);

  /// Signal blue (哨所电台). Used for CTAs, running state, accent strokes.
  static const Color accent = Color(0xFF5B9DB8);

  /// Secondary data accent (teal/cyan). Pairs with [accent].
  static const Color data = Color(0xFF4ECDC4);

  /// Non-fatal warning amber.
  static const Color warn = Color(0xFFFFB627);

  /// Fatal / error red.
  static const Color error = Color(0xFFE63946);

  /// Hairline divider colour: [accent] at 12% opacity.
  ///
  /// Use this for 1px borders / dividers that should read as part of the
  /// mission-control accent system rather than a neutral grey.
  static const Color hairline = Color(0x1F5B9DB8); // alpha 0x1F ~= 12%
}
