// Task #10 — UI 简化：去掉左上角 logo + agent 行左侧 logo 圆头像。
//
// 这些测试声明 dashboard 顶部 AppBar 不再渲染 brand logo（MissionControlMark
// 与 "Agent" wordmark），并断言 AgentRow 的 ListTile.leading 在非
// canvasSelectionMode 下不再渲染 logo Icon（leading == null）。
//
// 通用 picker 机制（session_logo_provider + dialog 函数）保留，详见
// session_logo_provider_test.dart 与 r002_logo_picker_widget_test.dart。
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/dashboard_screen.dart';
import 'package:agentapp/widgets/app_bar/mission_control_mark.dart';

Future<void> _pumpAgentRow(
  WidgetTester tester, {
  required AgentModel agent,
  required String nodeId,
  bool canvasSelectionMode = false,
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
            canvasSelectionMode: canvasSelectionMode,
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

Future<void> _pumpDashboard(
  WidgetTester tester, {
  Size screenSize = const Size(800, 600),
}) async {
  tester.binding.window.physicalSizeTestValue = screenSize;
  tester.binding.window.devicePixelRatioTestValue = 1.0;
  addTearDown(tester.binding.window.clearPhysicalSizeTestValue);
  addTearDown(tester.binding.window.clearDevicePixelRatioTestValue);

  final container = ProviderContainer();
  container.read(nodesProvider.notifier).loadNodes([
    {
      'id': 'n1',
      'name': 'TestNode',
      'host': '127.0.0.1',
      'status': 'connected',
      'location': {'lat': 0.0, 'lng': 0.0},
      'agentCount': 1,
    },
  ]);
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: DashboardScreen()),
    ),
  );
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 150));
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('Task #10 — Dashboard AppBar brand logo removed', () {
    setUp(() async {
      SharedPreferences.setMockInitialValues({});
    });

    testWidgets('dashboard does not render MissionControlMark in AppBar',
        (tester) async {
      await _pumpDashboard(tester);
      // The mission-control mark (圆+辐射线) is the brand logo. It must be
      // absent from the dashboard header so the top-left reads clean.
      expect(find.byType(MissionControlMark), findsNothing);

      // Tear down to cancel periodic refresh timer.
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });

    testWidgets('dashboard does not render the "Agent" wordmark', (tester) async {
      await _pumpDashboard(tester);
      // Wordmark text accompanies the brand mark; it should also be hidden.
      expect(find.text('Agent'), findsNothing);

      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });
  });

  group('Task #10 — AgentRow leading logo removed', () {
    final agent = AgentModel(
      id: 'a1',
      name: 'no-logo-test',
      workDir: '/tmp',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      runtimeState: 'live',
      sessionState: 'active',
      sessionId: 'sess-1',
    );

    testWidgets('ListTile.leading is null in non-canvasSelectionMode',
        (tester) async {
      await _pumpAgentRow(tester, agent: agent, nodeId: 'n1');

      final tile = tester.widget<ListTile>(find.byType(ListTile));
      expect(
        tile.leading,
        isNull,
        reason: 'logo 圆头像应已移除，文字应直接靠左',
      );
    });

    testWidgets('canvasSelectionMode keeps its add/remove leading icon',
        (tester) async {
      // Add/remove circle icons are part of canvas selection UX, not the
      // brand logo. They must still render in canvasSelectionMode so users
      // can manage canvas membership.
      await _pumpAgentRow(
        tester,
        agent: agent,
        nodeId: 'n1',
        canvasSelectionMode: true,
      );
      final tile = tester.widget<ListTile>(find.byType(ListTile));
      expect(tile.leading, isNotNull);
    });
  });
}
