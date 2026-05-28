/// Parses tool-call message text into `(toolName, summary)` for display in
/// collapsible tool-call cards.
///
/// The agentd backend formats tool-use messages as `[ToolName: params]` or
/// `[ToolName]` (no colon when there are no params). The `params` portion is
/// produced by `agentd/internal/agent/stream_parser.go BuildToolInputSummary`,
/// which emits raw values like file_path or full Bash commands.
///
/// This parser converts those raw values into concise, human-friendly
/// summaries suited for a one-line card title:
///
/// * Bash       → first non-empty line of the command (truncated)
/// * Read/Edit/Write/NotebookEdit → basename of the file path
/// * TaskCreate → subject (already concise)
/// * TaskUpdate → status / "#id -> status" (already concise)
/// * Glob       → pattern
/// * Grep       → pattern (strip the "pattern: " prefix added by backend)
/// * WebFetch   → url
/// * WebSearch  → query
/// * Agent / SendMessage / others → backend summary as-is, length-capped
library;

class ToolCallSummary {
  ToolCallSummary({required this.toolName, required this.summary});

  /// The tool name parsed out of `[ToolName: ...]`. Empty if parse failed.
  final String toolName;

  /// A concise summary of the call, intended for display after the tool name.
  /// Empty if no useful summary could be produced.
  final String summary;

  /// Maximum length of the summary string. Longer values are truncated and
  /// suffixed with an ellipsis.
  static const int maxSummaryLength = 80;

  /// Pattern matching tool call message text. Examples:
  ///   `[Bash: ls -la]`
  ///   `[Read: /tmp/foo.dart]`
  ///   `[TaskList]`
  ///
  /// Group 1 captures the tool name. Group 2 (optional) captures everything
  /// between the first `: ` (or `:`) and the trailing `]`.
  static final RegExp _pattern = RegExp(
    r'^\[(\w+)(?::\s*(.*?))?\]\s*$',
    dotAll: true,
  );

  /// Parses `text` into a `ToolCallSummary`. Returns `null` when `text` does
  /// not look like a tool-call message.
  static ToolCallSummary? parse(String text) {
    final match = _pattern.firstMatch(text);
    if (match == null) return null;
    final toolName = match.group(1) ?? '';
    if (toolName.isEmpty) return null;
    final rawParams = match.group(2)?.trim() ?? '';
    final summary = _summarize(toolName, rawParams);
    return ToolCallSummary(toolName: toolName, summary: summary);
  }

  /// Computes the concise summary for `toolName` given the raw `params`
  /// string (the portion after `: ` in `[ToolName: ...]`).
  static String _summarize(String toolName, String params) {
    if (params.isEmpty) return '';

    String result;
    switch (toolName) {
      case 'Bash':
        result = _firstNonEmptyLine(params);
        break;
      case 'Read':
      case 'Edit':
      case 'Write':
      case 'NotebookEdit':
        result = _basename(params);
        break;
      case 'Grep':
        // Backend prefixes with "pattern: ". Strip it for cleaner display.
        if (params.startsWith('pattern: ')) {
          result = params.substring('pattern: '.length);
        } else {
          result = params;
        }
        break;
      case 'TaskCreate':
      case 'TaskUpdate':
      case 'Glob':
      case 'WebFetch':
      case 'WebSearch':
      case 'Agent':
      case 'SendMessage':
      case 'TaskList':
      case 'TodoWrite':
      default:
        // Backend already produced a concise summary; just collapse newlines.
        result = _firstNonEmptyLine(params);
        break;
    }

    return _truncate(result.trim());
  }

  static String _firstNonEmptyLine(String input) {
    for (final line in input.split('\n')) {
      final trimmed = line.trim();
      if (trimmed.isNotEmpty) return trimmed;
    }
    return '';
  }

  /// Returns the basename of a unix-style path. If the path contains no
  /// slashes, returns the original string. Trailing slashes are ignored.
  static String _basename(String path) {
    var p = path.trim();
    while (p.endsWith('/') && p.length > 1) {
      p = p.substring(0, p.length - 1);
    }
    final idx = p.lastIndexOf('/');
    if (idx < 0) return p;
    return p.substring(idx + 1);
  }

  static String _truncate(String s) {
    if (s.length <= maxSummaryLength) return s;
    return '${s.substring(0, maxSummaryLength).trimRight()}…';
  }
}
