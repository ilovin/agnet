import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';

/// Mission-control geometric mark: a small circle with a thin radial
/// line shooting out at 36° (radio-signal feel).
///
/// Default size 28×28, suitable for an [AppBar] leading slot.
class MissionControlMark extends StatelessWidget {
  const MissionControlMark({
    super.key,
    this.size = 28,
    this.color,
  });

  final double size;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: size,
      height: size,
      child: CustomPaint(
        painter: _MarkPainter(color: color ?? AppColors.accent),
      ),
    );
  }
}

class _MarkPainter extends CustomPainter {
  _MarkPainter({required this.color});

  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    final centre = Offset(size.width / 2, size.height / 2);
    final radius = size.shortestSide * 0.32;

    // Outer ring (hairline accent).
    final ringPaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5;
    canvas.drawCircle(centre, radius, ringPaint);

    // Inner solid dot.
    final dotPaint = Paint()
      ..color = color
      ..style = PaintingStyle.fill;
    canvas.drawCircle(centre, radius * 0.35, dotPaint);

    // Radial line at 36° (measured from horizontal, going up-right).
    const angleRad = 36.0 * math.pi / 180.0;
    final cosA = math.cos(angleRad);
    final sinA = math.sin(angleRad);
    // Flutter's y axis grows downward, so we negate sin to point upward.
    final inner = centre + Offset(radius * 1.2 * cosA, -radius * 1.2 * sinA);
    final outerR = size.shortestSide / 2 - 1;
    final outer = centre + Offset(outerR * cosA, -outerR * sinA);
    final linePaint = Paint()
      ..color = color
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.2
      ..strokeCap = StrokeCap.round;
    canvas.drawLine(inner, outer, linePaint);
  }

  @override
  bool shouldRepaint(covariant _MarkPainter oldDelegate) =>
      oldDelegate.color != color;
}
