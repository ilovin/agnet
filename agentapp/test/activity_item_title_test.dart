// Verifies that the activity-item title produced by `_buildActivityItem`
// (exposed via `buildActivityItemForTest`) is always meaningful for tool-call
// messages, even when the backend emitted a bare `[ToolName]` with no detail.
//
// Background: the Flutter app receives tool-call messages as
// `[ToolName: summary]` (or `[ToolName]` when the backend produced no
// summary). The activity card in the agent detail screen shows the parsed
// summary as its title. Before the fix, a bare `[TaskUpdate]` resulted in
// an empty title; the defensive fallback now uses the tool name instead.

import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/screens/agent_detail_screen.dart';

void main() {
  group('buildActivityItemForTest title fallback', () {
    test('TaskUpdate with detail shows the formatted summary', () {
      final item = buildActivityItemForTest(
        '[TaskUpdate: #63 -> completed]',
        'tool_use',
        const {},
      );
      expect(item['kind'], 'tool_use');
      expect(item['toolName'], 'TaskUpdate');
      expect(item['title'], '#63 -> completed');
    });

    test('TaskUpdate with status only shows that status', () {
      final item = buildActivityItemForTest(
        '[TaskUpdate: completed]',
        'tool_use',
        const {},
      );
      expect(item['toolName'], 'TaskUpdate');
      expect(item['title'], 'completed');
    });

    test('Bare [TaskUpdate] (no detail) falls back to tool name', () {
      // Backend regression case: streaming `content_block_start` emitted the
      // card before the tool input deltas arrived, so the message text has
      // no colon / no detail. The card must still show a meaningful title.
      final item = buildActivityItemForTest(
        '[TaskUpdate]',
        'tool_use',
        const {},
      );
      expect(item['kind'], 'tool_use');
      expect(item['toolName'], 'TaskUpdate');
      expect(item['title'], 'TaskUpdate',
          reason: 'Empty detail must fall back to tool name, not empty.');
      expect((item['title'] as String).isNotEmpty, isTrue);
    });

    test('Bare [Agent] falls back to tool name', () {
      final item = buildActivityItemForTest(
        '[Agent]',
        'tool_use',
        const {},
      );
      expect(item['toolName'], 'Agent');
      expect(item['title'], 'Agent');
    });

    test('Agent with description shows description', () {
      final item = buildActivityItemForTest(
        '[Agent: Find Python bugs]',
        'tool_use',
        const {},
      );
      expect(item['toolName'], 'Agent');
      expect(item['title'], 'Find Python bugs');
    });

    test('Bash with command shows the command', () {
      final item = buildActivityItemForTest(
        '[Bash: ls -la]',
        'tool_use',
        const {},
      );
      expect(item['toolName'], 'Bash');
      expect(item['title'], 'ls -la');
    });

    test('Bare [Bash] (no detail) falls back to tool name', () {
      final item = buildActivityItemForTest(
        '[Bash]',
        'tool_use',
        const {},
      );
      expect(item['toolName'], 'Bash');
      expect(item['title'], 'Bash');
    });
  });
}
