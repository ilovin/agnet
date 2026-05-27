import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/http.dart' as http;
import 'package:http/testing.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/connection_config.dart';
import 'package:agentapp/models/message_model.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/providers/health_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/screens/connections_screen.dart';
import 'package:agentapp/screens/dashboard_screen.dart';
import 'package:agentapp/services/native_ws_channel.dart';
import 'package:agentapp/widgets/ask_user_question_card.dart';
import 'package:agentapp/widgets/exit_plan_mode_card.dart';
import 'package:agentapp/models/claude_interaction_models.dart';
import 'package:agentapp/widgets/permission_request_card.dart';

Future<void> pumpNodeCard(
  WidgetTester tester,
  NodeModel node, {
  List<Map<String, dynamic>> agents = const [],
  bool showSessionPreview = false,
  bool isLargeScreen = false,
  bool showDetails = false,
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
      final sessionId = (agent['sessionId'] as String? ?? '').trim();
      container
          .read(conversationProvider.notifier)
          .loadHistory(node.id, agentId, sessionId, [
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
          body: NodeCard(
            node: node,
            showSessionPreview: showSessionPreview,
            isLargeScreen: isLargeScreen,
            showDetails: showDetails,
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

Future<void> pumpAgentRow(
  WidgetTester tester,
  AgentModel agent,
  String nodeId, {
  bool showPreview = false,
  bool isLargeScreen = false,
  bool showDetails = false,
}) async {
  final container = ProviderContainer();
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: Scaffold(
          body: AgentRow(
            agent: agent,
            nodeId: nodeId,
            showPreview: showPreview,
            isLargeScreen: isLargeScreen,
            showDetails: showDetails,
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

Future<void> pumpDashboardScreen(
  WidgetTester tester, {
  List<Map<String, dynamic>> nodes = const [],
  List<Map<String, dynamic>> agents = const [],
  Size screenSize = const Size(1440, 900),
}) async {
  tester.binding.window.physicalSizeTestValue = screenSize;
  tester.binding.window.devicePixelRatioTestValue = 1.0;
  addTearDown(tester.binding.window.clearPhysicalSizeTestValue);
  addTearDown(tester.binding.window.clearDevicePixelRatioTestValue);

  final container = ProviderContainer();
  if (nodes.isNotEmpty) {
    container.read(nodesProvider.notifier).loadNodes(nodes);
  }
  for (final agent in agents) {
    final nodeId = agent['nodeId'] as String;
    container.read(nodesProvider.notifier).loadAgents(nodeId, [agent]);
  }
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: DashboardScreen()),
    ),
  );
  await tester.pump();
  // Allow Future.delayed in _startAutoRefresh to fire so its internal Timer
  // is removed before the test framework checks for pending timers.
  await tester.pump(const Duration(milliseconds: 150));
}

void main() {
  test('connectionProbeUri keeps path and swaps scheme', () {
    expect(
      connectionProbeUri('wss://ilovin.xyz/ws/fengming.xie?token=abc').toString(),
      equals('https://ilovin.xyz/ws/fengming.xie?token=abc'),
    );
    expect(
      connectionProbeUri('ws://127.0.0.1:8383/ws').toString(),
      equals('http://127.0.0.1:8383/ws'),
    );
  });

  test('friendlyConnectError reports agentgw offline from probe', () {
    const cfg = ConnectionConfig(url: 'wss://ilovin.xyz/ws/fengming.xie', token: 't');
    final message = friendlyConnectError(
      const NativeWebSocketException('ws error', closeCode: 1006),
      cfg,
      probeResult: const ConnectionProbeResult.response(502, 'agentgw offline'),
    );

    expect(message, equals('连接失败：服务器可达，但 agentgw offline。请检查网关进程或隧道是否已连接。'));
  });

  test('friendlyConnectError reports agentgw offline from probe json code/detail', () {
    const cfg = ConnectionConfig(url: 'wss://ilovin.xyz/ws/fengming.xie', token: 't');
    final message = friendlyConnectError(
      const NativeWebSocketException('ws error', closeCode: 1006),
      cfg,
      probeResult: const ConnectionProbeResult.response(
        502,
        '{"error":"agentgw offline","code":"GW_OFFLINE","detail":"user/token verified, but agentgw tunnel is offline"}',
      ),
    );

    expect(
      message,
      equals('连接失败：服务器可达，但 agentgw offline（user/token verified, but agentgw tunnel is offline）。请检查网关进程或隧道是否已连接。'),
    );
  });

  test('friendlyConnectError reports server unreachable when probe fails', () {
    const cfg = ConnectionConfig(url: 'wss://ilovin.xyz/ws/fengming.xie', token: 't');
    final message = friendlyConnectError(
      const NativeWebSocketException('ws error', closeCode: 1006),
      cfg,
      probeResult: ConnectionProbeResult.failure(const SocketException('offline')),
    );

    expect(message, equals('连接失败：无法连接到服务器。请检查网络、域名/IP、Tailscale 或代理是否在线。'));
  });

  test('friendlyConnectError reports reachable server with websocket failure', () {
    const cfg = ConnectionConfig(url: 'wss://ilovin.xyz/ws/fengming.xie', token: 't');
    final message = friendlyConnectError(
      const NativeWebSocketException('ws error', closeCode: 1006),
      cfg,
      probeResult: const ConnectionProbeResult.response(404, 'not found'),
    );

    expect(
      message,
      equals('连接失败：服务器可达，但 WebSocket 握手失败（HTTP 404）。请检查 URL 路径、代理升级配置或 token。'),
    );
  });

  test('friendlyConnectError keeps auth failures ahead of probe result', () {
    const cfg = ConnectionConfig(url: 'wss://ilovin.xyz/ws/fengming.xie', token: 'bad');
    final message = friendlyConnectError(
      Exception('401 unauthorized'),
      cfg,
      probeResult: const ConnectionProbeResult.response(502, 'agentgw offline'),
    );

    expect(message, equals('连接失败：Token 验证不通过（401/403）。请检查 token 是否正确。'));
  });

  test('shouldProbeConnectionError only probes ambiguous websocket failures', () {
    expect(shouldProbeConnectionError(const NativeWebSocketException('ws error', closeCode: 1006)), isTrue);
    expect(shouldProbeConnectionError(Exception('401 unauthorized')), isFalse);
    expect(shouldProbeConnectionError(Exception('404 not found')), isFalse);
  });

  testWidgets('ConnectionsScreen accepts injected probe client', (
    WidgetTester tester,
  ) async {
    final probeClient = MockClient((request) async => http.Response('agentgw offline', 502));

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(home: ConnectionsScreen(probeClient: probeClient)),
      ),
    );
    await tester.pump();

    expect(find.text('Agent Manager'), findsOneWidget);
  });

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
    'streamed text_delta with multiple sentence boundaries merges into one message',
    () {
      // Reproduces the production bug where the app showed only a fragment
      // like "行级别的修复" because each `。` mid-stream flushed mergeBuf into
      // a separate ChatMessage, fragmenting one assistant turn into many.
      // The authoritative final assistant/result event with the full text
      // (kind '' / 'text' / 'result') was then skipped because hadTextDelta
      // was true, so the UI only kept the broken fragments.
      final deltas = <Map<String, dynamic>>[
        {'seq': 1069, 'kind': 'text_delta', 'text': '（单'},
        {'seq': 1070, 'kind': 'text_delta', 'text': ' `REMOTE_'},
        {'seq': 1071, 'kind': 'text_delta', 'text': 'HOST=ws`），'},
        {'seq': 1072, 'kind': 'text_delta', 'text': '不'},
        {'seq': 1073, 'kind': 'text_delta', 'text': '是 `deploy_rem'},
        {'seq': 1074, 'kind': 'text_delta', 'text': 'ote_targets_parallel'},
        {'seq': 1075, 'kind': 'text_delta', 'text': '`（'},
        {'seq': 1076, 'kind': 'text_delta', 'text': '读'},
        {'seq': 1077, 'kind': 'text_delta', 'text': ' nodes 列表全'},
        {'seq': 1078, 'kind': 'text_delta', 'text': '推'},
        {'seq': 1079, 'kind': 'text_delta', 'text': '）。一'},
        {'seq': 1080, 'kind': 'text_delta', 'text': '行级别的'},
        {'seq': 1081, 'kind': 'text_delta', 'text': '修'},
        {'seq': 1082, 'kind': 'text_delta', 'text': '复。\n\n派'},
        {'seq': 1083, 'kind': 'text_delta', 'text': ' dev agent 改'},
        {'seq': 1084, 'kind': 'text_delta', 'text': '脚本 '},
        {'seq': 1085, 'kind': 'text_delta', 'text': '+'},
        {'seq': 1086, 'kind': 'text_delta', 'text': ' 加'},
        {'seq': 1087, 'kind': 'text_delta', 'text': '测'},
        {'seq': 1088, 'kind': 'text_delta', 'text': '试。同'},
        {'seq': 1089, 'kind': 'text_delta', 'text': '时立'},
        {'seq': 1090, 'kind': 'text_delta', 'text': '刻'},
        {'seq': 1091, 'kind': 'text_delta', 'text': '紧急推'},
        {'seq': 1092, 'kind': 'text_delta', 'text': ' oracle（不能'},
        {'seq': 1093, 'kind': 'text_delta', 'text': '等'},
        {'seq': 1094, 'kind': 'text_delta', 'text': '脚本修完）'},
        {'seq': 1095, 'kind': 'text_delta', 'text': '。'},
      ].map(
        (e) => {
          'seq': e['seq'],
          'role': 'assistant',
          'raw': false,
          'kind': e['kind'],
          'text': e['text'],
        },
      ).toList();

      final messages = convertEventsToMessages(deltas);

      expect(
        messages,
        hasLength(1),
        reason: 'streamed deltas should produce a single merged message',
      );
      // Each delta is trimmed individually before concatenation, so leading
      // spaces between fragments are squeezed. The critical invariant for
      // this regression test is that the full sentence "一行级别的修复" stays
      // inside one message — never stranded as its own bubble.
      expect(messages.first.text, contains('一行级别的修复'));
      expect(messages.first.text, contains('派'));
      expect(messages.first.text, contains('紧急推'));
      expect(messages.first.text, contains('脚本修完'));
      expect(messages.first.kind, equals('text_delta'));
    },
  );

  test(
    'streamed text_delta followed by complete message keeps full content',
    () {
      // Same scenario plus the trailing kind '' authoritative event that
      // Claude jsonl emits when the turn ends. The merged delta text and
      // the full text should converge — the authoritative event must not
      // be silently dropped while the deltas remain fragmented.
      final events = <Map<String, dynamic>>[
        {
          'seq': 1,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '第一段。',
        },
        {
          'seq': 2,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '第二段。',
        },
        {
          'seq': 3,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '第三段。',
        },
        {
          'seq': 4,
          'role': 'assistant',
          'raw': false,
          'kind': '',
          'text': '第一段。第二段。第三段。',
        },
      ];

      final messages = convertEventsToMessages(events);

      expect(messages, hasLength(1));
      expect(messages.first.text, equals('第一段。第二段。第三段。'));
    },
  );

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
    notifier.loadHistory('n1', 'a1', 's1', [
      {
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'assistant',
        'text': 'interrupt',
        'seq': 2,
      },
    ]);
    notifier.mergeHistory('n1', 'a1', 's1', [
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
      notifier.messagesFor('n1', 'a1', 's1'),
    );

    expect(lines, equals(['已更新最新信息']));
  });

  test('buildCollapsedPreview returns concise single-line preview', () {
    final preview = buildCollapsedPreview(
      'Line 1\nLine 2\nLine 3',
      maxChars: 13,
    );

    expect(preview, 'Line 1 Line 2…');
  });

  test('sessionPreviewLinesFromMessages shows last N lines of last non-empty message only', () {
    // New behaviour: preview is scoped to the last non-empty message only,
    // so lines from earlier messages are never mixed in.
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

    // Only the last message ('第三行') contributes; it has 1 line so result is 1 line.
    expect(lines, equals(['第三行']));
  });

  test('sessionPreviewLinesFromMessages takes last 2 lines of last message when it has multiple lines', () {
    final lines = sessionPreviewLinesFromMessages([
      const MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.user,
        text: '前一条消息',
        seq: 1,
      ),
      const MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.assistant,
        text: '第一行\n第二行\n第三行',
        seq: 2,
      ),
    ]);

    // Last message has 3 lines; with maxLines=2, we get the last 2 only.
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

  test('short user messages are displayed and not filtered by isNoiseOnlyText', () {
    final messages = convertEventsToMessages([
      {'role': 'user', 'text': 'hi', 'seq': 1, 'raw': false, 'kind': 'user'},
      {'role': 'user', 'text': 'ok', 'seq': 2, 'raw': false, 'kind': 'user'},
      {'role': 'user', 'text': '?', 'seq': 3, 'raw': false, 'kind': 'user'},
    ]);

    expect(messages.length, 3);
    expect(messages[0].text, 'hi');
    expect(messages[1].text, 'ok');
    expect(messages[2].text, '?');
  });

  test('empty user message is not added to messages', () {
    final messages = convertEventsToMessages([
      {'role': 'user', 'text': '', 'seq': 1, 'raw': false, 'kind': 'user'},
      {'role': 'user', 'text': 'ok', 'seq': 2, 'raw': false, 'kind': 'user'},
      {'role': 'user', 'text': '', 'seq': 3, 'raw': false, 'kind': 'user'},
    ]);

    expect(messages.length, 1);
    expect(messages[0].text, 'ok');
  });

  test('short user message is added to messages', () {
    final messages = convertEventsToMessages([
      {'role': 'user', 'text': 'ok', 'seq': 1, 'raw': false, 'kind': 'user'},
    ]);

    expect(messages.length, 1);
    expect(messages[0].text, 'ok');
  });

  // R-003: Compact dashboard header and status folding
  testWidgets('NodeCard hides summary chips when showDetails is false on large screen', (
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
      isLargeScreen: true,
      showDetails: false,
    );

    expect(find.text('会话 1'), findsNothing);
    expect(find.text('活跃 1'), findsNothing);
  });

  testWidgets('NodeCard shows summary chips when showDetails is true on large screen', (
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
      isLargeScreen: true,
      showDetails: true,
    );

    expect(find.text('会话 1'), findsOneWidget);
    expect(find.text('活跃 1'), findsOneWidget);
  });

  testWidgets('AgentRow hides meta badges when showDetails is false on large screen', (
    WidgetTester tester,
  ) async {
    await pumpAgentRow(
      tester,
      const AgentModel(
        id: 'a1',
        name: 'claude-live',
        workDir: '/tmp',
        nodeId: 'n1',
        provider: 'claude',
        status: AgentStatus.idle,
        runtimeState: 'live',
        sessionState: 'active',
      ),
      'n1',
      isLargeScreen: true,
      showDetails: false,
    );

    expect(find.text('运行中'), findsNothing);
    expect(find.text('会话活跃'), findsNothing);
  });

  testWidgets('AgentRow shows meta badges when showDetails is true on large screen', (
    WidgetTester tester,
  ) async {
    await pumpAgentRow(
      tester,
      const AgentModel(
        id: 'a1',
        name: 'claude-live',
        workDir: '/tmp',
        nodeId: 'n1',
        provider: 'claude',
        status: AgentStatus.idle,
        runtimeState: 'live',
        sessionState: 'active',
      ),
      'n1',
      isLargeScreen: true,
      showDetails: true,
    );

    expect(find.text('运行中'), findsOneWidget);
    expect(find.text('会话活跃'), findsOneWidget);
  });

  testWidgets('DashboardScreen AppBar shows subtitle with node and agent stats', (
    WidgetTester tester,
  ) async {
    await pumpDashboardScreen(
      tester,
      nodes: [
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'connected',
          'location': {
            'type': 'remote',
            'host': '10.0.0.1',
            'displayLocation': 'ws (10.0.0.1)',
          },
          'agentCount': 1,
        },
      ],
      agents: [
        {
          'nodeId': 'n1',
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

    expect(find.text('仪表盘'), findsOneWidget);
    expect(find.text('1 节点 · 1 活跃'), findsOneWidget);

    // Dispose widget tree to cancel DashboardScreen periodic timer
    await tester.pumpWidget(const SizedBox());
    await tester.pump();
  });

  testWidgets('DashboardScreen hides HealthIndicator when collapsed', (
    WidgetTester tester,
  ) async {
    await pumpDashboardScreen(
      tester,
      nodes: [
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'connected',
          'location': {
            'type': 'remote',
            'host': '10.0.0.1',
            'displayLocation': 'ws (10.0.0.1)',
          },
          'agentCount': 1,
        },
      ],
    );

    // HealthIndicator should not render when _showDetails defaults to false
    expect(find.byKey(const Key('healthIndicator')), findsNothing);

    // Dispose widget tree to cancel DashboardScreen periodic timer
    await tester.pumpWidget(const SizedBox());
    await tester.pump();
  });

  testWidgets('DashboardScreen toggle showDetails reveals summary chips and HealthIndicator', (
    WidgetTester tester,
  ) async {
    await pumpDashboardScreen(
      tester,
      nodes: [
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'connected',
          'location': {
            'type': 'remote',
            'host': '10.0.0.1',
            'displayLocation': 'ws (10.0.0.1)',
          },
          'agentCount': 1,
        },
      ],
      agents: [
        {
          'nodeId': 'n1',
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

    // Initially collapsed: no summary chips, no HealthIndicator
    expect(find.text('会话 1'), findsNothing);
    expect(find.byKey(const Key('healthIndicator')), findsNothing);
    expect(find.byIcon(Icons.expand_more), findsOneWidget);

    // Tap expand button
    await tester.tap(find.byIcon(Icons.expand_more));
    await tester.pumpAndSettle();

    expect(find.byIcon(Icons.expand_less), findsOneWidget);

    // Expanded: summary chips should appear
    expect(find.text('会话 1'), findsOneWidget);

    // HealthIndicator renders when expanded
    expect(find.byKey(const Key('healthIndicator')), findsOneWidget);

    // Tap collapse button
    await tester.tap(find.byIcon(Icons.expand_less));
    await tester.pumpAndSettle();

    // Collapsed again
    expect(find.text('会话 1'), findsNothing);
    expect(find.byKey(const Key('healthIndicator')), findsNothing);

    // Dispose widget tree to cancel DashboardScreen periodic timer
    await tester.pumpWidget(const SizedBox());
    await tester.pump();
  });

  // Timestamp toggle tests
  testWidgets('MessageBubble hides timestamp by default', (WidgetTester tester) async {
    final message = ChatMessage(
      role: 'assistant',
      text: 'Hello world',
      seq: 1,
      timestamp: 1714464000000,
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: MessageBubble(message: message),
          ),
        ),
      ),
    );

    // Timestamp should not be visible by default
    expect(find.text('16:00'), findsNothing);
  });

  testWidgets('MessageBubble shows timestamp when showTimestamp is true', (WidgetTester tester) async {
    final message = ChatMessage(
      role: 'assistant',
      text: 'Hello world',
      seq: 1,
      timestamp: 1714464000000,
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: MessageBubble(
              message: message,
              showTimestamp: true,
            ),
          ),
        ),
      ),
    );

    // Timestamp should be visible (4/30 16:00 in local timezone, not today)
    expect(find.text('4/30 16:00'), findsOneWidget);
  });

  testWidgets('MessageBubble never shows timestamp when null', (WidgetTester tester) async {
    final message = ChatMessage(
      role: 'assistant',
      text: 'Hello world',
      seq: 1,
      timestamp: null,
    );

    await tester.pumpWidget(
      ProviderScope(
        child: MaterialApp(
          home: Scaffold(
            body: MessageBubble(
              message: message,
              showTimestamp: true,
            ),
          ),
        ),
      ),
    );

    // No timestamp text should appear
    expect(find.text('16:00'), findsNothing);
  });

  // ─── R-010 T5: PermissionRequestCard widget tests ──────────────────────

  testWidgets('PermissionRequestCard renders Bash tool with command',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PermissionRequestCard(
            permissionRequest: {
              'tool_name': 'Bash',
              'request_id': 'req-001',
              'input': {'command': 'git status'},
            },
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Tool: Bash'), findsOneWidget);
    expect(find.text('git status'), findsOneWidget);
    expect(find.text('Allow'), findsOneWidget);
    expect(find.text('Deny'), findsOneWidget);
  });

  testWidgets('PermissionRequestCard renders Edit tool with file path and new_string',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PermissionRequestCard(
            permissionRequest: {
              'tool_name': 'Edit',
              'request_id': 'req-002',
              'input': {
                'file_path': '/tmp/foo.dart',
                'old_string': 'old content',
                'new_string': 'new content',
              },
            },
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Tool: Edit'), findsOneWidget);
    expect(find.text('/tmp/foo.dart'), findsOneWidget);
    expect(find.text('new content'), findsOneWidget);
    expect(find.text('Allow'), findsOneWidget);
    expect(find.text('Deny'), findsOneWidget);
  });

  testWidgets('PermissionRequestCard renders Write tool with file path and content',
      (WidgetTester tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: PermissionRequestCard(
            permissionRequest: {
              'tool_name': 'Write',
              'request_id': 'req-003',
              'input': {
                'file_path': '/tmp/bar.txt',
                'content': 'Hello, world!',
              },
            },
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('Tool: Write'), findsOneWidget);
    expect(find.text('/tmp/bar.txt'), findsOneWidget);
    expect(find.text('Hello, world!'), findsOneWidget);
    expect(find.text('Allow'), findsOneWidget);
    expect(find.text('Deny'), findsOneWidget);
  });

  test(
    'convertEventsToMessages passes permission_request payload to ChatMessage',
    () {
      final messages = convertEventsToMessages([
        {
          'seq': 1,
          'role': 'system',
          'text': '权限请求: Bash',
          'raw': false,
          'kind': 'permission_request',
          'permissionRequest': {
            'tool_name': 'Bash',
            'request_id': 'req-x',
            'input': {'command': 'ls -la'},
          },
        },
      ]);

      expect(messages, hasLength(1));
      expect(messages[0].kind, equals('permission_request'));
      expect(messages[0].permissionRequest, isNotNull);
      expect(messages[0].permissionRequest!['tool_name'], equals('Bash'));
      expect(
        (messages[0].permissionRequest!['input'] as Map)['command'],
        equals('ls -la'),
      );
    },
  );

  // ─── R-010 T3: AskUserQuestionCard widget tests ────────────────────────

  testWidgets('AskUserQuestionCard single-select: tap option submits immediately',
      (WidgetTester tester) async {
    String? sent;
    final payload = AskUserQuestionPayload(
      toolUseId: 'tid-1',
      questions: [
        const AskUserQuestion(
          question: 'Choose a fruit',
          header: 'Fruits',
          multiSelect: false,
          options: [
            AskUserQuestionOption(label: 'Apple', description: 'A red fruit'),
            AskUserQuestionOption(label: 'Banana'),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: AskUserQuestionCard(
            payload: payload,
            onSend: (c) => sent = c,
          ),
        ),
      ),
    );
    await tester.pump();

    // Options rendered
    expect(find.text('Apple'), findsOneWidget);
    expect(find.text('Banana'), findsOneWidget);
    expect(find.text('Fruits'), findsOneWidget);

    // Tap first option — should send immediately
    await tester.tap(find.text('Apple'));
    await tester.pump();

    expect(sent, equals('Choose a fruit: Apple'));
    expect(find.text('已提交'), findsOneWidget);
  });

  testWidgets('AskUserQuestionCard multi-select: shows submit button, requires selection',
      (WidgetTester tester) async {
    String? sent;
    final payload = AskUserQuestionPayload(
      toolUseId: 'tid-2',
      questions: [
        const AskUserQuestion(
          question: 'Pick languages',
          multiSelect: true,
          options: [
            AskUserQuestionOption(label: 'Dart'),
            AskUserQuestionOption(label: 'Go'),
            AskUserQuestionOption(label: 'Python'),
          ],
        ),
      ],
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: AskUserQuestionCard(
            payload: payload,
            onSend: (c) => sent = c,
          ),
        ),
      ),
    );
    await tester.pump();

    // Submit button is present but disabled (nothing selected)
    final submitBtn = find.widgetWithText(FilledButton, '提交');
    expect(submitBtn, findsOneWidget);

    // Select Dart and Go
    await tester.tap(find.text('Dart'));
    await tester.pump();
    await tester.tap(find.text('Go'));
    await tester.pump();

    // Now submit
    await tester.tap(submitBtn);
    await tester.pump();

    expect(sent, equals('Pick languages: Dart, Go'));
    expect(find.text('已提交'), findsOneWidget);
  });

  // ─── R-010 T4: ExitPlanModeCard widget tests ───────────────────────────

  testWidgets('ExitPlanModeCard approve: sends 批准计划 immediately',
      (WidgetTester tester) async {
    String? sent;
    final payload = ExitPlanModePayload(
      toolUseId: 'tid-3',
      plan: '1. Analyse\n2. Code\n3. Test',
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: ExitPlanModeCard(
            payload: payload,
            onSend: (c) => sent = c,
          ),
        ),
      ),
    );
    await tester.pump();

    expect(find.text('批准'), findsOneWidget);
    expect(find.text('拒绝'), findsOneWidget);
    expect(find.text('1. Analyse\n2. Code\n3. Test'), findsOneWidget);

    await tester.tap(find.text('批准'));
    await tester.pump();

    expect(sent, equals('批准计划'));
    expect(find.text('已批准'), findsOneWidget);
  });

  testWidgets('ExitPlanModeCard reject with feedback: shows field, sends 拒绝计划:feedback',
      (WidgetTester tester) async {
    String? sent;
    final payload = ExitPlanModePayload(
      toolUseId: 'tid-4',
      plan: 'Step 1',
    );

    await tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: SingleChildScrollView(
            child: ExitPlanModeCard(
              payload: payload,
              onSend: (c) => sent = c,
            ),
          ),
        ),
      ),
    );
    await tester.pump();

    // First tap shows feedback field
    await tester.tap(find.text('拒绝'));
    await tester.pump();

    expect(find.byType(TextField), findsOneWidget);
    expect(find.text('确认拒绝'), findsOneWidget);

    // Type feedback
    await tester.enterText(find.byType(TextField), 'Too risky');
    await tester.pump();

    // Second tap (now "确认拒绝") submits
    await tester.tap(find.text('确认拒绝'));
    await tester.pump();

    expect(sent, equals('拒绝计划：Too risky'));
    expect(find.text('已拒绝'), findsOneWidget);
  });

  // ─── R-010 T3+T4: convertEventsToMessages integration tests ───────────

  test(
    'convertEventsToMessages recognises ask_user_question kind and passes payload',
    () {
      final messages = convertEventsToMessages([
        {
          'seq': 1,
          'role': 'assistant',
          'text': '',
          'raw': false,
          'kind': 'ask_user_question',
          'askUserQuestion': {
            'tool_use_id': 'tu-1',
            'questions': [
              {
                'question': 'Choose one',
                'header': 'H',
                'multi_select': false,
                'options': [
                  {'label': 'A', 'description': ''},
                ],
              },
            ],
          },
        },
      ]);

      expect(messages, hasLength(1));
      expect(messages[0].kind, equals('ask_user_question'));
      expect(messages[0].askUserQuestion, isNotNull);
      expect(messages[0].askUserQuestion!.toolUseId, equals('tu-1'));
      expect(messages[0].askUserQuestion!.questions, hasLength(1));
      expect(
        messages[0].askUserQuestion!.questions[0].question,
        equals('Choose one'),
      );
    },
  );

  test(
    'convertEventsToMessages recognises exit_plan_mode kind and passes payload',
    () {
      final messages = convertEventsToMessages([
        {
          'seq': 1,
          'role': 'assistant',
          'text': '',
          'raw': false,
          'kind': 'exit_plan_mode',
          'exitPlanMode': {
            'tool_use_id': 'tu-2',
            'plan': 'Step 1\nStep 2',
          },
        },
      ]);

      expect(messages, hasLength(1));
      expect(messages[0].kind, equals('exit_plan_mode'));
      expect(messages[0].exitPlanMode, isNotNull);
      expect(messages[0].exitPlanMode!.toolUseId, equals('tu-2'));
      expect(messages[0].exitPlanMode!.plan, equals('Step 1\nStep 2'));
    },
  );

  // ─── Bug fix: message fragmentation across polling and non-streaming ────

  test(
    'text_delta stream followed by kind=assistant complete event produces one bubble',
    () {
      // Reproduces the core fragmentation bug: the backend emits text_delta
      // events during streaming, then emits a final kind='assistant' event
      // with the full text.  The guard at line ~880 only covered kind=='',
      // kind=='text', and kind=='result' — it did NOT cover kind=='assistant'.
      // As a result flushMergeBuf() flushed the deltas as bubble #1 and then
      // the kind='assistant' complete event was added as bubble #2.
      final events = <Map<String, dynamic>>[
        {
          'seq': 1,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '本次清理工作完成。',
        },
        {
          'seq': 2,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '如果还需要其他操作',
        },
        {
          'seq': 3,
          'role': 'assistant',
          'raw': false,
          'kind': 'text_delta',
          'text': '（比如 force push），告诉我。',
        },
        // Backend emits a final kind='assistant' event with the full text.
        {
          'seq': 4,
          'role': 'assistant',
          'raw': false,
          'kind': 'assistant',
          'text': '本次清理工作完成。如果还需要其他操作（比如 force push），告诉我。',
        },
      ];

      final messages = convertEventsToMessages(events);

      expect(
        messages,
        hasLength(1),
        reason:
            'text_delta stream + final kind=assistant event should produce exactly one bubble',
      );
      expect(messages.first.text, contains('本次清理工作完成'));
      expect(messages.first.text, contains('告诉我'));
    },
  );

  test(
    'short non-streaming kind=assistant message is shown completely without truncation',
    () {
      // A single complete assistant event (no text_delta precursor) should be
      // added as-is without any truncation.  This ensures the isNoiseOnlyText /
      // processTerminalOutput pipeline does not drop content from short Chinese
      // sentences that contain parentheses and punctuation.
      const fullText =
          '本次清理工作完成。如果还需要其他操作（比如 force push、删除 release 旧目录、'
          '清理工作区中其他未 track 的大文件），告诉我。';

      final messages = convertEventsToMessages([
        {
          'seq': 5,
          'role': 'assistant',
          'raw': false,
          'kind': 'assistant',
          'text': fullText,
        },
      ]);

      expect(
        messages,
        hasLength(1),
        reason: 'single non-streaming assistant event should produce one bubble',
      );
      expect(
        messages.first.text,
        equals(fullText),
        reason: 'full text must be preserved without truncation',
      );
    },
  );

  test(
    'mergeConversationEvents preserves both events when both have seq=0 (seq missing)',
    () {
      // If two events arrive without a seq field they both default to seq=0.
      // The previous implementation stored them by seq in a Map<int,…>, so the
      // second event silently overwrote the first one — dropping half the
      // conversation.  Both events must survive the merge.
      final merged = mergeConversationEvents(
        [],
        [
          {
            'role': 'assistant',
            'text': '第一条消息（无 seq）',
            'raw': false,
          },
          {
            'role': 'assistant',
            'text': '第二条消息（无 seq）',
            'raw': false,
          },
        ],
      );

      expect(
        merged,
        hasLength(2),
        reason:
            'both seq=0 events must be preserved; the second must not overwrite the first',
      );
      final texts = merged.map((e) => e['text'] as String).toSet();
      expect(texts, contains('第一条消息（无 seq）'));
      expect(texts, contains('第二条消息（无 seq）'));
    },
  );
}
