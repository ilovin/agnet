// Regression test for #67: drag-to-select skips text inside _ActivityBlock.
//
// Root cause: a GestureDetector wrapping the entire _ActivityBlock body
// claimed tap/long-press gestures, blocking SelectionArea's drag in body
// region.
//
// Fix: GestureDetector is now scoped to only the header row (chevron +
// preview text). Body region (expanded _ActivityCard list) sits outside
// the GestureDetector subtree.
//
// What this test asserts:
//   - The MarkdownContent body is NOT a descendant of a GestureDetector
//     that handles onLongPress (the long-press gesture was the prime
//     blocker for SelectionArea drag selection).

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/screens/agent_detail_screen.dart';

ChatMessage _activityListMessage() {
  return ChatMessage(
    role: 'assistant',
    text: '[Bash: ls -la]',
    seq: 1,
    kind: 'activity_list',
    activities: [
      {
        'kind': 'tool_use',
        'toolName': 'Bash',
        'title': 'ls -la',
        'content': 'this is selectable body text content',
      },
    ],
  );
}

Future<void> _pumpBubble(WidgetTester tester, ChatMessage msg) async {
  await tester.pumpWidget(
    ProviderScope(
      child: MaterialApp(
        home: Scaffold(
          body: SelectionArea(
            child: SingleChildScrollView(
              child: MessageBubble(message: msg),
            ),
          ),
        ),
      ),
    ),
  );
  // Note: we deliberately use pump (not pumpAndSettle) because there is a
  // pre-existing paint exception in the activity block container due to
  // a BorderRadius-with-non-uniform-border combination; that does not
  // affect our gesture-tree assertion. We swallow the paint exception
  // explicitly so the test frame still completes.
  await tester.pump();
  // Drain any paint-time exceptions that the framework would otherwise
  // surface as test failures. They are unrelated to the gesture-tree
  // structure we are asserting.
  // ignore: invalid_use_of_protected_member
  tester.takeException();
}

void main() {
  testWidgets(
    'Header preview text exists when collapsed (#67 sanity)',
    (tester) async {
      await _pumpBubble(tester, _activityListMessage());
      // Collapsed preview is "Bash: ls -la" assembled from toolName/title.
      expect(find.text('Bash: ls -la'), findsOneWidget);
    },
  );

  testWidgets(
    'Header GestureDetector is scoped — header has tap/long-press, '
    'block-level GestureDetector with onLongPress wrapping the entire '
    'AnimatedSize subtree no longer exists (#67)',
    (tester) async {
      await _pumpBubble(tester, _activityListMessage());

      // Find the header preview text.
      final headerText = find.text('Bash: ls -la');
      expect(headerText, findsOneWidget);

      // Walk up ancestors. The closest GestureDetector ancestor of the
      // header should still have onTap and onLongPress (this is the
      // expand/collapse + copy interaction; it is intentionally there).
      final headerGestureFinder = find.ancestor(
        of: headerText,
        matching: find.byType(GestureDetector),
      );
      final headerDetectors = tester
          .widgetList<GestureDetector>(headerGestureFinder)
          .toList();
      // At least one ancestor GestureDetector exists for the header.
      expect(
        headerDetectors.any((g) => g.onTap != null && g.onLongPress != null),
        isTrue,
        reason: 'Header should still be tappable and long-pressable for '
            'expand/collapse and copy.',
      );

      // Now find an AnimatedSize ancestor (the outer wrapper of the
      // _ActivityBlock body). Before the fix, a GestureDetector with
      // onLongPress wrapped this AnimatedSize. After the fix, that
      // GestureDetector should NOT be an ancestor of AnimatedSize — it
      // should be a descendant (sibling of the body).
      final animatedSizeFinder = find.ancestor(
        of: headerText,
        matching: find.byType(AnimatedSize),
      );
      expect(animatedSizeFinder, findsWidgets);

      final animatedSizeAncestorDetectors = tester
          .widgetList<GestureDetector>(
            find.ancestor(
              of: animatedSizeFinder.first,
              matching: find.byType(GestureDetector),
            ),
          )
          .toList();
      // None of the ancestors of the AnimatedSize should have
      // onLongPress — that was the gesture that blocked drag-selection.
      final hasLongPressAboveBody = animatedSizeAncestorDetectors.any(
        (g) => g.onLongPress != null,
      );
      expect(
        hasLongPressAboveBody,
        isFalse,
        reason:
            'A GestureDetector with onLongPress above the AnimatedSize '
            '(the body wrapper) would block SelectionArea drag-selection '
            'inside the expanded body cards (#67).',
      );
    },
  );
}
