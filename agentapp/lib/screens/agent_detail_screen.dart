import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';
import 'package:flutter/foundation.dart' show kIsWeb;
import 'dart:io';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:markdown/markdown.dart' as md;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:image_picker/image_picker.dart';
import 'package:url_launcher/url_launcher.dart';

import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/unread_provider.dart';
import '../providers/draft_provider.dart';
import '../services/ws_client.dart';
import '../theme/agent_status_theme.dart';
import '../theme/app_colors.dart';
import '../theme/app_spacing.dart';
import '../theme/app_text_styles.dart';
import '../widgets/app_bar/mission_control_app_bar.dart';
import '../widgets/app_bar/bypass_indicator.dart';
import '../widgets/composer/composer_plus_button.dart';
import '../widgets/loaders/oscilloscope_loader.dart';
import '../utils/ansi_span.dart';
import '../utils/highlight.dart';
import '../providers/color_mode_provider.dart';
import '../models/claude_interaction_models.dart';
import '../widgets/ask_user_question_card.dart';
import '../widgets/exit_plan_mode_card.dart';
import '../widgets/permission_request_card.dart';

/// Strips ANSI escape sequences from PTY output and handles terminal control characters.
/// Handles complex sequences including claude-code specific output.
String stripAnsi(String s) {
  // First pass: comprehensive ANSI escape sequence removal
  // Note: Some sequences may have ESC character stripped already, so we also match bare [ patterns
  var result = s
      // Kitty keyboard protocol sequences (ESC[<u, ESC[>1u, etc.)
      .replaceAll(RegExp(r'\x1B?\[[<>][0-9;]*u'), '')
      // CSI sequences with private markers (ESC [ > ...)
      // Matches: [>1u, [>4;2m, ESC[?25h, etc.
      .replaceAll(RegExp(r'\x1B\[[>\?][0-9;]*[a-zA-Z]'), '')
      // Standard CSI sequences (ESC [ ...m, ESC [ ...H, etc.)
      .replaceAll(RegExp(r'\x1B\[[0-9;]*[a-zA-Z]'), '')
      // Bare color codes (in case ESC was stripped): [38;5;21m, [1m, [0m, etc.
      .replaceAll(RegExp(r'(?<!\[)\[(?:[0-9]{1,3};?)+m'), '')
      // OSC sequences (ESC ] ... BEL or ESC \\)
      .replaceAll(RegExp(r'\x1B\][^\x07\x1B]*(?:\x07|\x1B\\)'), '')
      // Set mode/reset mode with question mark
      .replaceAll(RegExp(r'\x1B\[\?[0-9;]*[hlm]'), '')
      // Private sequences (ESC >, ESC =, ESC <)
      .replaceAll(RegExp(r'\x1B[>=<]'), '')
      // Designate G0/G1 character sets
      .replaceAll(RegExp(r'\x1B[\(\)][AB012]'), '')
      // Single shifts
      .replaceAll(RegExp(r'\x1B[NO]'), '')
      // Device control strings
      .replaceAll(RegExp(r'\x1BP[^\x1B]*\x1B\\'), '')
      // Application/Normal keypad mode
      .replaceAll(RegExp(r'\x1B[>=]'), '');

  // Second pass: handle control characters with proper terminal emulation
  final buffer = StringBuffer();
  int cursorPos = 0; // Track cursor position for backspace handling

  for (int i = 0; i < result.length; i++) {
    final char = result[i];
    final code = char.codeUnitAt(0);

    if (code == 0x08) {
      // Backspace: move cursor back but don't delete (we want to see what was typed)
      if (cursorPos > 0) {
        cursorPos--;
      }
    } else if (code == 0x0D) {
      // Carriage return: move to start of line
      // In terminal output, this usually means overwrite previous content
      // For display purposes, we convert to newline unless followed by LF
      if (i + 1 < result.length && result[i + 1] == '\n') {
        // CR LF - Windows line ending, keep LF
        continue;
      }
      // Otherwise treat as newline for display
      buffer.write('\n');
      cursorPos = buffer.length;
    } else if (code == 0x00 || code == 0x07) {
      // NUL and BEL - ignore
      continue;
    } else if (code >= 0x20 || code == 0x0A || code == 0x09) {
      // Printable chars (including Unicode drawing chars), newline, tab - keep
      buffer.write(char);
      cursorPos = buffer.length;
    }
    // Other control chars are ignored
  }

  return buffer.toString();
}

/// Removes terminal drawing characters that clutter the output.
/// Keeps arrows (←→↑↓) and geometric shapes as they carry meaning.
String stripTerminalDrawing(String s) {
  return s
      // Box drawing characters (│─┌┐└┘├┤┬┴┼ etc.)
      .replaceAll(RegExp(r'[\u2500-\u257F]'), '')
      // Block elements (▖▗▘▙▚▛▜▝▞▟█ etc.)
      .replaceAll(RegExp(r'[\u2580-\u259F]'), '')
      // Replace multiple spaces with single space
      .replaceAll(RegExp(r'  +'), ' ')
      // Clean up excessive newlines
      .replaceAll(RegExp(r'\n{3,}'), '\n\n')
      .trim();
}

/// Optimizes raw speech-to-text result before inserting into the text field.
/// [raw] is the final recognized words. [currentText] is the existing controller text.
/// Optimizes voice input text (from system IME speech recognition).
/// Can be called either as an inline insert ([raw] appended to [currentText])
/// or as a full rewrite ([currentText] empty).
String _optimizeVoiceInput(String raw, String currentText) {
  var cleaned = raw.trim();
  if (cleaned.isEmpty) return '';

  // Remove repeated consecutive words (common STT stuttering artifact)
  cleaned = cleaned.replaceAllMapped(
    RegExp(r'\b(\S+)(\s+\1)+\b', caseSensitive: false),
    (m) => m.group(1)!,
  );

  // Remove common Chinese filler words
  cleaned = cleaned
      .replaceAllMapped(
        RegExp(r'^(嗯|啊|呃|那个|就是|然后|所以|比如说)\s*[，,]?\s*', caseSensitive: false),
        (_) => '',
      )
      .replaceAllMapped(
        RegExp(
          r'\s+[，,]?\s*(嗯|啊|呃|那个|就是|然后|所以|比如说)\s*[，,]?\s*',
          caseSensitive: false,
        ),
        (_) => '，',
      );

  // Fix Chinese punctuation spacing: remove spaces around CJK punctuation
  cleaned = cleaned
      .replaceAll(RegExp('\\s+([。，、；：？！""\'（）])'), r'$1')
      .replaceAll(RegExp('([。，、；：？！""\'（）])\\s+'), r'$1')
      // Collapse multiple Chinese commas
      .replaceAll(RegExp(r'[，,]{2,}'), '，');

  // Auto-append sentence-ending punctuation if missing
  if (cleaned.isNotEmpty &&
      !RegExp(r'[。，、；：？!.?!\n]$').hasMatch(cleaned[cleaned.length - 1])) {
    cleaned += '。';
  }

  // Context-aware insertion
  final current = currentText.trim();
  if (current.isEmpty) return cleaned;

  // If current already ends with the same text (or very similar), deduplicate
  if (current.endsWith(cleaned)) {
    return '';
  }
  // If cleaned starts with the tail of current, trim the overlap
  for (int overlap = cleaned.length; overlap > 0; overlap--) {
    if (current.toLowerCase().endsWith(
      cleaned.substring(0, overlap).toLowerCase(),
    )) {
      cleaned = cleaned.substring(overlap).trimLeft();
      break;
    }
  }
  if (cleaned.isEmpty) return '';

  // Decide separator: newline after sentence terminator, otherwise space
  final lastChar = current.isNotEmpty
      ? current.substring(current.length - 1)
      : '';
  final terminators = {'.', '?', '!', '。', '？', '！', '\n'};
  final separator = terminators.contains(lastChar) ? '\n' : ' ';
  return separator + cleaned;
}

/// Chat message model for display
class ChatMessage {
  final String role;
  final String text;
  final int seq;
  final bool isRaw;
  final String kind;
  final bool isPermissionPrompt;
  final int imageCount;
  final List<Map<String, dynamic>> activities;
  final List<String> images;
  final int? timestamp;
  /// Non-null for `kind == 'permission_request'` events.
  /// Shape: { tool_name: String, request_id: String, input: Map }
  final Map<String, dynamic>? permissionRequest;

  /// Non-null for `kind == 'ask_user_question'` events.
  final AskUserQuestionPayload? askUserQuestion;

  /// Non-null for `kind == 'exit_plan_mode'` events.
  final ExitPlanModePayload? exitPlanMode;

  /// true if this is a tool call (e.g. [Bash: git status])
  bool get isToolCall => role == 'assistant' && _toolCallPattern.hasMatch(text);

  /// true if this is explicit thinking/reasoning content.
  ///
  /// Do not infer thinking from natural language keywords (e.g. "根据", "Let me")
  /// because normal assistant replies often contain these phrases.
  bool get isThinking {
    if (role != 'assistant' || isToolCall || isRaw) return false;
    if (_thinkingKinds.contains(kind)) return true;
    return _explicitThinkingPrefix.hasMatch(text);
  }

  /// true if this message is a grouped assistant activity block.
  bool get isActivityBlock =>
      role == 'assistant' &&
      (kind == 'activity_block' || kind == 'activity_list');

  /// Matches tool call patterns: [Bash: cmd], [Agent], [SendMessage], [TaskList], etc.
  static final _toolCallPattern = RegExp(r'^\[[\w]+[:\]]');
  static const _thinkingKinds = {'thinking', 'thinking_delta', 'reasoning'};
  static final _explicitThinkingPrefix = RegExp(
    r'^(thinking:|思考过程[:：]|\[thinking\])',
    caseSensitive: false,
  );

  ChatMessage({
    required this.role,
    required this.text,
    required this.seq,
    this.isRaw = false,
    this.kind = '',
    this.isPermissionPrompt = false,
    this.imageCount = 0,
    this.activities = const [],
    this.images = const [],
    this.timestamp,
    this.permissionRequest,
    this.askUserQuestion,
    this.exitPlanMode,
  });
}

bool _isToolActivityEvent({
  required String role,
  required String kind,
  required String text,
  required bool raw,
}) {
  if (role != 'assistant' || raw) return false;
  if (kind == 'tool_use' || kind == 'tool_result') return true;
  if (text.startsWith('[Using tool:')) return true;
  return ChatMessage._toolCallPattern.hasMatch(text);
}

bool _isThinkingEvent({
  required String role,
  required String kind,
  required String text,
  required bool raw,
}) {
  if (role != 'assistant' || raw) return false;
  if (const {'thinking', 'thinking_delta', 'reasoning'}.contains(kind))
    return true;
  return ChatMessage._explicitThinkingPrefix.hasMatch(text);
}

String _formatMessageTime(int timestampMillis) {
  final dt = DateTime.fromMillisecondsSinceEpoch(timestampMillis);
  final now = DateTime.now();
  if (dt.year == now.year && dt.month == now.month && dt.day == now.day) {
    return '${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
  }
  return '${dt.month}/${dt.day} ${dt.hour.toString().padLeft(2, '0')}:${dt.minute.toString().padLeft(2, '0')}';
}

String buildCollapsedPreview(String text, {int maxChars = 80}) {
  final singleLine = text.replaceAll(RegExp(r'\s+'), ' ').trim();
  if (singleLine.isEmpty) return '';
  if (singleLine.length <= maxChars) return singleLine;
  final truncated = singleLine.substring(0, maxChars).trimRight();
  return '$truncated…';
}

/// Processes raw PTY output text to extract meaningful content.
/// Handles terminal redraw patterns and extracts the final visible state.
/// Also removes terminal drawing characters used by claude-code UI.
String processTerminalOutput(String rawText) {
  // Handle carriage returns within lines (terminal redraw pattern)
  var processed = rawText;
  if (processed.contains('\r')) {
    final lines = processed.split('\n');
    final cleanedLines = <String>[];
    for (var line in lines) {
      if (line.contains('\r')) {
        // CR means terminal overwrote content - keep only the last segment
        final parts = line.split('\r');
        line = parts.last;
      }
      cleanedLines.add(line);
    }
    processed = cleanedLines.join('\n');
  }

  // Strip ANSI escape sequences
  processed = stripAnsi(processed);

  // Remove terminal drawing characters (box drawing, blocks, etc.)
  processed = stripTerminalDrawing(processed);

  // Replace tabs with spaces (Claude tool output often contains \t)
  processed = processed.replaceAll('\t', '  ');

  return processed;
}

/// Normalizes text for permission prompt detection.
/// Removes ANSI sequences, extra whitespace, and joins fragmented words.
String _normalizeForDetection(String s) {
  return s
      .replaceAll(RegExp(r'\x1B\[[0-9;]*[a-zA-Z]'), '')
      .replaceAll(RegExp(r'[⏵❯⏸◉◆\u23F9\u276F\u25B6\u25B8\u25B7]'), ' ')
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim()
      .toLowerCase();
}

bool isPermissionPromptText(String s) {
  final normalized = _normalizeForDetection(s);

  // Match the permission selection UI patterns
  // Fragmented text like "bypasspermissionson", "asspermissionson", etc.
  if (normalized.contains('bypass') && normalized.contains('permission'))
    return true;
  if (normalized.contains('permission') && normalized.contains('shift+tab'))
    return true;
  if (normalized.contains('bypass') && normalized.contains('shift+tab'))
    return true;
  if (normalized.contains('shift+tab') && normalized.contains('cycle'))
    return true;

  // Legacy patterns for complete text
  if (s.contains('⏵⏵') && s.toLowerCase().contains('bypass')) return true;
  if (s.contains('❯') && s.toLowerCase().contains('shift+tab')) return true;
  if (normalized.contains('ctrl+g') && normalized.contains('vim')) return true;

  return false;
}

/// Extracts a human-readable permission prompt message from raw terminal output.
/// Converts fragmented/escaped text into a clean display string.
String _extractPermissionPromptMessage(String rawText) {
  var cleaned = rawText
      // Remove ANSI escape sequences
      .replaceAll(RegExp(r'\x1B\[[0-9;]*[a-zA-Z]'), '')
      .replaceAll(RegExp(r'\x1B\][^\x07]*\x07'), '')
      // Remove terminal drawing characters
      .replaceAll(RegExp(r'[\u2500-\u257F]'), '')
      .replaceAll(RegExp(r'[\u2580-\u259F]'), '')
      // Remove UI indicator symbols
      .replaceAll(RegExp(r'[⏵❯⏸◉◆]'), ' ')
      // Normalize whitespace
      .replaceAll(RegExp(r'\s+'), ' ')
      .trim();

  // Try to reconstruct fragmented permission text
  // "bypasspermissionson" -> "bypass permissions on"
  cleaned = cleaned
      .replaceAllMapped(
        RegExp(r'bypass\s*permissions?\s*on', caseSensitive: false),
        (m) => 'bypass permissions on',
      )
      .replaceAllMapped(
        RegExp(r'shift\+tab\s*to\s*cycle', caseSensitive: false),
        (m) => 'shift+tab to cycle',
      );

  // If still garbled, return a standard message
  if (cleaned.length < 10 ||
      cleaned.replaceAll(RegExp(r'[^a-zA-Z]'), '').length < 5) {
    return 'Claude 需要权限确认';
  }

  return cleaned;
}

/// Extracts permission prompt state from events for overlay display
bool hasPendingPermissionPrompt(List<Map<String, dynamic>> events) {
  for (final e in events.reversed) {
    final kind = e['kind'] as String? ?? '';
    final awaitingPermission = e['awaitingPermission'] as bool? ?? false;

    if (kind == 'permission_prompt' || awaitingPermission) {
      return true;
    }

    // If we see a user message after permission prompt, it's been resolved
    final role = e['role'] as String? ?? '';
    if (role == 'user') {
      return false;
    }
  }
  return false;
}

bool isNoiseOnlyText(String s) {
  final normalized = s.replaceAll(RegExp(r'\s+'), ' ').trim();
  if (normalized.isEmpty) return true;
  if (normalized == 'Terminal') return true;
  if (normalized.length <= 2) return true;
  if (normalized == '⏵⏵') return true;
  // Spinner-only text: single animation frames (✢✳✶✻✽·) possibly with "Sautéing…" etc.
  // These are TUI spinner frames - not real content
  if (RegExp(r'^[✢✳✶✻✽·⏺⠂⠐⠁⠄⠆⠃⠇⠏\s]+$').hasMatch(normalized)) return true;
  // Only spinner word variants
  if (RegExp(r'^(Sautéing|Doing|Working)[\s…]*$').hasMatch(normalized))
    return true;
  // Box drawing only (after stripping ANSI)
  if (RegExp(r'^[─│╭╰╮╯\s]+$').hasMatch(normalized)) return true;
  return false;
}

Map<String, dynamic> normalizeHistoryEvent(Map<dynamic, dynamic> rawEvent) {
  final map = Map<String, dynamic>.from(rawEvent);
  final result = {
    'seq': map['seq'] ?? 0,
    'role': map['role'] ?? 'assistant',
    'text': map['text'] ?? '',
    'raw': map['raw'] ?? false,
    'kind': map['kind'] ?? '',
    'awaitingPermission': map['awaitingPermission'] ?? false,
    'imageCount': map['imageCount'] ?? 0,
    'activities': map['activities'] ?? <Map<String, dynamic>>[],
    'images':
        (map['images'] as List?)?.map((e) => e.toString()).toList() ??
        <String>[],
    if (map.containsKey('permissionRequest'))
      'permissionRequest': map['permissionRequest'],
    // R-010 T1: pass through new interaction payloads without error if absent.
    if (map.containsKey('askUserQuestion'))
      'askUserQuestion': map['askUserQuestion'],
    if (map.containsKey('exitPlanMode')) 'exitPlanMode': map['exitPlanMode'],
  };
  if (map['timestamp'] != null) {
    result['timestamp'] = (map['timestamp'] as num).toInt();
  }
  return result;
}

class ConversationEventCacheEntry {
  final List<Map<String, dynamic>> events;
  final DateTime touchedAt;

  const ConversationEventCacheEntry({
    required this.events,
    required this.touchedAt,
  });

  ConversationEventCacheEntry copyWith({
    List<Map<String, dynamic>>? events,
    DateTime? touchedAt,
  }) {
    return ConversationEventCacheEntry(
      events: events ?? this.events,
      touchedAt: touchedAt ?? this.touchedAt,
    );
  }
}

List<Map<String, dynamic>> mergeConversationEvents(
  List<Map<String, dynamic>> existing,
  Iterable<dynamic> incoming,
) {
  // Events with a real seq (>0) are deduplicated: incoming wins over existing.
  // Events with seq==0 (missing seq field) cannot be deduplicated safely —
  // each one is kept as a distinct entry so content is not silently dropped.
  final bySeq = <int, Map<String, dynamic>>{};
  final seqZeroEvents = <Map<String, dynamic>>[];

  void addEvent(Map<dynamic, dynamic> raw) {
    final normalized = normalizeHistoryEvent(raw);
    final seq = (normalized['seq'] as num?)?.toInt() ?? 0;
    if (seq > 0) {
      bySeq[seq] = normalized;
    } else {
      seqZeroEvents.add(normalized);
    }
  }

  for (final event in existing) {
    addEvent(event);
  }
  for (final event in incoming) {
    addEvent(event as Map);
  }

  final merged = [
    ...seqZeroEvents,
    ...bySeq.values,
  ]..sort(
    (a, b) => ((a['seq'] as num?)?.toInt() ?? 0).compareTo(
      (b['seq'] as num?)?.toInt() ?? 0,
    ),
  );
  return merged;
}

int latestConversationSeq(List<Map<String, dynamic>> events) {
  var lastSeq = 0;
  for (final event in events) {
    final seq = (event['seq'] as num?)?.toInt() ?? 0;
    if (seq > lastSeq) {
      lastSeq = seq;
    }
  }
  return lastSeq;
}

int oldestConversationSeq(List<Map<String, dynamic>> events) {
  if (events.isEmpty) return 0;
  var firstSeq = latestConversationSeq(events);
  for (final event in events) {
    final seq = (event['seq'] as num?)?.toInt() ?? 0;
    if (seq < firstSeq) {
      firstSeq = seq;
    }
  }
  return firstSeq;
}

Map<String, ConversationEventCacheEntry> pruneConversationCache(
  Map<String, ConversationEventCacheEntry> cache, {
  DateTime? now,
  Duration ttl = const Duration(hours: 12),
  int maxEntries = 24,
}) {
  final current = now ?? DateTime.now();
  final freshEntries =
      cache.entries
          .where(
            (entry) =>
                entry.value.events.isNotEmpty &&
                current.difference(entry.value.touchedAt) <= ttl,
          )
          .toList()
        ..sort((a, b) => b.value.touchedAt.compareTo(a.value.touchedAt));

  return Map<String, ConversationEventCacheEntry>.fromEntries(
    freshEntries.take(maxEntries),
  );
}

/// Converts raw events to display messages.
/// Handles both structured stream-json events and legacy PTY raw output.
/// Merges consecutive assistant text_delta fragments into complete messages.
/// Collapses consecutive tool/read activity into a stable block.
/// Builds a structured activity item from后端event data.
Map<String, dynamic> _buildActivityItem(
  String text,
  String kind,
  Map<String, dynamic> rawEvent,
) {
  final explicitToolName = (rawEvent['toolName'] as String?) ?? '';

  if (kind == 'tool_result') {
    return {
      'kind': 'tool_result',
      'toolName': explicitToolName,
      'title': '',
      'content': text,
    };
  }

  if (text.startsWith('[') && text.endsWith(']')) {
    final inner = text.substring(1, text.length - 1);
    final colonIdx = inner.indexOf(': ');
    final prefix = colonIdx >= 0 ? inner.substring(0, colonIdx) : inner;
    final suffix = colonIdx >= 0 ? inner.substring(colonIdx + 2).trim() : '';

    // Legacy "[Using tool: Read]" -> treat "Read" as the actual tool
    if (prefix == 'Using tool') {
      return {
        'kind': 'tool_use',
        'toolName': suffix,
        'title': suffix,
        'content': '',
      };
    }

    return {
      'kind': 'tool_use',
      'toolName': explicitToolName.isNotEmpty ? explicitToolName : prefix,
      'title': suffix,
      'content': '',
    };
  }

  return {
    'kind': 'activity',
    'toolName': explicitToolName,
    'title': text,
    'content': '',
  };
}

List<ChatMessage> convertEventsToMessages(List<Map<String, dynamic>> events) {
  final messages = <ChatMessage>[];

  // Buffer for merging consecutive assistant chunks (both raw and text_delta)
  final mergeBuf = StringBuffer();
  int mergeBufSeq = 0;
  bool mergeBufRaw = false;
  String mergeBufKind = '';
  // Track whether we've accumulated any text_delta fragments for the current
  // assistant turn. If so, skip the final complete 'text' event to avoid
  // duplicating content that was already flushed from the delta buffer.
  bool hadTextDelta = false;

  // Accumulate text_delta content to detect extra text in 'result' events
  // (e.g. btw addendum that isn't streamed as text_delta).
  final deltaTextBuf = StringBuffer();

  // Structured items for assistant activities (tools, reads, etc.)
  final List<Map<String, dynamic>> activityItems = [];
  int activitySeq = 0;

  // Buffer for grouping consecutive thinking events into a single block
  final thinkingBuf = StringBuffer();
  int thinkingSeq = 0;

  int? _currentTimestamp;

  void flushMergeBuf() {
    if (mergeBuf.isEmpty) return;
    final merged = mergeBuf.toString().trim();
    mergeBuf.clear();
    if (!isNoiseOnlyText(merged)) {
      messages.add(
        ChatMessage(
          role: 'assistant',
          text: merged,
          seq: mergeBufSeq,
          isRaw: mergeBufRaw,
          kind: mergeBufKind,
          timestamp: _currentTimestamp,
        ),
      );
    }
    mergeBufKind = '';
  }

  void flushActivityBuf() {
    if (activityItems.isEmpty) return;
    final items = List<Map<String, dynamic>>.from(activityItems);
    activityItems.clear();
    if (items.isNotEmpty) {
      messages.add(
        ChatMessage(
          role: 'assistant',
          text: '',
          seq: activitySeq,
          kind: 'activity_list',
          activities: items,
          timestamp: _currentTimestamp,
        ),
      );
    }
  }

  void flushThinkingBuf() {
    if (thinkingBuf.isEmpty) return;
    final thinkingText = thinkingBuf.toString().trim();
    thinkingBuf.clear();
    if (thinkingText.isNotEmpty) {
      messages.add(
        ChatMessage(
          role: 'assistant',
          text: thinkingText,
          seq: thinkingSeq,
          kind: 'thinking',
          timestamp: _currentTimestamp,
        ),
      );
    }
  }

  for (final e in events) {
    final seq = (e['seq'] as num?)?.toInt() ?? 0;
    final raw = e['raw'] as bool? ?? false;
    final role = e['role'] as String? ?? 'assistant';
    final rawText = e['text'] as String? ?? '';
    final kind = e['kind'] as String? ?? '';
    _currentTimestamp = e['timestamp'] as int?;

    // Handle permission prompts - don't add to message list, handled by overlay
    if (kind == 'permission_prompt') {
      continue;
    }

    // Handle permission_request events: carry the payload in ChatMessage.
    if (kind == 'permission_request') {
      flushMergeBuf();
      flushActivityBuf();
      flushThinkingBuf();
      final payload = e['permissionRequest'];
      messages.add(
        ChatMessage(
          role: role,
          text: e['text'] as String? ?? '',
          seq: seq,
          isRaw: false,
          kind: 'permission_request',
          timestamp: _currentTimestamp,
          permissionRequest: payload is Map
              ? Map<String, dynamic>.from(payload)
              : null,
        ),
      );
      continue;
    }

    // Handle ask_user_question events.
    if (kind == 'ask_user_question') {
      flushMergeBuf();
      flushActivityBuf();
      flushThinkingBuf();
      final raw2 = e['askUserQuestion'];
      AskUserQuestionPayload? askPayload;
      if (raw2 is Map) {
        try {
          askPayload = AskUserQuestionPayload.fromJson(
              Map<String, dynamic>.from(raw2));
        } catch (_) {}
      }
      messages.add(
        ChatMessage(
          role: role,
          text: e['text'] as String? ?? '',
          seq: seq,
          isRaw: false,
          kind: 'ask_user_question',
          timestamp: _currentTimestamp,
          askUserQuestion: askPayload,
        ),
      );
      continue;
    }

    // Handle exit_plan_mode events.
    if (kind == 'exit_plan_mode') {
      flushMergeBuf();
      flushActivityBuf();
      flushThinkingBuf();
      final raw2 = e['exitPlanMode'];
      ExitPlanModePayload? planPayload;
      if (raw2 is Map) {
        try {
          planPayload = ExitPlanModePayload.fromJson(
              Map<String, dynamic>.from(raw2));
        } catch (_) {}
      }
      messages.add(
        ChatMessage(
          role: role,
          text: e['text'] as String? ?? '',
          seq: seq,
          isRaw: false,
          kind: 'exit_plan_mode',
          timestamp: _currentTimestamp,
          exitPlanMode: planPayload,
        ),
      );
      continue;
    }

    final cleaned = processTerminalOutput(rawText).trim();

    // Handle legacy PTY-based permission prompt detection
    final permissionPrompt = isPermissionPromptText(rawText);
    if (permissionPrompt) {
      continue; // Don't add to message list, handled by overlay
    }

    // User messages: flush any pending buffer first, reset delta tracking
    if (role == 'user') {
      flushMergeBuf();
      flushActivityBuf();
      flushThinkingBuf();
      hadTextDelta = false;
      deltaTextBuf.clear();
      // User messages should always be shown regardless of length.
      // isNoiseOnlyText is for filtering TUI noise from assistant output.
      // Empty strings are still skipped.
      if (cleaned.isNotEmpty) {
        messages.add(
          ChatMessage(
            role: role,
            text: cleaned,
            seq: seq,
            isRaw: raw,
            kind: kind,
            imageCount: (e['imageCount'] as num?)?.toInt() ?? 0,
            images:
                (e['images'] as List?)?.map((i) => i.toString()).toList() ??
                <String>[],
            timestamp: e['timestamp'] as int?,
          ),
        );
      }
      continue;
    }

    // Group consecutive tool/read/use activity events into one structured block.
    if (_isToolActivityEvent(role: role, kind: kind, text: cleaned, raw: raw)) {
      flushMergeBuf();
      flushThinkingBuf();
      if (!isNoiseOnlyText(cleaned) && cleaned.isNotEmpty) {
        if (activityItems.isEmpty) {
          activitySeq = seq;
        }
        activityItems.add(_buildActivityItem(cleaned, kind, e));
      }
      continue;
    }

    // Group consecutive thinking events into one block.
    if (_isThinkingEvent(role: role, kind: kind, text: cleaned, raw: raw)) {
      flushMergeBuf();
      flushActivityBuf();
      if (!isNoiseOnlyText(cleaned) && cleaned.isNotEmpty) {
        if (thinkingBuf.isEmpty) {
          thinkingSeq = seq;
          thinkingBuf.write(cleaned);
        } else {
          thinkingBuf.write('\n');
          thinkingBuf.write(cleaned);
        }
      }
      continue;
    }

    // Assistant text_delta should not be mixed with activity block or thinking block.
    // Only flush if the delta has real content — empty/newline deltas are common
    // in stream-json and should not break consecutive tool activity grouping.
    if (kind == 'text_delta') {
      if (cleaned.isNotEmpty && !isNoiseOnlyText(cleaned)) {
        flushActivityBuf();
        flushThinkingBuf();
        hadTextDelta = true;
      }
      deltaTextBuf.write(cleaned);
    }

    // Determine if this is a fragment that should be merged
    // text_delta and raw events are fragments; other non-raw events are complete messages
    final isFragment = raw || kind == 'text_delta';

    if (isFragment) {
      // For raw PTY fragments, apply full noise filter (includes length <= 2 check)
      if (raw && isNoiseOnlyText(cleaned)) continue;
      if (cleaned.isNotEmpty) {
        if (mergeBuf.isEmpty) {
          mergeBufSeq = seq;
          mergeBufRaw = raw;
          mergeBufKind = kind;
        }
        mergeBuf.write(cleaned);
        // Do NOT flush mid-stream on sentence boundaries. A single Claude
        // assistant turn often contains many `。` / `.` characters and
        // streaming flush would shatter one message into many fragmented
        // bubbles (e.g. "行级别的修复" stranded between two `。`). Flush only
        // when a non-fragment event arrives or at end of stream — see the
        // boundary handling below and at the end of this loop.
      }
      continue;
    }

    // Non-fragment assistant message (complete message from stream-json).
    // If we already received text_delta fragments for this turn, the content
    // was already flushed via mergeBuf — skip to avoid duplicates.
    // Covered kinds:
    //   ''/'text'  — legacy authoritative event (same full text as deltas)
    //   'result'   — stream-json result; may contain extra "btw" content
    //   'assistant' — backend emits this after streaming deltas with the full
    //                 text; without this guard the deltas would be flushed as
    //                 bubble #1 and the 'assistant' event added as bubble #2,
    //                 fragmenting one Claude turn into two separate bubbles.
    if (role == 'assistant' &&
        !raw &&
        hadTextDelta &&
        (kind == 'text' || kind == '' || kind == 'result' || kind == 'assistant')) {
      flushMergeBuf();
      if (kind == 'result') {
        // Check if result contains extra content beyond text_deltas (e.g. btw)
        final deltaText = deltaTextBuf.toString().trim();
        final resultText = cleaned.trim();
        if (resultText.length > deltaText.length + 20) {
          // Result has significant extra content — extract it
          // Find where delta text ends in the result and take the remainder
          String extra = '';
          if (deltaText.isNotEmpty && resultText.contains(deltaText)) {
            extra = resultText
                .substring(resultText.indexOf(deltaText) + deltaText.length)
                .trim();
          } else if (deltaText.isNotEmpty) {
            // Fuzzy match: check if result starts with similar content
            final deltaLen = deltaText.length;
            if (resultText.length > deltaLen) {
              extra = resultText.substring(deltaLen).trim();
              // Skip leading separators (newlines, dashes, whitespace)
              extra = extra.replaceFirst(RegExp(r'^[\s\-─═]+\s*'), '');
            }
          }
          if (extra.isNotEmpty && !isNoiseOnlyText(extra)) {
            messages.add(
              ChatMessage(
                role: 'assistant',
                text: extra,
                seq: seq,
                isRaw: false,
                kind: 'btw',
                timestamp: e['timestamp'] as int?,
              ),
            );
          }
        }
      }
      hadTextDelta = false;
      deltaTextBuf.clear();
      continue;
    }
    flushMergeBuf();
    flushActivityBuf();
    flushThinkingBuf();
    hadTextDelta = false;
    deltaTextBuf.clear();
    if (role != 'user' && isNoiseOnlyText(cleaned)) continue;
    messages.add(
      ChatMessage(
        role: role,
        text: cleaned,
        seq: seq,
        isRaw: false,
        kind: kind,
        timestamp: e['timestamp'] as int?,
      ),
    );
    // Mark that we've seen a complete assistant message so the
    // subsequent result event (same content) can be skipped.
    if (role == 'assistant' && !raw && kind == 'assistant') {
      hadTextDelta = true;
    }
  }

  // Flush any remaining buffers
  flushMergeBuf();
  flushActivityBuf();
  flushThinkingBuf();

  return messages;
}

/// Collapses consecutive assistant activity blocks into a single block.
/// This ensures multiple adjacent activity_list messages are rendered in
/// one foldable container, showing only the latest activity when collapsed.
List<ChatMessage> collapseConsecutiveActivityBlocks(
  List<ChatMessage> messages,
) {
  final result = <ChatMessage>[];
  for (final msg in messages) {
    if (msg.isActivityBlock &&
        result.isNotEmpty &&
        result.last.isActivityBlock) {
      final last = result.last;
      final merged = List<Map<String, dynamic>>.from(last.activities)
        ..addAll(msg.activities);
      result[result.length - 1] = ChatMessage(
        role: last.role,
        text: last.text,
        seq: last.seq,
        kind: 'activity_list',
        activities: merged,
        isRaw: last.isRaw,
      );
    } else {
      result.add(msg);
    }
  }
  return result;
}

List<Map<String, String>> normalizeOpencodeModels(dynamic rawModels) {
  final models = ((rawModels as List?) ?? const [])
      .whereType<Map>()
      .map(
        (m) => m.map(
          (key, value) => MapEntry(key.toString(), (value ?? '').toString()),
        ),
      )
      .toList();

  models.sort((a, b) {
    final providerCompare = (a['provider'] ?? '').compareTo(
      b['provider'] ?? '',
    );
    if (providerCompare != 0) return providerCompare;

    final nameCompare = (a['name'] ?? a['id'] ?? '').compareTo(
      b['name'] ?? b['id'] ?? '',
    );
    if (nameCompare != 0) return nameCompare;

    return (a['id'] ?? '').compareTo(b['id'] ?? '');
  });

  return models;
}

bool opencodeModelMatches(String candidateId, String currentModelId) {
  if (candidateId == currentModelId) return true;
  if (candidateId.isEmpty || currentModelId.isEmpty) return false;
  if (!currentModelId.contains('/')) {
    return candidateId.endsWith('/$currentModelId');
  }
  return false;
}

String currentOpencodeModelId(Map<String, dynamic> data) {
  final current = (data['_opencodeCurrent'] ?? data['current'] ?? '')
      .toString()
      .trim();
  if (current.isEmpty) return '';

  final models = normalizeOpencodeModels(data['_opencodeModels']);
  for (final model in models) {
    final id = model['id'] ?? '';
    if (opencodeModelMatches(id, current)) {
      return id;
    }
  }

  return current;
}

String currentOpencodeModelLabel(Map<String, dynamic> data) {
  final current = currentOpencodeModelId(data);
  if (current.isEmpty) return '模型';

  final models = normalizeOpencodeModels(data['_opencodeModels']);
  for (final model in models) {
    final id = model['id'] ?? '';
    if (opencodeModelMatches(id, current)) {
      final name = (model['name'] ?? '').trim();
      return name.isEmpty ? id : name;
    }
  }

  return current;
}

class AgentDetailScreen extends ConsumerStatefulWidget {
  final String nodeId;
  final String agentId;

  const AgentDetailScreen({
    super.key,
    required this.nodeId,
    required this.agentId,
  });

  @override
  ConsumerState<AgentDetailScreen> createState() => _AgentDetailScreenState();
}

class _AgentDetailScreenState extends ConsumerState<AgentDetailScreen> {
  static const _conversationCacheTtl = Duration(hours: 12);
  static const _conversationCacheMaxEntries = 24;

  final _inputCtrl = TextEditingController();
  final _scrollCtrl = ScrollController();
  bool _loading = false;
  bool _initialLoading = true;
  bool _loadingOlder = false;
  bool _hasMoreHistory = true;
  bool _rawMode = false;
  bool _stopping = false;
  bool _stickToBottom = true;
  bool _showJumpToLatest = false;
  bool _isLoadingFreshData = false;
  bool _showTimestamps = false;
  String? _lastError;
  String? _agentName; // local override for renamed agent

  // Static cache for messages
  static final Map<String, ConversationEventCacheEntry> _messageCache = {};

  // Raw events from EventBuffer
  List<Map<String, dynamic>> _rawEvents = [];
  int _lastSeq = 0;
  int _oldestSeq = 0;
  Timer? _pollTimer;
  bool _pollingNewEvents = false;

  // Cached message processing results to avoid O(n) recomputation on every build.
  // These are rebuilt whenever _rawEvents changes.
  List<ChatMessage> _cachedReversed = [];
  int _cachedLastAssistantIndex = -1;

  // Permission handling mode (mobile-friendly option)
  // true = auto-resolve (default for mobile), false = manual confirmation
  bool _autoResolvePermissions = true;

  // Pending mode shown optimistically until fresh backend state arrives.
  String? _pendingMode;

  // Dynamic skills loaded from remote agentd
  List<SlashCommand> _dynamicSkills = [];

  // Connection state for banner display
  bool _wsConnected = true;

  // Push event listener handle for cleanup
  EventCallback? _eventHandler;

  // User interaction cooldown — prevents auto-scroll while user is reading
  DateTime? _lastUserScroll;
  DateTime? _lastUserTap; // track taps on expandable widgets

  @override
  void initState() {
    super.initState();
    _scrollCtrl.addListener(_handleScroll);

    // Restore draft text if any (use post-frame to ensure TextField is ready)
    final draft = ref.read(draftProvider.notifier).getDraft(widget.nodeId, widget.agentId);
    if (draft.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_inputCtrl.text != draft) {
          _inputCtrl.text = draft;
          _inputCtrl.selection = TextSelection.collapsed(offset: draft.length);
        }
      });
    }

    // Persist draft on every keystroke (web: dispose may not fire on browser back)
    _inputCtrl.addListener(_persistDraft);

    ref.read(unreadProvider.notifier).markAsRead(widget.nodeId, widget.agentId);

    _pruneMessageCache();

    // Load from cache if available
    final cacheKey = _cacheKey;
    final cachedEntry = _messageCache[cacheKey];
    if (cachedEntry != null && cachedEntry.events.isNotEmpty) {
      final cachedEvents = List<Map<String, dynamic>>.from(cachedEntry.events);
      setState(() {
        _rawEvents = cachedEvents;
        _lastSeq = latestConversationSeq(cachedEvents);
        _oldestSeq = oldestConversationSeq(cachedEvents);
        _hasMoreHistory = _oldestSeq > 1;
        _initialLoading = false;
        _rebuildMessageCache();
      });
      _touchMessageCache(cachedEvents: cachedEvents);
    }

    // Load fresh data in background
    _loadHistory();
    _loadSkills();

    // Poll every 1s for new events
    _pollTimer = Timer.periodic(
      const Duration(seconds: 1),
      (_) => _pollNewEvents(),
    );
    // Listen for connection state changes
    final notifier = ref.read(connectionProvider.notifier);
    notifier.onStateChanged.listen((state) {
      if (!mounted) return;
      setState(() {
        _wsConnected = state == WsConnectionState.connected;
      });
      // Reconnected — reload history silently
      if (state == WsConnectionState.connected && !_initialLoading) {
        _pollNewEvents();
      }
    });

    // Register push event listener for conversation.cleared
    final client = ref.read(connectionProvider);
    _eventHandler = _onPushEvent;
    client?.onEvent(_eventHandler!);
  }

  @override
  void didUpdateWidget(covariant AgentDetailScreen oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.nodeId == widget.nodeId && oldWidget.agentId == widget.agentId) {
      return;
    }
    _rawEvents = [];
    _lastSeq = 0;
    _oldestSeq = 0;
    _hasMoreHistory = true;
    _initialLoading = true;
    _lastError = null;
    _pollingNewEvents = false;
    _agentName = null;
    _rebuildMessageCache();
    final cachedEntry = _messageCache[_cacheKey];
    if (cachedEntry != null && cachedEntry.events.isNotEmpty) {
      final cachedEvents = List<Map<String, dynamic>>.from(cachedEntry.events);
      _rawEvents = cachedEvents;
      _lastSeq = latestConversationSeq(cachedEvents);
      _oldestSeq = oldestConversationSeq(cachedEvents);
      _hasMoreHistory = _oldestSeq > 1;
      _initialLoading = false;
      _rebuildMessageCache();
    }
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) {
        _loadHistory();
        _loadSkills();
      }
    });
  }

  void _persistDraft() {
    final draft = _inputCtrl.text;
    if (draft.isNotEmpty) {
      ref.read(draftProvider.notifier).setDraft(widget.nodeId, widget.agentId, draft);
    } else {
      ref.read(draftProvider.notifier).clearDraft(widget.nodeId, widget.agentId);
    }
  }

  @override
  void dispose() {
    _inputCtrl.removeListener(_persistDraft);

    _pollTimer?.cancel();
    _scrollCtrl.removeListener(_handleScroll);
    final client = ref.read(connectionProvider);
    if (_eventHandler != null) {
      client?.offEvent(_eventHandler!);
    }
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  String get _cacheKey => '${widget.nodeId}:${widget.agentId}';

  /// Looks up the current resume sessionId for this agent from nodesProvider.
  /// Empty string when the agent isn't yet in the node store.
  ///
  /// Used as the third dimension of the conversationProvider cache key
  /// (`(nodeId, agentId, sessionId)`), so messages from a stale session
  /// never bleed into the live transcript after a /clear-style switch.
  String _currentSessionId() {
    final nodeState = ref.read(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;
    return agent?.sessionId ?? '';
  }

  /// Rebuilds cached message processing results from _rawEvents.
  /// Must be called after any mutation to _rawEvents (inside setState is fine).
  void _rebuildMessageCache() {
    final messages = collapseConsecutiveActivityBlocks(
      convertEventsToMessages(_rawEvents),
    );
    _cachedReversed = messages.reversed.toList();
    int lastAssistantIndex = -1;
    for (int i = 0; i < _cachedReversed.length; i++) {
      if (_cachedReversed[i].role == 'assistant' &&
          !_cachedReversed[i].isToolCall &&
          !_cachedReversed[i].isActivityBlock) {
        lastAssistantIndex = i;
        break;
      }
    }
    _cachedLastAssistantIndex = lastAssistantIndex;
  }

  void _onPushEvent(WsMessage event) {
    if (event.method != 'conversation.cleared') return;
    final params = event.params as Map<String, dynamic>?;
    if (params == null) return;
    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String? ?? '';
    final sessionId = params['sessionId'] as String? ?? '';
    if (nodeId != widget.nodeId || agentId != widget.agentId) return;
    setState(() {
      _rawEvents = [];
      _lastSeq = 0;
      _oldestSeq = 0;
      _hasMoreHistory = true;
      _loading = false;
      _rebuildMessageCache();
    });
    _touchMessageCache(cachedEvents: []);
    ref.read(conversationProvider.notifier).clear(nodeId, agentId, sessionId);
    ref.read(unreadProvider.notifier).markAsRead(nodeId, agentId);
  }

  void _touchMessageCache({List<Map<String, dynamic>>? cachedEvents}) {
    _messageCache[_cacheKey] = ConversationEventCacheEntry(
      events: List<Map<String, dynamic>>.from(cachedEvents ?? _rawEvents),
      touchedAt: DateTime.now(),
    );
    _pruneMessageCache();
  }

  void _pruneMessageCache() {
    final pruned = pruneConversationCache(
      _messageCache,
      ttl: _conversationCacheTtl,
      maxEntries: _conversationCacheMaxEntries,
    );
    _messageCache
      ..clear()
      ..addAll(pruned);
  }

  void _handleScroll() {
    if (!_scrollCtrl.hasClients) return;
    _lastUserScroll = DateTime.now();
    // Reversed ListView: offset 0 = bottom (newest), maxScrollExtent = top (oldest)
    final shouldStick = _scrollCtrl.offset < 120;
    final showJump = !shouldStick;
    if (shouldStick != _stickToBottom || showJump != _showJumpToLatest) {
      setState(() {
        _stickToBottom = shouldStick;
        _showJumpToLatest = showJump;
      });
    }
    // Load older when near the top (near maxScrollExtent in reversed list)
    if (_scrollCtrl.position.maxScrollExtent - _scrollCtrl.offset < 200 && !_loadingOlder && _hasMoreHistory) {
      _loadOlderHistory();
    }
  }

  static const _pageSize = 30;

  Future<void> _loadHistory() async {
    if (_isLoadingFreshData) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;

    setState(() {
      _isLoadingFreshData = true;
    });

    // Capture the resume sessionId at request time. If the agent's session
    // rotates (e.g. Hermes /clear) before the RPC returns, we drop the stale
    // response so it can't overwrite the live transcript with old-session
    // messages. Mid-fetch race protection per #57+TaskC hotfix.
    final expectedSessionId = _currentSessionId();

    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'limit': _pageSize,
      }, timeout: const Duration(seconds: 5));
      final raw = result is Map ? result : <String, dynamic>{};
      // Drop stale responses where the agent's session has rotated underneath us.
      final responseSessionId = (raw['sessionId'] as String?) ?? '';
      final liveSessionId = _currentSessionId();
      if (expectedSessionId.isNotEmpty &&
          responseSessionId.isNotEmpty &&
          responseSessionId != expectedSessionId &&
          responseSessionId != liveSessionId) {
        debugPrint(
            'loadHistory: dropping stale response (response=$responseSessionId expected=$expectedSessionId live=$liveSessionId)');
        if (mounted) {
          setState(() {
            _isLoadingFreshData = false;
          });
        }
        return;
      }
      final events = (raw['events'] as List?) ?? [];
      final lastSeq = (raw['lastSeq'] as num?)?.toInt() ?? 0;
      final firstSeqFromResp = (raw['firstSeq'] as num?)?.toInt() ?? 0;
      final normalizedEvents = events
          .map((e) => normalizeHistoryEvent(e as Map))
          .toList();
      final mergedEvents = lastSeq < _lastSeq && lastSeq >= 0
          ? normalizedEvents
          : mergeConversationEvents(_rawEvents, normalizedEvents);

      if (mounted) {
        setState(() {
          _rawEvents = mergedEvents;
          _lastSeq = lastSeq;
          if (_rawEvents.isNotEmpty) {
            _oldestSeq = firstSeqFromResp > 0
                ? firstSeqFromResp
                : oldestConversationSeq(_rawEvents);
            _hasMoreHistory = _oldestSeq > 1 && events.length >= _pageSize;
          } else {
            _oldestSeq = 0;
            _hasMoreHistory = false;
          }
          _initialLoading = false;
          _lastError = null;
          _isLoadingFreshData = false;
          _rebuildMessageCache();
        });

        _touchMessageCache();

        // Sync last few messages to conversationProvider so dashboard preview stays in sync
        final previewEvents = mergedEvents.length > 12
            ? mergedEvents.sublist(mergedEvents.length - 12)
            : mergedEvents;
        final previewMessages = collapseConsecutiveActivityBlocks(
          convertEventsToMessages(previewEvents),
        ).where((m) => !m.isThinking && !m.isActivityBlock && !m.isToolCall).toList();
        if (previewMessages.isNotEmpty) {
          // Use the live session id (or the expected one if the live lookup
          // hasn't refreshed yet) so the preview lands in the matching bucket.
          final previewSessionId =
              _currentSessionId().isNotEmpty ? _currentSessionId() : expectedSessionId;
          ref.read(conversationProvider.notifier).mergeHistory(
            widget.nodeId,
            widget.agentId,
            previewSessionId,
            previewMessages.map((m) {
              final role = m.role == 'user' ? 'user' : 'assistant';
              return {
                'nodeId': widget.nodeId,
                'agentId': widget.agentId,
                'sessionId': previewSessionId,
                'role': role,
                'text': m.text,
                'seq': m.seq,
              };
            }).toList(),
          );
        }

        if (_stickToBottom) {
          _scrollToBottom(force: true, animate: false);
        }
      }
    } catch (e) {
      debugPrint('loadHistory error: $e');
      if (mounted) {
        setState(() {
          _initialLoading = false;
          _isLoadingFreshData = false;
          if (_rawEvents.isEmpty) {
            _lastError = '加载失败，点击重试';
          }
        });
      }
    }
  }

  Future<void> _loadSkills() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      final result = await client.call('system.skills', {
        'nodeId': widget.nodeId,
      });
      final raw = result is Map ? result : <String, dynamic>{};
      final skills = (raw['skills'] as List?) ?? [];
      if (mounted && skills.isNotEmpty) {
        setState(() {
          _dynamicSkills = skills
              .whereType<Map>()
              .map(
                (s) => SlashCommand(
                  (s['command'] as String?) ?? '',
                  (s['description'] as String?) ?? '',
                ),
              )
              .where((s) => s.command.isNotEmpty)
              .toList();
        });
      }
    } catch (_) {
      // Skills are optional — silently ignore errors
    }
  }

  Future<void> _loadOlderHistory() async {
    if (_loadingOlder || !_hasMoreHistory || _oldestSeq <= 1) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;

    setState(() {
      _loadingOlder = true;
    });

    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'before': _oldestSeq,
        'limit': _pageSize,
      });
      final raw = result is Map ? result : <String, dynamic>{};
      final events = (raw['events'] as List?) ?? [];
      if (!mounted) return;

      if (events.isEmpty) {
        setState(() {
          _hasMoreHistory = false;
          _loadingOlder = false;
        });
        return;
      }

      final older = events.map((e) => normalizeHistoryEvent(e as Map)).toList();

      setState(() {
        _rawEvents = [...older, ..._rawEvents];
        _oldestSeq = ((_rawEvents.first['seq'] as num?)?.toInt() ?? _oldestSeq);
        _hasMoreHistory = _oldestSeq > 1 && older.length >= _pageSize;
        _loadingOlder = false;
        _rebuildMessageCache();
      });
      _touchMessageCache();
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingOlder = false;
      });
    }
  }

  Future<void> _pollNewEvents() async {
    if (_initialLoading || _pollingNewEvents) return;
    _pollingNewEvents = true;
    final client = ref.read(connectionProvider);
    if (client == null) {
      _pollingNewEvents = false;
      return;
    }
    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'cursor': _lastSeq,
      });
      final raw = result is Map ? result : <String, dynamic>{};
      final events = (raw['events'] as List?) ?? [];
      final lastSeq = (raw['lastSeq'] as num?)?.toInt() ?? 0;
      final permissionRequests = (raw['permissionRequests'] as List?) ?? [];
      if (!mounted) return;

      // Detect sequence regression (e.g. after /clear resets server history)
      if (lastSeq < _lastSeq && lastSeq >= 0) {
        debugPrint(
          'seq regression detected: server=$lastSeq local=$_lastSeq, reloading history',
        );
        _pollingNewEvents = false;
        setState(() {
          _rawEvents.clear();
          _lastSeq = 0;
          _oldestSeq = 0;
          _hasMoreHistory = true;
          _initialLoading = true;
          _rebuildMessageCache();
        });
        _loadHistory();
        return;
      }

      if (events.isNotEmpty) {
        final newEvents = mergeConversationEvents(_rawEvents, events);
        final hadNewEvents = newEvents.length > _rawEvents.length;
        if (hadNewEvents) {
          setState(() {
            _rawEvents = newEvents;
            _lastSeq = lastSeq;
            if (_oldestSeq == 0 && _rawEvents.isNotEmpty) {
              _oldestSeq = oldestConversationSeq(_rawEvents);
              _hasMoreHistory = _oldestSeq > 1;
            }
            _lastError = null;
            _rebuildMessageCache();
          });
          _touchMessageCache();
          _scrollToBottom();
        } else {
          // All events were duplicates, just update seq
          if (lastSeq > _lastSeq) {
            setState(() {
              _lastSeq = lastSeq;
            });
          }
        }
      } else if (lastSeq > _lastSeq) {
        setState(() {
          _lastSeq = lastSeq;
          _lastError = null;
        });
      }

      // Handle permission requests
      if (permissionRequests.isNotEmpty) {
        if (_autoResolvePermissions) {
          // Auto-resolve all pending permission requests
          for (final perm in permissionRequests) {
            final requestId = perm['request_id'] as String?;
            if (requestId != null) {
              await _sendPermissionResponse(requestId, 'allow');
            }
          }
        } else {
          // Manual mode: show permission requests in UI
          setState(() {
            for (final perm in permissionRequests) {
              _rawEvents.add({
                'seq': _lastSeq + 1,
                'role': 'system',
                'text': '权限请求: ${perm['tool_name'] ?? 'Unknown'}',
                'raw': false,
                'kind': 'permission_request',
                'permissionRequest': perm,
              });
            }
            _rebuildMessageCache();
          });
          _touchMessageCache();
        }
      }
    } catch (e) {
      // Silently ignore polling errors; the connection indicator in the AppBar
      // is enough to signal transient disconnects.
      if (!mounted) return;
    } finally {
      _pollingNewEvents = false;
    }
  }

  Future<void> _sendPermissionResponse(
    String requestId,
    String behavior,
  ) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('conversation.permission_response', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'requestId': requestId,
        'behavior': behavior,
      });
    } catch (e) {
      debugPrint('sendPermissionResponse error: $e');
    }
  }

  Future<void> _pollBurstAfterSend() async {
    for (var i = 0; i < 6; i++) {
      await Future.delayed(const Duration(milliseconds: 250));
      await _pollNewEvents();
      if (!_loading || !mounted) {
        return;
      }
    }
  }

  List<Map<String, String>> _pendingImages = [];

  Future<void> _sendMessage() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty && _pendingImages.isEmpty) return;

    // Intercept /clear command and send RPC instead of raw input
    if (text == '/clear') {
      final client = ref.read(connectionProvider);
      if (client == null) {
        setState(() {
          _lastError = '连接未就绪，无法发送';
        });
        return;
      }
      _inputCtrl.clear();
      ref.read(draftProvider.notifier).clearDraft(widget.nodeId, widget.agentId);
      setState(() => _lastError = null);
      try {
        await client.call(
          'conversation.clear',
          {
            'nodeId': widget.nodeId,
            'agentId': widget.agentId,
          },
          timeout: const Duration(seconds: 10),
        );
      } catch (e) {
        debugPrint('conversation.clear error: $e');
        if (mounted) {
          setState(() => _lastError = '清除会话失败: $e');
        }
      }
      return;
    }

    final nodeState0 = ref.read(nodesProvider);
    final agents0 = nodeState0.agentsFor(widget.nodeId);
    final agent0 = agents0.where((a) => a.id == widget.agentId).firstOrNull;
    // Claude -p mode doesn't support concurrent input; PTY/tmux modes do
    final isClaudePipe = (agent0?.provider ?? '') == 'claude';
    if (_loading && isClaudePipe) {
      setState(() {
        _lastError = '正在等待上一条消息回复，请稍后再试';
      });
      return;
    }
    final client = ref.read(connectionProvider);
    if (client == null) {
      setState(() {
        _lastError = '连接未就绪，无法发送';
      });
      return;
    }

    final nodeState = ref.read(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;
    if (agent == null) {
      setState(() {
        _lastError = '会话不存在或已失效';
      });
      return;
    }
    // Allow sending even when stopped — conversation.send will auto-restart Claude -p agents
    if (agent.isReadOnly) {
      setState(() {
        _lastError = '当前会话为只读附加会话，请回到原 Claude 终端继续输入';
      });
      return;
    }
    if (agent.status == AgentStatus.crashed) {
      setState(() {
        _lastError = '会话崩溃，请先重启';
      });
      return;
    }

    _inputCtrl.clear();
    ref.read(draftProvider.notifier).clearDraft(widget.nodeId, widget.agentId);
    final images = List<Map<String, String>>.from(_pendingImages);
    setState(() {
      _loading = true;
      _lastError = null;
      _pendingImages = [];
    });
    try {
      final params = <String, dynamic>{
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'message': text.isEmpty ? '请看这张图片' : text,
        'raw': _rawMode,
      };
      if (images.isNotEmpty) {
        params['images'] = images;
      }
      final result = await client.call(
        'conversation.send',
        params,
        timeout: const Duration(seconds: 30),
      );

      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      final newId = map['id'] as String?;

      if (newId != null && newId.isNotEmpty && newId != widget.agentId) {
        final listResult = await client.call('agent.list', {
          'nodeId': widget.nodeId,
        });
        final agents = (listResult is List
            ? listResult
            : (listResult['agents'] as List?) ?? []);
        ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);
        if (mounted) {
          setState(() => _loading = false);
          context.go('/agent/${widget.nodeId}/$newId');
        }
        return;
      }

      await _pollNewEvents();
      await _pollBurstAfterSend();
    } catch (e) {
      debugPrint('sendMessage error: $e');
      if (mounted) {
        String msg = '发送失败，请重试';
        if (e.toString().contains('timeout')) {
          msg = '发送超时，图片可能过大或网络较慢';
        } else if (e.toString().contains(
          'tmux-attached sessions do not support image',
        )) {
          msg = 'tmux 附加会话不支持图片附件，请新建普通 Claude 会话再试';
        } else if (e.toString().contains(
          'opencode sessions do not support image',
        )) {
          msg = 'OpenCode 会话不支持图片附件';
        }
        setState(() {
          _lastError = msg;
        });
      }
    }
    if (mounted) {
      setState(() => _loading = false);
    }
  }

  Future<void> _resolvePermissionPrompt() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('conversation.key', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'key': 'tab',
      });
      await client.call('conversation.key', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'key': 'enter',
      });
      await _pollBurstAfterSend();
    } catch (e) {
      debugPrint('resolve permission prompt error: $e');
      if (mounted) {
        setState(() {
          _lastError = '处理权限提示失败，请重试';
        });
      }
    }
  }

  void _showSpecialKeysPanel(BuildContext context) {
    showModalBottomSheet(
      context: context,
      builder: (ctx) => Container(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              '特殊按键',
              style: AppTextStyles.bodyLarge.copyWith(fontWeight: FontWeight.bold),
            ),
            const SizedBox(height: AppSpacing.lg),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                _KeyChip(
                  label: 'ESC',
                  onTap: () => _sendKeyAndClose(ctx, 'esc'),
                ),
                _KeyChip(
                  label: 'Ctrl+C',
                  onTap: () => _sendKeyAndClose(ctx, 'ctrl_c'),
                ),
                _KeyChip(
                  label: 'Ctrl+D',
                  onTap: () => _sendKeyAndClose(ctx, 'ctrl_d'),
                ),
                _KeyChip(
                  label: 'Ctrl+Z',
                  onTap: () => _sendKeyAndClose(ctx, 'ctrl_z'),
                ),
                _KeyChip(
                  label: 'Ctrl+A',
                  onTap: () => _sendKeyAndClose(ctx, 'ctrl_a'),
                ),
                _KeyChip(
                  label: 'Ctrl+E',
                  onTap: () => _sendKeyAndClose(ctx, 'ctrl_e'),
                ),
                _KeyChip(
                  label: 'Tab',
                  onTap: () => _sendKeyAndClose(ctx, 'tab'),
                ),
                _KeyChip(icon: Icons.arrow_upward, onTap: () => _sendKeyAndClose(ctx, 'up')),
                _KeyChip(
                  icon: Icons.arrow_downward,
                  onTap: () => _sendKeyAndClose(ctx, 'down'),
                ),
                _KeyChip(
                  icon: Icons.arrow_back,
                  onTap: () => _sendKeyAndClose(ctx, 'left'),
                ),
                _KeyChip(
                  icon: Icons.arrow_forward,
                  onTap: () => _sendKeyAndClose(ctx, 'right'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _sendKeyAndClose(BuildContext ctx, String key) {
    Navigator.pop(ctx);
    _sendKey(key);
  }

  Future<void> _sendKey(String key) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('conversation.key', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'key': key,
      });
      await _pollNewEvents();
    } catch (e) {
      debugPrint('sendKey error: $e');
    }
  }

  Future<void> _control(String action) async {
    final client = ref.read(connectionProvider);
    if (client == null) {
      if (mounted) {
        setState(() {
          _lastError = '连接未就绪，操作失败';
        });
      }
      return;
    }

    if (action == 'stop' && _stopping) return;

    if (mounted) {
      setState(() {
        if (action == 'stop') _stopping = true;
        _lastError = null;
      });
    }

    try {
      await client.call('agent.$action', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
      });
      final listResult = await client.call('agent.list', {
        'nodeId': widget.nodeId,
      });
      final agents = (listResult is List
          ? listResult
          : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);
    } catch (e) {
      debugPrint('control $action error: $e');
      if (mounted) {
        setState(() {
          _lastError = action == 'stop' ? '停止失败，请重试' : '操作失败，请重试';
        });
      }
    } finally {
      if (mounted) {
        setState(() {
          _stopping = false;
        });
      }
    }
  }

  Future<void> _switchModel(String model) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('agent.restart', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'model': model,
      });

      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      final newId = map['id'] as String?;

      final listResult = await client.call('agent.list', {
        'nodeId': widget.nodeId,
      });
      final agents = (listResult is List
          ? listResult
          : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);

      if (newId != null &&
          newId.isNotEmpty &&
          mounted &&
          newId != widget.agentId) {
        context.go('/agent/${widget.nodeId}/$newId');
      }
    } catch (e) {
      debugPrint('switchModel error: $e');
    }
  }

  Future<void> _switchMode(String mode) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('切换权限模式'),
        content: Text('切换到 ${_modeLabel(mode)} 模式。对话历史将通过 --resume 保留。确定要切换吗？'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;

    final client = ref.read(connectionProvider);
    if (client == null) return;
    setState(() => _pendingMode = mode);
    try {
      await client.call('agent.restart', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'permissionMode': mode,
      });
      final listResult = await client.call('agent.list', {
        'nodeId': widget.nodeId,
      });
      final agents = (listResult is List
          ? listResult
          : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);
    } catch (e) {
      debugPrint('switchMode error: $e');
    } finally {
      if (mounted) {
        setState(() => _pendingMode = null);
      }
    }
  }

  String _modeLabel(String modeId) {
    final allModes = [...kClaudeModes, ...kOpenCodeModes];
    return allModes
        .firstWhere(
          (m) => m.id == modeId,
          orElse: () => AgentMode(id: modeId, label: modeId, icon: Icons.help),
        )
        .label;
  }

  Future<void> _switchProvider(
    String providerId, {
    VoidCallback? onSwitched,
  }) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('provider.switch', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'providerId': providerId,
      });
      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      final providerName = map['providerName'] as String? ?? providerId;
      final model = map['model'] as String? ?? '';

      final listResult = await client.call('agent.list', {
        'nodeId': widget.nodeId,
      });
      final agents = (listResult is List
          ? listResult
          : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);

      onSwitched?.call();

      if (mounted) {
        final msg = model.isNotEmpty
            ? '已切换到 $providerName ($model)'
            : '已切换到 $providerName';
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text(msg)));
      }
    } catch (e) {
      debugPrint('switchProvider error: $e');
    }
  }

  void _scrollToBottom({bool force = false, bool animate = true}) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollCtrl.hasClients) return;
      if (!force && !_stickToBottom) return;
      if (!force) {
        final now = DateTime.now();
        if (_lastUserScroll != null &&
            now.difference(_lastUserScroll!) < const Duration(seconds: 5)) {
          return;
        }
        if (_lastUserTap != null &&
            now.difference(_lastUserTap!) < const Duration(seconds: 3)) {
          return;
        }
      }
      // Reversed ListView: bottom is offset 0
      // Skip if already at bottom to avoid visual jitter
      if (_scrollCtrl.offset <= 1) return;
      if (!animate) {
        _scrollCtrl.jumpTo(0);
        return;
      }
      _scrollCtrl.animateTo(
        0,
        duration: const Duration(milliseconds: 200),
        curve: Curves.easeOut,
      );
    });
  }

  Future<void> _renameAgent(String currentName) async {
    final controller = TextEditingController(text: currentName);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名会话'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '输入新名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (newName != null && newName.isNotEmpty) {
      final client = ref.read(connectionProvider);
      if (client == null) return;
      try {
        await client.call('agent.rename', {
          'nodeId': widget.nodeId,
          'agentId': widget.agentId,
          'name': newName,
        });
        setState(() => _agentName = newName);
        ref
            .read(nodesProvider.notifier)
            .renameAgent(widget.nodeId, widget.agentId, newName);
      } catch (e) {
        debugPrint('rename agent error: $e');
      }
    }
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;

    // Use cached message processing results (rebuilt only when _rawEvents changes)
    final reversed = _cachedReversed;
    final lastAssistantIndex = _cachedLastAssistantIndex;
    final showPermissionOverlay = hasPendingPermissionPrompt(_rawEvents);
    final provider = agent?.provider ?? '';

    final activeMode = agent == null
        ? ''
        : effectiveModeForAgent(agent, pendingMode: _pendingMode);

    return Scaffold(
      appBar: MissionControlAppBar(
        showWordmark: false,
        titleWidget: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              _agentName ?? agent?.name ?? widget.agentId,
              style: AppTextStyles.bodyMedium,
            ),
            if (agent != null)
              Text(
                _statusLabel(agent.status),
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.w600,
                  color: _statusColor(agent.status),
                ),
              ),
            if (agent != null)
              Text(
                _buildMetaLine(agent),
                style: TextStyle(
                  fontFamily: AppTextStyles.monoFontFamily,
                  fontSize: 11,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
          ],
        ),
        actions: [
          // Permission-mode chip (replaces the in-composer mode button so
          // the input row stays compact). Tapping it opens the same config
          // sheet that the old composer button used.
          if (agent != null && modesForProvider(agent.provider).isNotEmpty)
            BypassIndicator(
              modeLabel: modesForProvider(agent.provider)
                  .firstWhere(
                    (m) => m.id == activeMode,
                    orElse: () => modesForProvider(agent.provider).first,
                  )
                  .label,
              onTap: () => _showAgentConfigSheet(agent, activeMode),
            ),
          if (!_wsConnected)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: SizedBox(
                width: 16,
                height: 16,
                child: CircularProgressIndicator(
                  strokeWidth: 2,
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                ),
              ),
            ),
          // Interrupt button (shown when agent is working)
          if (agent?.status == AgentStatus.working)
            IconButton(
              onPressed: () => _sendKey('ctrl_c'),
              icon: const Icon(Icons.stop_circle),
              tooltip: '中断 (Ctrl+C)',
            ),
        ],
      ),
      body: Column(
        children: [
          // Error status bar at top
          if (_lastError != null)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              color: Theme.of(context).colorScheme.errorContainer,
              child: Row(
                children: [
                  Expanded(
                    child: Text(
                      _lastError!,
                      style: TextStyle(
                        color: Theme.of(context).colorScheme.onErrorContainer,
                        fontSize: 12,
                      ),
                    ),
                  ),
                  TextButton(
                    onPressed: () {
                      setState(() {
                        _lastError = null;
                      });
                      _loadHistory();
                    },
                    child: const Text('重试'),
                  ),
                  IconButton(
                    onPressed: () {
                      setState(() {
                        _lastError = null;
                      });
                    },
                    icon: const Icon(Icons.close, size: 16),
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints(),
                  ),
                ],
              ),
            ),
          if (_loading || _stopping)
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
              color: Theme.of(context).colorScheme.surfaceContainerHighest,
              child: Text(
                _stopping ? '正在停止会话…' : '正在发送并等待回复…',
                style: TextStyle(
                  color: Theme.of(context).colorScheme.onSurfaceVariant,
                  fontSize: 12,
                ),
              ),
            ),
          // Messages
          Expanded(
            child: Stack(
              children: [
                _initialLoading
                    ? const Center(child: OscilloscopeLoader())
                    : (_rawEvents.isEmpty && _lastError != null)
                    ? Center(
                        child: GestureDetector(
                          onTap: _loadHistory,
                          child: Column(
                            mainAxisSize: MainAxisSize.min,
                            children: [
                              Icon(Icons.refresh, color: Colors.grey, size: 32),
                              const SizedBox(height: 8),
                              Text(
                                _lastError!,
                                style: const TextStyle(color: Colors.grey),
                              ),
                            ],
                          ),
                        ),
                      )
                    : reversed.isEmpty
                    ? const Center(
                        child: Text(
                          '暂无对话',
                          style: TextStyle(color: Colors.grey),
                        ),
                      )
                    : GestureDetector(
                        behavior: HitTestBehavior.translucent,
                        onTap: () {
                          setState(() {
                            _showTimestamps = !_showTimestamps;
                          });
                        },
                        child: SelectionArea(
                          child: NotificationListener<ScrollNotification>(
                          onNotification: (notification) {
                            // Reversed: load older near maxScrollExtent (top)
                            if (notification is ScrollUpdateNotification &&
                                notification.metrics.maxScrollExtent -
                                    notification.metrics.pixels <= 24) {
                              _loadOlderHistory();
                            }
                            return false;
                          },
                          child: ListView.builder(
                            controller: _scrollCtrl,
                            reverse: true,
                            padding: const EdgeInsets.all(12),
                            itemCount:
                                reversed.length +
                                (_loadingOlder ? 1 : 0) +
                                (!_initialLoading && _isLoadingFreshData
                                    ? 1
                                    : 0),
                            itemBuilder: (_, i) {
                              if (!_initialLoading &&
                                  _isLoadingFreshData &&
                                  i == 0) {
                                return const Padding(
                                  padding: EdgeInsets.only(bottom: 8),
                                  child: Center(
                                    child: Text(
                                      '· · ·',
                                      style: TextStyle(
                                        color: Colors.grey,
                                        fontSize: 14,
                                        fontStyle: FontStyle.italic,
                                      ),
                                    ),
                                  ),
                                );
                              }
                              final msgOffset = (!_initialLoading && _isLoadingFreshData ? 1 : 0);
                              final total = reversed.length + msgOffset + (_loadingOlder ? 1 : 0);
                              // Loading older at top (last index in reversed list)
                              if (_loadingOlder && i == total - 1) {
                                return const Padding(
                                  padding: EdgeInsets.only(bottom: 8),
                                  child: Center(
                                    child: SizedBox(
                                      width: 16,
                                      height: 16,
                                      child: CircularProgressIndicator(
                                        strokeWidth: 2,
                                      ),
                                    ),
                                  ),
                                );
                              }
                              final idx = i - msgOffset;
                              return MessageBubble(
                                message: reversed[idx],
                                isLastAssistant: idx == lastAssistantIndex,
                                showTimestamp: _showTimestamps,
                                onResolvePermissionPrompt:
                                    reversed[idx].isPermissionPrompt
                                    ? _resolvePermissionPrompt
                                    : null,
                                onPermissionResponse:
                                    reversed[idx].kind == 'permission_request'
                                    ? _sendPermissionResponse
                                    : null,
                                onSend: (reversed[idx].kind ==
                                            'ask_user_question' ||
                                        reversed[idx].kind == 'exit_plan_mode')
                                    ? (content) {
                                        _inputCtrl.text = content;
                                        _sendMessage();
                                      }
                                    : null,
                                onToggleExpand: () {
                                  _lastUserTap = DateTime.now();
                                },
                              );
                            },
                          ),
                        ),
                      ),
                    ),
                // Refresh button (above jump to bottom)
                Positioned(
                  right: 12,
                  bottom: 72,
                  child: FloatingActionButton.small(
                    heroTag: "refreshBtn",
                    onPressed: () {
                      _loadHistory();
                    },
                    child: const Icon(Icons.refresh),
                  ),
                ),
                if (_showJumpToLatest)
                  Positioned(
                    right: 12,
                    bottom: 12,
                    child: FloatingActionButton.small(
                      heroTag: "jumpToBottom",
                      onPressed: () {
                        setState(() {
                          _stickToBottom = true;
                          _showJumpToLatest = false;
                        });
                        _scrollToBottom(force: true);
                      },
                      child: const Icon(Icons.arrow_downward),
                    ),
                  ),
              ],
            ),
          ),
          // Permission prompt overlay (above input bar)
          if (showPermissionOverlay)
            _PermissionPromptOverlay(onResolve: _resolvePermissionPrompt),
          // Input bar
          _InputBar(
            agent: agent,
            controller: _inputCtrl,
            loading: _loading,
            rawMode: _rawMode,
            showTerminalControls: shouldShowTerminalControls(provider),
            showRawToggle: shouldShowRawToggle(provider),
            onToggleRaw: (v) => setState(() => _rawMode = v),
            onSend: _sendMessage,
            onKey: _sendKey,
            pendingImages: _pendingImages,
            onImagesChanged: (imgs) => setState(() => _pendingImages = imgs),
            extraCommands: _dynamicSkills,
            currentMode: activeMode,
            stopping: _stopping,
            nodeId: widget.nodeId,
            onControl: _control,
            onSwitchModel: _switchModel,
            onSwitchProvider: _switchProvider,
            onSwitchMode: _switchMode,
          ),
        ],
      ),
    );
  }

  Color _statusColor(AgentStatus s) => AgentStatusTheme.getColor(s);

  String _statusLabel(AgentStatus s) => AgentStatusTheme.getLabel(s);

  /// Opens the same provider/model/mode configuration sheet that used to be
  /// triggered from the in-composer mode button. Now wired to the AppBar
  /// [BypassIndicator] so the composer row stays minimal.
  void _showAgentConfigSheet(AgentModel agent, String currentMode) {
    showModalBottomSheet(
      context: context,
      isScrollControlled: true,
      builder: (_) => _AgentConfigSheet(
        agent: agent,
        nodeId: widget.nodeId,
        stopping: _stopping,
        currentMode: currentMode,
        onControl: _control,
        onSwitchModel: _switchModel,
        onSwitchProvider: _switchProvider,
        onSwitchMode: _switchMode,
      ),
    );
  }

  String _buildMetaLine(AgentModel agent) {
    final parts = <String>[
      if ((agent.pid ?? 0) > 0) '${agent.pid}',
      if ((agent.sessionId ?? '').isNotEmpty) agent.sessionId!,
      if (agent.workDir.isNotEmpty) agent.workDir,
    ];
    return parts.join(' · ');
  }
}

String effectiveModeForAgent(AgentModel agent, {String? pendingMode}) {
  if ((pendingMode ?? '').isNotEmpty) return pendingMode!;
  final backendMode = (agent.permissionMode ?? '').trim();
  if (backendMode.isNotEmpty) return backendMode;
  final modes = modesForProvider(agent.provider);
  if (modes.isNotEmpty) return modes.first.id;
  return '';
}

class _ControlBar extends ConsumerStatefulWidget {
  final AgentModel agent;
  final String nodeId;
  final bool stopping;
  final String currentMode;
  final Future<void> Function(String action) onControl;
  final Future<void> Function(String model) onSwitchModel;
  final Future<void> Function(String providerId, {VoidCallback? onSwitched})
  onSwitchProvider;
  final Future<void> Function(String mode) onSwitchMode;

  const _ControlBar({
    required this.agent,
    required this.nodeId,
    required this.stopping,
    required this.currentMode,
    required this.onControl,
    required this.onSwitchModel,
    required this.onSwitchProvider,
    required this.onSwitchMode,
  });

  @override
  ConsumerState<_ControlBar> createState() => _ControlBarState();
}

class _ControlBarState extends ConsumerState<_ControlBar> {
  List<Widget> _buildModeSelector(BuildContext context) {
    final modes = modesForProvider(widget.agent.provider);
    if (modes.isEmpty) return [];
    final scheme = Theme.of(context).colorScheme;
    final current = modes.firstWhere(
      (m) => m.id == widget.currentMode,
      orElse: () => modes.first,
    );
    return [
      PopupMenuButton<String>(
        tooltip: '切换权限模式',
        onSelected: (id) {
          if (id != widget.currentMode) widget.onSwitchMode(id);
        },
        itemBuilder: (_) => modes.map((m) {
          final active = m.id == widget.currentMode;
          return PopupMenuItem<String>(
            value: m.id,
            child: Row(
              children: [
                if (active) ...[
                  const Icon(Icons.check, size: 16),
                  const SizedBox(width: 4),
                ] else
                  const SizedBox(width: 20),
                Icon(m.icon, size: 16),
                const SizedBox(width: 6),
                Text(
                  m.label,
                  style: TextStyle(
                    fontWeight: active ? FontWeight.w600 : FontWeight.normal,
                  ),
                ),
              ],
            ),
          );
        }).toList(),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 6),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(current.icon, size: 16, color: scheme.primary),
              const SizedBox(width: 3),
              Text(
                current.label,
                style: TextStyle(
                  fontSize: 12,
                  color: scheme.primary,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const Icon(Icons.arrow_drop_down, size: 16),
            ],
          ),
        ),
      ),
      const SizedBox(width: 2),
    ];
  }

  Future<Map<String, dynamic>>? _providerListFuture;

  Widget _buildConfigSelector(BuildContext context) {
    if (widget.agent.provider != 'claude' &&
        widget.agent.provider != 'opencode') {
      return const SizedBox.shrink();
    }
    final scheme = Theme.of(context).colorScheme;

    return FutureBuilder<Map<String, dynamic>>(
      future: _providerListFuture,
      builder: (context, snapshot) {
        final data = snapshot.data ?? {};
        final providers = (data['providers'] as List?) ?? [];
        final currentProviderId = data['current'] as String? ?? '';
        final providerWriteMode = data['providerWriteMode'] as String? ?? '';
        final providerReadOnlyReason =
            (data['providerReadOnlyReason'] as String? ?? '').trim();
        final isClaudeProvider = widget.agent.provider == 'claude';
        final isOpencodeProvider = widget.agent.provider == 'opencode';
        final isProviderReadOnly =
            isClaudeProvider &&
            (!snapshot.hasData || providerWriteMode == 'read_only');

        String currentProviderName = 'Default';
        if (isOpencodeProvider) {
          currentProviderName = currentOpencodeModelLabel(data);
        } else if (currentProviderId.isNotEmpty) {
          for (final p in providers) {
            if ((p['id'] ?? '') == currentProviderId) {
              currentProviderName = (p['name'] ?? currentProviderId).toString();
              break;
            }
          }
        }

        Widget triggerChild = Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          margin: const EdgeInsets.only(left: 4),
          decoration: BoxDecoration(
            color: scheme.surfaceContainerLow,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(
              color: scheme.outlineVariant.withValues(alpha: 0.5),
            ),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                isProviderReadOnly ? Icons.lock_outline : Icons.tune,
                size: 14,
                color: isProviderReadOnly ? Colors.orange : scheme.primary,
              ),
              const SizedBox(width: 4),
              Text(
                currentProviderName,
                style: TextStyle(fontSize: 12, color: scheme.onSurface),
              ),
              Icon(
                Icons.arrow_drop_down,
                size: 16,
                color: scheme.onSurfaceVariant,
              ),
            ],
          ),
        );

        if (isProviderReadOnly) {
          return Tooltip(
            message: providerReadOnlyReason.isEmpty
                ? (snapshot.hasData
                      ? '当前 Provider 仅支持只读展示'
                      : '正在加载 Provider 状态')
                : providerReadOnlyReason,
            child: Opacity(opacity: 0.8, child: triggerChild),
          );
        }

        return PopupMenuButton<String>(
          tooltip: '配置 Provider 和 模型',
          onSelected: (value) {
            if (value.startsWith('__provider__')) {
              final id = value.substring('__provider__'.length);
              widget.onSwitchProvider(id, onSwitched: _fetchProviders);
            } else if (value == '__add_provider__') {
              _showAddProviderDialog(context);
            } else if (value.startsWith('__model__')) {
              final model = value.substring('__model__'.length);
              widget.onSwitchModel(model).whenComplete(() {
                if (mounted) _fetchProviders();
              });
            }
          },
          itemBuilder: (_) {
            final items = <PopupMenuEntry<String>>[];
            if (widget.agent.provider == 'claude') {
              items.add(
                const PopupMenuItem<String>(
                  enabled: false,
                  child: Text(
                    'Provider',
                    style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12),
                  ),
                ),
              );
              if (providers.isEmpty) {
                items.add(
                  const PopupMenuItem<String>(
                    enabled: false,
                    child: Text(
                      '暂无可用 Provider',
                      style: TextStyle(fontSize: 13),
                    ),
                  ),
                );
              } else {
                for (final p in providers) {
                  final id = (p['id'] ?? '').toString();
                  final name = (p['name'] ?? id).toString();
                  final isActive = id == currentProviderId;
                  items.add(
                    PopupMenuItem<String>(
                      value: '__provider__$id',
                      child: Row(
                        children: [
                          if (isActive) ...[
                            const Icon(Icons.check, size: 16),
                            const SizedBox(width: 4),
                          ] else
                            const SizedBox(width: 20),
                          Text(name, style: const TextStyle(fontSize: 13)),
                        ],
                      ),
                    ),
                  );
                }
              }
              items.add(const PopupMenuDivider());
              items.add(
                const PopupMenuItem<String>(
                  value: '__add_provider__',
                  child: Row(
                    children: [
                      Icon(Icons.add, size: 16),
                      SizedBox(width: 6),
                      Text('新增 Provider', style: TextStyle(fontSize: 13)),
                    ],
                  ),
                ),
              );
              return items;
            }

            items.add(
              const PopupMenuItem<String>(
                enabled: false,
                child: Text(
                  'Model',
                  style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12),
                ),
              ),
            );
            if (widget.agent.provider == 'opencode') {
              final opencodeModels = normalizeOpencodeModels(
                snapshot.data?['_opencodeModels'],
              );
              final currentModelId = currentOpencodeModelId(data);
              if (opencodeModels.isNotEmpty) {
                String? lastProvider;
                for (final m in opencodeModels) {
                  final prov = m['provider'] ?? '';
                  if (prov != lastProvider && prov.isNotEmpty) {
                    if (lastProvider != null)
                      items.add(const PopupMenuDivider());
                    items.add(
                      PopupMenuItem<String>(
                        enabled: false,
                        child: Text(
                          prov,
                          style: const TextStyle(
                            fontWeight: FontWeight.bold,
                            fontSize: 11,
                            color: Colors.grey,
                          ),
                        ),
                      ),
                    );
                    lastProvider = prov;
                  }
                  final id = m['id'] ?? '';
                  final name = m['name'] ?? id;
                  final isActive = opencodeModelMatches(id, currentModelId);
                  items.add(
                    PopupMenuItem<String>(
                      value: '__model__$id',
                      child: Row(
                        children: [
                          if (isActive) ...[
                            const Icon(Icons.check, size: 16),
                            const SizedBox(width: 4),
                          ] else
                            const SizedBox(width: 20),
                          Expanded(
                            child: Text(
                              name,
                              style: const TextStyle(fontSize: 13),
                            ),
                          ),
                        ],
                      ),
                    ),
                  );
                }
              } else {
                items.add(
                  const PopupMenuItem<String>(
                    enabled: false,
                    child: Text(
                      'No models found',
                      style: TextStyle(fontSize: 13),
                    ),
                  ),
                );
              }
            }
            return items;
          },
          child: triggerChild,
        );
      },
    );
  }

  @override
  void initState() {
    super.initState();
    if (widget.agent.provider == 'claude' ||
        widget.agent.provider == 'opencode') {
      _fetchProviders();
    }
  }

  @override
  void didUpdateWidget(covariant _ControlBar oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.agent.id != widget.agent.id ||
        oldWidget.nodeId != widget.nodeId ||
        oldWidget.agent.provider != widget.agent.provider) {
      if (widget.agent.provider == 'claude' ||
          widget.agent.provider == 'opencode') {
        _fetchProviders();
      } else {
        setState(() {
          _providerListFuture = null;
        });
      }
    }
  }

  void _fetchProviders() {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    setState(() {
      _providerListFuture = (() async {
        if (widget.agent.provider == 'opencode') {
          final modelResult = await client.call('opencode.models', {
            'nodeId': widget.nodeId,
          });
          final modelData = modelResult is Map
              ? Map<String, dynamic>.from(modelResult)
              : <String, dynamic>{};
          return {
            '_opencodeModels': normalizeOpencodeModels(modelData['models']),
            '_opencodeCurrent': (modelData['current'] ?? '').toString(),
          };
        }

        final result = await client.call('provider.list', {
          'nodeId': widget.nodeId,
          'agentId': widget.agent.id,
        });
        return result is Map
            ? Map<String, dynamic>.from(result)
            : <String, dynamic>{};
      })();
    });
  }

  void _showAddProviderDialog(BuildContext context) {
    final nameCtrl = TextEditingController();
    final urlCtrl = TextEditingController();
    final tokenCtrl = TextEditingController();
    final modelCtrl = TextEditingController();
    String error = '';

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: const Text('新增 Provider'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称 *',
                    hintText: 'My Provider',
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: urlCtrl,
                  decoration: const InputDecoration(
                    labelText: 'API Base URL',
                    hintText: 'https://api.anthropic.com',
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: tokenCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Auth Token',
                    hintText: 'sk-...',
                    isDense: true,
                  ),
                  obscureText: true,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: modelCtrl,
                  decoration: const InputDecoration(
                    labelText: '模型',
                    hintText: 'claude-sonnet-4-6',
                    isDense: true,
                  ),
                ),
                if (error.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Text(
                      error,
                      style: const TextStyle(color: Colors.red, fontSize: 13),
                    ),
                  ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                if (nameCtrl.text.trim().isEmpty) {
                  setDialogState(() => error = '名称不能为空');
                  return;
                }
                final client = ref.read(connectionProvider);
                if (client == null) return;
                try {
                  await client.call('provider.add', {
                    'nodeId': widget.nodeId,
                    'name': nameCtrl.text.trim(),
                    'baseUrl': urlCtrl.text.trim(),
                    'authToken': tokenCtrl.text.trim(),
                    'model': modelCtrl.text.trim(),
                  });
                  if (ctx.mounted) Navigator.pop(ctx);
                  // Refresh provider list
                  _fetchProviders();
                } catch (e) {
                  setDialogState(() => error = e.toString());
                }
              },
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final stopped =
        widget.agent.status == AgentStatus.stopped ||
        widget.agent.status == AgentStatus.crashed;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        border: Border(bottom: BorderSide(color: Colors.grey.shade300)),
      ),
      child: Row(
        children: [
          // Mode selector dropdown
          ..._buildModeSelector(context),
          // Unified provider + model config selector
          _buildConfigSelector(context),
          const Spacer(),
          if (stopped)
            IconButton(
              onPressed: () => widget.onControl('restart'),
              icon: const Icon(Icons.play_arrow, size: 18),
              tooltip: '启动',
              visualDensity: VisualDensity.compact,
            ),
          if (!stopped)
            IconButton(
              onPressed: widget.stopping
                  ? null
                  : () => widget.onControl('stop'),
              icon: widget.stopping
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.stop, size: 18),
              tooltip: widget.stopping ? '停止中…' : '停止',
              visualDensity: VisualDensity.compact,
            ),
          IconButton(
            onPressed: () => widget.onControl('restart'),
            icon: const Icon(Icons.refresh, size: 18),
            tooltip: '重启',
            visualDensity: VisualDensity.compact,
          ),
        ],
      ),
    );
  }
}

class _AgentConfigSheet extends ConsumerStatefulWidget {
  final AgentModel agent;
  final String nodeId;
  final bool stopping;
  final String currentMode;
  final Future<void> Function(String action) onControl;
  final Future<void> Function(String model) onSwitchModel;
  final Future<void> Function(String providerId, {VoidCallback? onSwitched})
      onSwitchProvider;
  final Future<void> Function(String mode) onSwitchMode;

  const _AgentConfigSheet({
    required this.agent,
    required this.nodeId,
    required this.stopping,
    required this.currentMode,
    required this.onControl,
    required this.onSwitchModel,
    required this.onSwitchProvider,
    required this.onSwitchMode,
  });

  @override
  ConsumerState<_AgentConfigSheet> createState() => _AgentConfigSheetState();
}

class _AgentConfigSheetState extends ConsumerState<_AgentConfigSheet> {
  Future<Map<String, dynamic>>? _providerListFuture;

  @override
  void initState() {
    super.initState();
    if (widget.agent.provider == 'claude' ||
        widget.agent.provider == 'opencode') {
      _fetchProviders();
    }
  }

  void _fetchProviders() {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    setState(() {
      _providerListFuture = (() async {
        if (widget.agent.provider == 'opencode') {
          final modelResult = await client.call('opencode.models', {
            'nodeId': widget.nodeId,
          });
          final modelData = modelResult is Map
              ? Map<String, dynamic>.from(modelResult)
              : <String, dynamic>{};
          return {
            '_opencodeModels': normalizeOpencodeModels(modelData['models']),
            '_opencodeCurrent': (modelData['current'] ?? '').toString(),
          };
        }

        final result = await client.call('provider.list', {
          'nodeId': widget.nodeId,
          'agentId': widget.agent.id,
        });
        return result is Map
            ? Map<String, dynamic>.from(result)
            : <String, dynamic>{};
      })();
    });
  }

  void _showAddProviderDialog(BuildContext context) {
    final nameCtrl = TextEditingController();
    final urlCtrl = TextEditingController();
    final tokenCtrl = TextEditingController();
    final modelCtrl = TextEditingController();
    String error = '';

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setDialogState) => AlertDialog(
          title: const Text('新增 Provider'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称 *',
                    hintText: 'My Provider',
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: urlCtrl,
                  decoration: const InputDecoration(
                    labelText: 'API Base URL',
                    hintText: 'https://api.anthropic.com',
                    isDense: true,
                  ),
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: tokenCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Auth Token',
                    hintText: 'sk-...',
                    isDense: true,
                  ),
                  obscureText: true,
                ),
                const SizedBox(height: 12),
                TextField(
                  controller: modelCtrl,
                  decoration: const InputDecoration(
                    labelText: '模型',
                    hintText: 'claude-sonnet-4-6',
                    isDense: true,
                  ),
                ),
                if (error.isNotEmpty)
                  Padding(
                    padding: const EdgeInsets.only(top: 8),
                    child: Text(
                      error,
                      style: const TextStyle(color: Colors.red, fontSize: 13),
                    ),
                  ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                if (nameCtrl.text.trim().isEmpty) {
                  setDialogState(() => error = '名称不能为空');
                  return;
                }
                final client = ref.read(connectionProvider);
                if (client == null) return;
                try {
                  await client.call('provider.add', {
                    'nodeId': widget.nodeId,
                    'name': nameCtrl.text.trim(),
                    'baseUrl': urlCtrl.text.trim(),
                    'authToken': tokenCtrl.text.trim(),
                    'model': modelCtrl.text.trim(),
                  });
                  if (ctx.mounted) Navigator.pop(ctx);
                  _fetchProviders();
                } catch (e) {
                  setDialogState(() => error = e.toString());
                }
              },
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildConfigSelector(BuildContext context) {
    if (widget.agent.provider != 'claude' &&
        widget.agent.provider != 'opencode') {
      return const SizedBox.shrink();
    }
    final scheme = Theme.of(context).colorScheme;

    return FutureBuilder<Map<String, dynamic>>(
      future: _providerListFuture,
      builder: (context, snapshot) {
        final data = snapshot.data ?? {};
        final providers = (data['providers'] as List?) ?? [];
        final currentProviderId = data['current'] as String? ?? '';
        final providerWriteMode = data['providerWriteMode'] as String? ?? '';
        final providerReadOnlyReason =
            (data['providerReadOnlyReason'] as String? ?? '').trim();
        final isClaudeProvider = widget.agent.provider == 'claude';
        final isOpencodeProvider = widget.agent.provider == 'opencode';
        final isProviderReadOnly =
            isClaudeProvider &&
            (!snapshot.hasData || providerWriteMode == 'read_only');

        String currentProviderName = 'Default';
        if (isOpencodeProvider) {
          currentProviderName = currentOpencodeModelLabel(data);
        } else if (currentProviderId.isNotEmpty) {
          for (final p in providers) {
            if ((p['id'] ?? '') == currentProviderId) {
              currentProviderName = (p['name'] ?? currentProviderId).toString();
              break;
            }
          }
        }

        Widget triggerChild = Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
          margin: const EdgeInsets.only(left: 4),
          decoration: BoxDecoration(
            color: scheme.surfaceContainerLow,
            borderRadius: BorderRadius.circular(16),
            border: Border.all(
              color: scheme.outlineVariant.withValues(alpha: 0.5),
            ),
          ),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Icon(
                isProviderReadOnly ? Icons.lock_outline : Icons.tune,
                size: 14,
                color: isProviderReadOnly ? Colors.orange : scheme.primary,
              ),
              const SizedBox(width: 4),
              Text(
                currentProviderName,
                style: TextStyle(fontSize: 12, color: scheme.onSurface),
              ),
              Icon(
                Icons.arrow_drop_down,
                size: 16,
                color: scheme.onSurfaceVariant,
              ),
            ],
          ),
        );

        if (isProviderReadOnly) {
          return Tooltip(
            message: providerReadOnlyReason.isEmpty
                ? (snapshot.hasData
                      ? '当前 Provider 仅支持只读展示'
                      : '正在加载 Provider 状态')
                : providerReadOnlyReason,
            child: Opacity(opacity: 0.8, child: triggerChild),
          );
        }

        return PopupMenuButton<String>(
          tooltip: '配置 Provider 和 模型',
          onSelected: (value) {
            if (value.startsWith('__provider__')) {
              final id = value.substring('__provider__'.length);
              widget.onSwitchProvider(id, onSwitched: _fetchProviders);
            } else if (value == '__add_provider__') {
              _showAddProviderDialog(context);
            } else if (value.startsWith('__model__')) {
              final model = value.substring('__model__'.length);
              widget.onSwitchModel(model).whenComplete(() {
                if (mounted) _fetchProviders();
              });
            }
          },
          itemBuilder: (_) {
            final items = <PopupMenuEntry<String>>[];
            if (widget.agent.provider == 'claude') {
              items.add(
                const PopupMenuItem<String>(
                  enabled: false,
                  child: Text(
                    'Provider',
                    style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12),
                  ),
                ),
              );
              if (providers.isEmpty) {
                items.add(
                  const PopupMenuItem<String>(
                    enabled: false,
                    child: Text(
                      '暂无可用 Provider',
                      style: TextStyle(fontSize: 13),
                    ),
                  ),
                );
              } else {
                for (final p in providers) {
                  final id = (p['id'] ?? '').toString();
                  final name = (p['name'] ?? id).toString();
                  final isActive = id == currentProviderId;
                  items.add(
                    PopupMenuItem<String>(
                      value: '__provider__$id',
                      child: Row(
                        children: [
                          if (isActive) ...[
                            const Icon(Icons.check, size: 16),
                            const SizedBox(width: 4),
                          ] else
                            const SizedBox(width: 20),
                          Text(name, style: const TextStyle(fontSize: 13)),
                        ],
                      ),
                    ),
                  );
                }
              }
              items.add(const PopupMenuDivider());
              items.add(
                const PopupMenuItem<String>(
                  value: '__add_provider__',
                  child: Row(
                    children: [
                      Icon(Icons.add, size: 16),
                      SizedBox(width: 6),
                      Text('新增 Provider', style: TextStyle(fontSize: 13)),
                    ],
                  ),
                ),
              );
              return items;
            }

            items.add(
              const PopupMenuItem<String>(
                enabled: false,
                child: Text(
                  'Model',
                  style: TextStyle(fontWeight: FontWeight.bold, fontSize: 12),
                ),
              ),
            );
            if (widget.agent.provider == 'opencode') {
              final opencodeModels = normalizeOpencodeModels(
                snapshot.data?['_opencodeModels'],
              );
              final currentModelId = currentOpencodeModelId(data);
              if (opencodeModels.isNotEmpty) {
                String? lastProvider;
                for (final m in opencodeModels) {
                  final prov = m['provider'] ?? '';
                  if (prov != lastProvider && prov.isNotEmpty) {
                    if (lastProvider != null)
                      items.add(const PopupMenuDivider());
                    items.add(
                      PopupMenuItem<String>(
                        enabled: false,
                        child: Text(
                          prov,
                          style: const TextStyle(
                            fontWeight: FontWeight.bold,
                            fontSize: 11,
                            color: Colors.grey,
                          ),
                        ),
                      ),
                    );
                    lastProvider = prov;
                  }
                  final id = m['id'] ?? '';
                  final name = m['name'] ?? id;
                  final isActive = opencodeModelMatches(id, currentModelId);
                  items.add(
                    PopupMenuItem<String>(
                      value: '__model__$id',
                      child: Row(
                        children: [
                          if (isActive) ...[
                            const Icon(Icons.check, size: 16),
                            const SizedBox(width: 4),
                          ] else
                            const SizedBox(width: 20),
                          Expanded(
                            child: Text(
                              name,
                              style: const TextStyle(fontSize: 13),
                            ),
                          ),
                        ],
                      ),
                    ),
                  );
                }
              } else {
                items.add(
                  const PopupMenuItem<String>(
                    enabled: false,
                    child: Text(
                      'No models found',
                      style: TextStyle(fontSize: 13),
                    ),
                  ),
                );
              }
            }
            return items;
          },
          child: triggerChild,
        );
      },
    );
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final modes = modesForProvider(widget.agent.provider);
    final stopped =
        widget.agent.status == AgentStatus.stopped ||
        widget.agent.status == AgentStatus.crashed;

    return SafeArea(
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(
                  '配置',
                  style: Theme.of(context).textTheme.titleLarge,
                ),
                const Spacer(),
                IconButton(
                  onPressed: () => Navigator.pop(context),
                  icon: const Icon(Icons.close),
                ),
              ],
            ),
            const SizedBox(height: 16),
            if (modes.isNotEmpty) ...[
              Text(
                '模式',
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.bold,
                  color: scheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 8),
              ...modes.map((m) {
                final active = m.id == widget.currentMode;
                return ListTile(
                  dense: true,
                  leading: Icon(
                    m.icon,
                    size: 20,
                    color: active ? scheme.primary : scheme.onSurfaceVariant,
                  ),
                  title: Text(
                    m.label,
                    style: TextStyle(
                      color: active ? scheme.primary : scheme.onSurface,
                      fontWeight:
                          active ? FontWeight.w600 : FontWeight.normal,
                    ),
                  ),
                  trailing: active
                      ? Icon(Icons.check, size: 18, color: scheme.primary)
                      : null,
                  onTap: () {
                    if (!active) {
                      widget.onSwitchMode(m.id);
                      Navigator.pop(context);
                    }
                  },
                );
              }),
              const Divider(),
            ],
            if (widget.agent.provider == 'claude' ||
                widget.agent.provider == 'opencode') ...[
              Text(
                'Provider / Model',
                style: TextStyle(
                  fontSize: 12,
                  fontWeight: FontWeight.bold,
                  color: scheme.onSurfaceVariant,
                ),
              ),
              const SizedBox(height: 8),
              _buildConfigSelector(context),
              const Divider(),
            ],
            Text(
              '操作',
              style: TextStyle(
                fontSize: 12,
                fontWeight: FontWeight.bold,
                color: scheme.onSurfaceVariant,
              ),
            ),
            const SizedBox(height: 8),
            if (stopped)
              ListTile(
                dense: true,
                leading: const Icon(Icons.play_arrow),
                title: const Text('启动'),
                onTap: () {
                  widget.onControl('restart');
                  Navigator.pop(context);
                },
              ),
            if (!stopped)
              ListTile(
                dense: true,
                leading: Icon(
                  Icons.stop,
                  color: widget.stopping ? scheme.onSurfaceVariant : null,
                ),
                title: Text(
                  widget.stopping ? '停止中…' : '停止',
                  style: TextStyle(
                    color: widget.stopping ? scheme.onSurfaceVariant : null,
                  ),
                ),
                enabled: !widget.stopping,
                onTap: () {
                  widget.onControl('stop');
                  Navigator.pop(context);
                },
              ),
            ListTile(
              dense: true,
              leading: const Icon(Icons.refresh),
              title: const Text('重启'),
              onTap: () {
                widget.onControl('restart');
                Navigator.pop(context);
              },
            ),
          ],
        ),
      ),
    );
  }
}

class _CollapsibleBubble extends StatefulWidget {
  final String header;
  final String content;
  final String? collapsedPreview;
  final IconData icon;
  final Color color;
  final VoidCallback? onToggle;

  const _CollapsibleBubble({
    required this.header,
    required this.content,
    this.collapsedPreview,
    required this.icon,
    required this.color,
    this.onToggle,
  });

  @override
  State<_CollapsibleBubble> createState() => _CollapsibleBubbleState();
}

class _CollapsibleBubbleState extends State<_CollapsibleBubble> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          CircleAvatar(
            radius: 12,
            backgroundColor: scheme.surfaceContainerHighest,
            child: Icon(widget.icon, size: 14, color: widget.color),
          ),
          const SizedBox(width: 8),
          Flexible(
            child: GestureDetector(
              onTap: () {
                widget.onToggle?.call();
                setState(() => _expanded = !_expanded);
              },
              onLongPress: () {
                if (widget.content.isEmpty) return;
                Clipboard.setData(ClipboardData(text: widget.content));
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text('已复制'),
                    duration: Duration(seconds: 1),
                    behavior: SnackBarBehavior.floating,
                  ),
                );
              },
              child: AnimatedSize(
                duration: const Duration(milliseconds: 200),
                curve: Curves.easeInOut,
                alignment: Alignment.topCenter,
                child: _buildContent(scheme),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildContent(ColorScheme scheme) {
    final preview = widget.collapsedPreview?.trim() ?? '';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLow,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: scheme.outlineVariant.withValues(alpha: 0.3)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              AnimatedRotation(
                turns: _expanded ? 0.25 : 0,
                duration: const Duration(milliseconds: 200),
                child: Icon(Icons.chevron_right, size: 16, color: widget.color),
              ),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  widget.header,
                  style: TextStyle(
                    fontSize: 12,
                    color: scheme.onSurfaceVariant,
                    fontWeight: _expanded ? FontWeight.w500 : FontWeight.normal,
                  ),
                  maxLines: _expanded ? null : 1,
                  overflow: _expanded ? null : TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          if (!_expanded && preview.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(
              preview,
              style: TextStyle(
                fontSize: 12,
                color: scheme.onSurfaceVariant.withValues(alpha: 0.8),
              ),
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
          ],
          if (_expanded) ...[
            const SizedBox(height: 6),
            _MarkdownContent(
              text: widget.content,
              fontSize: 12,
              textColor: scheme.onSurfaceVariant,
            ),
          ],
        ],
      ),
    );
  }
}

class _ActivityCard extends StatelessWidget {
  final String kind;
  final String toolName;
  final String title;
  final String content;

  const _ActivityCard({
    required this.kind,
    required this.toolName,
    required this.title,
    required this.content,
  });

  (IconData, Color) _iconAndColor() {
    final name = toolName.isNotEmpty ? toolName : title;
    switch (toolName) {
      case 'Bash':
        return (Icons.terminal, Colors.blue);
      case 'Read':
        return (Icons.insert_drive_file, Colors.indigo);
      case 'Grep':
        return (Icons.search, Colors.amber);
      case 'Edit':
      case 'Write':
        return (Icons.edit, Colors.orange);
      case 'Agent':
        return (Icons.smart_toy, Colors.purple);
      case 'SendMessage':
        return (Icons.send, Colors.teal);
      case 'TaskCreate':
      case 'TaskUpdate':
      case 'TaskList':
        return (Icons.checklist, Colors.green);
      case 'TodoWrite':
        return (Icons.check_box, Colors.green);
      case 'WebSearch':
        return (Icons.public, Colors.cyan);
      case 'WebFetch':
        return (Icons.download, Colors.cyan);
      case 'NotebookEdit':
        return (Icons.note, Colors.pink);
      case 'Skill':
        return (Icons.auto_awesome, Colors.deepPurple);
      default:
        if (name.toLowerCase().contains('skill'))
          return (Icons.auto_awesome, Colors.deepPurple);
        if (name.toLowerCase().contains('todo'))
          return (Icons.checklist, Colors.green);
        return (Icons.build, Colors.grey);
    }
  }

  @override
  Widget build(BuildContext context) {
    final (icon, color) = _iconAndColor();
    // final isResult = kind == 'tool_result' || kind == 'result';
    final scheme = Theme.of(context).colorScheme;

    Widget? body;
    if (content.isNotEmpty) {
      body = _MarkdownContent(
        text: content,
        fontSize: 12,
        textColor: scheme.onSurfaceVariant,
      );
    }

    String headerText;
    if (toolName.isNotEmpty && title.isNotEmpty) {
      headerText = '$toolName: $title';
    } else if (toolName.isNotEmpty) {
      headerText = toolName;
    } else if (title.isNotEmpty) {
      headerText = title;
    } else {
      headerText = '助手活动';
    }

    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLow,
        borderRadius: BorderRadius.circular(8),
        border: Border(
          left: BorderSide(color: color.withValues(alpha: 0.7), width: 3),
          top: BorderSide(color: scheme.outlineVariant.withValues(alpha: 0.2)),
          right: BorderSide(
            color: scheme.outlineVariant.withValues(alpha: 0.2),
          ),
          bottom: BorderSide(
            color: scheme.outlineVariant.withValues(alpha: 0.2),
          ),
        ),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(icon, size: 14, color: color),
              const SizedBox(width: 6),
              Expanded(
                child: Text(
                  headerText,
                  style: TextStyle(
                    fontSize: 12,
                    fontWeight: FontWeight.w600,
                    color: scheme.onSurface,
                  ),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
              ),
            ],
          ),
          if (body != null) ...[
            const SizedBox(height: 6),
            DefaultTextStyle(
              style: TextStyle(color: scheme.onSurfaceVariant),
              child: body,
            ),
          ],
        ],
      ),
    );
  }
}

class _ActivityBlock extends StatefulWidget {
  final ChatMessage message;
  final VoidCallback? onToggle;
  const _ActivityBlock({required this.message, this.onToggle});

  @override
  State<_ActivityBlock> createState() => _ActivityBlockState();
}

class _ActivityBlockState extends State<_ActivityBlock> {
  bool _expanded = false;

  Color _dominantColor() {
    final items = _parseActivities();
    for (final item in items.reversed) {
      final name = ((item['toolName'] as String?) ?? '').isNotEmpty
          ? item['toolName'] as String
          : (item['title'] as String?) ?? '';
      switch (item['toolName'] as String? ?? '') {
        case 'Bash':
          return Colors.blue;
        case 'Read':
          return Colors.indigo;
        case 'Grep':
          return Colors.amber;
        case 'Edit':
        case 'Write':
          return Colors.orange;
        case 'Agent':
          return Colors.purple;
        case 'SendMessage':
          return Colors.teal;
        case 'TaskCreate':
        case 'TaskUpdate':
        case 'TaskList':
        case 'TodoWrite':
          return Colors.green;
        default:
          if (name.toLowerCase().contains('skill')) return Colors.deepPurple;
      }
    }
    return Colors.grey;
  }

  List<Map<String, dynamic>> _parseActivities() {
    if (widget.message.kind == 'activity_list') {
      return widget.message.activities;
    }
    // Legacy activity_block: split by newline and parse [Name: ...] lines
    final items = <Map<String, dynamic>>[];
    for (final line in widget.message.text.split('\n')) {
      final trimmed = line.trim();
      if (trimmed.isEmpty) continue;
      final match = RegExp(r'^\[(\w+)(?::\s*(.+?))?\]\$').firstMatch(trimmed);
      if (match != null) {
        items.add({
          'kind': 'tool_use',
          'toolName': match.group(1)!,
          'title': (match.group(2) ?? '').trim(),
          'content': '',
        });
      } else {
        items.add({
          'kind': 'activity',
          'toolName': '',
          'title': trimmed,
          'content': '',
        });
      }
    }
    return items;
  }

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final items = _parseActivities();
    final accentColor = _dominantColor();

    final latest = items.isNotEmpty ? items.last : null;
    String preview = '助手活动';
    if (latest != null) {
      final tn = (latest['toolName'] as String?) ?? '';
      final t = (latest['title'] as String?) ?? '';
      preview = tn.isNotEmpty && t.isNotEmpty
          ? '$tn: $t'
          : (tn.isNotEmpty ? tn : t);
    }

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: MainAxisAlignment.start,
        children: [
          CircleAvatar(
            radius: 16,
            backgroundColor: accentColor.withValues(alpha: 0.15),
            child: Icon(Icons.build, size: 18, color: accentColor),
          ),
          const SizedBox(width: 8),
          Flexible(
            child: GestureDetector(
              onTap: () {
                widget.onToggle?.call();
                setState(() => _expanded = !_expanded);
              },
              onLongPress: () {
                final text = widget.message.text;
                if (text.isEmpty) return;
                Clipboard.setData(ClipboardData(text: text));
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(
                    content: Text('已复制'),
                    duration: Duration(seconds: 1),
                    behavior: SnackBarBehavior.floating,
                  ),
                );
              },
              child: AnimatedSize(
                duration: const Duration(milliseconds: 200),
                curve: Curves.easeInOut,
                alignment: Alignment.topCenter,
                child: Container(
                  padding: _expanded
                      ? const EdgeInsets.all(10)
                      : const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                  decoration: BoxDecoration(
                    color: scheme.surfaceContainerLow,
                    borderRadius: BorderRadius.circular(12),
                    border: Border(
                      left: BorderSide(
                        color: accentColor.withValues(alpha: 0.7),
                        width: 3,
                      ),
                      top: BorderSide(
                        color: scheme.outlineVariant.withValues(alpha: 0.2),
                      ),
                      right: BorderSide(
                        color: scheme.outlineVariant.withValues(alpha: 0.2),
                      ),
                      bottom: BorderSide(
                        color: scheme.outlineVariant.withValues(alpha: 0.2),
                      ),
                    ),
                  ),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          AnimatedRotation(
                            turns: _expanded ? 0.25 : 0,
                            duration: const Duration(milliseconds: 200),
                            child: Icon(
                              Icons.chevron_right,
                              size: 16,
                              color: scheme.primary,
                            ),
                          ),
                          const SizedBox(width: 6),
                          Expanded(
                            child: Text(
                              _expanded ? '助手活动' : preview,
                              style: TextStyle(
                                fontSize: 12,
                                color: scheme.onSurfaceVariant,
                                fontWeight: _expanded
                                    ? FontWeight.w600
                                    : FontWeight.normal,
                              ),
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        ],
                      ),
                      if (_expanded) ...[
                        const SizedBox(height: 8),
                        ...items.map(
                          (item) => _ActivityCard(
                            kind: (item['kind'] as String?) ?? 'activity',
                            toolName: (item['toolName'] as String?) ?? '',
                            title: (item['title'] as String?) ?? '',
                            content: (item['content'] as String?) ?? '',
                          ),
                        ),
                      ],
                    ],
                  ),
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _UserImages extends StatefulWidget {
  final List<String> paths;
  const _UserImages({required this.paths});

  @override
  State<_UserImages> createState() => _UserImagesState();
}

class _UserImagesState extends State<_UserImages> {
  final Map<String, Uint8List> _loaded = {};

  @override
  void initState() {
    super.initState();
    _loadImages();
  }

  @override
  void didUpdateWidget(_UserImages oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (widget.paths != oldWidget.paths) {
      _loaded.clear();
      _loadImages();
    }
  }

  Future<void> _loadImages() async {
    final container = ProviderScope.containerOf(context);
    final client = container.read(connectionProvider);
    if (client == null) return;
    for (final p in widget.paths) {
      if (_loaded.containsKey(p)) continue;
      try {
        final result = await client.call('conversation.image', {'path': p});
        if (result is Map && result['data'] is String) {
          final bytes = base64Decode(result['data'] as String);
          if (mounted) {
            setState(() {
              _loaded[p] = bytes;
            });
          }
        }
      } catch (e) {
        debugPrint('load image error: $e');
      }
    }
  }

  void _showPreview(BuildContext context, Uint8List bytes) {
    showDialog(
      context: context,
      builder: (ctx) => Dialog(
        backgroundColor: Colors.black,
        insetPadding: const EdgeInsets.all(12),
        child: InteractiveViewer(
          boundaryMargin: const EdgeInsets.all(20),
          minScale: 0.5,
          maxScale: 4.0,
          child: Image.memory(bytes, fit: BoxFit.contain),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (_loaded.isEmpty) {
      return Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(
            Icons.image,
            size: 16,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
          const SizedBox(width: 4),
          Text(
            widget.paths.length == 1 ? '图片附件' : '${widget.paths.length} 张图片',
            style: TextStyle(
              fontSize: 12,
              color: Theme.of(context).colorScheme.onSurfaceVariant,
            ),
          ),
        ],
      );
    }
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: _loaded.entries.map((entry) {
        return GestureDetector(
          onTap: () => _showPreview(context, entry.value),
          child: ClipRRect(
            borderRadius: BorderRadius.circular(8),
            child: Image.memory(
              entry.value,
              width: 64,
              height: 64,
              fit: BoxFit.cover,
            ),
          ),
        );
      }).toList(),
    );
  }
}

class MessageBubble extends StatelessWidget {
  final ChatMessage message;
  final bool isLastAssistant;
  final bool showTimestamp;
  final Future<void> Function()? onResolvePermissionPrompt;
  final Future<void> Function(String requestId, String behavior)?
  onPermissionResponse;
  final VoidCallback? onToggleExpand;
  final void Function(String content)? onSend;

  const MessageBubble({
    required this.message,
    this.isLastAssistant = false,
    this.showTimestamp = false,
    this.onResolvePermissionPrompt,
    this.onPermissionResponse,
    this.onToggleExpand,
    this.onSend,
  });

  void _showCopyMenu(BuildContext context, String text) {
    if (text.isEmpty) return;
    Clipboard.setData(ClipboardData(text: text));
    ScaffoldMessenger.of(context).showSnackBar(
      const SnackBar(
        content: Text('已复制'),
        duration: Duration(seconds: 1),
        behavior: SnackBarBehavior.floating,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    // Render structured permission_request card (kind == 'permission_request').
    if (message.kind == 'permission_request' &&
        message.permissionRequest != null) {
      return PermissionRequestCard(
        permissionRequest: message.permissionRequest!,
        onPermissionResponse: onPermissionResponse,
      );
    }

    // Render ask_user_question card.
    if (message.kind == 'ask_user_question' &&
        message.askUserQuestion != null) {
      return AskUserQuestionCard(
        payload: message.askUserQuestion!,
        onSend: onSend,
      );
    }

    // Render exit_plan_mode card.
    if (message.kind == 'exit_plan_mode' && message.exitPlanMode != null) {
      return ExitPlanModeCard(
        payload: message.exitPlanMode!,
        onSend: onSend,
      );
    }


    if (message.isPermissionPrompt) {
      final scheme = Theme.of(context).colorScheme;
      return Padding(
        padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
        child: Card(
          elevation: 0,
          color: scheme.secondaryContainer.withOpacity(0.6),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
            side: BorderSide(color: scheme.secondary.withOpacity(0.3)),
          ),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.security, size: 20, color: scheme.secondary),
                    const SizedBox(width: AppSpacing.sm),
                    Text(
                      '权限确认',
                      style: AppTextStyles.bodySmall.copyWith(
                        fontWeight: FontWeight.w600,
                        color: scheme.onSecondaryContainer,
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 8),
                Text(
                  'Claude 正在等待权限确认。点击下方按钮自动确认并继续。',
                  style: TextStyle(
                    fontSize: 13,
                    color: scheme.onSecondaryContainer.withOpacity(0.9),
                  ),
                ),
                const SizedBox(height: 12),
                Align(
                  alignment: Alignment.centerRight,
                  child: FilledButton.icon(
                    onPressed: onResolvePermissionPrompt,
                    icon: const Icon(Icons.check_circle, size: 18),
                    label: const Text('确认并继续'),
                  ),
                ),
              ],
            ),
          ),
        ),
      );
    }

    final isUser = message.role == 'user';
    final isRaw = message.isRaw;
    final scheme = Theme.of(context).colorScheme;

    // Collapsible thinking block
    if (message.isThinking) {
      // 提取内容预览（60字符），直接放在标题中
      String contentPreview = '';
      final firstLine = message.text
          .split('\n')
          .firstWhere((line) => line.trim().isNotEmpty, orElse: () => '');
      if (firstLine.isNotEmpty) {
        final trimmed = firstLine.trim();
        contentPreview = trimmed.length > 60
            ? trimmed.substring(0, 60)
            : trimmed;
      }
      final header = contentPreview.isNotEmpty
          ? '💭 $contentPreview'
          : '💭 思考过程';
      return _CollapsibleBubble(
        header: header,
        content: message.text,
        collapsedPreview: '', // header 已包含预览，不需要重复
        icon: Icons.psychology,
        color: Colors.orange.shade700,
        onToggle: onToggleExpand,
      );
    }

    if (message.isActivityBlock) {
      return _ActivityBlock(message: message, onToggle: onToggleExpand);
    }

    // Collapsible tool call
    if (message.isToolCall) {
      final toolName = message.text.substring(1, message.text.indexOf(':'));
      // 提取参数作为标题的一部分
      String params = '';
      final paramMatch = RegExp(r':\s*(.+?)(?:\]|$)').firstMatch(message.text);
      if (paramMatch != null) {
        params = paramMatch.group(1)?.trim() ?? '';
        if (params.length > 50) {
          params = params.substring(0, 50) + '...';
        }
      }
      final header = params.isNotEmpty
          ? '🔧 $toolName: $params'
          : '🔧 $toolName';
      return _CollapsibleBubble(
        header: header,
        content: message.text,
        collapsedPreview: '', // header 已包含参数预览
        icon: Icons.build,
        color: scheme.primary,
        onToggle: onToggleExpand,
      );
    }

    // Raw ANSI output: dark terminal-like background
    final isDark = Theme.of(context).brightness == Brightness.dark;
    final bgColor = isUser
        ? scheme.primaryContainer
        : isRaw
        ? (isDark ? const Color(0xFF1A1A2E) : const Color(0xFF1E1E2E))
        : scheme.surfaceContainerHighest;

    final textColor = isUser
        ? scheme.onPrimaryContainer
        : isRaw
        ? const Color(0xFFE5E5E5)
        : scheme.onSurface;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 2),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: isUser
            ? MainAxisAlignment.end
            : MainAxisAlignment.start,
        children: [
          if (!isUser) ...[
            CircleAvatar(
              radius: isRaw ? 12 : 16,
              backgroundColor: isRaw
                  ? const Color(0xFF1A1A2E)
                  : scheme.primaryContainer,
              child: Icon(
                isRaw ? Icons.terminal : Icons.smart_toy,
                size: isRaw ? 14 : 18,
                color: isRaw
                    ? const Color(0xFF23D18B)
                    : scheme.onPrimaryContainer,
              ),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
            child: GestureDetector(
              onLongPress: () => _showCopyMenu(context, message.text),
              child: Container(
                padding: EdgeInsets.symmetric(
                  horizontal: isRaw ? 10 : 14,
                  vertical: isRaw ? 6 : 10,
                ),
                decoration: BoxDecoration(
                  color: bgColor,
                  borderRadius: BorderRadius.only(
                    topLeft: const Radius.circular(12),
                    topRight: const Radius.circular(12),
                    bottomLeft: Radius.circular(isUser ? 12 : (isRaw ? 4 : 4)),
                    bottomRight: Radius.circular(isUser ? (isRaw ? 4 : 4) : 12),
                  ),
                  border: isRaw
                      ? Border.all(
                          color: const Color(0xFF23D18B).withValues(alpha: 0.2),
                          width: 1,
                        )
                      : (isLastAssistant
                            ? Border.all(
                                color: scheme.primary.withValues(alpha: 0.5),
                                width: 2,
                              )
                            : null),
                ),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (isRaw)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 2),
                        child: Text(
                          'Terminal',
                          style: TextStyle(
                            fontSize: 10,
                            color: const Color(
                              0xFF23D18B,
                            ).withValues(alpha: 0.7),
                            fontStyle: FontStyle.italic,
                          ),
                        ),
                      ),
                    if (isUser && message.images.isNotEmpty)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 6),
                        child: _UserImages(paths: message.images),
                      )
                    else if (isUser && message.imageCount > 0)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 6),
                        child: Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(
                              Icons.image,
                              size: 16,
                              color: textColor.withValues(alpha: 0.8),
                            ),
                            const SizedBox(width: 4),
                            Text(
                              message.imageCount == 1
                                  ? '图片附件'
                                  : '${message.imageCount} 张图片',
                              style: TextStyle(
                                fontSize: 12,
                                color: textColor.withValues(alpha: 0.8),
                              ),
                            ),
                          ],
                        ),
                      ),
                    _MarkdownContent(
                      text: message.text,
                      fontSize: isRaw ? 12 : 14,
                      textColor: textColor,
                      isRaw: isRaw,
                    ),
                    if (showTimestamp && message.timestamp != null)
                      Padding(
                        padding: const EdgeInsets.only(top: 2, right: 4, left: 4),
                        child: Text(
                          _formatMessageTime(message.timestamp!),
                          style: TextStyle(
                            fontSize: 10,
                            color: Colors.grey.shade500,
                          ),
                        ),
                      ),
                  ],
                ),
              ),
            ),
          ),
          if (isUser) ...[
            const SizedBox(width: 8),
            CircleAvatar(
              radius: 16,
              backgroundColor: scheme.primary,
              child: Icon(Icons.person, size: 18, color: scheme.onPrimary),
            ),
          ],
        ],
      ),
    );
  }
}

class _ExpandableCodeBuilder extends MarkdownElementBuilder {
  final double fontSize;
  final bool isDark;
  final bool isNaive;
  _ExpandableCodeBuilder({
    required this.fontSize,
    required this.isDark,
    this.isNaive = false,
  });

  @override
  Widget visitElementAfterWithContext(
    BuildContext context,
    md.Element element,
    _,
    __,
  ) {
    final code = element.textContent;
    final lines = '\n'.allMatches(code).length + 1;
    final collapsedLines = 8;
    if (lines <= collapsedLines) {
      return _codeBlock(code, fontSize, isDark, isNaive: isNaive);
    }
    return _ExpandableCodeBlock(
      code: code,
      fontSize: fontSize,
      isDark: isDark,
      collapsedLines: collapsedLines,
      isNaive: isNaive,
    );
  }
}

class _ExpandableCodeBlock extends StatefulWidget {
  final String code;
  final double fontSize;
  final bool isDark;
  final int collapsedLines;
  final bool isNaive;
  const _ExpandableCodeBlock({
    required this.code,
    required this.fontSize,
    required this.isDark,
    required this.collapsedLines,
    this.isNaive = false,
  });

  @override
  State<_ExpandableCodeBlock> createState() => _ExpandableCodeBlockState();
}

class _ExpandableCodeBlockState extends State<_ExpandableCodeBlock> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final collapsedCode = _expanded
        ? widget.code
        : widget.code.split('\n').take(widget.collapsedLines).join('\n');

    return Container(
      decoration: BoxDecoration(
        color: widget.isDark
            ? const Color(0xFF282C34)
            : const Color(0xFFF6F8FA),
        borderRadius: BorderRadius.circular(8),
      ),
      margin: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.all(12),
            child: widget.isNaive
                ? Text(
                    collapsedCode,
                    style: TextStyle(
                      fontFamily: 'Noto Sans SC',
                      fontFamilyFallback: const ['Noto Sans SC'],
                      fontSize: widget.fontSize,
                      height: 1.4,
                      fontWeight: FontWeight.w500,
                      color: widget.isDark
                          ? Colors.grey.shade300
                          : const Color(0xFF1F2328),
                    ),
                  )
                : Text.rich(
                    TextSpan(
                      children: [
                        highlightCode(collapsedCode, isDark: widget.isDark),
                      ],
                      style: TextStyle(
                        fontFamily: 'Noto Sans SC',
                        fontFamilyFallback: const ['Noto Sans SC'],
                        fontSize: widget.fontSize,
                        height: 1.4,
                        fontWeight: FontWeight.w500,
                      ),
                    ),
                  ),
          ),
          Material(
            color: Colors.transparent,
            child: InkWell(
              onTap: () => setState(() => _expanded = !_expanded),
              child: Container(
                padding: const EdgeInsets.symmetric(vertical: 6),
                decoration: BoxDecoration(
                  border: Border(top: BorderSide(color: scheme.outlineVariant)),
                ),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    Icon(
                      _expanded ? Icons.expand_less : Icons.expand_more,
                      size: 16,
                      color: scheme.onSurfaceVariant,
                    ),
                    const SizedBox(width: 4),
                    Text(
                      _expanded ? '收起' : '展开全部',
                      style: TextStyle(
                        fontSize: 12,
                        color: scheme.onSurfaceVariant,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ],
      ),
    );
  }
}

Widget _codeBlock(
  String code,
  double fontSize,
  bool isDark, {
  bool isNaive = false,
}) {
  // GitHub-style code block: elev surface + 1px border outline + JetBrains
  // Mono. Body text uses a high-contrast on-surface colour rather than the
  // legacy off-grey so dense code stays legible.
  final codeColor = isDark ? AppColors.textDark : AppColors.textLight;
  final blockBg = isDark ? AppColors.elevDark : AppColors.elevLight;
  final blockBorder = isDark ? AppColors.borderDark : AppColors.borderLight;
  final Widget textWidget;
  if (isNaive) {
    textWidget = Text(
      code,
      style: TextStyle(
        fontFamily: AppTextStyles.monoFontFamily,
        fontFamilyFallback: const ['Noto Sans SC'],
        fontSize: fontSize,
        height: 1.4,
        fontWeight: FontWeight.w500,
        color: codeColor,
      ),
    );
  } else {
    textWidget = Text.rich(
      TextSpan(
        children: [highlightCode(code, isDark: isDark)],
        style: TextStyle(
          fontFamily: AppTextStyles.monoFontFamily,
          fontFamilyFallback: const ['Noto Sans SC'],
          fontSize: fontSize,
          height: 1.4,
          fontWeight: FontWeight.w500,
          color: codeColor,
        ),
      ),
    );
  }
  return Container(
    decoration: BoxDecoration(
      color: blockBg,
      border: Border.all(color: blockBorder, width: 1),
      borderRadius: BorderRadius.circular(8),
    ),
    margin: const EdgeInsets.symmetric(vertical: 4),
    padding: const EdgeInsets.all(12),
    child: textWidget,
  );
}

/// Markdown content widget that renders into a single SelectableText
/// so that the entire response is selectable across paragraphs.
/// Falls back to MarkdownBody only for complex content (code blocks, tables).
class _MarkdownContent extends ConsumerWidget {
  final String text;
  final double fontSize;
  final Color textColor;
  final bool isRaw;

  const _MarkdownContent({
    required this.text,
    required this.fontSize,
    required this.textColor,
    this.isRaw = false,
  });

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final scheme = Theme.of(context).colorScheme;
    final isNaive = ref.watch(colorModeProvider) == ColorMode.naive;

    if (isRaw) {
      if (isNaive) {
        return Text(
          text,
          style: TextStyle(
            fontSize: fontSize,
            color: textColor,
            height: 1.4,
            fontWeight: FontWeight.w500,
          ),
        );
      }
      return Text.rich(
        parseAnsiToSpan(text, defaultColor: textColor, fontSize: fontSize),
      );
    }

    // Use MarkdownBody for complex markdown (code blocks, tables, headings).
    // Simple text (most assistant responses) gets SelectableText for full selection.
    final hasComplexMarkdown =
        text.contains('```') ||
        text.contains(RegExp(r'^#{1,6}\s', multiLine: true)) ||
        text.contains(RegExp(r'^\|', multiLine: true));

    if (hasComplexMarkdown) {
      final isDark = Theme.of(context).brightness == Brightness.dark;
      return MarkdownBody(
        data: text,
        builders: {
          'pre': _ExpandableCodeBuilder(
            fontSize: fontSize,
            isDark: isDark,
            isNaive: isNaive,
          ),
        },
        styleSheet: MarkdownStyleSheet(
          p: TextStyle(
            fontSize: fontSize,
            color: textColor,
            height: 1.4,
            fontWeight: FontWeight.w500,
          ),
          h1: TextStyle(
            fontSize: fontSize + 8,
            fontWeight: FontWeight.bold,
            color: textColor,
            height: 1.4,
          ),
          h2: TextStyle(
            fontSize: fontSize + 6,
            fontWeight: FontWeight.bold,
            color: textColor,
            height: 1.4,
          ),
          h3: TextStyle(
            fontSize: fontSize + 4,
            fontWeight: FontWeight.bold,
            color: textColor,
            height: 1.4,
          ),
          h4: TextStyle(
            fontSize: fontSize + 2,
            fontWeight: FontWeight.bold,
            color: textColor,
            height: 1.4,
          ),
          code: TextStyle(
            fontFamily: AppTextStyles.monoFontFamily,
            fontFamilyFallback: const ['Noto Sans SC'],
            fontSize: fontSize,
            fontWeight: FontWeight.w500,
            color: isNaive
                ? textColor
                : (isDark
                    ? AppColors.onAccentContainerDark
                    : AppColors.onAccentContainerLight),
            backgroundColor: isDark ? AppColors.elevDark : AppColors.elevLight,
          ),
          codeblockDecoration: BoxDecoration(
            color: isDark ? AppColors.elevDark : AppColors.elevLight,
            border: Border.all(
              color: isDark ? AppColors.borderDark : AppColors.borderLight,
              width: 1,
            ),
            borderRadius: BorderRadius.circular(8),
          ),
          codeblockPadding: const EdgeInsets.all(12),
          blockquote: TextStyle(
            fontSize: fontSize,
            color: textColor.withValues(alpha: 0.8),
            fontStyle: FontStyle.italic,
            height: 1.4,
          ),
          blockquoteDecoration: BoxDecoration(
            border: Border(
              left: BorderSide(
                color: scheme.primary.withValues(alpha: 0.5),
                width: 4,
              ),
            ),
          ),
          blockquotePadding: const EdgeInsets.only(left: 12),
          listBullet: TextStyle(fontSize: fontSize, color: textColor),
          a: TextStyle(
            fontSize: fontSize,
            color: scheme.primary,
            decoration: TextDecoration.underline,
          ),
        ),
        onTapLink: (text, href, title) async {
          if (href == null) return;
          final uri = Uri.tryParse(href);
          if (uri != null) {
            await launchUrl(uri, mode: LaunchMode.externalApplication);
          }
        },
      );
    }

    // Simple text: render inline markdown (bold, italic, inline code) into TextSpans.
    final isDarkSimple = Theme.of(context).brightness == Brightness.dark;
    return Text.rich(
      _buildTextSpan(text, fontSize, textColor, scheme, isDarkSimple),
    );
  }

  TextSpan _buildTextSpan(
    String data,
    double fontSize,
    Color color,
    ColorScheme scheme,
    bool isDark,
  ) {
    final spans = <TextSpan>[];
    // Process inline markdown: **bold**, *italic*, `code`, [links](url)
    final regex = RegExp(
      r'(\*\*(.+?)\*\*)|(\*(.+?)\*)|(`([^`]+)`)|(\[(.+?)\]\((.+?)\))',
    );
    var lastEnd = 0;

    for (final match in regex.allMatches(data)) {
      // Add plain text before this match
      if (match.start > lastEnd) {
        spans.add(TextSpan(text: data.substring(lastEnd, match.start)));
      }

      if (match[2] != null) {
        // **bold**
        spans.add(
          TextSpan(
            text: match[2]!,
            style: TextStyle(fontWeight: FontWeight.bold),
          ),
        );
      } else if (match[4] != null) {
        // *italic*
        spans.add(
          TextSpan(
            text: match[4]!,
            style: TextStyle(fontStyle: FontStyle.italic),
          ),
        );
      } else if (match[6] != null) {
        // `code`
        spans.add(
          TextSpan(
            text: match[6]!,
            style: TextStyle(
              fontFamily: 'Noto Sans SC',
              fontFamilyFallback: const ['Noto Sans SC'],
              fontSize: fontSize - 1,
              color: isDark ? const Color(0xFF98C379) : const Color(0xFF0550AE),
              backgroundColor: null,
            ),
          ),
        );
      } else if (match[8] != null) {
        // [link](url)
        spans.add(
          TextSpan(
            text: match[8]!,
            style: TextStyle(
              color: scheme.primary,
              decoration: TextDecoration.underline,
            ),
          ),
        );
      }

      lastEnd = match.end;
    }

    // Add remaining plain text
    if (lastEnd < data.length) {
      spans.add(TextSpan(text: data.substring(lastEnd)));
    }

    return TextSpan(
      style: TextStyle(fontSize: fontSize, color: color, height: 1.4),
      children: spans.isEmpty ? [TextSpan(text: data)] : spans,
    );
  }
}

class _KeyChip extends StatelessWidget {
  final String? label;
  final IconData? icon;
  final VoidCallback onTap;
  const _KeyChip({this.label, this.icon, required this.onTap})
      : assert(label != null || icon != null,
            '_KeyChip requires either a label or an icon');
  @override
  Widget build(BuildContext context) {
    final Widget child = icon != null
        ? Icon(icon, size: 18, key: Key('keychip-${icon!.codePoint.toRadixString(16)}'))
        : Text(label!);
    return ActionChip(label: child, onPressed: onTap);
  }
}

class _KeyButton extends StatelessWidget {
  final String label;
  final String displayLabel;
  final VoidCallback onTap;
  final bool isArrow;
  final bool enabled;

  const _KeyButton({
    required this.label,
    required this.onTap,
    this.displayLabel = '',
    this.isArrow = false,
    this.enabled = true,
  });

  @override
  Widget build(BuildContext context) {
    final display = displayLabel.isNotEmpty ? displayLabel : label;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: enabled ? onTap : null,
        borderRadius: BorderRadius.circular(8),
        child: Opacity(
          opacity: enabled ? 1 : 0.45,
          child: Container(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
            decoration: BoxDecoration(
              color: isArrow
                  ? (isDark
                        ? Colors.blue.shade900.withValues(alpha: 0.3)
                        : Colors.blue.shade50)
                  : (isDark ? Colors.grey.shade800 : Colors.grey.shade100),
              borderRadius: BorderRadius.circular(8),
              border: Border.all(
                color: isArrow
                    ? (isDark ? Colors.blue.shade700 : Colors.blue.shade300)
                    : (isDark ? Colors.grey.shade700 : Colors.grey.shade300),
              ),
            ),
            child: Text(
              display,
              style: AppTextStyles.bodyMedium.copyWith(
                fontWeight: FontWeight.w500,
                color: isArrow
                    ? (isDark ? Colors.blue.shade300 : Colors.blue.shade700)
                    : Theme.of(context).colorScheme.onSurface,
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// Maps special keys to display-friendly representations.
String keyToDisplay(String key) {
  switch (key.toLowerCase()) {
    case 'up':
      return '↑';
    case 'down':
      return '↓';
    case 'left':
      return '←';
    case 'right':
      return '→';
    case 'enter':
      return '⏎';
    case 'esc':
      return 'ESC';
    case 'tab':
      return 'TAB';
    case 'backspace':
      return '⌫';
    case 'ctrl_c':
      return 'Ctrl+C';
    case 'ctrl_d':
      return 'Ctrl+D';
    case 'ctrl_z':
      return 'Ctrl+Z';
    case 'ctrl_a':
      return 'Ctrl+A';
    case 'ctrl_e':
      return 'Ctrl+E';
    default:
      return key;
  }
}

/// Mode definitions per provider.
class AgentMode {
  final String id;
  final String label;
  final IconData icon;
  const AgentMode({required this.id, required this.label, required this.icon});
}

const kClaudeModes = [
  AgentMode(id: 'bypassPermissions', label: 'Bypass', icon: Icons.build),
  AgentMode(id: 'plan', label: 'Plan', icon: Icons.architecture),
  AgentMode(id: 'auto', label: 'Auto', icon: Icons.auto_mode),
];

const kOpenCodeModes = [
  AgentMode(id: 'plan', label: 'Plan', icon: Icons.architecture),
  AgentMode(id: 'build', label: 'Build', icon: Icons.build),
];

List<AgentMode> modesForProvider(String provider) {
  switch (provider) {
    case 'claude':
      return kClaudeModes;
    case 'opencode':
      return kOpenCodeModes;
    default:
      return [];
  }
}

String _runtimeStateLabel(String? value) {
  switch (value) {
    case 'live':
      return '运行中';
    case 'exited':
      return '已退出';
    case 'stopped':
      return '已停止';
    case 'crashed':
      return '异常退出';
    case 'starting':
      return '启动中';
    default:
      return value ?? '';
  }
}

String _sessionStateLabel(String? value) {
  switch (value) {
    case 'active':
      return '会话活跃';
    case 'standby':
      return '会话待机';
    case 'resumable':
      return '可恢复';
    case 'missing':
      return '会话缺失';
    case 'broken':
      return '会话异常';
    case 'none':
      return '无会话';
    default:
      return value ?? '';
  }
}

String _sessionControlLabel(String? value) {
  switch (value) {
    case 'managed':
      return '已托管';
    case 'attachable':
      return '可附加';
    case 'rebindable':
      return '可重绑';
    case 'read_only':
      return '只读';
    case 'unavailable':
      return '不可接管';
    default:
      return value ?? '';
  }
}

String _providerStateLabel(String? value) {
  switch (value) {
    case 'synced':
      return 'Provider 已同步';
    case 'drifted':
      return 'Provider 漂移';
    case 'unknown':
      return 'Provider 未知';
    default:
      return value ?? '';
  }
}

String _providerScopeLabel(String? value) {
  switch (value) {
    case 'root':
      return 'Root Scope';
    case 'inherited':
      return '继承 Root';
    case 'standalone':
      return '独立 Scope';
    default:
      return value ?? '';
  }
}

bool shouldShowTerminalControls(String provider) {
  return provider != 'claude';
}

bool shouldShowRawToggle(String provider) {
  return shouldShowTerminalControls(provider);
}

bool isReadOnlyAgent(AgentModel? agent) {
  return agent?.isReadOnly ?? false;
}

String readOnlyHintText(AgentModel? agent) {
  if (!isReadOnlyAgent(agent)) return '输入消息…';
  final reason = agent?.readOnlyReason.trim() ?? '';
  if (reason.isNotEmpty) {
    return '只读会话：$reason';
  }
  if (agent?.provider == 'claude') {
    return '只读会话：请回到原 Claude 终端继续输入';
  }
  return '只读会话，无法在此发送消息';
}

/// Permission prompt overlay widget
/// Displays as a floating banner above the input bar, similar to Claude's native UI
class _PermissionPromptOverlay extends StatelessWidget {
  final VoidCallback onResolve;

  const _PermissionPromptOverlay({required this.onResolve});

  @override
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;

    return Container(
      width: double.infinity,
      margin: const EdgeInsets.fromLTRB(12, 0, 12, 8),
      padding: const EdgeInsets.all(16),
      decoration: BoxDecoration(
        color: scheme.secondaryContainer,
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: scheme.secondary.withOpacity(0.3)),
        boxShadow: [
          BoxShadow(
            color: Colors.black.withOpacity(0.1),
            blurRadius: 8,
            offset: const Offset(0, 2),
          ),
        ],
      ),
      child: Row(
        children: [
          Container(
            padding: const EdgeInsets.all(8),
            decoration: BoxDecoration(
              color: scheme.secondary.withOpacity(0.2),
              shape: BoxShape.circle,
            ),
            child: Icon(Icons.security, size: 20, color: scheme.secondary),
          ),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  '权限确认',
                  style: AppTextStyles.bodySmall.copyWith(
                    fontWeight: FontWeight.w600,
                    color: scheme.onSecondaryContainer,
                  ),
                ),
                const SizedBox(height: 4),
                Text(
                  'Claude 需要权限确认才能继续执行',
                  style: TextStyle(
                    fontSize: 13,
                    color: scheme.onSecondaryContainer.withOpacity(0.9),
                  ),
                ),
              ],
            ),
          ),
          const SizedBox(width: 8),
          FilledButton.icon(
            onPressed: onResolve,
            icon: const Icon(Icons.check_circle, size: 18),
            label: const Text('确认'),
          ),
        ],
      ),
    );
  }
}

class SlashCommand {
  final String command;
  final String description;
  const SlashCommand(this.command, this.description);
}

const kSlashCommands = [
  SlashCommand('/help', '获取帮助信息'),
  SlashCommand('/clear', '清除当前对话'),
  SlashCommand('/compact', '压缩对话上下文'),
  SlashCommand('/status', '查看当前状态'),
  SlashCommand('/model', '切换模型'),
  SlashCommand('/cost', '查看 token 使用量'),
  SlashCommand('/doctor', '诊断连接问题'),
  SlashCommand('/review', '审查代码变更'),
  SlashCommand('/init', '初始化 CLAUDE.md'),
  SlashCommand('/terminal-setup', '终端设置'),
  SlashCommand('/memory', '管理记忆'),
  SlashCommand('/bug', '报告 bug'),
  SlashCommand('/login', '登录账户'),
  SlashCommand('/logout', '登出账户'),
  SlashCommand('/fast', '切换快速模式'),
  SlashCommand('/commit', '生成 commit'),
  SlashCommand('/plan', '进入规划模式'),
  SlashCommand('/mcp', '管理 MCP 服务器'),
  SlashCommand('/permissions', '管理权限'),
  SlashCommand('/config', '查看/修改配置'),
  SlashCommand('/btw', '快速补充说明 / by the way'),
  SlashCommand('/undo', '撤销最后一次编辑'),
  SlashCommand('/redo', '重做最后一次编辑'),
  SlashCommand('/pr', '创建 Pull Request'),
  SlashCommand('/search', '搜索代码库'),
  SlashCommand('/explain', '解释选中代码'),
  SlashCommand('/fix', '修复当前文件问题'),
  SlashCommand('/test', '生成或运行测试'),
  SlashCommand('/simplify', '审查并简化代码'),
  SlashCommand('/loop', '循环执行命令'),
  SlashCommand('/skill', '调用内置技能'),
  SlashCommand('/keybindings-help', '键盘快捷键帮助'),
];

class _InputBar extends StatefulWidget {
  final AgentModel? agent;
  final TextEditingController controller;
  final bool loading;
  final bool rawMode;
  final bool showTerminalControls;
  final bool showRawToggle;
  final ValueChanged<bool> onToggleRaw;
  final VoidCallback onSend;
  final Future<void> Function(String key) onKey;
  final List<Map<String, String>> pendingImages;
  final ValueChanged<List<Map<String, String>>> onImagesChanged;
  final List<SlashCommand> extraCommands;
  final String currentMode;
  final bool stopping;
  final String nodeId;
  final Future<void> Function(String action) onControl;
  final Future<void> Function(String model) onSwitchModel;
  final Future<void> Function(String providerId, {VoidCallback? onSwitched})
  onSwitchProvider;
  final Future<void> Function(String mode) onSwitchMode;

  const _InputBar({
    required this.agent,
    required this.controller,
    required this.loading,
    required this.rawMode,
    required this.showTerminalControls,
    required this.showRawToggle,
    required this.onToggleRaw,
    required this.onSend,
    required this.onKey,
    this.pendingImages = const [],
    required this.onImagesChanged,
    this.extraCommands = const [],
    required this.currentMode,
    required this.stopping,
    required this.nodeId,
    required this.onControl,
    required this.onSwitchModel,
    required this.onSwitchProvider,
    required this.onSwitchMode,
  });

  @override
  State<_InputBar> createState() => _InputBarState();
}

class _InputBarState extends State<_InputBar> {
  bool _showSlashMenu = false;
  String _slashFilter = '';
  final _imagePicker = ImagePicker();
  bool _isListening = false;
  String _speechPreview = '';

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onControllerChanged);
  }

  @override
  void didUpdateWidget(_InputBar oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller != widget.controller) {
      oldWidget.controller.removeListener(_onControllerChanged);
      widget.controller.addListener(_onControllerChanged);
    }
  }

  void _showImagePreview(BuildContext context, Uint8List bytes) {
    showDialog(
      context: context,
      builder: (ctx) => Dialog(
        backgroundColor: Colors.black,
        insetPadding: const EdgeInsets.all(12),
        child: InteractiveViewer(
          boundaryMargin: const EdgeInsets.all(20),
          minScale: 0.5,
          maxScale: 4.0,
          child: Image.memory(bytes, fit: BoxFit.contain),
        ),
      ),
    );
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onControllerChanged);
    super.dispose();
  }

  void _onControllerChanged() {
    final text = widget.controller.text;
    if (text.startsWith('/')) {
      final filter = text.substring(1).toLowerCase();
      if (!_showSlashMenu || _slashFilter != filter) {
        setState(() {
          _showSlashMenu = true;
          _slashFilter = filter;
        });
      }
    } else {
      if (_showSlashMenu) {
        setState(() => _showSlashMenu = false);
      }
    }
  }

  List<SlashCommand> get _filteredCommands {
    final all = <SlashCommand>[...kSlashCommands];
    final existing = kSlashCommands.map((c) => c.command).toSet();
    for (final s in widget.extraCommands) {
      if (!existing.contains(s.command)) {
        all.add(s);
        existing.add(s.command);
      }
    }
    if (_slashFilter.isEmpty) return all;
    return all
        .where((c) => c.command.substring(1).contains(_slashFilter))
        .toList();
  }

  /// Bottom sheet of "special keys" (ESC / Ctrl-C / arrow keys / …) that
  /// previously lived behind the right-edge keyboard_hide button. Now
  /// surfaced from the consolidated "+" button on the composer's left.
  void _showSpecialKeysSheet(BuildContext context) {
    showModalBottomSheet(
      context: context,
      backgroundColor: AppColors.inkElev,
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => Container(
        decoration: const BoxDecoration(
          border: Border(
            top: BorderSide(color: AppColors.accent, width: 1),
          ),
        ),
        padding: const EdgeInsets.all(16),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              '特殊按键',
              style: AppTextStyles.titleLarge.copyWith(
                fontSize: 18,
                color: AppColors.accent,
              ),
            ),
            const SizedBox(height: AppSpacing.lg),
            Wrap(
              spacing: 8,
              runSpacing: 8,
              children: [
                ActionChip(
                  label: const Text('ESC'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('esc');
                  },
                ),
                ActionChip(
                  label: const Text('Ctrl+C'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('ctrl_c');
                  },
                ),
                ActionChip(
                  label: const Text('Ctrl+D'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('ctrl_d');
                  },
                ),
                ActionChip(
                  label: const Text('Ctrl+Z'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('ctrl_z');
                  },
                ),
                ActionChip(
                  label: const Text('Ctrl+A'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('ctrl_a');
                  },
                ),
                ActionChip(
                  label: const Text('Ctrl+E'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('ctrl_e');
                  },
                ),
                ActionChip(
                  label: const Text('Tab'),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('tab');
                  },
                ),
                ActionChip(
                  label: const Icon(Icons.arrow_upward, size: 18,
                      key: Key('keychip-arrow_upward')),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('up');
                  },
                ),
                ActionChip(
                  label: const Icon(Icons.arrow_downward, size: 18,
                      key: Key('keychip-arrow_downward')),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('down');
                  },
                ),
                ActionChip(
                  label: const Icon(Icons.arrow_back, size: 18,
                      key: Key('keychip-arrow_back')),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('left');
                  },
                ),
                ActionChip(
                  label: const Icon(Icons.arrow_forward, size: 18,
                      key: Key('keychip-arrow_forward')),
                  onPressed: () {
                    Navigator.pop(ctx);
                    widget.onKey('right');
                  },
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _pickImage() async {
    try {
      if (kIsWeb) {
        // Web: use multi-image picker directly
        final picked = await _imagePicker.pickMultiImage();
        if (picked.isEmpty) return;
        await _processPickedImages(picked);
        return;
      }

      // Mobile: show source selector
      final isMulti = await showModalBottomSheet<bool>(
        context: context,
        builder: (ctx) => SafeArea(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              ListTile(
                leading: const Icon(Icons.camera_alt),
                title: const Text('拍照'),
                onTap: () => Navigator.pop(ctx, false),
              ),
              ListTile(
                leading: const Icon(Icons.photo_library),
                title: const Text('从相册选择（可多选）'),
                onTap: () => Navigator.pop(ctx, true),
              ),
            ],
          ),
        ),
      );
      if (isMulti == null) return;

      if (isMulti) {
        final picked = await _imagePicker.pickMultiImage(
          maxWidth: 1920,
          maxHeight: 1920,
          imageQuality: 85,
        );
        if (picked.isEmpty) return;
        await _processPickedImages(picked);
      } else {
        final picked = await _imagePicker.pickImage(source: ImageSource.camera);
        if (picked == null) return;
        await _processPickedImages([picked]);
      }
    } catch (e, st) {
      debugPrint('pickImage error: $e\n$st');
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('选择图片失败: $e')));
      }
    }
  }

  Future<void> _processPickedImages(List<XFile> files) async {
    final updated = List<Map<String, String>>.from(widget.pendingImages);
    for (final picked in files) {
      final bytes = await picked.readAsBytes();
      final b64 = base64Encode(bytes);
      // Web端未压缩，限制单张图片 base64 后不超过 5MB（约 3.75MB 原始文件）
      if (kIsWeb && b64.length > 5 * 1024 * 1024) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('${picked.name} 过大，已跳过（Web 端限制 3MB）')),
          );
        }
        continue;
      }
      String mime = picked.mimeType ?? '';
      if (mime.isEmpty) {
        mime = _detectMimeType(bytes);
      }
      updated.add({'data': b64, 'mimeType': mime});
    }
    widget.onImagesChanged(updated);
  }

  String _detectMimeType(List<int> bytes) {
    if (bytes.length >= 4) {
      if (bytes[0] == 0x89 &&
          bytes[1] == 0x50 &&
          bytes[2] == 0x4E &&
          bytes[3] == 0x47) {
        return 'image/png';
      }
      if (bytes[0] == 0xFF && bytes[1] == 0xD8 && bytes[2] == 0xFF) {
        return 'image/jpeg';
      }
      if (bytes[0] == 0x47 && bytes[1] == 0x49 && bytes[2] == 0x46) {
        return 'image/gif';
      }
      if (bytes.length >= 12 &&
          bytes[0] == 0x52 &&
          bytes[1] == 0x49 &&
          bytes[2] == 0x46 &&
          bytes[3] == 0x46 &&
          bytes[8] == 0x57 &&
          bytes[9] == 0x45 &&
          bytes[10] == 0x42 &&
          bytes[11] == 0x50) {
        return 'image/webp';
      }
    }
    return 'image/jpeg';
  }

  Future<void> _toggleListening() async {
    // Mic button now works as an "AI optimize" trigger for voice text.
    // Users should speak via the system keyboard IME first, then tap the mic
    // to clean / punctuate / deduplicate the text.
    final currentText = widget.controller.text.trim();

    if (_isListening) return;

    if (currentText.isEmpty) {
      // Prompt user to use keyboard voice input
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('请使用键盘语音输入，完成后再次点击麦克风进行优化')),
        );
      }
      return;
    }

    setState(() {
      _isListening = true;
      _speechPreview = '';
    });

    // Give a tiny delay so the UI shows the loading state
    await Future.delayed(const Duration(milliseconds: 80));

    final optimized = _optimizeVoiceInput(currentText, '');

    if (optimized.isNotEmpty && mounted) {
      widget.controller.text = optimized;
      widget.controller.selection = TextSelection.fromPosition(
        TextPosition(offset: widget.controller.text.length),
      );
    }

    if (mounted) {
      setState(() {
        _isListening = false;
        _speechPreview = '';
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final isReadOnly = isReadOnlyAgent(widget.agent);
    final isClaudePipe = (widget.agent?.provider ?? '') == 'claude';
    // For PTY/tmux providers, loading doesn't block input (PTY accepts concurrent input)
    final effectiveLoading = widget.loading && !isReadOnly && isClaudePipe;
    final readOnlyHint = readOnlyHintText(widget.agent);

    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 12,
        top: 4,
        bottom: MediaQuery.of(context).padding.bottom + 4,
      ),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (isReadOnly)
            Container(
              width: double.infinity,
              margin: const EdgeInsets.only(bottom: 8),
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(12),
                border: Border.all(
                  color: Theme.of(context).colorScheme.outlineVariant,
                ),
              ),
              child: Row(
                children: [
                  Icon(
                    Icons.visibility_off_outlined,
                    size: 18,
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                  const SizedBox(width: 8),
                  Expanded(
                    child: Text(
                      readOnlyHint,
                      style: TextStyle(
                        fontSize: 12,
                        color: Theme.of(context).colorScheme.onSurfaceVariant,
                      ),
                    ),
                  ),
                ],
              ),
            ),
          if (_showSlashMenu && _filteredCommands.isNotEmpty)
            Container(
              constraints: const BoxConstraints(maxHeight: 200),
              margin: const EdgeInsets.only(bottom: 4),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(12),
                border: Border.all(
                  color: Theme.of(context).colorScheme.outlineVariant,
                ),
                boxShadow: [
                  BoxShadow(
                    color: Colors.black.withOpacity(0.1),
                    blurRadius: 8,
                    offset: const Offset(0, -2),
                  ),
                ],
              ),
              child: ListView(
                shrinkWrap: true,
                padding: const EdgeInsets.symmetric(vertical: 4),
                children: _filteredCommands
                    .map(
                      (cmd) => ListTile(
                        dense: true,
                        title: Text(
                          cmd.command,
                          style: AppTextStyles.bodySmall.copyWith(
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                        subtitle: Text(
                          cmd.description,
                          style: const TextStyle(fontSize: 12),
                        ),
                        onTap: () {
                          widget.controller.text = '${cmd.command} ';
                          widget
                              .controller
                              .selection = TextSelection.fromPosition(
                            TextPosition(offset: widget.controller.text.length),
                          );
                          setState(() => _showSlashMenu = false);
                        },
                      ),
                    )
                    .toList(),
              ),
            ),
          // Image preview strip
          if (widget.pendingImages.isNotEmpty)
            SizedBox(
              height: 72,
              child: ListView.separated(
                scrollDirection: Axis.horizontal,
                padding: const EdgeInsets.only(bottom: 6),
                itemCount: widget.pendingImages.length,
                separatorBuilder: (_, __) => const SizedBox(width: 6),
                itemBuilder: (ctx, i) {
                  final bytes = base64Decode(widget.pendingImages[i]['data']!);
                  return Stack(
                    children: [
                      GestureDetector(
                        onTap: () => _showImagePreview(ctx, bytes),
                        child: ClipRRect(
                          borderRadius: BorderRadius.circular(8),
                          child: Image.memory(
                            bytes,
                            width: 64,
                            height: 64,
                            fit: BoxFit.cover,
                          ),
                        ),
                      ),
                      Positioned(
                        top: -4,
                        right: -4,
                        child: GestureDetector(
                          onTap: () {
                            final updated = List<Map<String, String>>.from(
                              widget.pendingImages,
                            )..removeAt(i);
                            widget.onImagesChanged(updated);
                          },
                          child: Container(
                            decoration: const BoxDecoration(
                              color: Colors.black54,
                              shape: BoxShape.circle,
                            ),
                            padding: const EdgeInsets.all(2),
                            child: const Icon(
                              Icons.close,
                              size: 14,
                              color: Colors.white,
                            ),
                          ),
                        ),
                      ),
                    ],
                  );
                },
              ),
            ),
          // Speech preview chip
          if (_isListening && _speechPreview.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 6),
              child: Align(
                alignment: Alignment.centerLeft,
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 12,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: Colors.red.shade50,
                    borderRadius: BorderRadius.circular(16),
                    border: Border.all(color: Colors.red.shade200),
                  ),
                  child: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      Icon(Icons.mic, size: 14, color: Colors.red.shade700),
                      const SizedBox(width: 6),
                      Flexible(
                        child: Text(
                          _speechPreview,
                          style: TextStyle(
                            fontSize: 13,
                            color: Colors.red.shade900,
                          ),
                          maxLines: 2,
                          overflow: TextOverflow.ellipsis,
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
          Row(
            crossAxisAlignment: CrossAxisAlignment.center,
            children: [
              // Mode config button moved out of the composer row and into the
              // screen's AppBar actions slot (see [BypassIndicator]); the
              // composer now only carries the input + send affordances.
              // Consolidated "+" button: opens modal sheet with image + special-keys.
              if (!isReadOnly &&
                  widget.agent?.provider != 'opencode' &&
                  widget.agent?.attachMode != 'tmux')
                ComposerPlusButton(
                  onPickImage: effectiveLoading ? null : _pickImage,
                  onShowSpecialKeys:
                      isReadOnly ? null : () => _showSpecialKeysSheet(context),
                ),
              Expanded(
                child: TextField(
                  controller: widget.controller,
                  enabled: !isReadOnly && !effectiveLoading,
                  decoration: InputDecoration(
                    hintText: readOnlyHint,
                    border: const OutlineInputBorder(
                      borderRadius: BorderRadius.all(Radius.circular(10)),
                    ),
                    prefixIcon: Padding(
                      padding: const EdgeInsets.only(left: 12, right: 8),
                      child: Text(
                        '▸',
                        style: AppTextStyles.monoLarge.copyWith(
                          color: AppColors.accent,
                        ),
                      ),
                    ),
                    prefixIconConstraints: const BoxConstraints(
                      minWidth: 28,
                      minHeight: 0,
                    ),
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: 12,
                      vertical: 8,
                    ),
                    isDense: true,
                  ),
                  textInputAction: TextInputAction.send,
                  onSubmitted: isReadOnly ? null : (_) => widget.onSend(),
                  maxLines: 4,
                  minLines: 1,
                ),
              ),
              const SizedBox(width: 6),
              // Right-side buttons
              Row(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.center,
                children: [
                  effectiveLoading
                      ? const SizedBox(
                          width: 32,
                          height: 32,
                          child: Padding(
                            padding: EdgeInsets.all(4),
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                        )
                      : IconButton(
                          onPressed: isReadOnly ? null : widget.onSend,
                          icon: const Icon(Icons.send, size: 20),
                          visualDensity: VisualDensity.compact,
                          constraints: const BoxConstraints(
                            minWidth: 32,
                            minHeight: 32,
                          ),
                          padding: EdgeInsets.zero,
                          color: isReadOnly
                              ? null
                              : Theme.of(context).colorScheme.primary,
                        ),
                ],
              ),
            ],
          ),
        ],
      ),
    );
  }
}
