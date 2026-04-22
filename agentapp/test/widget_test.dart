import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/message_model.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/screens/connections_screen.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

Future<void> pumpNodeCard(
  WidgetTester tester,
  NodeModel node, {
  List<Map<String, dynamic>> agents = const [],
  bool showSessionPreview = false,
}) async {
  final container = ProviderContainer();
  container.read(nodesProvider.notifier).loadNodes([
    {
      'id': node.id,
      'name': node.name,
      'host': node.host,
      'status': switch (node.status) {
        NodeStatus.connected => 'connected',
        NodeStatus.disconnected => 'disconnected',
        NodeStatus.connecting => 'connecting',
        NodeStatus.deploying => 'deploying',
        NodeStatus.error => 'error',
      },
      'location': node.location.toJson(),
      'agentCount': node.agentCount,
    },
  ]);
  if (agents.isNotEmpty) {
    container.read(nodesProvider.notifier).loadAgents(node.id, agents);
  }
  if (showSessionPreview) {
    for (final agent in agents) {
      final agentId = agent['id'] as String;
      container.read(conversationProvider.notifier).loadHistory(node.id, agentId, [
        {
          'nodeId': node.id,
          'agentId': agentId,
          'role': 'assistant',
          'text': '第一行\n第二行\n第三行',
          'seq': 1,
        },
      ]);
    }
  }
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: Scaffold(
          body: NodeCard(node: node, showSessionPreview: showSessionPreview),
        ),
      ),
    ),
  );
  await tester.pump();
}

void main() {
  test('currentOpencodeModelLabel shows active model name', () {
    final data = {
      '_opencodeCurrent': 'tb-api/claude-sonnet-4-6',
      '_opencodeModels': [
        {
          'id': 'tb-api/claude-sonnet-4-6',
          'name': 'Claude Sonnet 4.6',
          'provider': 'tb-api',
        },
      ],
    };

    expect(currentOpencodeModelId(data), equals('tb-api/claude-sonnet-4-6'));
    expect(currentOpencodeModelLabel(data), equals('Claude Sonnet 4.6'));
  });

  test('currentOpencodeModelLabel falls back to bare current model id', () {
    final data = {
      '_opencodeCurrent': 'claude-sonnet-4-6',
      '_opencodeModels': [
        {
          'id': 'tb-api/claude-sonnet-4-6',
          'name': 'Claude Sonnet 4.6',
          'provider': 'tb-api',
        },
      ],
    };

    expect(currentOpencodeModelId(data), equals('tb-api/claude-sonnet-4-6'));
    expect(currentOpencodeModelLabel(data), equals('Claude Sonnet 4.6'));
    expect(
      opencodeModelMatches('tb-api/claude-sonnet-4-6', 'claude-sonnet-4-6'),
      isTrue,
    );
  });

  test('normalizeOpencodeModels sorts by provider then name', () {
    final models = normalizeOpencodeModels([
      {'id': 'z-api/model-b', 'name': 'Model B', 'provider': 'z-api'},
      {'id': 'a-api/model-c', 'name': 'Model C', 'provider': 'a-api'},
      {'id': 'a-api/model-a', 'name': 'Model A', 'provider': 'a-api'},
    ]);

    expect(
      models.map((m) => m['id']).toList(),
      equals(['a-api/model-a', 'a-api/model-c', 'z-api/model-b']),
    );
  });

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

  test(
    'mergeConversationEvents preserves order and refreshes duplicate seqs',
    () {
      final merged = mergeConversationEvents(
        [
          normalizeHistoryEvent({
            'seq': 1,
            'role': 'user',
            'text': 'old user',
            'raw': false,
          }),
          normalizeHistoryEvent({
            'seq': 2,
            'role': 'assistant',
            'text': 'stale assistant',
            'raw': false,
          }),
        ],
        [
          {
            'seq': 2,
            'role': 'assistant',
            'text': 'fresh assistant',
            'raw': false,
          },
          {
            'seq': 3,
            'role': 'assistant',
            'text': 'latest assistant',
            'raw': false,
          },
        ],
      );

      expect(merged.map((event) => event['seq']), equals([1, 2, 3]));
      expect(merged[1]['text'], equals('fresh assistant'));
      expect(latestConversationSeq(merged), equals(3));
      expect(oldestConversationSeq(merged), equals(1));
    },
  );

  test(
    'pruneConversationCache removes stale entries and keeps newest sessions',
    () {
      final now = DateTime(2026, 4, 15, 12);
      final pruned = pruneConversationCache(
        {
          'stale': ConversationEventCacheEntry(
            events: [
              normalizeHistoryEvent({
                'seq': 1,
                'role': 'assistant',
                'text': 'old',
              }),
            ],
            touchedAt: now.subtract(const Duration(hours: 13)),
          ),
          'recent-a': ConversationEventCacheEntry(
            events: [
              normalizeHistoryEvent({
                'seq': 10,
                'role': 'assistant',
                'text': 'A',
              }),
            ],
            touchedAt: now.subtract(const Duration(minutes: 10)),
          ),
          'recent-b': ConversationEventCacheEntry(
            events: [
              normalizeHistoryEvent({
                'seq': 20,
                'role': 'assistant',
                'text': 'B',
              }),
            ],
            touchedAt: now.subtract(const Duration(minutes: 5)),
          ),
          'recent-c': ConversationEventCacheEntry(
            events: [
              normalizeHistoryEvent({
                'seq': 30,
                'role': 'assistant',
                'text': 'C',
              }),
            ],
            touchedAt: now.subtract(const Duration(minutes: 1)),
          ),
        },
        now: now,
        maxEntries: 2,
      );

      expect(pruned.keys, equals(['recent-c', 'recent-b']));
    },
  );

  test('complete assistant message can still be treated as thinking', () {
    final message = ChatMessage(
      role: 'assistant',
      text: 'Thinking: I should verify the event shape first.',
      seq: 1,
      kind: 'thinking_delta',
    );

    expect(message.isThinking, isTrue);
  });

  test('session preview uses refreshed messages instead of stale interrupt', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    final notifier = container.read(conversationProvider.notifier);
    notifier.loadHistory('n1', 'a1', [
      {
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'assistant',
        'text': 'interrupt',
        'seq': 2,
      },
    ]);
    notifier.mergeHistory('n1', 'a1', [
      {
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'assistant',
        'text': '开始做界面',
        'seq': 2,
      },
      {
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'assistant',
        'text': '已更新最新信息',
        'seq': 3,
      },
    ]);

    final lines = sessionPreviewLinesFromMessages(
      notifier.messagesFor('n1', 'a1'),
    );

    expect(lines, equals(['开始做界面', '已更新最新信息']));
  });

  test('buildCollapsedPreview returns concise single-line preview', () {
    final preview = buildCollapsedPreview(
      'Line 1\nLine 2\nLine 3',
      maxChars: 13,
    );

    expect(preview, 'Line 1 Line 2…');
  });

  test('sessionPreviewLinesFromMessages keeps the latest two non-empty lines', () {
    final lines = sessionPreviewLinesFromMessages([
      const MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.user,
        text: '第一行\n第二行',
        seq: 1,
      ),
      const MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.assistant,
        text: '第三行',
        seq: 2,
      ),
    ]);

    expect(lines, equals(['第二行', '第三行']));
  });

  test('sessionPreviewLinesFromMessages truncates long lines', () {
    final lines = sessionPreviewLinesFromMessages([
      const MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.assistant,
        text: '12345678901234567890',
        seq: 1,
      ),
    ]);

    expect(lines, equals(['12345678901234567890']));
  });

  test(
    'convertEventsToMessages groups tool activity into one stable block',
    () {
      final messages = convertEventsToMessages([
        {'seq': 1, 'role': 'user', 'text': '查一下文件', 'raw': false},
        {
          'seq': 2,
          'role': 'assistant',
          'text': '[Using tool: Read]',
          'raw': false,
          'kind': 'tool_use',
        },
        {
          'seq': 3,
          'role': 'assistant',
          'text': '[Read: /tmp/a.txt]',
          'raw': false,
        },
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
      expect(messages[1].kind, equals('activity_list'));
      expect(messages[1].activities, hasLength(3));
      expect(messages[1].activities[0]['toolName'], equals('Read'));
      expect(messages[1].activities[2]['content'], equals('{"ok":true}'));
      expect(messages[2].text, equals('文件读取完成。'));
    },
  );

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

  test('provider write mode alone does not force chat input read-only', () {
    const agent = AgentModel(
      id: 'a3',
      name: 'claude-child',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      providerScope: 'inherited',
      providerWriteMode: 'read_only',
      providerReadOnlyReason: 'provider scope is inherited from root session',
    );

    expect(isReadOnlyAgent(agent), isFalse);
    expect(readOnlyHintText(agent), equals('输入消息…'));
  });

  test('effectiveModeForAgent prefers backend permission mode', () {
    const agent = AgentModel(
      id: 'a4',
      name: 'claude-live',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      permissionMode: 'plan',
    );

    expect(effectiveModeForAgent(agent), equals('plan'));
  });

  test('effectiveModeForAgent prefers pending mode over backend state', () {
    const agent = AgentModel(
      id: 'a5',
      name: 'claude-live',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      permissionMode: 'plan',
    );

    expect(effectiveModeForAgent(agent, pendingMode: 'auto'), equals('auto'));
  });

  test('effectiveModeForAgent falls back to provider default mode', () {
    const agent = AgentModel(
      id: 'a6',
      name: 'claude-live',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
    );

    expect(effectiveModeForAgent(agent), equals('bypassPermissions'));
  });

  test('Claude bypass mode label no longer says Build', () {
    expect(
      kClaudeModes.firstWhere((m) => m.id == 'bypassPermissions').label,
      equals('Bypass'),
    );
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

  testWidgets('remote disconnected node shows connect action', (
    WidgetTester tester,
  ) async {
    await pumpNodeCard(
      tester,
      const NodeModel(
        id: 'n1',
        name: 'remote1',
        host: '10.0.0.1',
        status: NodeStatus.disconnected,
        location: NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: 'ws (10.0.0.1)',
        ),
      ),
    );

    expect(find.text('连接'), findsOneWidget);
  });

  testWidgets('remote connected node shows restart action', (
    WidgetTester tester,
  ) async {
    await pumpNodeCard(
      tester,
      const NodeModel(
        id: 'n1',
        name: 'remote1',
        host: '10.0.0.1',
        status: NodeStatus.connected,
        location: NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: 'ws (10.0.0.1)',
        ),
      ),
    );

    expect(find.text('重启节点'), findsOneWidget);
  });

  testWidgets('NodeCard keeps same-name sessions with different session IDs', (
    WidgetTester tester,
  ) async {
    await pumpNodeCard(
      tester,
      const NodeModel(
        id: 'n1',
        name: 'remote1',
        host: '10.0.0.1',
        status: NodeStatus.connected,
        location: NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: 'ws (10.0.0.1)',
        ),
      ),
      agents: [
        {
          'id': 'a1',
          'name': 'phone-talk (claude)',
          'provider': 'claude',
          'workDir': '/repo/phone-talk',
          'status': 'idle',
          'sessionId': 'sess-a',
          'projectName': 'phone-talk',
        },
        {
          'id': 'a2',
          'name': 'phone-talk (claude)',
          'provider': 'claude',
          'workDir': '/repo/phone-talk',
          'status': 'idle',
          'sessionId': 'sess-b',
          'projectName': 'phone-talk',
        },
      ],
    );

    expect(find.byType(AgentRow), findsNWidgets(2));
  });

  testWidgets('long press agent row shows actions without trailing menu button', (
    WidgetTester tester,
  ) async {
    await pumpNodeCard(
      tester,
      const NodeModel(
        id: 'n1',
        name: 'remote1',
        host: '10.0.0.1',
        status: NodeStatus.connected,
        location: NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: 'ws (10.0.0.1)',
        ),
      ),
      agents: [
        {
          'id': 'a1',
          'name': 'phone-talk (claude)',
          'provider': 'claude',
          'workDir': '/repo/phone-talk',
          'status': 'idle',
          'sessionId': 'sess-a',
          'projectName': 'phone-talk',
        },
      ],
    );

    expect(find.byIcon(Icons.more_vert), findsNothing);

    await tester.longPress(find.byType(AgentRow));
    await tester.pumpAndSettle();

    expect(find.text('重命名'), findsOneWidget);
  });

  test('btw assistant message is not skipped after text_delta stream', () {
    final messages = convertEventsToMessages([
      {
        'seq': 1,
        'role': 'assistant',
        'text': 'Hello',
        'raw': false,
        'kind': 'text_delta',
      },
      {
        'seq': 2,
        'role': 'assistant',
        'text': ' world',
        'raw': false,
        'kind': 'text_delta',
      },
      {
        'seq': 3,
        'role': 'assistant',
        'text': 'Main response complete.',
        'raw': false,
        'kind': 'result',
      },
      {
        'seq': 4,
        'role': 'assistant',
        'text': 'By the way, here is an extra note.',
        'raw': false,
        'kind': 'assistant',
      },
    ]);

    expect(messages, hasLength(2));
    expect(messages[0].text, equals('Helloworld'));
    expect(messages[0].kind, equals('text_delta'));
    expect(messages[1].text, equals('By the way, here is an extra note.'));
    expect(messages[1].kind, equals('assistant'));
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
