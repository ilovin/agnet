import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/providers/dashboard_detail_panel_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

const double kCollapsedRailWidth = 48.0;

List<Map<String, dynamic>> _nodesFixture() => [
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
    ];

List<Map<String, dynamic>> _agentsFixture() => [
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
    ];

Future<ProviderContainer> _pumpDashboard(
  WidgetTester tester, {
  Size screenSize = const Size(1440, 900),
}) async {
  tester.binding.window.physicalSizeTestValue = screenSize;
  tester.binding.window.devicePixelRatioTestValue = 1.0;
  addTearDown(tester.binding.window.clearPhysicalSizeTestValue);
  addTearDown(tester.binding.window.clearDevicePixelRatioTestValue);

  final container = ProviderContainer();
  container.read(nodesProvider.notifier).loadNodes(_nodesFixture());
  container.read(nodesProvider.notifier).loadAgents('n1', _agentsFixture());
  addTearDown(container.dispose);

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: DashboardScreen()),
    ),
  );
  await tester.pump();
  await tester.pump(const Duration(milliseconds: 150));
  return container;
}

Future<void> _disposeTree(WidgetTester tester) async {
  await tester.pumpWidget(const SizedBox());
  await tester.pump();
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  testWidgets(
    'detail panel renders collapsed (48px rail) by default on large screens',
    (tester) async {
      await _pumpDashboard(tester);

      final panel = find.byKey(const Key('dashboard_detail_panel'));
      expect(panel, findsOneWidget);
      final size = tester.getSize(panel);
      expect(
        size.width,
        kCollapsedRailWidth,
        reason: 'Detail panel should default to a 48px rail',
      );

      // Toggle button must be present in the rail
      expect(find.byKey(const Key('dashboard_detail_panel_toggle')),
          findsOneWidget);

      await _disposeTree(tester);
    },
  );

  testWidgets(
    'tapping toggle expands the detail panel beyond 48px',
    (tester) async {
      await _pumpDashboard(tester);

      await tester.tap(find.byKey(const Key('dashboard_detail_panel_toggle')));
      // Allow AnimatedContainer to settle.
      await tester.pumpAndSettle(const Duration(milliseconds: 300));

      final size = tester
          .getSize(find.byKey(const Key('dashboard_detail_panel')));
      expect(
        size.width,
        greaterThan(kCollapsedRailWidth + 100),
        reason: 'Expanded detail panel should be wider than the rail',
      );

      await _disposeTree(tester);
    },
  );

  testWidgets(
    'tapping toggle a second time collapses back to 48px',
    (tester) async {
      await _pumpDashboard(tester);

      await tester.tap(find.byKey(const Key('dashboard_detail_panel_toggle')));
      await tester.pumpAndSettle(const Duration(milliseconds: 300));
      await tester.tap(find.byKey(const Key('dashboard_detail_panel_toggle')));
      await tester.pumpAndSettle(const Duration(milliseconds: 300));

      final size = tester
          .getSize(find.byKey(const Key('dashboard_detail_panel')));
      expect(size.width, kCollapsedRailWidth);

      await _disposeTree(tester);
    },
  );

  testWidgets(
    'expand state is persisted to SharedPreferences',
    (tester) async {
      final container = await _pumpDashboard(tester);

      await tester.tap(find.byKey(const Key('dashboard_detail_panel_toggle')));
      await tester.pumpAndSettle(const Duration(milliseconds: 300));

      // The notifier should have set the prefs key.
      final prefs = await SharedPreferences.getInstance();
      expect(prefs.getBool(DashboardDetailPanelNotifier.prefsKey), isTrue);
      expect(
        container.read(dashboardDetailPanelExpandedProvider),
        isTrue,
      );

      await _disposeTree(tester);
    },
  );

  testWidgets(
    'narrow screens (<800) skip the rail and avoid splitting horizontally',
    (tester) async {
      // 700px wide → still "large screen" (>=900 threshold breaks here, so
      // the dashboard shows the mobile ListView). We just assert the dual-pane
      // 48px rail is NOT rendered on narrow widths.
      await _pumpDashboard(tester, screenSize: const Size(700, 900));

      expect(find.byKey(const Key('dashboard_detail_panel')), findsNothing);

      await _disposeTree(tester);
    },
  );
}
