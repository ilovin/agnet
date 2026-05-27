import 'package:flutter/material.dart';

import '../../theme/app_colors.dart';
import '../../theme/app_text_styles.dart';

/// Composer "+" button — consolidates "添加图片" and "特殊按键" into a single
/// affordance.
///
/// Tapping the button opens a [showModalBottomSheet] containing a 2-cell
/// grid (image / special-keys). The icon rotates 45° to a "×" while the
/// sheet is visible, mirroring the open / close affordance.
///
/// Either callback can be `null` to disable that particular option (the
/// surrounding caller already gates by provider/attachMode/readOnly).
class ComposerPlusButton extends StatefulWidget {
  const ComposerPlusButton({
    super.key,
    required this.onPickImage,
    required this.onShowSpecialKeys,
  });

  final VoidCallback? onPickImage;
  final VoidCallback? onShowSpecialKeys;

  @override
  State<ComposerPlusButton> createState() => _ComposerPlusButtonState();
}

class _ComposerPlusButtonState extends State<ComposerPlusButton> {
  bool _open = false;

  bool get _disabled =>
      widget.onPickImage == null && widget.onShowSpecialKeys == null;

  Future<void> _onTap() async {
    if (_disabled) return;
    setState(() => _open = true);
    await showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.inkElev,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => ComposerPlusSheet(
        onPickImage: widget.onPickImage,
        onShowSpecialKeys: widget.onShowSpecialKeys,
      ),
    );
    if (mounted) setState(() => _open = false);
  }

  @override
  Widget build(BuildContext context) {
    return IconButton(
      key: const Key('composer-plus-button'),
      onPressed: _disabled ? null : _onTap,
      icon: AnimatedRotation(
        turns: _open ? 0.125 : 0.0,
        duration: const Duration(milliseconds: 200),
        child: const Icon(Icons.add, size: 22),
      ),
      tooltip: '更多',
      visualDensity: VisualDensity.compact,
      constraints: const BoxConstraints(
        minWidth: 32,
        minHeight: 32,
      ),
      padding: EdgeInsets.zero,
    );
  }
}

/// Bottom sheet shown by [ComposerPlusButton]. Exposed publicly so tests
/// can pump it standalone.
class ComposerPlusSheet extends StatelessWidget {
  const ComposerPlusSheet({
    super.key,
    required this.onPickImage,
    required this.onShowSpecialKeys,
  });

  final VoidCallback? onPickImage;
  final VoidCallback? onShowSpecialKeys;

  @override
  Widget build(BuildContext context) {
    return Container(
      decoration: const BoxDecoration(
        border: Border(
          top: BorderSide(color: AppColors.accent, width: 1),
        ),
      ),
      padding: const EdgeInsets.fromLTRB(16, 20, 16, 24),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Wrap(
            spacing: 16,
            runSpacing: 16,
            alignment: WrapAlignment.start,
            children: [
              ComposerPlusItem(
                key: const Key('composer-plus-image'),
                icon: Icons.image_outlined,
                label: '图片',
                enabled: onPickImage != null,
                onTap: () {
                  Navigator.of(context).pop();
                  onPickImage?.call();
                },
              ),
              ComposerPlusItem(
                key: const Key('composer-plus-special'),
                icon: Icons.tag,
                label: '特殊按键',
                enabled: onShowSpecialKeys != null,
                onTap: () {
                  Navigator.of(context).pop();
                  onShowSpecialKeys?.call();
                },
              ),
            ],
          ),
        ],
      ),
    );
  }
}

class ComposerPlusItem extends StatelessWidget {
  const ComposerPlusItem({
    super.key,
    required this.icon,
    required this.label,
    required this.onTap,
    this.enabled = true,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;
  final bool enabled;

  @override
  Widget build(BuildContext context) {
    final accent = AppColors.accent.withValues(alpha: enabled ? 1.0 : 0.4);
    return SizedBox(
      width: 80,
      height: 80,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          onTap: enabled ? onTap : null,
          borderRadius: BorderRadius.circular(8),
          child: Column(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              Icon(icon, color: accent, size: 28),
              const SizedBox(height: 6),
              Text(
                label,
                style: AppTextStyles.bodySmall.copyWith(
                  color: enabled ? null : Colors.grey,
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
