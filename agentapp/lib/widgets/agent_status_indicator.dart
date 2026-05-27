import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../theme/app_colors.dart';

/// Logical visual states for [AgentStatusIndicator].
///
/// Mapped from domain `AgentStatus` by callers — this enum is intentionally
/// small (4 states) so each gets a distinctive animation, instead of paying
/// per-status visual cost.
enum AgentIndicatorStatus {
  /// Active / executing. Breathing accent dot.
  running,

  /// Reasoning / waiting on model. Radar scan stroke ring.
  thinking,

  /// Standby. Static stroke ring, no animation.
  idle,

  /// Crash / error. Pulsing red dot.
  error,
}

/// Compact circular status badge that conveys agent state through a
/// distinct animation per status.
///
/// Default size is 16×16, matching the existing dot affordance used in
/// list rows. Color defaults to [AppColors.accent] (or [AppColors.error]
/// for [AgentIndicatorStatus.error]).
class AgentStatusIndicator extends StatefulWidget {
  const AgentStatusIndicator({
    super.key,
    required this.status,
    this.size = 16,
    this.color,
  });

  final AgentIndicatorStatus status;
  final double size;

  /// Override the dot/stroke color. Defaults to [AppColors.accent] for
  /// non-error statuses, [AppColors.error] for [AgentIndicatorStatus.error].
  final Color? color;

  @override
  State<AgentStatusIndicator> createState() => _AgentStatusIndicatorState();
}

class _AgentStatusIndicatorState extends State<AgentStatusIndicator>
    with SingleTickerProviderStateMixin {
  AnimationController? _controller;

  @override
  void initState() {
    super.initState();
    _ensureControllerForStatus(widget.status);
  }

  @override
  void didUpdateWidget(covariant AgentStatusIndicator oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.status != widget.status) {
      _ensureControllerForStatus(widget.status);
    }
  }

  void _ensureControllerForStatus(AgentIndicatorStatus status) {
    Duration? duration;
    switch (status) {
      case AgentIndicatorStatus.running:
        duration = const Duration(milliseconds: 1500);
        break;
      case AgentIndicatorStatus.thinking:
        duration = const Duration(milliseconds: 2000);
        break;
      case AgentIndicatorStatus.error:
        duration = const Duration(milliseconds: 800);
        break;
      case AgentIndicatorStatus.idle:
        duration = null;
        break;
    }

    if (duration == null) {
      _controller?.stop();
      _controller?.dispose();
      _controller = null;
      return;
    }
    if (_controller == null) {
      _controller = AnimationController(vsync: this, duration: duration)
        ..repeat(reverse: status == AgentIndicatorStatus.running ||
            status == AgentIndicatorStatus.error);
    } else {
      _controller!.duration = duration;
      _controller!.repeat(
        reverse: status == AgentIndicatorStatus.running ||
            status == AgentIndicatorStatus.error,
      );
    }
  }

  @override
  void dispose() {
    _controller?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final isError = widget.status == AgentIndicatorStatus.error;
    final color = widget.color ??
        (isError ? AppColors.error : AppColors.accent);
    final size = widget.size;

    Widget child;
    switch (widget.status) {
      case AgentIndicatorStatus.running:
        child = _buildRunning(size, color);
        break;
      case AgentIndicatorStatus.thinking:
        child = _buildThinking(size, color);
        break;
      case AgentIndicatorStatus.idle:
        child = _buildIdle(size, color);
        break;
      case AgentIndicatorStatus.error:
        child = _buildError(size, color);
        break;
    }
    return SizedBox(width: size, height: size, child: child);
  }

  Widget _buildRunning(double size, Color color) {
    final controller = _controller!;
    final scaleAnim = Tween<double>(begin: 0.9, end: 1.0).animate(
      CurvedAnimation(parent: controller, curve: Curves.easeInOut),
    );
    final opacityAnim = Tween<double>(begin: 0.6, end: 1.0).animate(
      CurvedAnimation(parent: controller, curve: Curves.easeInOut),
    );
    return ScaleTransition(
      scale: scaleAnim,
      child: FadeTransition(
        opacity: opacityAnim,
        child: Container(
          decoration: BoxDecoration(
            color: color,
            shape: BoxShape.circle,
          ),
        ),
      ),
    );
  }

  Widget _buildThinking(double size, Color color) {
    return AnimatedBuilder(
      animation: _controller!,
      builder: (_, __) => CustomPaint(
        size: Size.square(size),
        painter: _ThinkingScanPainter(
          color: color,
          progress: _controller!.value,
        ),
      ),
    );
  }

  Widget _buildIdle(double size, Color color) {
    return CustomPaint(
      size: Size.square(size),
      painter: _IdleRingPainter(color: color),
    );
  }

  Widget _buildError(double size, Color color) {
    final controller = _controller!;
    final opacityAnim = Tween<double>(begin: 0.3, end: 1.0).animate(
      CurvedAnimation(parent: controller, curve: Curves.easeInOut),
    );
    return FadeTransition(
      opacity: opacityAnim,
      child: Container(
        decoration: BoxDecoration(
          color: color,
          shape: BoxShape.circle,
        ),
      ),
    );
  }
}

class _IdleRingPainter extends CustomPainter {
  _IdleRingPainter({required this.color});

  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;
    final r = (size.shortestSide / 2) - 1;
    canvas.drawCircle(size.center(Offset.zero), r, paint);
  }

  @override
  bool shouldRepaint(covariant _IdleRingPainter old) => old.color != color;
}

class _ThinkingScanPainter extends CustomPainter {
  _ThinkingScanPainter({required this.color, required this.progress});

  final Color color;
  final double progress;

  @override
  void paint(Canvas canvas, Size size) {
    final centre = size.center(Offset.zero);
    final r = (size.shortestSide / 2) - 1;

    // Stroke ring.
    final ringPaint = Paint()
      ..color = color.withValues(alpha: 0.6)
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.2;
    canvas.drawCircle(centre, r, ringPaint);

    // Scanning sweep — a thin line from centre rotating with progress.
    final angle = progress * 2 * math.pi;
    final endpoint = Offset(
      centre.dx + r * math.cos(angle),
      centre.dy + r * math.sin(angle),
    );
    final scanPaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;
    canvas.drawLine(centre, endpoint, scanPaint);
  }

  @override
  bool shouldRepaint(covariant _ThinkingScanPainter old) =>
      old.progress != progress || old.color != color;
}
