// Source-level guard for the composer's special-keys bottom sheet in
// agent_detail_screen.dart.
//
// Background: a previous round of UI work (commits f8b304d / 3d36d6b)
// left two parallel implementations of the "special keys" panel in the
// same file:
//
//   1. _showSpecialKeysPanel  — older implementation built with the local
//      _KeyChip wrapper. No call sites; pure dead code.
//   2. _showSpecialKeysSheet  — the live one, surfaced from the
//      ComposerPlusButton ("+") on the composer's left edge.
//
// Plus an unused _KeyButton widget and a never-called keyToDisplay()
// helper that referenced the same dead path.
//
// This test guards two invariants:
//
//   (a) Exactly ONE special-keys bottom-sheet builder exists, and it is
//       the live `_showSpecialKeysSheet`. The dead `_showSpecialKeysPanel`,
//       `_KeyChip`, `_KeyButton`, `_sendKeyAndClose`, and `keyToDisplay`
//       must NOT come back.
//
//   (b) The remaining sheet uses Material Icons (Icons.arrow_*) for the
//       four arrow keys, never literal Unicode arrow codepoints
//       (U+2191/2193/2190/2192) which previously rendered as tofu (□)
//       on Chrome.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  late String source;

  setUpAll(() {
    final file = File('lib/screens/agent_detail_screen.dart');
    expect(file.existsSync(), isTrue,
        reason: 'expected agent_detail_screen.dart at lib/screens/');
    source = file.readAsStringSync();
  });

  test('exactly one special-keys bottom sheet builder, and it is _showSpecialKeysSheet', () {
    final sheetStart = source.indexOf('_showSpecialKeysSheet(BuildContext context)');
    expect(sheetStart, greaterThan(0),
        reason: 'expected the live _showSpecialKeysSheet builder');

    // The dead duplicate builder must be gone.
    expect(source.contains('_showSpecialKeysPanel'), isFalse,
        reason: 'dead _showSpecialKeysPanel duplicate must be removed; '
            'the live builder is _showSpecialKeysSheet inside _InputBarState');

    // Helpers that only existed for the dead path must also be gone.
    expect(source.contains('_sendKeyAndClose'), isFalse,
        reason: '_sendKeyAndClose was only used by the deleted '
            '_showSpecialKeysPanel; remove it as well');
    expect(source.contains('class _KeyChip'), isFalse,
        reason: '_KeyChip wrapper was only used by the deleted '
            '_showSpecialKeysPanel; the live sheet uses ActionChip directly');
    expect(source.contains('class _KeyButton'), isFalse,
        reason: '_KeyButton has no call sites; remove it');
    expect(source.contains('String keyToDisplay'), isFalse,
        reason: 'keyToDisplay() has no call sites; remove it');
  });

  test('the live special-keys sheet uses Material Icons (no literal arrow codepoints)', () {
    final sheetStart = source.indexOf('_showSpecialKeysSheet(BuildContext context)');
    expect(sheetStart, greaterThan(0),
        reason: 'expected _showSpecialKeysSheet builder');

    // Slice ~6KB ahead — the builder is well under that.
    final end = (sheetStart + 6000).clamp(0, source.length);
    final sheetBody = source.substring(sheetStart, end);

    // The four arrow codepoints that previously rendered as tofu (□).
    const arrowCodepoints = <String, int>{
      'arrow_upward (↑)': 0x2191,
      'arrow_downward (↓)': 0x2193,
      'arrow_back (←)': 0x2190,
      'arrow_forward (→)': 0x2192,
    };

    for (final entry in arrowCodepoints.entries) {
      final ch = String.fromCharCode(entry.value);
      expect(sheetBody.contains(ch), isFalse,
          reason: '_showSpecialKeysSheet must not embed literal '
              '${entry.key}; use Icon(Icons.${entry.key.split(' ').first}) '
              'so Material Icons font (always present) renders it.');
    }

    // Positive: the sheet must reference each Material Icons identifier.
    const expectedIcons = [
      'Icons.arrow_upward',
      'Icons.arrow_downward',
      'Icons.arrow_back',
      'Icons.arrow_forward',
    ];
    for (final iconRef in expectedIcons) {
      expect(sheetBody.contains(iconRef), isTrue,
          reason: '_showSpecialKeysSheet should reference $iconRef');
    }
  });
}
