import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';
import '../../theme/app_text_styles.dart';
import 'sentinel_illustration.dart';

/// Mission-control empty state with a [SentinelIllustration] and a centred
/// caption. Use to replace generic "暂无…" messages in screen bodies.
///
/// Defaults are deliberate:
/// - illustration size 160px (legible at app-content widths)
/// - message "等待信号..." in display font with letter-spacing 1.5px
/// - optional sub-message rendered in body small below the main caption
class EmptyState extends StatelessWidget {
  const EmptyState({
    super.key,
    this.message = '等待信号...',
    this.subMessage,
    this.illustrationSize = 160,
  });

  final String message;
  final String? subMessage;
  final double illustrationSize;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Center(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          SentinelIllustration(size: illustrationSize),
          const SizedBox(height: 24),
          Text(
            message,
            textAlign: TextAlign.center,
            style: AppTextStyles.titleLarge.copyWith(
              fontSize: 22,
              letterSpacing: 1.5,
              color: theme.colorScheme.onSurface,
            ),
          ),
          if (subMessage != null) ...[
            const SizedBox(height: 8),
            Text(
              subMessage!,
              textAlign: TextAlign.center,
              style: AppTextStyles.bodySmall.copyWith(
                color: AppColors.accent.withValues(alpha: 0.7),
              ),
            ),
          ],
        ],
      ),
    );
  }
}
