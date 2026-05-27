import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../../providers/connection_provider.dart';
import '../../theme/app_colors.dart';

/// 4-state visual model for the connection-status dot.
///
/// Mapped from [WsConnectionState] (3 raw states) plus an "unknown" fallback
/// for the brief moment before the first stream value arrives.
enum ConnectionStatus { connected, reconnecting, disconnected, unknown }

/// 12px circular indicator that visualises the live websocket connection.
///
/// Replaces the static [MissionControlMark] in the dashboard AppBar slot.
/// Pulls from [connectionStateProvider]; renders a slow opacity pulse for
/// the reconnecting state and a static dot otherwise.
class ConnectionStatusIndicator extends ConsumerWidget {
  const ConnectionStatusIndicator({super.key, this.size = 12});

  final double size;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final asyncState = ref.watch(connectionStateProvider);
    final status = asyncState.maybeWhen(
      data: _mapState,
      orElse: () => ConnectionStatus.unknown,
    );
    return _StatusDot(status: status, size: size);
  }

  static ConnectionStatus _mapState(WsConnectionState s) {
    switch (s) {
      case WsConnectionState.connected:
        return ConnectionStatus.connected;
      case WsConnectionState.connecting:
        return ConnectionStatus.reconnecting;
      case WsConnectionState.disconnected:
        return ConnectionStatus.disconnected;
    }
  }
}

/// Visual-only widget for the four states; exposed for tests.
class _StatusDot extends StatefulWidget {
  const _StatusDot({required this.status, required this.size});

  final ConnectionStatus status;
  final double size;

  @override
  State<_StatusDot> createState() => _StatusDotState();
}

class _StatusDotState extends State<_StatusDot>
    with SingleTickerProviderStateMixin {
  AnimationController? _ctrl;

  @override
  void initState() {
    super.initState();
    _ensureController();
  }

  @override
  void didUpdateWidget(covariant _StatusDot oldWidget) {
    super.didUpdateWidget(oldWidget);
    _ensureController();
  }

  void _ensureController() {
    if (widget.status == ConnectionStatus.reconnecting) {
      _ctrl ??= AnimationController(
        vsync: this,
        duration: const Duration(milliseconds: 1500),
      )..repeat(reverse: true);
    } else {
      _ctrl?.stop();
    }
  }

  @override
  void dispose() {
    _ctrl?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final colour = _colourFor(widget.status);
    Widget dot = Container(
      width: widget.size,
      height: widget.size,
      decoration: BoxDecoration(
        color: colour,
        shape: BoxShape.circle,
      ),
    );
    if (widget.status == ConnectionStatus.reconnecting && _ctrl != null) {
      dot = FadeTransition(
        opacity: Tween<double>(begin: 0.4, end: 1.0).animate(_ctrl!),
        child: dot,
      );
    }
    return Semantics(
      label: _semanticLabel(widget.status),
      child: SizedBox(
        key: const ValueKey('connection-status-dot-root'),
        width: widget.size,
        height: widget.size,
        child: Center(child: dot),
      ),
    );
  }

  static Color _colourFor(ConnectionStatus s) {
    switch (s) {
      case ConnectionStatus.connected:
        return AppColors.accent;
      case ConnectionStatus.reconnecting:
        return AppColors.warn;
      case ConnectionStatus.disconnected:
        return AppColors.error;
      case ConnectionStatus.unknown:
        return const Color(0xFF6B7280);
    }
  }

  static String _semanticLabel(ConnectionStatus s) {
    switch (s) {
      case ConnectionStatus.connected:
        return '已连接';
      case ConnectionStatus.reconnecting:
        return '正在重连';
      case ConnectionStatus.disconnected:
        return '已断开';
      case ConnectionStatus.unknown:
        return '连接状态未知';
    }
  }
}

/// Visual-only variant of the indicator that takes an explicit [status],
/// bypassing the Riverpod provider. Used in widget tests and in contexts
/// where the surrounding widget already owns the connection state.
class ConnectionStatusDot extends StatelessWidget {
  const ConnectionStatusDot({
    super.key,
    required this.status,
    this.size = 12,
  });

  final ConnectionStatus status;
  final double size;

  @override
  Widget build(BuildContext context) {
    return _StatusDot(status: status, size: size);
  }
}
