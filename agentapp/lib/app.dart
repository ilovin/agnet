import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

import 'screens/connections_screen.dart';
import 'screens/dashboard_screen.dart';
import 'screens/agent_detail_screen.dart';
import 'screens/settings_screen.dart';

final _router = GoRouter(
  initialLocation: '/connections',
  routes: [
    GoRoute(
      path: '/connections',
      builder: (_, __) => const ConnectionsScreen(),
    ),
    GoRoute(
      path: '/dashboard',
      builder: (_, __) => const DashboardScreen(),
    ),
    GoRoute(
      path: '/agent/:nodeId/:agentId',
      builder: (_, state) => AgentDetailScreen(
        nodeId: state.pathParameters['nodeId']!,
        agentId: state.pathParameters['agentId']!,
      ),
    ),
    GoRoute(
      path: '/settings',
      builder: (_, __) => const SettingsScreen(),
    ),
  ],
);

class AgentApp extends StatelessWidget {
  const AgentApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp.router(
      title: 'Agent Manager',
      theme: ThemeData(
        colorScheme: ColorScheme.fromSeed(seedColor: Colors.indigo),
        useMaterial3: true,
      ),
      routerConfig: _router,
    );
  }
}
