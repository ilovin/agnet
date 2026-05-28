import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/providers/connection_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/agent_detail_screen.dart';
import 'package:agentapp/services/ws_client.dart';
import 'package:agentapp/widgets/app_bar/dashboard_status_dot.dart';
import 'package:agentapp/widgets/app_bar/mission_control_app_bar.dart';

/// A fake [WsClient] that returns empty responses and avoids network calls.
class _FakeWsClient extends Fake implements WsClient {
  @override
  String get token => 'fake-token';

  @override
  Stream<bool> get onConnectionChanged => const Stream<bool>.empty();

  @override
  Stream<bool> get onReconnecting => const Stream<bool>.empty();

  @override
  void onEvent(dynamic handler) {}

  @override
  void offEvent(dynamic handler) {}

  @override
  void dispose() {}

  @override
  Future<dynamic> call(
    String method,
    Map<String, dynamic> params, {
    Duration timeout = const Duration(seconds: 10),
  }) async {
    // Return minimal empty responses for history/skills calls
    if (method == 'conversation.history') {
      return {
        'events': <Map<String, dynamic>>[],
        'lastSeq': 0,
        'firstSeq': 0,
        'sessionId': '',
      };
    }
    if (method == 'system.skills') {
      return {'skills': <dynamic>[] };
    }
    return null;
  }
}

Future<void> _pumpAgentDetail(
  WidgetTester tester, {
  required String nodeId,
  required String agentId,
  required List<Map<String, dynamic>> agents,
  Size screenSize = const Size(800, 600),
}) async {
  tester.binding.window.physicalSizeTestValue = screenSize;
  tester.binding.window.devicePixelRatioTestValue = 1.0;
  addTearDown(tester.binding.window.clearPhysicalSizeTestValue);
  addTearDown(tester.binding.window.clearDevicePixelRatioTestValue);

  final container = ProviderContainer(
    overrides: [
      connectionProvider.overrideWith((ref) {
        final notifier = ConnectionNotifier(
          ref.watch(connectionStoreProvider),
        );
        notifier.state = _FakeWsClient();
        return notifier;
      }),
    ],
  );
  container.read(nodesProvider.notifier).loadNodes([
    {
      'id': nodeId,
      'name': 'TestNode',
      'host': '127.0.0.1',
      'status': 'connected',
      'location': {'lat': 0.0, 'lng': 0.0},
      'agentCount': agents.length,
    },
  ]);
  container.read(nodesProvider.notifier).loadAgents(nodeId, agents);
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: AgentDetailScreen(nodeId: nodeId, agentId: agentId),
      ),
    ),
  );
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 150));
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('Agent Detail AppBar status dot', () {
    setUp(() async {
      SharedPreferences.setMockInitialValues({});
    });

    testWidgets('renders DashboardStatusDot in AppBar', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'idle',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      expect(find.byType(DashboardStatusDot), findsOneWidget);
    });

    testWidgets('dot color matches agent status (idle = green)', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'idle',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      final dot = tester.widget<DashboardStatusDot>(find.byType(DashboardStatusDot));
      expect(dot.status, AgentStatus.idle);

      // Verify the actual rendered color via the Container decoration
      final container = tester.widget<Container>(find.descendant(
        of: find.byType(DashboardStatusDot),
        matching: find.byType(Container),
      ));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.green);
    });

    testWidgets('dot color matches agent status (working = blue)', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'working',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      final dot = tester.widget<DashboardStatusDot>(find.byType(DashboardStatusDot));
      expect(dot.status, AgentStatus.working);

      final container = tester.widget<Container>(find.descendant(
        of: find.byType(DashboardStatusDot),
        matching: find.byType(Container),
      ));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.blue);
    });

    testWidgets('dot color matches agent status (crashed = red)', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'crashed',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      final dot = tester.widget<DashboardStatusDot>(find.byType(DashboardStatusDot));
      expect(dot.status, AgentStatus.crashed);

      final container = tester.widget<Container>(find.descendant(
        of: find.byType(DashboardStatusDot),
        matching: find.byType(Container),
      ));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.red);
    });

    testWidgets('dot is positioned inside MissionControlAppBar with title', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'idle',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      // The dot should be inside the MissionControlAppBar
      expect(
        find.descendant(
          of: find.byType(MissionControlAppBar),
          matching: find.byType(DashboardStatusDot),
        ),
        findsOneWidget,
      );

      // Title text should also be present
      expect(find.text('TestAgent'), findsOneWidget);
    });

    testWidgets('no vertical separator between back arrow and dot', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'idle',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      // The titleWidget Row contains: [DashboardStatusDot, SizedBox, Flexible(Text)]
      // No _BarSeparator widget should be in this Row.
      // Find the specific Row that is the direct ancestor of the DashboardStatusDot.
      final dotFinder = find.byType(DashboardStatusDot);
      final rowFinder = find.ancestor(
        of: dotFinder,
        matching: find.byType(Row),
        matchRoot: false,
      );
      // There may be multiple Row ancestors; pick the one whose children
      // include the DashboardStatusDot directly.
      final rows = tester.widgetList<Row>(rowFinder).toList();
      final titleRow = rows.firstWhere(
        (r) => r.children.any((w) => w is DashboardStatusDot),
      );
      expect(titleRow.children.length, 3);
      expect(titleRow.children[0], isA<DashboardStatusDot>());
      expect(titleRow.children[1], isA<SizedBox>());
      expect(titleRow.children[2], isA<Flexible>());
    });

    testWidgets('AppBar does not show wordmark or brand mark', (tester) async {
      await _pumpAgentDetail(
        tester,
        nodeId: 'n1',
        agentId: 'a1',
        agents: [
          {
            'id': 'a1',
            'name': 'TestAgent',
            'workDir': '/tmp',
            'nodeId': 'n1',
            'provider': 'claude',
            'status': 'idle',
            'runtimeState': 'live',
            'sessionState': 'active',
          },
        ],
      );

      // Agent detail should not show the brand wordmark
      expect(find.text('Agent'), findsNothing);
    });
  });
}
