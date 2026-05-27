import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';
import '../../theme/app_text_styles.dart';
import 'mission_control_mark.dart';
import 'scanning_line.dart';

/// "Mission control" branded [AppBar] for the phone-talk app shell.
///
/// Visual signature:
/// - Geometric mark + "phone-talk" wordmark on the left.
/// - Title fragment ([title]) sits next to the wordmark in [titleMedium]
///   weight, so individual screens still get an identifying caption.
/// - Faint accent scanning line traverses the top of the bar.
/// - 1px hairline divider at the bottom (matches the global theme).
///
/// Implements [PreferredSizeWidget] so it can be dropped into any
/// [Scaffold.appBar] slot.
class MissionControlAppBar extends StatelessWidget
    implements PreferredSizeWidget {
  const MissionControlAppBar({
    super.key,
    this.title,
    this.titleWidget,
    this.leading,
    this.actions,
    this.toolbarHeight = 56,
    this.showScanningLine = true,
    this.showWordmark = true,
    this.markWidget,
  });

  /// Optional sub-title shown next to the brand wordmark.
  final String? title;

  /// Optional custom title widget (overrides [title] when provided).
  /// Use this when the screen needs richer title content (multi-line,
  /// embedded indicators).
  final Widget? titleWidget;

  /// Optional leading widget shown before the wordmark slot.
  final Widget? leading;

  /// Optional trailing actions, mirroring [AppBar.actions].
  final List<Widget>? actions;

  /// Height of the bar excluding the scanning line (which adds 1px on top).
  final double toolbarHeight;

  /// Whether to render the slow scanning-line animation across the top
  /// edge of the bar. Disable in tests / contexts where animation noise
  /// is undesirable.
  final bool showScanningLine;

  /// Whether to render the brand wordmark (phone-talk logo). Disable for
  /// nested or modal screens where the brand would be redundant.
  final bool showWordmark;

  /// Custom widget to render in the wordmark "mark" slot, replacing the
  /// default [MissionControlMark]. Set on the dashboard to host the
  /// live connection-status indicator. When null, the geometric brand
  /// mark is used.
  final Widget? markWidget;

  @override
  Size get preferredSize {
    // Scanning line (1px when enabled) + toolbar + hairline (1px).
    final extras = (showScanningLine ? 1.0 : 0.0) + 1.0;
    return Size.fromHeight(toolbarHeight + extras);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final isDark = theme.brightness == Brightness.dark;
    final hairlineColor =
        isDark ? AppColors.borderDark : AppColors.borderLight;
    final accentColor =
        isDark ? AppColors.accentDark : AppColors.accentLight;
    final wordmarkStyle = AppTextStyles.titleLarge.copyWith(
      fontSize: 20,
      letterSpacing: -0.4,
      color: accentColor,
    );

    return Material(
      color: theme.scaffoldBackgroundColor,
      elevation: 0,
      child: Column(
        children: [
          if (showScanningLine) ScanningLine(color: accentColor),
          SafeArea(
            bottom: false,
            child: SizedBox(
              height: toolbarHeight,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 12),
                child: Row(
                  children: [
                    if (leading != null) ...[
                      leading!,
                      const SizedBox(width: 4),
                    ],
                    if (showWordmark) ...[
                      markWidget ?? const MissionControlMark(size: 22),
                      const SizedBox(width: 8),
                      Text('Agent', style: wordmarkStyle),
                      const SizedBox(width: 12),
                    ],
                    if (titleWidget != null) ...[
                      _BarSeparator(),
                      const SizedBox(width: 12),
                      Flexible(child: titleWidget!),
                    ] else if (title != null && title!.isNotEmpty) ...[
                      _BarSeparator(),
                      const SizedBox(width: 12),
                      Flexible(
                        child: Text(
                          title!,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.titleMedium?.copyWith(
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                    const Spacer(),
                    if (actions != null) ...actions!,
                  ],
                ),
              ),
            ),
          ),
          Container(
            height: 1,
            color: hairlineColor,
          ),
        ],
      ),
    );
  }
}

class _BarSeparator extends StatelessWidget {
  @override
  Widget build(BuildContext context) {
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final hairlineColor =
        isDark ? AppColors.borderDark : AppColors.borderLight;
    return Container(
      width: 1,
      height: 16,
      color: hairlineColor,
    );
  }
}
