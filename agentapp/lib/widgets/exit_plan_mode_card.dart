import 'package:flutter/material.dart';

import '../models/claude_interaction_models.dart';

/// A card that renders a Claude `exit_plan_mode` event.
///
/// Shows the plan text (monospace, wrappable) and exposes Approve / Reject
/// buttons.  Rejecting opens an optional feedback [TextField].  After a
/// decision is submitted the buttons are disabled and the chosen state is
/// shown.
///
/// The decision is forwarded via [onSend] as a plain-text message so that
/// Claude can resume the conversation naturally.
class ExitPlanModeCard extends StatefulWidget {
  /// The decoded payload from the event.
  final ExitPlanModePayload payload;

  /// Called with the assembled plain-text decision string.
  final void Function(String content)? onSend;

  const ExitPlanModeCard({
    super.key,
    required this.payload,
    this.onSend,
  });

  @override
  State<ExitPlanModeCard> createState() => _ExitPlanModeCardState();
}

class _ExitPlanModeCardState extends State<ExitPlanModeCard> {
  bool _submitted = false;
  bool _approved = false;
  bool _showFeedbackField = false;
  final _feedbackCtrl = TextEditingController();

  @override
  void dispose() {
    _feedbackCtrl.dispose();
    super.dispose();
  }

  void _approve() {
    if (_submitted) return;
    setState(() {
      _submitted = true;
      _approved = true;
    });
    widget.onSend?.call('批准计划');
  }

  void _reject() {
    if (_submitted) return;
    // First tap: show feedback field; second tap (submit): send.
    if (!_showFeedbackField) {
      setState(() => _showFeedbackField = true);
      return;
    }
    final feedback = _feedbackCtrl.text.trim();
    final content = feedback.isEmpty ? '拒绝计划' : '拒绝计划：$feedback';
    setState(() {
      _submitted = true;
      _approved = false;
    });
    widget.onSend?.call(content);
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final planBg = isDark ? const Color(0xFF1A2030) : const Color(0xFFF5F5F5);
    final planText = isDark ? const Color(0xFFCDD5E0) : const Color(0xFF1A1A2E);

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
      child: Card(
        elevation: 0,
        color: scheme.tertiaryContainer.withOpacity(0.18),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: scheme.tertiary.withOpacity(0.35)),
        ),
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // ─── Card header ────────────────────────────────────────────
              Row(
                children: [
                  Icon(Icons.checklist_rtl, size: 18, color: scheme.tertiary),
                  const SizedBox(width: 8),
                  Text(
                    'Claude 已生成计划',
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                      color: scheme.onTertiaryContainer,
                    ),
                  ),
                  const Spacer(),
                  if (_submitted)
                    Chip(
                      label: Text(_approved ? '已批准' : '已拒绝'),
                      backgroundColor: _approved
                          ? scheme.primaryContainer
                          : scheme.errorContainer,
                      labelStyle: TextStyle(
                        fontSize: 11,
                        color: _approved
                            ? scheme.onPrimaryContainer
                            : scheme.onErrorContainer,
                      ),
                      padding: EdgeInsets.zero,
                    ),
                ],
              ),

              const SizedBox(height: 12),

              // ─── Plan body ───────────────────────────────────────────────
              Container(
                width: double.infinity,
                constraints: const BoxConstraints(maxHeight: 320),
                padding: const EdgeInsets.symmetric(
                  horizontal: 12,
                  vertical: 10,
                ),
                decoration: BoxDecoration(
                  color: planBg,
                  borderRadius: BorderRadius.circular(8),
                ),
                child: SingleChildScrollView(
                  child: SelectableText(
                    widget.payload.plan.isEmpty
                        ? '(no plan content)'
                        : widget.payload.plan,
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 13,
                      color: planText,
                      height: 1.5,
                    ),
                  ),
                ),
              ),

              const SizedBox(height: 16),

              // ─── Feedback field (shown after first Reject tap) ───────────
              if (_showFeedbackField && !_submitted) ...[
                TextField(
                  controller: _feedbackCtrl,
                  maxLines: 3,
                  minLines: 1,
                  decoration: InputDecoration(
                    hintText: '请输入拒绝原因（可选）',
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(8),
                    ),
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 8,
                    ),
                  ),
                  style: const TextStyle(fontSize: 13),
                ),
                const SizedBox(height: 12),
              ],

              // ─── Action buttons ──────────────────────────────────────────
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  OutlinedButton(
                    onPressed: _submitted ? null : _reject,
                    style: OutlinedButton.styleFrom(
                      foregroundColor: scheme.error,
                      side: BorderSide(color: scheme.error.withOpacity(0.6)),
                    ),
                    child: Text(
                      _showFeedbackField && !_submitted ? '确认拒绝' : '拒绝',
                    ),
                  ),
                  const SizedBox(width: 12),
                  FilledButton(
                    onPressed: _submitted ? null : _approve,
                    style: FilledButton.styleFrom(
                      backgroundColor: scheme.primary,
                      foregroundColor: scheme.onPrimary,
                    ),
                    child: const Text('批准'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }
}
