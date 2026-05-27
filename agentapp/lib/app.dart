import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import 'screens/connections_screen.dart';
import 'screens/dashboard_screen.dart';
import 'screens/agent_detail_screen.dart';
import 'screens/settings_screen.dart';
import 'providers/connection_provider.dart';
import 'providers/density_mode_provider.dart';
import 'providers/theme_provider.dart';
import 'theme/app_theme.dart';

final _router = GoRouter(
  initialLocation: '/connections',
  redirect: (context, state) {
    // If navigating to dashboard/agent/settings without ever connecting, go to connections.
    // We intentionally do NOT redirect just because the WebSocket is temporarily
    // disconnected — DashboardScreen already shows a reconnect indicator and auto-refreshes.
    final container = ProviderScope.containerOf(context);
    final client = container.read(connectionProvider);
    final needsConnection = client == null;
    if (needsConnection && state.matchedLocation != '/connections') {
      return '/connections';
    }
    return null;
  },
  routes: [
    GoRoute(
      path: '/',
      redirect: (_, __) => '/connections',
    ),
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

class AgentApp extends ConsumerWidget {
  const AgentApp({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final density = ref.watch(densityModeProvider);
    final themeMode = themeModeFromSetting(ref.watch(themeModeProvider));
    return MaterialApp.router(
      title: 'Agnet',
      theme: AppTheme.build(densityMode: density),
      darkTheme: AppTheme.build(
        densityMode: density,
        brightness: Brightness.dark,
      ),
      themeMode: themeMode,
      routerConfig: _router,
    );
  }
}
