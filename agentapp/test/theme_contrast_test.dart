// Card readability guard.
//
// User reported "task-notification" (i.e. user-message bubbles styled with
// `primaryContainer` background and `onPrimaryContainer` text) and
// `ask_user_question` cards were rendering blue-text-on-blue-background
// because Material 3's automatic container-token derivation collapsed
// when the seed `primary` was set without explicit container values, so
// `primaryContainer` fell back to a colour very close to `primary` and
// `onPrimaryContainer` followed suit.
//
// The fix: AppTheme._buildColorScheme now sets primary/secondary/tertiary
// container tokens explicitly. This test guards the WCAG AA contrast
// floor (>= 4.5:1 for normal text) for those token pairs in BOTH dark
// and light themes, so any future regression that drops the explicit
// values will fail here.

import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_theme.dart';
import 'package:agentapp/theme/density_mode.dart';

double _relativeLuminance(Color c) {
  double channel(double srgb) {
    final s = srgb / 255.0;
    return s <= 0.03928 ? s / 12.92 : math.pow((s + 0.055) / 1.055, 2.4) as double;
  }

  final r = channel(c.red.toDouble());
  final g = channel(c.green.toDouble());
  final b = channel(c.blue.toDouble());
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

double contrastRatio(Color fg, Color bg) {
  final l1 = _relativeLuminance(fg);
  final l2 = _relativeLuminance(bg);
  final brighter = math.max(l1, l2);
  final darker = math.min(l1, l2);
  return (brighter + 0.05) / (darker + 0.05);
}

void main() {
  group('AppTheme card-token contrast', () {
    for (final brightness in const [Brightness.dark, Brightness.light]) {
      test('${brightness.name} primaryContainer / onPrimaryContainer >= 4.5:1', () {
        final theme = AppTheme.build(
          densityMode: DensityMode.standard,
          brightness: brightness,
        );
        final ratio = contrastRatio(
          theme.colorScheme.onPrimaryContainer,
          theme.colorScheme.primaryContainer,
        );
        expect(
          ratio,
          greaterThanOrEqualTo(4.5),
          reason: '${brightness.name} primaryContainer/onPrimaryContainer '
              'contrast must clear WCAG AA (>= 4.5:1) so user-message bubbles '
              'and ask_user_question cards stay legible. Got $ratio.',
        );
      });

      test('${brightness.name} secondaryContainer / onSecondaryContainer >= 4.5:1', () {
        final theme = AppTheme.build(
          densityMode: DensityMode.standard,
          brightness: brightness,
        );
        final ratio = contrastRatio(
          theme.colorScheme.onSecondaryContainer,
          theme.colorScheme.secondaryContainer,
        );
        expect(
          ratio,
          greaterThanOrEqualTo(4.5),
          reason: '${brightness.name} secondaryContainer/onSecondaryContainer '
              'contrast must clear WCAG AA (>= 4.5:1). Got $ratio.',
        );
      });
    }
  });
}
