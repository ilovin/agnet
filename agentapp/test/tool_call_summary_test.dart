import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/utils/tool_call_summary.dart';

void main() {
  group('ToolCallSummary.parse', () {
    test('returns null for non tool-call text', () {
      expect(ToolCallSummary.parse('hello'), isNull);
      expect(ToolCallSummary.parse(''), isNull);
      expect(ToolCallSummary.parse('not a [tool] call'), isNull);
    });

    test('parses tool name with no params', () {
      final r = ToolCallSummary.parse('[TaskList]');
      expect(r, isNotNull);
      expect(r!.toolName, 'TaskList');
      expect(r.summary, '');
    });

    test('Bash: takes first non-empty line of command', () {
      final r =
          ToolCallSummary.parse('[Bash: scripts/deploy.sh local --with-web]');
      expect(r, isNotNull);
      expect(r!.toolName, 'Bash');
      expect(r.summary, 'scripts/deploy.sh local --with-web');
    });

    test('Bash: collapses multi-line command to first line', () {
      final r = ToolCallSummary.parse(
        '[Bash: cd /tmp && \\\nls -la\nthird-line]',
      );
      expect(r!.toolName, 'Bash');
      expect(r.summary, r'cd /tmp && \');
    });

    test('Bash: skips leading blank lines', () {
      final r = ToolCallSummary.parse('[Bash: \n   \nactual command]');
      expect(r!.summary, 'actual command');
    });

    test('Bash: truncates summaries longer than max length', () {
      final long = 'a' * 200;
      final r = ToolCallSummary.parse('[Bash: $long]');
      expect(r!.summary.length, lessThanOrEqualTo(81)); // 80 + ellipsis
      expect(r.summary.endsWith('…'), isTrue);
    });

    test('Read: shows basename of absolute file path', () {
      final r = ToolCallSummary.parse(
        '[Read: /Users/foo/project/lib/screens/dashboard_screen.dart]',
      );
      expect(r!.toolName, 'Read');
      expect(r.summary, 'dashboard_screen.dart');
    });

    test('Read: keeps name when path has no slash', () {
      final r = ToolCallSummary.parse('[Read: foo.dart]');
      expect(r!.summary, 'foo.dart');
    });

    test('Read: ignores trailing slash', () {
      final r = ToolCallSummary.parse('[Read: /tmp/some/dir/]');
      expect(r!.summary, 'dir');
    });

    test('Edit: shows basename', () {
      final r = ToolCallSummary.parse(
        '[Edit: /Users/foo/agentapp/lib/widgets/app_bar/mission_control_app_bar.dart]',
      );
      expect(r!.toolName, 'Edit');
      expect(r.summary, 'mission_control_app_bar.dart');
    });

    test('Write: shows basename', () {
      final r = ToolCallSummary.parse('[Write: /tmp/foo.dart]');
      expect(r!.toolName, 'Write');
      expect(r.summary, 'foo.dart');
    });

    test('NotebookEdit: shows basename', () {
      final r = ToolCallSummary.parse(
        '[NotebookEdit: /Users/foo/notebook.ipynb]',
      );
      expect(r!.summary, 'notebook.ipynb');
    });

    test('TaskCreate: shows subject as-is', () {
      final r = ToolCallSummary.parse('[TaskCreate: 修复 ☒ 字符 tofu]');
      expect(r!.toolName, 'TaskCreate');
      expect(r.summary, '修复 ☒ 字符 tofu');
    });

    test('TaskUpdate: shows formatted status', () {
      final r = ToolCallSummary.parse('[TaskUpdate: #abc123 -> completed]');
      expect(r!.toolName, 'TaskUpdate');
      expect(r.summary, '#abc123 -> completed');
    });

    test('Grep: strips backend "pattern: " prefix', () {
      final r = ToolCallSummary.parse('[Grep: pattern: foo.*bar]');
      expect(r!.toolName, 'Grep');
      expect(r.summary, 'foo.*bar');
    });

    test('Grep: leaves raw pattern when no prefix', () {
      final r = ToolCallSummary.parse('[Grep: foo.*bar]');
      expect(r!.summary, 'foo.*bar');
    });

    test('Glob: shows pattern', () {
      final r = ToolCallSummary.parse('[Glob: **/*.dart]');
      expect(r!.summary, '**/*.dart');
    });

    test('WebFetch: shows url', () {
      final r =
          ToolCallSummary.parse('[WebFetch: https://example.com/api/v1/foo]');
      expect(r!.summary, 'https://example.com/api/v1/foo');
    });

    test('WebSearch: shows query', () {
      final r = ToolCallSummary.parse('[WebSearch: how to write flutter tests]');
      expect(r!.summary, 'how to write flutter tests');
    });

    test('Agent: shows description', () {
      final r =
          ToolCallSummary.parse('[Agent: Run tests and report failures]');
      expect(r!.summary, 'Run tests and report failures');
    });

    test('SendMessage: shows formatted summary', () {
      final r =
          ToolCallSummary.parse('[SendMessage: -> manager: 任务完成]');
      expect(r!.summary, '-> manager: 任务完成');
    });

    test('Unknown tool: falls back to first line, truncated', () {
      final r = ToolCallSummary.parse('[CustomTool: some param value]');
      expect(r!.toolName, 'CustomTool');
      expect(r.summary, 'some param value');
    });

    test('handles tool name with only colon and trailing space', () {
      final r = ToolCallSummary.parse('[Bash: ]');
      expect(r!.toolName, 'Bash');
      expect(r.summary, '');
    });
  });
}
