import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';
import '../../theme/app_text_styles.dart';

/// Compact mono `[MODE]` chip that lives in the [MissionControlAppBar]
/// actions slot on the agent-detail screen, replacing the wider mode
/// button that previously occupied a slot inside the composer row.
///
/// Tapping it invokes [onTap] (typically opens the existing mode/config
/// bottom sheet so the rest of the switching UX is unchanged).
class BypassIndicator extends StatelessWidget {
  const BypassIndicator({
    super.key,
    required this.modeLabel,
    this.onTap,
  });

  /// Label for the active mode (e.g. 'Bypass', 'Auto', 'Plan', 'Build').
  /// Already-localised string; rendered uppercased inside the chip.
  final String modeLabel;

  /// Tap handler. When null the chip renders as non-interactive.
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final label = '[${modeLabel.toUpperCase()}]';
    return Padding(
      padding: const EdgeInsets.only(right: 4),
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(4),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
          decoration: BoxDecoration(
            border: Border.all(color: AppColors.accent, width: 1),
            borderRadius: BorderRadius.circular(4),
          ),
          child: Text(
            label,
            style: TextStyle(
              fontFamily: AppTextStyles.monoFontFamily,
              fontFamilyFallback: AppTextStyles.fontFamilyFallback,
              fontSize: 11,
              height: 1.2,
              fontWeight: FontWeight.w600,
              color: scheme.primary,
              letterSpacing: 0.5,
            ),
          ),
        ),
      ),
    );
  }
}
