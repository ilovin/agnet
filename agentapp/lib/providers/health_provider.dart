import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import 'connection_provider.dart';
import '../services/ws_client.dart';

enum HealthStatus { healthy, degraded, down, unknown }

class HealthInfo {
  final HealthStatus status;
  final Map<String, NodeHealth> nodes;
  final int uptimeSeconds;
  final String? timestamp;
  final DateTime checkedAt;

  const HealthInfo({
    required this.status,
    required this.nodes,
    required this.uptimeSeconds,
    this.timestamp,
    required this.checkedAt,
  });
}

class NodeHealth {
  final String status;
  final int latencyMs;
  final int agents;
  final String? error;

  const NodeHealth({
    required this.status,
    required this.latencyMs,
    required this.agents,
    this.error,
  });
}

class HealthNotifier extends StateNotifier<HealthInfo?> {
  final WsClient? _client;
  Timer? _timer;

  HealthNotifier(this._client) : super(null) {
    if (_client != null) {
      _startPolling();
    }
  }

  void _startPolling() {
    _checkHealth();
    _timer = Timer.periodic(const Duration(seconds: 10), (_) => _checkHealth());
  }

  Future<void> _checkHealth() async {
    if (_client == null || !_client.isConnected) {
      state = HealthInfo(
        status: HealthStatus.down,
        nodes: const {},
        uptimeSeconds: 0,
        checkedAt: DateTime.now(),
      );
      return;
    }

    try {
      final result = await _client.call(
        'system.health',
        {},
        timeout: const Duration(seconds: 6),
      );

      final map = result is Map<String, dynamic> ? result : <String, dynamic>{};

      final statusStr = map['status'] as String? ?? 'down';
      final status = switch (statusStr) {
        'healthy' => HealthStatus.healthy,
        'degraded' => HealthStatus.degraded,
        _ => HealthStatus.down,
      };

      final rawNodes = map['nodes'] is Map ? Map<String, dynamic>.from(map['nodes'] as Map) : <String, dynamic>{};
      final nodes = <String, NodeHealth>{};
      for (final entry in rawNodes.entries) {
        final v = entry.value is Map ? Map<String, dynamic>.from(entry.value as Map) : <String, dynamic>{};
        nodes[entry.key] = NodeHealth(
          status: v['status'] as String? ?? 'unknown',
          latencyMs: (v['latency_ms'] as num?)?.toInt() ?? 0,
          agents: (v['agents'] as num?)?.toInt() ?? 0,
          error: v['error'] as String?,
        );
      }

      state = HealthInfo(
        status: status,
        nodes: nodes,
        uptimeSeconds: (map['uptime_seconds'] as num?)?.toInt() ?? 0,
        timestamp: map['timestamp'] as String?,
        checkedAt: DateTime.now(),
      );
    } catch (_) {
      state = HealthInfo(
        status: HealthStatus.down,
        nodes: const {},
        uptimeSeconds: 0,
        checkedAt: DateTime.now(),
      );
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }
}

final healthProvider = StateNotifierProvider<HealthNotifier, HealthInfo?>(
  (ref) {
    final client = ref.watch(connectionProvider);
    return HealthNotifier(client);
  },
);
