import 'package:flutter/material.dart';

import '../../models/agent_model.dart';
import '../../theme/agent_status_theme.dart';

/// Small status dot for the Dashboard AppBar title.
///
/// Maps [AgentStatus] to a solid color circle:
/// - idle     → green
/// - working  → orange
/// - starting → orange
/// - stopped  → grey
/// - crashed  → red
///
/// Default size is 10×10 logical pixels, vertically centered with text.
class DashboardStatusDot extends StatelessWidget {
  const DashboardStatusDot({
    super.key,
    this.status,
    this.size = 10,
  });

  /// The agent status to visualise. When null the dot is hidden.
  final AgentStatus? status;

  /// Diameter of the dot in logical pixels.
  final double size;

  @override
  Widget build(BuildContext context) {
    final s = status;
    if (s == null) return const SizedBox.shrink();

    return Container(
      width: size,
      height: size,
      decoration: BoxDecoration(
        color: AgentStatusTheme.getColor(s),
        shape: BoxShape.circle,
      ),
    );
  }
}
