// Source-level guard for the composer's input box shape and the mono
// prompt-symbol affordance.
//
// Background: round-rect input replaces the previous capsule (Radius 24)
// to widen the visible character count and harmonise with the mission-
// control aesthetic. A single mono ▸ (U+25B8) glyph appears as a prefix
// hint, rendered with the JetBrainsMono token at AppColors.accent.
//
// This test scopes its assertions to the `_InputBarState.build` method so
// any future regression on those exact decisions surfaces here before
// landing in Chrome.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('input bar uses Radius.circular(10) round-rect and mono ▸ prefix', () {
    final file = File('lib/screens/agent_detail_screen.dart');
    expect(file.existsSync(), isTrue,
        reason: 'expected agent_detail_screen.dart at lib/screens/');
    final source = file.readAsStringSync();

    // Locate the _InputBar widget block by its well-known state class name.
    final inputBarStart = source.indexOf('class _InputBarState');
    expect(inputBarStart, greaterThan(0),
        reason: 'expected _InputBarState class in agent_detail_screen.dart');
    final inputBarBody = source.substring(inputBarStart);

    // The capsule radius must be gone: Radius.circular(24) should not appear
    // anywhere inside the _InputBar widget block.
    expect(inputBarBody.contains('Radius.circular(24)'), isFalse,
        reason: 'capsule radius (24) must be replaced with round-rect (10) '
            'inside _InputBar');

    // The new round-rect radius should be present.
    expect(inputBarBody.contains('Radius.circular(10)'), isTrue,
        reason: 'expected Radius.circular(10) inside _InputBar TextField');

    // The mono prompt-symbol ▸ (U+25B8) should appear exactly once inside
    // _InputBar — as the prefixIcon Text content.
    final promptMatches =
        '▸'.allMatches(inputBarBody).length;
    expect(promptMatches, 1,
        reason: 'expected ▸ (U+25B8) prompt symbol exactly once in _InputBar');
  });
}
