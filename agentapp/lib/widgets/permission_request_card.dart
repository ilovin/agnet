import 'dart:convert';
import 'package:flutter/material.dart';

import '../theme/app_text_styles.dart';

/// A card that renders a Claude `permission_request` event.
///
/// Displays tool-specific content (Bash command, Edit file+diff, Write
/// file+content) and exposes [Allow] / [Deny] buttons that route through
/// the existing `conversation.permission_response` RPC via the provided
/// callbacks.
class PermissionRequestCard extends StatelessWidget {
  /// The raw `permissionRequest` map from the event payload.
  /// Expected shape: { tool_name: String, request_id: String, input: Map }
  final Map<String, dynamic> permissionRequest;

  /// Called with behavior == 'allow' or 'deny' when the user taps a button.
  final Future<void> Function(String requestId, String behavior)?
  onPermissionResponse;

  const PermissionRequestCard({
    super.key,
    required this.permissionRequest,
    this.onPermissionResponse,
  });

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    final toolName =
        (permissionRequest['tool_name'] as String? ?? '').toLowerCase();
    final requestId = permissionRequest['request_id'] as String? ?? '';
    final input =
        (permissionRequest['input'] as Map?)?.cast<String, dynamic>() ?? {};

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
      child: Card(
        elevation: 0,
        color: scheme.errorContainer.withOpacity(0.18),
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(12),
          side: BorderSide(color: scheme.error.withOpacity(0.35)),
        ),
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              // ─── Header ───────────────────────────────────────────────
              Row(
                children: [
                  Icon(Icons.lock_outline, size: 18, color: scheme.error),
                  const SizedBox(width: 8),
                  Text(
                    'Tool: ${_displayToolName(toolName)}',
                    style: TextStyle(
                      fontSize: 14,
                      fontWeight: FontWeight.w700,
                      color: scheme.onErrorContainer,
                    ),
                  ),
                ],
              ),
              const SizedBox(height: 12),

              // ─── Tool-specific content ─────────────────────────────────
              _buildContent(context, toolName, input, scheme),

              const SizedBox(height: 16),

              // ─── Action buttons ────────────────────────────────────────
              Row(
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  OutlinedButton(
                    onPressed: onPermissionResponse == null
                        ? null
                        : () => onPermissionResponse!(requestId, 'deny'),
                    style: OutlinedButton.styleFrom(
                      foregroundColor: scheme.error,
                      side: BorderSide(color: scheme.error.withOpacity(0.6)),
                    ),
                    child: const Text('Deny'),
                  ),
                  const SizedBox(width: 12),
                  FilledButton(
                    onPressed: onPermissionResponse == null
                        ? null
                        : () => onPermissionResponse!(requestId, 'allow'),
                    style: FilledButton.styleFrom(
                      backgroundColor: scheme.primary,
                      foregroundColor: scheme.onPrimary,
                    ),
                    child: const Text('Allow'),
                  ),
                ],
              ),
            ],
          ),
        ),
      ),
    );
  }

  // ─── Helpers ────────────────────────────────────────────────────────────

  String _displayToolName(String toolName) {
    // Capitalise first letter for display
    if (toolName.isEmpty) return 'Unknown';
    return toolName[0].toUpperCase() + toolName.substring(1);
  }

  Widget _buildContent(
    BuildContext context,
    String toolName,
    Map<String, dynamic> input,
    ColorScheme scheme,
  ) {
    switch (toolName) {
      case 'bash':
        return _BashContent(input: input, scheme: scheme);
      case 'edit':
        return _EditContent(input: input, scheme: scheme);
      case 'write':
        return _WriteContent(input: input, scheme: scheme);
      default:
        return _FallbackContent(
          toolName: toolName,
          input: input,
          scheme: scheme,
        );
    }
  }
}

// ─── Bash ──────────────────────────────────────────────────────────────────

class _BashContent extends StatelessWidget {
  final Map<String, dynamic> input;
  final ColorScheme scheme;

  const _BashContent({required this.input, required this.scheme});

  @override
  Widget build(BuildContext context) {
    final command = input['command'] as String? ?? '(no command)';
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bgColor = isDark ? const Color(0xFF1A1A2E) : const Color(0xFF1E1E2E);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Command',
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: scheme.onSurface.withOpacity(0.6),
          ),
        ),
        const SizedBox(height: 4),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: bgColor,
            borderRadius: BorderRadius.circular(8),
          ),
          child: SelectableText(
            command,
            style: TextStyle(
              fontFamily: 'monospace',
              fontFamilyFallback: AppTextStyles.fontFamilyFallback,
              fontSize: 13,
              color: const Color(0xFFE5E5E5),
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Edit ──────────────────────────────────────────────────────────────────

class _EditContent extends StatelessWidget {
  final Map<String, dynamic> input;
  final ColorScheme scheme;

  const _EditContent({required this.input, required this.scheme});

  @override
  Widget build(BuildContext context) {
    final filePath = input['file_path'] as String? ?? '(unknown file)';
    final newString = input['new_string'] as String? ?? '';
    final oldString = input['old_string'] as String?;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // File path
        Row(
          children: [
            Icon(Icons.edit_document, size: 14, color: scheme.primary),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                filePath,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: scheme.primary,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),

        // Old content (if present)
        if (oldString != null && oldString.isNotEmpty) ...[
          _DiffLine(
            label: '− old',
            content: oldString,
            labelColor: Colors.red.shade700,
            bgColor: Colors.red.shade50,
            textColor: Colors.red.shade900,
          ),
          const SizedBox(height: 4),
        ],

        // New content
        if (newString.isNotEmpty)
          _DiffLine(
            label: '+ new',
            content: newString,
            labelColor: Colors.green.shade700,
            bgColor: Colors.green.shade50,
            textColor: Colors.green.shade900,
          ),
      ],
    );
  }
}

class _DiffLine extends StatelessWidget {
  final String label;
  final String content;
  final Color labelColor;
  final Color bgColor;
  final Color textColor;

  const _DiffLine({
    required this.label,
    required this.content,
    required this.labelColor,
    required this.bgColor,
    required this.textColor,
  });

  @override
  Widget build(BuildContext context) {
    // Preview: first 200 chars
    final preview =
        content.length > 200 ? '${content.substring(0, 200)}…' : content;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: TextStyle(
            fontSize: 11,
            fontWeight: FontWeight.w700,
            color: labelColor,
          ),
        ),
        const SizedBox(height: 2),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
          decoration: BoxDecoration(
            color: bgColor.withOpacity(0.5),
            borderRadius: BorderRadius.circular(6),
          ),
          child: SelectableText(
            preview,
            style: TextStyle(
              fontFamily: 'monospace',
              fontSize: 12,
              color: textColor,
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Write ─────────────────────────────────────────────────────────────────

class _WriteContent extends StatelessWidget {
  final Map<String, dynamic> input;
  final ColorScheme scheme;

  const _WriteContent({required this.input, required this.scheme});

  @override
  Widget build(BuildContext context) {
    final filePath = input['file_path'] as String? ?? '(unknown file)';
    final content = input['content'] as String? ?? '';

    const maxChars = 300;
    final preview = content.length > maxChars
        ? '${content.substring(0, maxChars)}…'
        : content;

    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bgColor = isDark ? const Color(0xFF1E2828) : const Color(0xFFE8F5E9);
    final textColor = isDark ? const Color(0xFFB2DFDB) : const Color(0xFF1B5E20);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        // File path
        Row(
          children: [
            Icon(Icons.note_add_outlined, size: 14, color: scheme.primary),
            const SizedBox(width: 6),
            Expanded(
              child: Text(
                filePath,
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: scheme.primary,
                ),
                overflow: TextOverflow.ellipsis,
              ),
            ),
          ],
        ),
        const SizedBox(height: 8),

        // Content preview
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: bgColor,
            borderRadius: BorderRadius.circular(8),
          ),
          child: SelectableText(
            preview.isEmpty ? '(empty file)' : preview,
            style: TextStyle(
              fontFamily: 'monospace',
              fontFamilyFallback: AppTextStyles.fontFamilyFallback,
              fontSize: 12,
              color: textColor,
            ),
          ),
        ),
      ],
    );
  }
}

// ─── Fallback ───────────────────────────────────────────────────────────────

class _FallbackContent extends StatelessWidget {
  final String toolName;
  final Map<String, dynamic> input;
  final ColorScheme scheme;

  const _FallbackContent({
    required this.toolName,
    required this.input,
    required this.scheme,
  });

  @override
  Widget build(BuildContext context) {
    String prettyInput;
    try {
      prettyInput = const JsonEncoder.withIndent('  ').convert(input);
    } catch (_) {
      prettyInput = input.toString();
    }

    const maxChars = 400;
    final preview = prettyInput.length > maxChars
        ? '${prettyInput.substring(0, maxChars)}…'
        : prettyInput;

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          'Input',
          style: TextStyle(
            fontSize: 12,
            fontWeight: FontWeight.w600,
            color: scheme.onSurface.withOpacity(0.6),
          ),
        ),
        const SizedBox(height: 4),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
          decoration: BoxDecoration(
            color: scheme.surfaceContainerHighest,
            borderRadius: BorderRadius.circular(8),
          ),
          child: SelectableText(
            preview,
            style: TextStyle(
              fontFamily: 'monospace',
              fontFamilyFallback: AppTextStyles.fontFamilyFallback,
              fontSize: 12,
              color: scheme.onSurface,
            ),
          ),
        ),
      ],
    );
  }
}
