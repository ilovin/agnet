import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/screens/connections_screen.dart';

void main() {
  test('merged text_delta assistant output is not treated as thinking', () {
    final messages = convertEventsToMessages([
      {
        'seq': 1,
        'role': 'assistant',
        'text': '让我',
        'raw': false,
        'kind': 'text_delta',
      },
      {
        'seq': 2,
        'role': 'assistant',
        'text': '先看一下。',
        'raw': false,
        'kind': 'text_delta',
      },
    ]);

    expect(messages, hasLength(1));
    expect(messages.first.text, equals('让我先看一下。'));
    expect(messages.first.kind, equals('text_delta'));
    expect(messages.first.isThinking, isFalse);
  });

  test('history normalization preserves kind metadata', () {
    final normalized = normalizeHistoryEvent({
      'seq': 7,
      'role': 'assistant',
      'text': '让我',
      'raw': false,
      'kind': 'text_delta',
    });

    expect(normalized['kind'], equals('text_delta'));
    expect(normalized['raw'], isFalse);
  });

  test('complete assistant message can still be treated as thinking', () {
    final message = ChatMessage(
      role: 'assistant',
      text: 'Thinking: I should verify the event shape first.',
      seq: 1,
      kind: 'thinking_delta',
    );

    expect(message.isThinking, isTrue);
  });

  test('buildCollapsedPreview returns concise single-line preview', () {
    final preview = buildCollapsedPreview('Line 1\nLine 2\nLine 3', maxChars: 13);

    expect(preview, 'Line 1 Line 2…');
  });

  test('convertEventsToMessages groups tool activity into one stable block', () {
    final messages = convertEventsToMessages([
      {'seq': 1, 'role': 'user', 'text': '查一下文件', 'raw': false},
      {
        'seq': 2,
        'role': 'assistant',
        'text': '[Using tool: Read]',
        'raw': false,
        'kind': 'tool_use',
      },
      {'seq': 3, 'role': 'assistant', 'text': '[Read: /tmp/a.txt]', 'raw': false},
      {
        'seq': 4,
        'role': 'assistant',
        'text': '{"ok":true}',
        'raw': false,
        'kind': 'tool_result',
      },
      {'seq': 5, 'role': 'assistant', 'text': '文件读取完成。', 'raw': false},
    ]);

    expect(messages, hasLength(3));
    expect(messages[1].isActivityBlock, isTrue);
    expect(messages[1].text, contains('[Using tool: Read]'));
    expect(messages[1].text, contains('[Read: /tmp/a.txt]'));
    expect(messages[2].text, equals('文件读取完成。'));
  });

  test('read-only Claude sessions return clear input hint', () {
    const agent = AgentModel(
      id: 'a1',
      name: 'claude-attached-123',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      isReadOnly: true,
    );

    expect(isReadOnlyAgent(agent), isTrue);
    expect(readOnlyHintText(agent), equals('只读会话：请回到原 Claude 终端继续输入'));
  });

  test('writable sessions keep normal input hint', () {
    const agent = AgentModel(
      id: 'a2',
      name: 'claude-live',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
    );

    expect(isReadOnlyAgent(agent), isFalse);
    expect(readOnlyHintText(agent), equals('输入消息…'));
  });

  testWidgets('AgentApp smoke test — renders without crash', (
    WidgetTester tester,
  ) async {
    await tester.pumpWidget(
      const ProviderScope(child: MaterialApp(home: ConnectionsScreen())),
    );
    await tester.pump();
    expect(find.text('Agent Manager'), findsOneWidget);
  });

  test('Claude sessions hide terminal controls', () {
    expect(shouldShowTerminalControls('claude'), isFalse);
  });

  test('Claude sessions hide Raw toggle', () {
    expect(shouldShowRawToggle('claude'), isFalse);
  });

  test('Non-Claude sessions keep terminal controls and Raw toggle', () {
    expect(shouldShowTerminalControls('opencode'), isTrue);
    expect(shouldShowTerminalControls('custom'), isTrue);
    expect(shouldShowRawToggle('opencode'), isTrue);
    expect(shouldShowRawToggle('custom'), isTrue);
  });
}
