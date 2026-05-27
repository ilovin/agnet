import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/density_mode.dart';

void main() {
  group('DensityMode', () {
    test('three modes exist', () {
      expect(DensityMode.values, hasLength(3));
      expect(DensityMode.values, contains(DensityMode.compact));
      expect(DensityMode.values, contains(DensityMode.standard));
      expect(DensityMode.values, contains(DensityMode.comfortable));
    });

    test('compact: textScale 0.92, spacingScale 0.85', () {
      expect(DensityMode.compact.textScale, 0.92);
      expect(DensityMode.compact.spacingScale, 0.85);
    });

    test('standard: textScale 1.0, spacingScale 1.0', () {
      expect(DensityMode.standard.textScale, 1.0);
      expect(DensityMode.standard.spacingScale, 1.0);
    });

    test('comfortable: textScale 1.08, spacingScale 1.15', () {
      expect(DensityMode.comfortable.textScale, 1.08);
      expect(DensityMode.comfortable.spacingScale, 1.15);
    });

    test('label is human-readable', () {
      expect(DensityMode.compact.label, isNotEmpty);
      expect(DensityMode.standard.label, isNotEmpty);
      expect(DensityMode.comfortable.label, isNotEmpty);
      expect(DensityMode.compact.label, isNot(equals(DensityMode.standard.label)));
    });
  });
}
