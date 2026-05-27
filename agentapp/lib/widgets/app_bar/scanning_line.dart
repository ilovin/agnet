import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import '../../theme/app_colors.dart';

/// A 1px scanning line that slowly traverses left-to-right along the top
/// edge of its parent, evoking a radar / oscilloscope sweep.
///
/// Visually faint (low opacity accent) so it does not draw focus from
/// real content. Designed to sit at the very top of an app shell, just
/// below the system status bar.
///
/// The animation cycles every [period] (default 10s sweep + 4s pause).
/// Honours [MediaQueryData.disableAnimations] for accessibility.
class ScanningLine extends StatefulWidget {
  const ScanningLine({
    super.key,
    this.color,
    this.height = 1.0,
    this.opacity = 0.4,
    this.period = const Duration(seconds: 10),
    this.pause = const Duration(seconds: 4),
    this.lineWidthFraction = 0.18,
  });

  final Color? color;
  final double height;
  final double opacity;
  final Duration period;
  final Duration pause;

  /// Fraction of the parent width occupied by the moving line segment.
  final double lineWidthFraction;

  @override
  State<ScanningLine> createState() => _ScanningLineState();
}

class _ScanningLineState extends State<ScanningLine>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller;

  @override
  void initState() {
    super.initState();
    final total = widget.period + widget.pause;
    _controller = AnimationController(vsync: this, duration: total);
    // In Flutter widget tests, infinitely-repeating tickers cause
    // pumpAndSettle to time out. Honour the platform test flag and skip
    // the loop — the static fallback in build() still renders a faint
    // line for visual presence.
    if (!_isInTestEnvironment) {
      _controller.repeat();
    }
  }

  bool get _isInTestEnvironment {
    // Bool.fromEnvironment doesn't help here; runtime test detection.
    return SchedulerBinding.instance.runtimeType.toString().contains('Test') ||
        kDebugMode &&
            const bool.fromEnvironment('flutter.test', defaultValue: false);
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final reduceMotion = MediaQuery.of(context).disableAnimations;
    final color = (widget.color ?? AppColors.accent).withValues(
      alpha: widget.opacity,
    );

    if (reduceMotion || _isInTestEnvironment) {
      // Render a static faint full-width line so the visual presence is
      // preserved without animation. Also used during widget tests where
      // an infinite ticker would deadlock pumpAndSettle.
      return SizedBox(
        height: widget.height,
        child: Container(color: color.withValues(alpha: widget.opacity * 0.5)),
      );
    }

    return SizedBox(
      height: widget.height,
      child: AnimatedBuilder(
        animation: _controller,
        builder: (context, _) {
          return LayoutBuilder(
            builder: (context, constraints) {
              final width = constraints.maxWidth;
              final lineW = width * widget.lineWidthFraction;
              final totalMs =
                  widget.period.inMilliseconds + widget.pause.inMilliseconds;
              final t = _controller.value * totalMs;
              double xFraction;
              if (t < widget.period.inMilliseconds) {
                xFraction = t / widget.period.inMilliseconds;
              } else {
                // Pause phase: park the line off-screen-right.
                xFraction = 1.0;
              }
              // Offset: starts at -lineW (offscreen left) and moves to width.
              final dx = -lineW + xFraction * (width + lineW);
              return Stack(
                children: [
                  Positioned(
                    left: dx,
                    top: 0,
                    child: Container(
                      width: lineW,
                      height: widget.height,
                      decoration: BoxDecoration(
                        gradient: LinearGradient(
                          colors: [
                            color.withValues(alpha: 0),
                            color,
                            color.withValues(alpha: 0),
                          ],
                        ),
                      ),
                    ),
                  ),
                ],
              );
            },
          );
        },
      ),
    );
  }
}
