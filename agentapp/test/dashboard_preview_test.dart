import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/message_model.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

MessageModel _msg(String text) => MessageModel(
      nodeId: 'n1',
      agentId: 'a1',
      role: MessageRole.user,
      text: text,
      seq: 0,
      msgId: 'm1',
    );

void main() {
  group('buildSessionPreviewLines', () {
    test('shows last N messages, one line per message', () {
      final lines = buildSessionPreviewLines(
        [
          'Hello world\nThis is a test',
          'Another message here',
          'Latest message\nWith multiple lines',
        ],
        maxLines: 3,
      );
      expect(lines, [
        'Hello world',
        'Another message here',
        'Latest message',
      ]);
    });

    test('shows fewer lines when fewer messages exist', () {
      final lines = buildSessionPreviewLines(
        [
          'Only message\nWith multiple lines',
        ],
        maxLines: 3,
      );
      expect(lines, ['Only message']);
    });

    test('respects maxLines limit', () {
      final lines = buildSessionPreviewLines(
        [
          'Message 1',
          'Message 2',
          'Message 3',
          'Message 4',
        ],
        maxLines: 2,
      );
      expect(lines, ['Message 3', 'Message 4']);
    });

    test('skips empty messages', () {
      final lines = buildSessionPreviewLines(
        [
          'First message',
          '   ',
          'Second message',
          '',
          'Third message',
        ],
        maxLines: 3,
      );
      expect(lines, ['First message', 'Second message', 'Third message']);
    });

    test('truncates long messages with ellipsis', () {
      final longText = 'A' * 100;
      final lines = buildSessionPreviewLines(
        [longText],
        maxLines: 1,
        maxCharsPerLine: 80,
      );
      expect(lines.length, 1);
      // buildCollapsedPreview truncates at maxChars then appends '…'
      expect(lines[0].length, 81); // 80 chars + '…'
      expect(lines[0].endsWith('…'), isTrue);
    });

    test('multi-line message shows only first line', () {
      final lines = buildSessionPreviewLines(
        [
          'First line\nSecond line\nThird line',
        ],
        maxLines: 1,
      );
      expect(lines, ['First line']);
    });

    test('returns empty for empty texts', () {
      expect(buildSessionPreviewLines([]), isEmpty);
    });

    test('returns empty when all texts are blank', () {
      expect(buildSessionPreviewLines(['   ', '']), isEmpty);
    });

    test('uses buildCollapsedPreview for long single line', () {
      final lines = buildSessionPreviewLines(
        ['Short'],
        maxLines: 1,
        maxCharsPerLine: 80,
      );
      expect(lines, ['Short']);
    });
  });

  group('sessionPreviewLinesFromMessages', () {
    test('shows last N messages, one line per message', () {
      final messages = <MessageModel>[
        _msg('Hello world\nThis is a test'),
        _msg('Another message here'),
        _msg('Latest message\nWith multiple lines'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 3);
      expect(preview, [
        'Hello world',
        'Another message here',
        'Latest message',
      ]);
    });

    test('shows fewer lines when fewer messages exist', () {
      final messages = <MessageModel>[
        _msg('Only message\nWith multiple lines'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 3);
      expect(preview, ['Only message']);
    });

    test('respects maxLines limit', () {
      final messages = <MessageModel>[
        _msg('Message 1'),
        _msg('Message 2'),
        _msg('Message 3'),
        _msg('Message 4'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Message 3', 'Message 4']);
    });

    test('skips blank messages', () {
      final messages = <MessageModel>[
        _msg('First message'),
        _msg('   '),
        _msg('Second message'),
        _msg(''),
        _msg('Third message'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 3);
      expect(preview, ['First message', 'Second message', 'Third message']);
    });

    test('returns empty for no messages', () {
      expect(sessionPreviewLinesFromMessages([]), isEmpty);
    });

    test('returns empty when all messages are blank', () {
      final messages = <MessageModel>[
        _msg('   '),
        _msg(''),
      ];
      expect(sessionPreviewLinesFromMessages(messages), isEmpty);
    });

    test('multi-line message shows only first line', () {
      final messages = <MessageModel>[
        _msg('First line\nSecond line\nThird line'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 1);
      expect(preview, ['First line']);
    });

    test('does not span across messages - each message is one line', () {
      // Earlier bug: content from message N-1 would appear in preview
      // Now each message contributes exactly one line (its first line)
      final messages = <MessageModel>[
        _msg('Earlier message'),
        _msg('Later message\nWith extra lines'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Earlier message', 'Later message']);
    });
  });

  group('sessionPreviewLinesFromMessages tool-call summarization', () {
    // The dashboard list preview must surface semantic info from tool-call
    // messages so users see e.g. "Bash: scripts/deploy.sh local" instead of
    // a blunt truncation like "[Bash: scripts/deploy.sh local --with-web fl…".
    // These tests pin that integration with ToolCallSummary.

    test('Bash tool call shows ToolName: command preview', () {
      final messages = <MessageModel>[
        _msg('[Bash: scripts/deploy.sh local --with-web]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Bash: scripts/deploy.sh local --with-web']);
    });

    test('Read tool call shows basename instead of full path', () {
      final messages = <MessageModel>[
        _msg('[Read: /Users/foo/project/lib/screens/dashboard_screen.dart]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Read: dashboard_screen.dart']);
    });

    test('TaskCreate tool call shows subject', () {
      final messages = <MessageModel>[
        _msg('[TaskCreate: Wire ToolCallSummary into dashboard]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['TaskCreate: Wire ToolCallSummary into dashboard']);
    });

    test('Agent tool call shows description', () {
      final messages = <MessageModel>[
        _msg('[Agent: Run dashboard preview tests]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Agent: Run dashboard preview tests']);
    });

    test('tool call without details falls back to collapsed preview', () {
      // [TaskList] has no params — the preview should still show the raw
      // bracketed token rather than blowing up.
      final messages = <MessageModel>[
        _msg('[TaskList]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['[TaskList]']);
    });

    test('non tool-call line falls back to buildCollapsedPreview', () {
      final messages = <MessageModel>[
        _msg('Just a normal assistant reply'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Just a normal assistant reply']);
    });

    test('multi-line message: each line parsed independently', () {
      // When the last message contains multiple tool-call lines, each line
      // should be summarized via ToolCallSummary individually.
      final messages = <MessageModel>[
        _msg(
          '[Bash: ls -la]\n[Read: /a/b/foo.dart]',
        ),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Bash: ls -la', 'Read: foo.dart']);
    });

    test('mixed tool-call + plain text lines summarize per line', () {
      final messages = <MessageModel>[
        _msg('Working on it…\n[Bash: pwd]'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview, ['Working on it…', 'Bash: pwd']);
    });
  });
}
