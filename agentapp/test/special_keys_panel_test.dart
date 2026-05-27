// Source-level guard for the special-keys bottom sheets in
// agent_detail_screen.dart.
//
// Background: in two prior rounds of font-fallback tweaks, the four arrow
// keys (↑ ↓ ← →) still rendered as tofu (□) on Chrome because the literal
// codepoints (U+2191/2193/2190/2192) hit fontFamily fallback gaps before
// the Material Icons font ever ran. The actual fix is to swap the literal
// arrow Text widgets for `Icon(Icons.arrow_upward/downward/back/forward)`,
// which uses the bundled Material Icons font and always renders.
//
// This test guards that change: the literal arrow codepoints must NOT
// appear inside either special-keys bottom sheet builder (`_showSpecialKeys
// Panel` and `_showSpecialKeysSheet`). Any future regression that
// reintroduces them will fail here before it lands in Chrome.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('agent_detail_screen.dart special-keys panels use Material Icons (no literal arrow codepoints)', () {
    final file = File('lib/screens/agent_detail_screen.dart');
    expect(file.existsSync(), isTrue,
        reason: 'expected agent_detail_screen.dart at lib/screens/');
    final source = file.readAsStringSync();

    // Locate both special-keys bottom-sheet builders by their well-known
    // method names.
    final panelStart = source.indexOf('_showSpecialKeysPanel(BuildContext context)');
    final sheetStart = source.indexOf('_showSpecialKeysSheet(BuildContext context)');
    expect(panelStart, greaterThan(0),
        reason: 'expected _showSpecialKeysPanel builder');
    expect(sheetStart, greaterThan(0),
        reason: 'expected _showSpecialKeysSheet builder');

    // For each builder, slice ~6KB ahead — both builders are well under that.
    String slice(int start) {
      final end = (start + 6000).clamp(0, source.length);
      return source.substring(start, end);
    }

    final panelBody = slice(panelStart);
    final sheetBody = slice(sheetStart);

    // The four arrow codepoints that previously rendered as tofu (□) on
    // Chrome. None of these may reappear inside either special-keys panel.
    const arrowCodepoints = <String, int>{
      'arrow_upward (↑)': 0x2191,
      'arrow_downward (↓)': 0x2193,
      'arrow_back (←)': 0x2190,
      'arrow_forward (→)': 0x2192,
    };

    for (final entry in arrowCodepoints.entries) {
      final ch = String.fromCharCode(entry.value);
      expect(panelBody.contains(ch), isFalse,
          reason: '_showSpecialKeysPanel must not embed literal '
              '${entry.key}; use Icon(Icons.${entry.key.split(' ').first}) '
              'so Material Icons font (always present) renders it.');
      expect(sheetBody.contains(ch), isFalse,
          reason: '_showSpecialKeysSheet must not embed literal '
              '${entry.key}; use Icon(Icons.${entry.key.split(' ').first}) '
              'so Material Icons font (always present) renders it.');
    }

    // Positive: each builder must reference each Material Icons identifier.
    const expectedIcons = [
      'Icons.arrow_upward',
      'Icons.arrow_downward',
      'Icons.arrow_back',
      'Icons.arrow_forward',
    ];
    for (final iconRef in expectedIcons) {
      expect(panelBody.contains(iconRef), isTrue,
          reason: '_showSpecialKeysPanel should reference $iconRef');
      expect(sheetBody.contains(iconRef), isTrue,
          reason: '_showSpecialKeysSheet should reference $iconRef');
    }
  });
}
