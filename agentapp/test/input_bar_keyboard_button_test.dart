// Source-level guard for the composer's input bar — the right-side
// keyboard icon button must not return.
//
// Background (Task #12): the input bar previously had a trailing
// IconButton with `Icons.keyboard_hide` to the right of the send arrow.
// It opened a special-keys bottom sheet, duplicating the function of
// the left-side ComposerPlusButton ("+"). The duplicate was removed so
// only one special-keys entry point remains (the "+" button at the
// composer's left edge, which calls `_showSpecialKeysSheet`).
//
// This test guards that the keyboard icon and its inline bottom-sheet
// builder do not come back into the _InputBar widget.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  late String inputBarBody;

  setUpAll(() {
    final file = File('lib/screens/agent_detail_screen.dart');
    expect(file.existsSync(), isTrue,
        reason: 'expected agent_detail_screen.dart at lib/screens/');
    final source = file.readAsStringSync();
    final inputBarStart = source.indexOf('class _InputBarState');
    expect(inputBarStart, greaterThan(0),
        reason: 'expected _InputBarState class in agent_detail_screen.dart');
    // Slice from _InputBarState to end of file — the build() method and
    // any helpers tied to the input bar live in this region.
    inputBarBody = source.substring(inputBarStart);
  });

  test('_InputBar must not contain the right-side keyboard IconButton',
      () {
    // The deleted IconButton used Icons.keyboard_hide. No occurrence of
    // that identifier should remain inside _InputBar.
    expect(inputBarBody.contains('Icons.keyboard_hide'), isFalse,
        reason: 'right-side keyboard icon button (Icons.keyboard_hide) was '
            'removed in Task #12; the "+" ComposerPlusButton is the only '
            'special-keys entry on the composer.');
  });

  test('_InputBar must not have an inline duplicate special-keys sheet',
      () {
    // The deleted IconButton inlined its own showModalBottomSheet using
    // `widget.onKey?.call(...)` (note the null-aware ?. — the live
    // _showSpecialKeysSheet calls `widget.onKey(...)` directly because
    // onKey is non-nullable on _InputBar). If `widget.onKey?.call` ever
    // shows up again it means the duplicate has come back.
    expect(inputBarBody.contains('widget.onKey?.call('), isFalse,
        reason: 'inline special-keys sheet (with widget.onKey?.call) '
            'was removed in Task #12; the live builder is '
            '_showSpecialKeysSheet which uses widget.onKey(...) directly.');
  });
}
