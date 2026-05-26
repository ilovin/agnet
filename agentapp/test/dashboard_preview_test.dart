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
    test('does not span multiple messages - takes last N lines of last text only', () {
      // The last text is the important one. When we have 2 texts and maxLines=2,
      // the old code takes the last 2 lines globally (potentially spanning texts).
      // Here the last text has 5 lines; we want only the last 2 from it,
      // NOT one line from the first text + one from the last.
      final lines = buildSessionPreviewLines(
        [
          // first "message" text - should NOT appear in preview
          '前一条消息内容\n更前一条行A',
          // last "message" text - only last 2 lines should appear
          '- M4 任务（\n详情A\n详情B\n详情C\n），完成后...',
        ],
        maxLines: 2,
      );
      // Should not include anything from the first text
      expect(lines.any((line) => line.contains('前一条') || line.contains('更前一条行A')), isFalse,
          reason: 'Preview should not include text from earlier messages');
      // Should include last 2 lines of last text
      expect(lines.any((line) => line.contains('详情C') || line.contains('完成后')), isTrue,
          reason: 'Preview should contain last lines of last message');
    });

    test('bracket-in-one-message bug: paren open on earlier message, close on last', () {
      // Bug scenario: earlier message ends with '（', later message starts with '）'
      // Old code: last 2 global lines = ['（', '）'] → looks like empty brackets
      // New code: last 2 lines from LAST text only
      final lines = buildSessionPreviewLines(
        [
          '一些内容\n括号开始（',  // ends with open paren
          '），后续处理完成',       // starts with close paren - this is the "last message"
        ],
        maxLines: 2,
      );
      // Old behavior would give ['括号开始（', '），后续处理完成'] - spanning two messages
      // New behavior: only from last text
      expect(lines.any((line) => line.contains('括号开始')), isFalse,
          reason: 'Should not include line from an earlier message');
      expect(lines.any((line) => line.contains('），后续处理完成')), isTrue,
          reason: 'Should contain content from last message');
    });
  });

  group('sessionPreviewLinesFromMessages', () {
    test('does not span multiple messages - paren bug reproduction', () {
      // Reproduces the actual bug:
      // Earlier message contains '（' on a line, later message contains '）'
      final messages = <MessageModel>[
        _msg('一些背景内容\n括号开始（'),
        _msg('），后续处理\n完成后报根因'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      // Old code: last 2 global lines across both messages = ['括号开始（', '），后续处理']
      // New code: only last 2 lines from the LAST message = ['），后续处理', '完成后报根因']
      expect(preview.any((line) => line.contains('括号开始')), isFalse,
          reason: 'Preview must not include content from earlier message');
      expect(preview.any((line) => line.contains('完成后报根因') || line.contains('），后续处理')), isTrue,
          reason: 'Preview must contain content from last message only');
    });

    test('does not span multiple messages - multi-line last message', () {
      final messages = <MessageModel>[
        _msg('前一条消息内容\n更前一条'),
        _msg('- M4 任务（\n详情A\n详情B\n详情C\n），完成后...'),
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview.any((line) => line.contains('前一条')), isFalse,
          reason: 'Preview must not include earlier message content');
      expect(preview.any((line) => line.contains('详情C') || line.contains('完成后')), isTrue,
          reason: 'Preview must contain last lines of last message');
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

    test('skips blank messages and finds last non-empty one', () {
      final messages = <MessageModel>[
        _msg('实际内容\n第二行'),
        _msg('   '),   // blank - should be skipped
      ];
      final preview = sessionPreviewLinesFromMessages(messages, maxLines: 2);
      expect(preview.any((line) => line.contains('实际内容') || line.contains('第二行')), isTrue,
          reason: 'Should use last non-empty message even if last message is blank');
    });
  });
}
