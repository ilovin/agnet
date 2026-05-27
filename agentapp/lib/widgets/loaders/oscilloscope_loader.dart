import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';

/// Three-line oscilloscope loader — small horizontal accent waveforms that
/// independently oscillate in height, evoking a sensor read-out.
///
/// Default size 60×24, intended for inline list / card body "loading"
/// affordances where a centered [CircularProgressIndicator] would feel
/// generic.
class OscilloscopeLoader extends StatefulWidget {
  const OscilloscopeLoader({
    super.key,
    this.width = 60,
    this.height = 24,
    this.amplitude = 12,
    this.period = const Duration(milliseconds: 800),
  });

  final double width;
  final double height;
  final double amplitude;
  final Duration period;

  @override
  State<OscilloscopeLoader> createState() => _OscilloscopeLoaderState();
}

class _OscilloscopeLoaderState extends State<OscilloscopeLoader>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller = AnimationController(
    vsync: this,
    duration: widget.period,
  )..repeat();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      width: widget.width,
      height: widget.height,
      child: AnimatedBuilder(
        animation: _controller,
        builder: (_, __) => CustomPaint(
          painter: _OscilloscopePainter(
            progress: _controller.value,
            amplitude: widget.amplitude,
          ),
        ),
      ),
    );
  }
}

class _OscilloscopePainter extends CustomPainter {
  _OscilloscopePainter({required this.progress, required this.amplitude});

  final double progress;
  final double amplitude;

  @override
  void paint(Canvas canvas, Size size) {
    // Three lines: accent / data / muted grey.
    const phases = [0.0, 0.25, 0.5];
    final colors = <Color>[
      AppColors.accent,
      AppColors.data,
      const Color(0xFF6B7280),
    ];

    final paint = Paint()
      ..style = PaintingStyle.stroke
      ..strokeWidth = 1.5
      ..strokeCap = StrokeCap.round;

    final yMid = size.height / 2;

    for (var i = 0; i < phases.length; i++) {
      final t = (progress + phases[i]) % 1.0;
      final h = amplitude * math.sin(t * 2 * math.pi).abs();
      final yTop = yMid - h / 2;
      final yBottom = yMid + h / 2;
      final x = (size.width / phases.length) * (i + 0.5);
      paint.color = colors[i];
      canvas.drawLine(Offset(x, yTop), Offset(x, yBottom), paint);
    }
  }

  @override
  bool shouldRepaint(covariant _OscilloscopePainter old) =>
      old.progress != progress || old.amplitude != amplitude;
}
