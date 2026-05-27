import 'package:flutter/material.dart';

/// GitHub-style coding palette.
///
/// Migrated from the original mission-control / "哨所" signal-blue + ink
/// palette to GitHub's standard light + dark coding-UI tokens. The intent
/// is: a familiar, calm, code-first surface that lets content (chat
/// messages, code blocks, status chips) carry the colour weight rather
/// than the chrome.
///
/// Tokens map onto Material 3 [ColorScheme] slots in
/// [AppTheme._buildColorScheme]:
/// - [bgDark] / [bgLight]                       → [ColorScheme.surface]
/// - [elevDark] / [elevLight]                   → [ColorScheme.surfaceContainerHighest]
/// - [textDark] / [textLight]                   → [ColorScheme.onSurface]
/// - [mutedDark] / [mutedLight]                 → [ColorScheme.onSurfaceVariant]
/// - [borderDark] / [borderLight]               → [ColorScheme.outline] / outlineVariant
/// - [accentDark] / [accentLight]               → [ColorScheme.primary]
/// - [onAccent]                                 → [ColorScheme.onPrimary]
/// - [accentContainerDark] / [accentContainerLight]
///                                              → [ColorScheme.primaryContainer]
/// - [onAccentContainerDark] / [onAccentContainerLight]
///                                              → [ColorScheme.onPrimaryContainer]
///
/// Status colours ([data], [warn], [error]) carry over unchanged from the
/// previous palette so semantic meaning (running / warning / error) does
/// not silently shift across the migration.
///
/// Legacy aliases ([ink], [inkElev], [surface], [accent], [hairline]) are
/// preserved so direct consumers do not need to be touched in the same
/// changeset; new code should prefer the brightness-aware tokens above
/// or pull from [Theme.of(context).colorScheme].
class AppColors {
  AppColors._();

  // ── GitHub palette: dark ────────────────────────────────────────────────
  /// Dark background / [ColorScheme.surface].
  static const Color bgDark = Color(0xFF0D1117);

  /// Dark elevated surface (cards, sheets) / [ColorScheme.surfaceContainerHighest].
  static const Color elevDark = Color(0xFF161B22);

  /// Dark primary text / [ColorScheme.onSurface].
  static const Color textDark = Color(0xFFC9D1D9);

  /// Dark secondary / muted text / [ColorScheme.onSurfaceVariant].
  static const Color mutedDark = Color(0xFF8B949E);

  /// Dark border / hairline divider / [ColorScheme.outline].
  static const Color borderDark = Color(0xFF30363D);

  /// Dark accent (primary CTA, links, focus ring) / [ColorScheme.primary].
  static const Color accentDark = Color(0xFF58A6FF);

  /// Dark accent container (primary-tinted card surface) /
  /// [ColorScheme.primaryContainer]. Carried over from the previous theme
  /// because it already cleared WCAG AA against [onAccentContainerDark].
  static const Color accentContainerDark = Color(0xFF1F3D49);

  /// Dark text on [accentContainerDark] / [ColorScheme.onPrimaryContainer].
  static const Color onAccentContainerDark = Color(0xFF79C0FF);

  // ── GitHub palette: light ───────────────────────────────────────────────
  /// Light background / [ColorScheme.surface].
  static const Color bgLight = Color(0xFFFFFFFF);

  /// Light elevated surface (cards, sheets) / [ColorScheme.surfaceContainerHighest].
  static const Color elevLight = Color(0xFFF6F8FA);

  /// Light primary text / [ColorScheme.onSurface].
  static const Color textLight = Color(0xFF1F2328);

  /// Light secondary / muted text / [ColorScheme.onSurfaceVariant].
  static const Color mutedLight = Color(0xFF656D76);

  /// Light border / hairline divider / [ColorScheme.outline].
  static const Color borderLight = Color(0xFFD0D7DE);

  /// Light accent (primary CTA, links, focus ring) / [ColorScheme.primary].
  static const Color accentLight = Color(0xFF0969DA);

  /// Light accent container (primary-tinted card surface) /
  /// [ColorScheme.primaryContainer].
  static const Color accentContainerLight = Color(0xFFDDF4FF);

  /// Light text on [accentContainerLight] / [ColorScheme.onPrimaryContainer].
  static const Color onAccentContainerLight = Color(0xFF0969DA);

  /// Always-white text on filled accent surfaces (CTAs).
  static const Color onAccent = Color(0xFFFFFFFF);

  // ── Status (carry-over) ────────────────────────────────────────────────
  /// Secondary data accent (teal/cyan). Pairs with [accentDark]/[accentLight].
  static const Color data = Color(0xFF4ECDC4);

  /// Non-fatal warning amber.
  static const Color warn = Color(0xFFFFB627);

  /// Fatal / error red.
  static const Color error = Color(0xFFE63946);

  // ── Legacy aliases (kept so existing call sites compile unchanged) ─────
  // These intentionally point at the dark-palette tokens because nearly
  // all direct consumers (scanning line, mission-control app bar shell,
  // composer "+" sheet, sentinel illustration) were originally designed
  // against the dark surface and read as accents on either brightness.

  /// Legacy alias for [bgDark] (was the dark scaffold background).
  static const Color ink = bgDark;

  /// Legacy alias for [elevDark] (was the dark elevated surface).
  static const Color inkElev = elevDark;

  /// Legacy alias for [bgLight] (was the warm off-white light surface).
  static const Color surface = bgLight;

  /// Legacy alias for [accentDark] (was the signal-blue brand accent).
  /// Resolves to GitHub dark accent; on light surfaces it still reads as
  /// a familiar primary blue.
  static const Color accent = accentDark;

  /// Legacy alias for [borderDark] (was a 12%-alpha accent hairline).
  /// Now a solid border line so dividers read consistently across both
  /// brightnesses without disappearing on light backgrounds.
  static const Color hairline = borderDark;
}
