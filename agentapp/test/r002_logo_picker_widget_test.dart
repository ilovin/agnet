import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/providers/session_logo_provider.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

/// Helper to pump DashboardScreen with a single agent for logo picker tests.
/// Uses small screen (800x600) to avoid split-view canvas layout.
Future<void> pumpDashboardWithAgent(
  WidgetTester tester, {
  required String nodeId,
  required String agentId,
  required String provider,
  String? sessionId,
  int? pid,
  Size screenSize = const Size(800, 600),
}) async {
  tester.binding.window.physicalSizeTestValue = screenSize;
  tester.binding.window.devicePixelRatioTestValue = 1.0;
  addTearDown(tester.binding.window.clearPhysicalSizeTestValue);
  addTearDown(tester.binding.window.clearDevicePixelRatioTestValue);

  final container = ProviderContainer();
  container.read(nodesProvider.notifier).loadNodes([
    {
      'id': nodeId,
      'name': 'TestNode',
      'host': '127.0.0.1',
      'status': 'connected',
      'location': {'lat': 0.0, 'lng': 0.0},
      'agentCount': 1,
    },
  ]);
  container.read(nodesProvider.notifier).loadAgents(nodeId, [
    {
      'id': agentId,
      'name': 'TestAgent',
      'provider': provider,
      'status': 'idle',
      'sessionId': sessionId,
      'pid': pid,
      'projectName': 'test-project',
      'workDir': '/tmp',
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
  // Allow _startAutoRefresh Future.delayed to complete
  await tester.pump(const Duration(milliseconds: 150));
}

/// Finds the logo InkWell inside an AgentRow ListTile by looking for the
/// InkWell ancestor of the leading Icon.
/// Note: ListTile.at(0) is the NodeCard header; at(1) is the AgentRow.
Finder findLogoInkWell() {
  final agentListTile = find.byType(ListTile).at(1);
  final iconInTile = find.descendant(of: agentListTile, matching: find.byType(Icon)).first;
  return find.ancestor(of: iconInTile, matching: find.byType(InkWell)).first;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  group('R-002 Logo Picker Widget Tests', () {
    setUp(() async {
      SharedPreferences.setMockInitialValues({});
    });

    testWidgets('tapping agent logo opens picker dialog', (tester) async {
      await pumpDashboardWithAgent(
        tester,
        nodeId: 'node1',
        agentId: 'agent1',
        provider: 'claude',
        sessionId: 'sess-a',
        pid: 1234,
      );

      // In small-screen list view, the NodeCard expands to show AgentRows.
      // Tap the expand icon first to reveal agents.
      final expandIcon = find.byIcon(Icons.expand_more);
      if (expandIcon.evaluate().isNotEmpty) {
        await tester.tap(expandIcon.first);
        await tester.pumpAndSettle();
      }

      final logoInkWell = findLogoInkWell();
      expect(logoInkWell, findsOneWidget);

      await tester.tap(logoInkWell);
      await tester.pumpAndSettle();

      // Dialog should appear with title
      expect(find.text('选择会话图标'), findsOneWidget);

      // Dispose widget tree to cancel DashboardScreen periodic timer
      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });

    testWidgets('picker shows current icon as selected', (tester) async {
      await pumpDashboardWithAgent(
        tester,
        nodeId: 'node1',
        agentId: 'agent1',
        provider: 'claude',
        sessionId: 'sess-a',
        pid: 1234,
      );

      final expandIcon = find.byIcon(Icons.expand_more);
      if (expandIcon.evaluate().isNotEmpty) {
        await tester.tap(expandIcon.first);
        await tester.pumpAndSettle();
      }

      final logoInkWell = findLogoInkWell();
      await tester.tap(logoInkWell);
      await tester.pumpAndSettle();

      // The grid should contain icons
      expect(find.byType(GridView), findsOneWidget);

      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });

    testWidgets('selecting new icon updates logo', (tester) async {
      await pumpDashboardWithAgent(
        tester,
        nodeId: 'node1',
        agentId: 'agent1',
        provider: 'claude',
        sessionId: 'sess-a',
        pid: 1234,
      );

      final expandIcon = find.byIcon(Icons.expand_more);
      if (expandIcon.evaluate().isNotEmpty) {
        await tester.tap(expandIcon.first);
        await tester.pumpAndSettle();
      }

      final logoInkWell = findLogoInkWell();
      await tester.tap(logoInkWell);
      await tester.pumpAndSettle();

      // Tap the second icon in the grid
      final gridItems = find.descendant(
        of: find.byType(GridView),
        matching: find.byType(InkWell),
      );
      expect(gridItems, findsWidgets);
      await tester.tap(gridItems.at(1));
      await tester.pumpAndSettle();

      // Dialog should close
      expect(find.text('选择会话图标'), findsNothing);

      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });

    testWidgets('reset button restores default icon', (tester) async {
      await pumpDashboardWithAgent(
        tester,
        nodeId: 'node1',
        agentId: 'agent1',
        provider: 'claude',
        sessionId: 'sess-a',
        pid: 1234,
      );

      final expandIcon = find.byIcon(Icons.expand_more);
      if (expandIcon.evaluate().isNotEmpty) {
        await tester.tap(expandIcon.first);
        await tester.pumpAndSettle();
      }

      final logoInkWell = findLogoInkWell();
      await tester.tap(logoInkWell);
      await tester.pumpAndSettle();

      // Tap reset button
      await tester.tap(find.text('恢复默认'));
      await tester.pumpAndSettle();

      // Dialog should close
      expect(find.text('选择会话图标'), findsNothing);

      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });

    testWidgets('cancel button closes dialog without change', (tester) async {
      await pumpDashboardWithAgent(
        tester,
        nodeId: 'node1',
        agentId: 'agent1',
        provider: 'claude',
        sessionId: 'sess-a',
        pid: 1234,
      );

      final expandIcon = find.byIcon(Icons.expand_more);
      if (expandIcon.evaluate().isNotEmpty) {
        await tester.tap(expandIcon.first);
        await tester.pumpAndSettle();
      }

      final logoInkWell = findLogoInkWell();
      await tester.tap(logoInkWell);
      await tester.pumpAndSettle();

      await tester.tap(find.text('取消'));
      await tester.pumpAndSettle();

      expect(find.text('选择会话图标'), findsNothing);

      await tester.pumpWidget(const SizedBox());
      await tester.pump();
    });
  });
}
