import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_spacing.dart';

void main() {
  group('AppSpacing (8pt grid)', () {
    test('xs == 4', () => expect(AppSpacing.xs, 4));
    test('sm == 8', () => expect(AppSpacing.sm, 8));
    test('md == 12', () => expect(AppSpacing.md, 12));
    test('lg == 16', () => expect(AppSpacing.lg, 16));
    test('xl == 24', () => expect(AppSpacing.xl, 24));
    test('xxl == 32', () => expect(AppSpacing.xxl, 32));

    test('values are monotonically increasing', () {
      final values = <double>[
        AppSpacing.xs,
        AppSpacing.sm,
        AppSpacing.md,
        AppSpacing.lg,
        AppSpacing.xl,
        AppSpacing.xxl,
      ];
      for (var i = 1; i < values.length; i++) {
        expect(values[i] > values[i - 1], isTrue,
            reason: 'spacing must monotonically increase');
      }
    });
  });
}
