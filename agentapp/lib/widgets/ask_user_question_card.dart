import 'package:flutter/material.dart';

import '../models/claude_interaction_models.dart';

/// A card that renders a Claude `ask_user_question` event.
///
/// Displays each question with its header chip and option list.
/// Supports single-select (tap to immediately submit) and multi-select
/// (checkboxes + submit button).  After the user submits, the card is
/// locked and shows a "已提交" badge.
///
/// User responses are sent via [onSend] as a plain-text message so that
/// Claude can resume the conversation naturally.
class AskUserQuestionCard extends StatefulWidget {
  /// The decoded payload from the event.
  final AskUserQuestionPayload payload;

  /// Called with the assembled plain-text answer string.
  final void Function(String content)? onSend;

  const AskUserQuestionCard({
    super.key,
    required this.payload,
    this.onSend,
  });

  @override
  State<AskUserQuestionCard> createState() => _AskUserQuestionCardState();
}

class _AskUserQuestionCardState extends State<AskUserQuestionCard> {
  /// Per-question selected option indices.
  late final List<Set<int>> _selected;
  bool _submitted = false;

  @override
  void initState() {
    super.initState();
    _selected = List.generate(
      widget.payload.questions.length,
      (_) => <int>{},
    );
  }

  void _submitSingle(int qIdx, int optIdx) {
    if (_submitted) return;
    final q = widget.payload.questions[qIdx];
    final label = q.options[optIdx].label;
    final content = '${q.question}: $label';
    setState(() => _submitted = true);
    widget.onSend?.call(content);
  }

  void _submitMulti(int qIdx) {
    if (_submitted) return;
    final q = widget.payload.questions[qIdx];
    final chosen = _selected[qIdx]
        .map((i) => q.options[i].label)
        .join(', ');
    final content = '${q.question}: $chosen';
    setState(() => _submitted = true);
    widget.onSend?.call(content);
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
      child: Card(
        elevation: 0,
        color: scheme.primaryContainer.withOpacity(0.18),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: scheme.primary.withOpacity(0.35)),
        ),
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // ─── Card header ────────────────────────────────────────────
              Row(
                children: [
                  Icon(Icons.help_outline, size: 18, color: scheme.primary),
                  const SizedBox(width: 8),
                  Text(
                    'Claude 需要你的回答',
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                      color: scheme.onPrimaryContainer,
                    ),
                  ),
                  const Spacer(),
                  if (_submitted)
                    Chip(
                      label: const Text('已提交'),
                      backgroundColor: scheme.primaryContainer,
                      labelStyle: TextStyle(
                        fontSize: 11,
                        color: scheme.onPrimaryContainer,
                      ),
                      padding: EdgeInsets.zero,
                    ),
                ],
              ),

              const SizedBox(height: 12),

              // ─── Per-question blocks ────────────────────────────────────
              for (int qi = 0; qi < widget.payload.questions.length; qi++) ...[
                if (qi > 0) const SizedBox(height: 16),
                _QuestionBlock(
                  question: widget.payload.questions[qi],
                  selected: _selected[qi],
                  disabled: _submitted,
                  onSelectSingle: (optIdx) => _submitSingle(qi, optIdx),
                  onToggleMulti: (optIdx) {
                    if (_submitted) return;
                    setState(() {
                      if (_selected[qi].contains(optIdx)) {
                        _selected[qi].remove(optIdx);
                      } else {
                        _selected[qi].add(optIdx);
                      }
                    });
                  },
                  onSubmitMulti: () => _submitMulti(qi),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }
}

// ─── Single question block ────────────────────────────────────────────────────

class _QuestionBlock extends StatelessWidget {
  final AskUserQuestion question;
  final Set<int> selected;
  final bool disabled;
  final void Function(int optIdx) onSelectSingle;
  final void Function(int optIdx) onToggleMulti;
  final VoidCallback onSubmitMulti;

  const _QuestionBlock({
    required this.question,
    required this.selected,
    required this.disabled,
    required this.onSelectSingle,
    required this.onToggleMulti,
    required this.onSubmitMulti,
  });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // Header chip (if non-empty)
        if (question.header.isNotEmpty) ...[
          Chip(
            label: Text(
              question.header,
              style: const TextStyle(fontSize: 11),
            ),
            backgroundColor: scheme.secondaryContainer,
            labelStyle: TextStyle(color: scheme.onSecondaryContainer),
            padding: EdgeInsets.zero,
            visualDensity: VisualDensity.compact,
          ),
          const SizedBox(height: 6),
        ],

        // Question text
        Text(
          question.question,
          style: TextStyle(
            fontSize: 14,
            fontWeight: FontWeight.w600,
            color: scheme.onSurface,
          ),
        ),

        const SizedBox(height: 10),

        // Options
        for (int oi = 0; oi < question.options.length; oi++)
          _OptionTile(
            option: question.options[oi],
            multiSelect: question.multiSelect,
            isSelected: selected.contains(oi),
            disabled: disabled,
            onTap: () => question.multiSelect
                ? onToggleMulti(oi)
                : onSelectSingle(oi),
          ),

        // Submit button (multi-select only)
        if (question.multiSelect && !disabled) ...[
          const SizedBox(height: 10),
          Align(
            alignment: Alignment.centerRight,
            child: FilledButton(
              onPressed: selected.isEmpty ? null : onSubmitMulti,
              child: const Text('提交'),
            ),
          ),
        ],
      ],
    );
  }
}

// ─── Single option tile ───────────────────────────────────────────────────────

class _OptionTile extends StatelessWidget {
  final AskUserQuestionOption option;
  final bool multiSelect;
  final bool isSelected;
  final bool disabled;
  final VoidCallback onTap;

  const _OptionTile({
    required this.option,
    required this.multiSelect,
    required this.isSelected,
    required this.disabled,
    required this.onTap,
  });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: InkWell(
        borderRadius: BorderRadius.circular(8),
        onTap: disabled ? null : onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            borderRadius: BorderRadius.circular(8),
            color: isSelected
                ? scheme.primaryContainer.withOpacity(0.45)
                : scheme.surfaceContainerHighest.withOpacity(0.5),
            border: Border.all(
              color: isSelected
                  ? scheme.primary.withOpacity(0.6)
                  : scheme.outlineVariant.withOpacity(0.4),
            ),
          ),
          child: Row(
            children: [
              if (multiSelect)
                Icon(
                  isSelected
                      ? Icons.check_box
                      : Icons.check_box_outline_blank,
                  size: 18,
                  color: isSelected ? scheme.primary : scheme.onSurfaceVariant,
                )
              else
                Icon(
                  Icons.radio_button_unchecked,
                  size: 18,
                  color: scheme.onSurfaceVariant,
                ),
              const SizedBox(width: 10),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      option.label,
                      style: TextStyle(
                        fontSize: 13,
                        fontWeight: FontWeight.w600,
                        color: scheme.onSurface,
                      ),
                    ),
                    if (option.description.isNotEmpty) ...[
                      const SizedBox(height: 2),
                      Text(
                        option.description,
                        style: TextStyle(
                          fontSize: 12,
                          color: scheme.onSurfaceVariant,
                        ),
                      ),
                    ],
                  ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
