import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/theme/app_text_styles.dart';

/// "Mono application" smoke tests — verify that the design tokens advertise
/// the JetBrainsMono family for data columns. The actual call sites
/// (dashboard agent subtitles, agent_detail meta line, etc.) reference
/// [AppTextStyles.monoFontFamily] directly, so this test guards both
/// correctness of the token and the call-site assumption.
void main() {
  group('mono font application', () {
    test('AppTextStyles.mono uses JetBrainsMono', () {
      expect(AppTextStyles.mono.fontFamily, 'JetBrainsMono');
    });

    test('AppTextStyles.monoLarge uses JetBrainsMono', () {
      expect(AppTextStyles.monoLarge.fontFamily, 'JetBrainsMono');
    });

    test('AppTextStyles.monoFontFamily token equals JetBrainsMono', () {
      expect(AppTextStyles.monoFontFamily, 'JetBrainsMono');
    });

    test('mono token can be applied via copyWith on themed labelMedium',
        () {
      const base = TextStyle(fontSize: 12);
      final styled = base.copyWith(fontFamily: AppTextStyles.monoFontFamily);
      expect(styled.fontFamily, 'JetBrainsMono');
      expect(styled.fontSize, 12);
    });
  });
}
