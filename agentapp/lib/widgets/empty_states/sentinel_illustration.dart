import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';

/// "Sentinel" illustration — three concentric accent rings emanating from a
/// small radar mark at the centre, evoking a listening-post / mission-control
/// motif.
///
/// Used as the illustration for [EmptyState]. Default size 160×160.
class SentinelIllustration extends StatefulWidget {
  const SentinelIllustration({
    super.key,
    this.size = 160,
    this.color,
    this.animate = true,
  });

  final double size;
  final Color? color;

  /// When false, paints the static "rest" state — useful for screenshots and
  /// reduced-motion users. Defaults to true.
  final bool animate;

  @override
  State<SentinelIllustration> createState() => _SentinelIllustrationState();
}

class _SentinelIllustrationState extends State<SentinelIllustration>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: const Duration(milliseconds: 3000),
  );

  @override
  void initState() {
    super.initState();
    if (widget.animate) {
      _controller.repeat();
    }
  }

  @override
  void didUpdateWidget(covariant SentinelIllustration oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.animate && !_controller.isAnimating) {
      _controller.repeat();
    } else if (!widget.animate && _controller.isAnimating) {
      _controller.stop();
    }
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reduceMotion = MediaQuery.of(context).disableAnimations;
    final color = widget.color ?? AppColors.accent;
    final shouldAnimate = widget.animate && !reduceMotion;

    return SizedBox(
      width: widget.size,
      height: widget.size,
      child: AnimatedBuilder(
        animation: _controller,
        builder: (_, __) => CustomPaint(
          painter: _SentinelPainter(
            color: color,
            progress: shouldAnimate ? _controller.value : 0.0,
          ),
        ),
      ),
    );
  }
}

class _SentinelPainter extends CustomPainter {
  _SentinelPainter({required this.color, required this.progress});

  /// Continuous 0..1 driver from the animation controller.
  final double progress;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final centre = size.center(Offset.zero);

    // Three rings at base radii 30 / 55 / 80.
    const baseRadii = [30.0, 55.0, 80.0];
    final ringPaint = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;

    for (var i = 0; i < baseRadii.length; i++) {
      // Stagger 0 / 0.33 / 0.66.
      final t = (progress + i / baseRadii.length) % 1.0;
      final scale = 1.0 + 0.3 * t;
      final opacity = (1.0 - t).clamp(0.0, 1.0);
      ringPaint.color = color.withValues(alpha: opacity);
      canvas.drawCircle(centre, baseRadii[i] * scale, ringPaint);
    }

    // Centre radar mark — small filled circle + radial line.
    final centreFill = Paint()
      ..color = color
      ..style = PaintingStyle.fill;
    canvas.drawCircle(centre, 6, centreFill);

    final centreRing = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;
    canvas.drawCircle(centre, 12, centreRing);

    // Radial direction line at 45° (up-right).
    const angleRad = 45.0 * math.pi / 180.0;
    final cosA = math.cos(angleRad);
    final sinA = math.sin(angleRad);
    final inner = centre + Offset(12 * cosA, -12 * sinA);
    final outer = centre + Offset(30 * cosA, -30 * sinA);
    final linePaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;
    canvas.drawLine(inner, outer, linePaint);
  }

  @override
  bool shouldRepaint(covariant _SentinelPainter old) =>
      old.progress != progress || old.color != color;
}
