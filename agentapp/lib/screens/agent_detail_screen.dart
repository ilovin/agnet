import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';

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

/// Removes terminal drawing characters (box drawing, block elements, etc.)
/// These are used by claude-code for UI elements but clutter the output.
String stripTerminalDrawing(String s) {
  return s
      // Box drawing characters (│─┌┐└┘├┤┬┴┼ etc.)
      .replaceAll(RegExp(r'[\u2500-\u257F]'), '')
      // Block elements (▖▗▘▙▚▛▜▝▞▟█ etc.)
      .replaceAll(RegExp(r'[\u2580-\u259F]'), '')
      // Geometric shapes (▴▵▶▷▸▹►▻▼▽▾▿ etc.)
      .replaceAll(RegExp(r'[\u25A0-\u25FF]'), '')
      // Arrows and special symbols
      .replaceAll(RegExp(r'[\u2190-\u21FF]'), '')
      // Dingbats and other symbols that clutter output
      .replaceAll(RegExp(r'[\u2700-\u27BF]'), '')
      // Replace multiple spaces with single space
      .replaceAll(RegExp(r'  +'), ' ')
      // Clean up excessive newlines
      .replaceAll(RegExp(r'\n{3,}'), '\n\n')
      .trim();
}

/// Chat message model for display
class ChatMessage {
  final String role;
  final String text;
  final int seq;
  final bool isRaw;
  final String kind;
  final bool isPermissionPrompt;

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
  bool get isActivityBlock => role == 'assistant' && kind == 'activity_block';

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
  if (const {'thinking', 'thinking_delta', 'reasoning'}.contains(kind)) return true;
  return ChatMessage._explicitThinkingPrefix.hasMatch(text);
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
  return {
    'seq': map['seq'] ?? 0,
    'role': map['role'] ?? 'assistant',
    'text': map['text'] ?? '',
    'raw': map['raw'] ?? false,
    'kind': map['kind'] ?? '',
    'awaitingPermission': map['awaitingPermission'] ?? false,
    if (map.containsKey('permissionRequest'))
      'permissionRequest': map['permissionRequest'],
  };
}

/// Converts raw events to display messages.
/// Handles both structured stream-json events and legacy PTY raw output.
/// Merges consecutive assistant text_delta fragments into complete messages.
/// Collapses consecutive tool/read activity into a stable block.
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

  // Buffer for grouping consecutive tool activities into a single stable block
  final activityBuf = StringBuffer();
  int activitySeq = 0;

  // Buffer for grouping consecutive thinking events into a single block
  final thinkingBuf = StringBuffer();
  int thinkingSeq = 0;

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
        ),
      );
    }
    mergeBufKind = '';
  }

  void flushActivityBuf() {
    if (activityBuf.isEmpty) return;
    final activityText = activityBuf.toString().trim();
    activityBuf.clear();
    if (activityText.isNotEmpty) {
      messages.add(
        ChatMessage(
          role: 'assistant',
          text: activityText,
          seq: activitySeq,
          kind: 'activity_block',
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

    // Handle permission prompts - don't add to message list, handled by overlay
    if (kind == 'permission_prompt') {
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
      if (!isNoiseOnlyText(cleaned)) {
        messages.add(
          ChatMessage(
            role: role,
            text: cleaned,
            seq: seq,
            isRaw: raw,
            kind: kind,
          ),
        );
      }
      continue;
    }

    // Group consecutive tool/read/use activity events into one block.
    if (_isToolActivityEvent(role: role, kind: kind, text: cleaned, raw: raw)) {
      flushMergeBuf();
      flushThinkingBuf();
      if (!isNoiseOnlyText(cleaned) && cleaned.isNotEmpty) {
        if (activityBuf.isEmpty) {
          activitySeq = seq;
          activityBuf.write(cleaned);
        } else {
          final existing = activityBuf.toString();
          if (!existing.endsWith(cleaned)) {
            activityBuf.write('\n');
            activityBuf.write(cleaned);
          }
        }
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
    if (kind == 'text_delta') {
      flushActivityBuf();
      flushThinkingBuf();
      hadTextDelta = true;
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
        // Flush when we see sentence-ending content or significant length
        if (cleaned.contains('？') ||
            cleaned.contains('。') ||
            cleaned.contains('！') ||
            cleaned.endsWith('?') ||
            cleaned.endsWith('.') ||
            cleaned.endsWith('!') ||
            cleaned.endsWith('\n') ||
            mergeBuf.length > 200) {
          flushMergeBuf();
        }
      }
      continue;
    }

    // Non-fragment assistant message (complete message from stream-json).
    // If we already received text_delta fragments for this turn, the content
    // was already flushed via mergeBuf — skip to avoid duplicates.
    // Also handle legacy events where kind is empty.
    if (role == 'assistant' && !raw && hadTextDelta &&
        (kind == 'text' || kind == '' || kind == 'assistant')) {
      // Reset delta flag for the next turn; flush any remaining delta buffer
      flushMergeBuf();
      hadTextDelta = false;
      continue;
    }
    flushMergeBuf();
    flushActivityBuf();
    flushThinkingBuf();
    hadTextDelta = false;
    if (isNoiseOnlyText(cleaned)) continue;
    messages.add(
      ChatMessage(
        role: role,
        text: cleaned,
        seq: seq,
        isRaw: false,
        kind: kind,
      ),
    );
  }

  // Flush any remaining buffers
  flushMergeBuf();
  flushActivityBuf();
  flushThinkingBuf();

  return messages;
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
  String? _lastError;
  String? _agentName; // local override for renamed agent

  // Raw events from EventBuffer
  List<Map<String, dynamic>> _rawEvents = [];
  int _lastSeq = 0;
  int _oldestSeq = 0;
  Timer? _pollTimer;
  bool _pollingNewEvents = false;

  // Permission handling mode (mobile-friendly option)
  // true = auto-resolve (default for mobile), false = manual confirmation
  bool _autoResolvePermissions = true;

  // Current agent mode (e.g. bypassPermissions, plan)
  String _currentMode = 'bypassPermissions';

  @override
  void initState() {
    super.initState();
    _scrollCtrl.addListener(_handleScroll);
    _loadHistory();
    // Poll every 1s for new events
    _pollTimer = Timer.periodic(
      const Duration(seconds: 1),
      (_) => _pollNewEvents(),
    );
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _scrollCtrl.removeListener(_handleScroll);
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  void _handleScroll() {
    if (!_scrollCtrl.hasClients) return;
    final distance = _scrollCtrl.position.maxScrollExtent - _scrollCtrl.offset;
    final shouldStick = distance < 120;
    final showJump = !shouldStick;
    if (shouldStick != _stickToBottom || showJump != _showJumpToLatest) {
      setState(() {
        _stickToBottom = shouldStick;
        _showJumpToLatest = showJump;
      });
    }
  }

  Future<void> _loadHistory() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'limit': 200,
      });
      final raw = result is Map ? result : <String, dynamic>{};
      final events = (raw['events'] as List?) ?? [];
      final lastSeq = (raw['lastSeq'] as num?)?.toInt() ?? 0;
      final firstSeqFromResp = (raw['firstSeq'] as num?)?.toInt() ?? 0;
      if (mounted) {
        setState(() {
          _rawEvents = events
              .map((e) => normalizeHistoryEvent(e as Map))
              .toList();
          _lastSeq = lastSeq;
          if (_rawEvents.isNotEmpty) {
            _oldestSeq = firstSeqFromResp > 0
                ? firstSeqFromResp
                : ((_rawEvents.first['seq'] as num?)?.toInt() ?? 0);
            _hasMoreHistory = _oldestSeq > 1;
          } else {
            _oldestSeq = 0;
            _hasMoreHistory = false;
          }
          _initialLoading = false;
          _lastError = null;
        });
        _scrollToBottom(force: true);
      }
    } catch (e) {
      debugPrint('loadHistory error: $e');
      if (mounted) {
        setState(() {
          _initialLoading = false;
          _lastError = '加载历史失败，请重试';
        });
      }
    }
  }

  Future<void> _loadOlderHistory() async {
    if (_loadingOlder || !_hasMoreHistory || _oldestSeq <= 1) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;

    final prevOffset = _scrollCtrl.hasClients ? _scrollCtrl.offset : 0.0;
    final prevMax = _scrollCtrl.hasClients
        ? _scrollCtrl.position.maxScrollExtent
        : 0.0;

    setState(() {
      _loadingOlder = true;
    });

    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'before': _oldestSeq,
        'limit': 200,
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
        _hasMoreHistory = _oldestSeq > 1 && older.length >= 200;
        _loadingOlder = false;
      });

      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!_scrollCtrl.hasClients) return;
        final newMax = _scrollCtrl.position.maxScrollExtent;
        final delta = newMax - prevMax;
        final target = (prevOffset + delta).clamp(
          _scrollCtrl.position.minScrollExtent,
          _scrollCtrl.position.maxScrollExtent,
        );
        _scrollCtrl.jumpTo(target);
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loadingOlder = false;
        _lastError = '加载更多历史失败，请重试';
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
        debugPrint('seq regression detected: server=$lastSeq local=$_lastSeq, reloading history');
        _pollingNewEvents = false;
        setState(() {
          _rawEvents.clear();
          _lastSeq = 0;
          _oldestSeq = 0;
          _hasMoreHistory = true;
          _initialLoading = true;
        });
        _loadHistory();
        return;
      }

      if (events.isNotEmpty) {
        // Deduplicate by seq before appending
        final existingSeqs = _rawEvents
            .map((e) => (e['seq'] as num?)?.toInt() ?? 0)
            .toSet();
        final newEvents = events
            .map((e) => normalizeHistoryEvent(e as Map))
            .where(
              (e) => !existingSeqs.contains((e['seq'] as num?)?.toInt() ?? 0),
            )
            .toList();
        if (newEvents.isNotEmpty) {
          setState(() {
            _rawEvents.addAll(newEvents);
            _lastSeq = lastSeq;
            if (_oldestSeq == 0 && _rawEvents.isNotEmpty) {
              _oldestSeq = ((_rawEvents.first['seq'] as num?)?.toInt() ?? 0);
              _hasMoreHistory = _oldestSeq > 1;
            }
            _lastError = null;
          });
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
          });
        }
      }
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _lastError = '拉取新消息失败，稍后重试';
      });
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

  Future<void> _sendMessage() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty) return;
    final nodeState0 = ref.read(nodesProvider);
    final agents0 = nodeState0.agentsFor(widget.nodeId);
    final agent0 = agents0.where((a) => a.id == widget.agentId).firstOrNull;
    // Claude -p mode doesn't support concurrent input; PTY/tmux modes do
    final isClaudePipe = (agent0?.provider ?? '') == 'claude';
    if (_loading && isClaudePipe) return;
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
    setState(() {
      _loading = true;
      _lastError = null;
    });
    try {
      final result = await client.call('conversation.send', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'message': text,
        'raw': _rawMode,
      });

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
        setState(() {
          _lastError = '发送失败，请重试';
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
    setState(() => _currentMode = mode);
    try {
      final result = await client.call('agent.restart', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'permissionMode': mode,
      });
      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      // Agent ID stays the same — just refresh the agent list
      final listResult = await client.call('agent.list', {
        'nodeId': widget.nodeId,
      });
      final agents = (listResult is List
          ? listResult
          : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);
    } catch (e) {
      debugPrint('switchMode error: $e');
    }
  }

  String _modeLabel(String modeId) {
    final allModes = [...kClaudeModes, ...kOpenCodeModes];
    return allModes
        .firstWhere((m) => m.id == modeId, orElse: () => AgentMode(id: modeId, label: modeId, icon: Icons.help))
        .label;
  }

  Future<void> _switchProvider(String providerId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    // Check if this is a cc-switch provider ID (not a legacy 'claude'/'claude-bedrock' value).
    // cc-switch provider IDs are typically numeric strings or UUIDs, not the legacy names.
    final isLegacyProvider = providerId == 'claude' ||
        providerId == 'claude-bedrock' ||
        providerId == 'claude-vertex';

    try {
      if (!isLegacyProvider) {
        // Use provider.switch to update ~/.claude/settings.json via cc-switch
        await client.call('provider.switch', {
          'nodeId': widget.nodeId,
          'providerId': providerId,
        });
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            SnackBar(content: Text('已切换到 $providerId')),
          );
        }
        return;
      }

      // Legacy path: restart agent with provider env var
      final result = await client.call('agent.restart', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'provider': providerId,
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
      debugPrint('switchProvider error: $e');
    }
  }

  void _scrollToBottom({bool force = false}) {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scrollCtrl.hasClients) return;
      if (!force && !_stickToBottom) return;
      _scrollCtrl.animateTo(
        _scrollCtrl.position.maxScrollExtent,
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

    final messages = convertEventsToMessages(_rawEvents);
    final showPermissionOverlay = hasPendingPermissionPrompt(_rawEvents);
    final provider = agent?.provider ?? '';

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              _agentName ?? agent?.name ?? widget.agentId,
              style: const TextStyle(fontSize: 16),
            ),
            if (agent != null)
              Text(
                '${agent.provider} · ${_statusLabel(agent.status)}',
                style: TextStyle(
                  fontSize: 12,
                  color: _statusColor(agent.status),
                ),
              ),
          ],
        ),
        actions: [
          // Rename button
          IconButton(
            icon: const Icon(Icons.edit, size: 20),
            tooltip: '重命名会话',
            onPressed: () => _renameAgent(agent?.name ?? widget.agentId),
          ),
          // Permission mode toggle
          Tooltip(
            message: _autoResolvePermissions ? '权限模式: 自动' : '权限模式: 手动',
            child: IconButton(
              icon: Icon(
                _autoResolvePermissions ? Icons.shield : Icons.shield_outlined,
                size: 20,
                color: _autoResolvePermissions ? Colors.green : null,
              ),
              onPressed: () {
                setState(() {
                  _autoResolvePermissions = !_autoResolvePermissions;
                });
              },
            ),
          ),
          // Refresh button
          IconButton(
            icon: const Icon(Icons.refresh, size: 20),
            onPressed: () {
              setState(() {
                _rawEvents.clear();
                _lastSeq = 0;
                _oldestSeq = 0;
                _hasMoreHistory = true;
                _initialLoading = true;
              });
              _loadHistory();
            },
          ),
        ],
      ),
      body: Column(
        children: [
          if (_lastError != null)
            MaterialBanner(
              backgroundColor: Theme.of(context).colorScheme.errorContainer,
              content: Text(
                _lastError!,
                style: TextStyle(
                  color: Theme.of(context).colorScheme.onErrorContainer,
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () {
                    setState(() {
                      _lastError = null;
                    });
                  },
                  child: const Text('关闭'),
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
              ],
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
          // Control bar
          if (agent != null)
            _ControlBar(
              agent: agent,
              nodeId: widget.nodeId,
              stopping: _stopping,
              currentMode: _currentMode,
              onControl: _control,
              onSwitchModel: _switchModel,
              onSwitchProvider: _switchProvider,
              onSwitchMode: _switchMode,
            ),
          // Messages
          Expanded(
            child: Stack(
              children: [
                _initialLoading
                    ? const Center(child: CircularProgressIndicator())
                    : messages.isEmpty
                    ? const Center(
                        child: Text(
                          '暂无对话',
                          style: TextStyle(color: Colors.grey),
                        ),
                      )
                    : NotificationListener<ScrollNotification>(
                        onNotification: (notification) {
                          if (notification is ScrollUpdateNotification &&
                              notification.metrics.pixels <=
                                  notification.metrics.minScrollExtent + 24) {
                            _loadOlderHistory();
                          }
                          return false;
                        },
                        child: ListView.builder(
                          controller: _scrollCtrl,
                          padding: const EdgeInsets.all(12),
                          itemCount: messages.length + (_loadingOlder ? 1 : 0),
                          itemBuilder: (_, i) {
                            if (_loadingOlder && i == 0) {
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
                            final idx = i - (_loadingOlder ? 1 : 0);
                            return _MessageBubble(
                              message: messages[idx],
                              onResolvePermissionPrompt:
                                  messages[idx].isPermissionPrompt
                                  ? _resolvePermissionPrompt
                                  : null,
                            );
                          },
                        ),
                      ),
                if (_showJumpToLatest)
                  Positioned(
                    right: 12,
                    bottom: 12,
                    child: FloatingActionButton.small(
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
          ),
        ],
      ),
    );
  }

  Color _statusColor(AgentStatus s) {
    switch (s) {
      case AgentStatus.working:
        return Colors.blue;
      case AgentStatus.idle:
        return Colors.green;
      case AgentStatus.starting:
        return Colors.orange;
      case AgentStatus.stopped:
        return Colors.grey;
      case AgentStatus.crashed:
        return Colors.red;
    }
  }

  String _statusLabel(AgentStatus s) {
    switch (s) {
      case AgentStatus.working:
        return 'Working';
      case AgentStatus.idle:
        return 'Standby';
      case AgentStatus.starting:
        return 'Starting…';
      case AgentStatus.stopped:
        return 'Stopped';
      case AgentStatus.crashed:
        return 'Crashed';
    }
  }
}

class _ControlBar extends ConsumerStatefulWidget {
  final AgentModel agent;
  final String nodeId;
  final bool stopping;
  final String currentMode;
  final Future<void> Function(String action) onControl;
  final Future<void> Function(String model) onSwitchModel;
  final Future<void> Function(String providerId) onSwitchProvider;
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
                Text(m.label, style: TextStyle(
                  fontWeight: active ? FontWeight.w600 : FontWeight.normal,
                )),
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
              Text(current.label, style: TextStyle(
                fontSize: 12, color: scheme.primary, fontWeight: FontWeight.w600,
              )),
              const Icon(Icons.arrow_drop_down, size: 16),
            ],
          ),
        ),
      ),
      const SizedBox(width: 2),
    ];
  }

  Future<Map<String, dynamic>>? _providerListFuture;

  @override
  void initState() {
    super.initState();
    if (widget.agent.provider == 'claude') {
      _fetchProviders();
    }
  }

  void _fetchProviders() {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    setState(() {
      _providerListFuture = client
          .call('provider.list', {'nodeId': widget.nodeId})
          .then((r) => r is Map ? Map<String, dynamic>.from(r) : <String, dynamic>{});
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
                    child: Text(error, style: const TextStyle(color: Colors.red, fontSize: 13)),
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
          if (widget.agent.provider == 'claude') ...[
            // Dynamic provider selector from cc-switch
            FutureBuilder<Map<String, dynamic>>(
              future: _providerListFuture,
              builder: (context, snapshot) {
                final data = snapshot.data ?? {};
                final providers = (data['providers'] as List?) ?? [];
                final current = data['current'] as String? ?? '';

                // Find current provider name for display
                String currentName = 'Provider';
                if (current.isNotEmpty) {
                  for (final p in providers) {
                    if ((p['id'] ?? '') == current) {
                      currentName = (p['name'] ?? current).toString();
                      break;
                    }
                  }
                }

                if (providers.isEmpty) {
                  // Fallback to static list when cc-switch is not available
                  return PopupMenuButton<String>(
                    tooltip: '切换 Provider',
                    onSelected: (id) {
                      if (id == '__add__') {
                        _showAddProviderDialog(context);
                      } else {
                        widget.onSwitchProvider(id);
                      }
                    },
                    itemBuilder: (_) => [
                      const PopupMenuItem(value: 'claude', child: Text('Anthropic (Default)')),
                      const PopupMenuItem(value: 'claude-bedrock', child: Text('AWS Bedrock')),
                      const PopupMenuItem(value: 'claude-vertex', child: Text('Google Vertex')),
                      const PopupMenuDivider(),
                      const PopupMenuItem(value: '__add__', child: Text('＋ 新增 Provider')),
                    ],
                    child: Padding(
                      padding: const EdgeInsets.symmetric(horizontal: 4),
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          const Icon(Icons.cloud, size: 16),
                          const SizedBox(width: 3),
                          Text(currentName, style: const TextStyle(fontSize: 12)),
                        ],
                      ),
                    ),
                  );
                }

                return PopupMenuButton<String>(
                  tooltip: '切换 Provider',
                  onSelected: (id) {
                    if (id == '__add__') {
                      _showAddProviderDialog(context);
                    } else {
                      widget.onSwitchProvider(id);
                    }
                  },
                  itemBuilder: (_) => [
                    ...providers.map((p) {
                      final id = (p['id'] ?? '').toString();
                      final name = (p['name'] ?? id).toString();
                      final isActive = id == current;
                      return PopupMenuItem<String>(
                        value: id,
                        child: Row(
                          children: [
                            if (isActive) ...[
                              const Icon(Icons.check, size: 16),
                              const SizedBox(width: 4),
                            ] else
                              const SizedBox(width: 20),
                            Text(name),
                          ],
                        ),
                      );
                    }),
                    const PopupMenuDivider(),
                    const PopupMenuItem(value: '__add__', child: Text('＋ 新增 Provider')),
                  ],
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 4),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        const Icon(Icons.cloud, size: 16),
                        const SizedBox(width: 3),
                        Text(currentName, style: const TextStyle(fontSize: 12)),
                      ],
                    ),
                  ),
                );
              },
            ),
            PopupMenuButton<String>(
              tooltip: '切换模型',
              onSelected: widget.onSwitchModel,
              itemBuilder: (_) => const [
                PopupMenuItem(
                  value: 'claude-sonnet-4-6',
                  child: Text('Sonnet 4.6'),
                ),
                PopupMenuItem(
                  value: 'claude-opus-4-6',
                  child: Text('Opus 4.6'),
                ),
                PopupMenuItem(
                  value: 'claude-haiku-4-5-20251001',
                  child: Text('Haiku 4.5'),
                ),
              ],
              child: const Padding(
                padding: EdgeInsets.symmetric(horizontal: 4),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.psychology, size: 16),
                    SizedBox(width: 3),
                    Text('模型', style: TextStyle(fontSize: 12)),
                  ],
                ),
              ),
            ),
          ],
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
              onPressed: widget.stopping ? null : () => widget.onControl('stop'),
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

class _CollapsibleBubble extends StatefulWidget {
  final String header;
  final String content;
  final String? collapsedPreview;
  final IconData icon;
  final Color color;

  const _CollapsibleBubble({
    required this.header,
    required this.content,
    this.collapsedPreview,
    required this.icon,
    required this.color,
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
            child: InkWell(
              onTap: () => setState(() => _expanded = !_expanded),
              borderRadius: BorderRadius.circular(8),
              child: AnimatedCrossFade(
                firstChild: _buildCollapsed(scheme),
                secondChild: _buildExpanded(scheme),
                crossFadeState: _expanded
                    ? CrossFadeState.showSecond
                    : CrossFadeState.showFirst,
                duration: const Duration(milliseconds: 200),
              ),
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildCollapsed(ColorScheme scheme) {
    final preview = widget.collapsedPreview?.trim() ?? '';
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      decoration: BoxDecoration(
        color: scheme.surfaceContainerLow,
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: scheme.outlineVariant.withValues(alpha: 0.3)),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Icon(Icons.chevron_right, size: 16, color: widget.color),
          const SizedBox(width: 6),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  widget.header,
                  style: TextStyle(fontSize: 12, color: scheme.onSurfaceVariant),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                ),
                if (preview.isNotEmpty) ...[
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
              ],
            ),
          ),
        ],
      ),
    );
  }

  Widget _buildExpanded(ColorScheme scheme) {
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
              Icon(Icons.expand_more, size: 16, color: widget.color),
              const SizedBox(width: 6),
              Text(
                widget.header,
                style: TextStyle(
                  fontSize: 12,
                  color: scheme.onSurfaceVariant,
                  fontWeight: FontWeight.w500,
                ),
              ),
            ],
          ),
          const SizedBox(height: 6),
          _MarkdownContent(
            text: widget.content,
            fontSize: 12,
            textColor: scheme.onSurfaceVariant,
          ),
        ],
      ),
    );
  }
}

class _MessageBubble extends StatelessWidget {
  final ChatMessage message;
  final Future<void> Function()? onResolvePermissionPrompt;

  const _MessageBubble({required this.message, this.onResolvePermissionPrompt});

  @override
  Widget build(BuildContext context) {
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
                    const SizedBox(width: 8),
                    Text(
                      '权限确认',
                      style: TextStyle(
                        fontSize: 14,
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
        contentPreview = trimmed.length > 60 ? trimmed.substring(0, 60) : trimmed;
      }
      final header = contentPreview.isNotEmpty ? '💭 $contentPreview' : '💭 思考过程';
      return _CollapsibleBubble(
        header: header,
        content: message.text,
        collapsedPreview: '', // header 已包含预览，不需要重复
        icon: Icons.psychology,
        color: Colors.orange.shade700,
      );
    }

    if (message.isActivityBlock) {
      final firstLine = message.text
          .split('\n')
          .firstWhere((line) => line.trim().isNotEmpty, orElse: () => '助手活动');
      return _CollapsibleBubble(
        header: '助手活动',
        content: message.text,
        collapsedPreview: buildCollapsedPreview(firstLine, maxChars: 90),
        icon: Icons.build,
        color: scheme.primary,
      );
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
      final header = params.isNotEmpty ? '🔧 $toolName: $params' : '🔧 $toolName';
      return _CollapsibleBubble(
        header: header,
        content: message.text,
        collapsedPreview: '', // header 已包含参数预览
        icon: Icons.build,
        color: scheme.primary,
      );
    }

    // Raw ANSI output has different styling (dimmed, smaller)
    final bgColor = isUser
        ? scheme.primaryContainer
        : isRaw
        ? scheme.surfaceContainerLow
        : scheme.surfaceContainerHighest;

    final textColor = isUser
        ? scheme.onPrimaryContainer
        : isRaw
        ? scheme.onSurfaceVariant.withValues(alpha: 0.7)
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
                  ? scheme.surfaceContainerHighest
                  : scheme.primaryContainer,
              child: Icon(
                isRaw ? Icons.terminal : Icons.smart_toy,
                size: isRaw ? 14 : 18,
                color: isRaw
                    ? scheme.onSurfaceVariant
                    : scheme.onPrimaryContainer,
              ),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
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
                        color: scheme.outlineVariant.withValues(alpha: 0.3),
                        width: 1,
                      )
                    : null,
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
                          color: scheme.onSurfaceVariant.withValues(alpha: 0.5),
                          fontStyle: FontStyle.italic,
                        ),
                      ),
                    ),
                  _MarkdownContent(
                    text: message.text,
                    fontSize: isRaw ? 12 : 14,
                    textColor: textColor,
                    isRaw: isRaw,
                  ),
                ],
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

/// Markdown content widget with syntax highlighting and security settings.
class _MarkdownContent extends StatelessWidget {
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
  Widget build(BuildContext context) {
    final scheme = Theme.of(context).colorScheme;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    // For raw terminal output, use plain text to preserve formatting
    if (isRaw) {
      return SelectableText(
        text,
        style: TextStyle(
          fontSize: fontSize,
          color: textColor,
          fontFamily: 'monospace',
          height: 1.4,
        ),
      );
    }

    return MarkdownBody(
      data: text,
      selectable: true,
      styleSheet: MarkdownStyleSheet(
        p: TextStyle(
          fontSize: fontSize,
          color: textColor,
          height: 1.4,
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
        h5: TextStyle(
          fontSize: fontSize + 1,
          fontWeight: FontWeight.bold,
          color: textColor,
          height: 1.4,
        ),
        h6: TextStyle(
          fontSize: fontSize,
          fontWeight: FontWeight.bold,
          color: textColor,
          height: 1.4,
        ),
        code: TextStyle(
          fontSize: fontSize - 1,
          color: textColor,
          fontFamily: 'monospace',
          backgroundColor: scheme.surfaceContainerHighest,
        ),
        codeblockDecoration: BoxDecoration(
          color: isDark ? Colors.grey.shade900 : Colors.grey.shade100,
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
        listBullet: TextStyle(
          fontSize: fontSize,
          color: textColor,
        ),
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
              style: TextStyle(
                fontSize: 16,
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
  AgentMode(id: 'bypassPermissions', label: 'Build', icon: Icons.build),
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
                  style: TextStyle(
                    fontSize: 14,
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
  SlashCommand('/skill', '调用内置技能'),
  SlashCommand('/fast', '切换快速模式'),
  SlashCommand('/commit', '生成 commit'),
  SlashCommand('/plan', '进入规划模式'),
  SlashCommand('/mcp', '管理 MCP 服务器'),
  SlashCommand('/permissions', '管理权限'),
  SlashCommand('/config', '查看/修改配置'),
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
  });

  @override
  State<_InputBar> createState() => _InputBarState();
}

class _InputBarState extends State<_InputBar> {
  bool _showSlashMenu = false;
  String _slashFilter = '';

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
    if (_slashFilter.isEmpty) return kSlashCommands;
    return kSlashCommands
        .where((c) => c.command.substring(1).contains(_slashFilter))
        .toList();
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
        top: 8,
        bottom: MediaQuery.of(context).padding.bottom + 8,
      ),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(top: BorderSide(color: Colors.grey.shade300)),
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
                          style: const TextStyle(
                            fontWeight: FontWeight.w500,
                            fontSize: 14,
                          ),
                        ),
                        subtitle: Text(
                          cmd.description,
                          style: const TextStyle(fontSize: 12),
                        ),
                        onTap: () {
                          widget.controller.text = '${cmd.command} ';
                          widget.controller.selection =
                              TextSelection.fromPosition(
                            TextPosition(
                              offset: widget.controller.text.length,
                            ),
                          );
                          setState(() => _showSlashMenu = false);
                        },
                      ),
                    )
                    .toList(),
              ),
            ),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: widget.controller,
                  enabled: !isReadOnly && !effectiveLoading,
                  decoration: InputDecoration(
                    hintText: readOnlyHint,
                    border: const OutlineInputBorder(
                      borderRadius: BorderRadius.all(Radius.circular(24)),
                    ),
                    contentPadding: const EdgeInsets.symmetric(
                      horizontal: 16,
                      vertical: 10,
                    ),
                    isDense: true,
                  ),
                  textInputAction: TextInputAction.send,
                  onSubmitted: isReadOnly ? null : (_) => widget.onSend(),
                  maxLines: 4,
                  minLines: 1,
                ),
              ),
              const SizedBox(width: 8),
              effectiveLoading
                  ? const SizedBox(
                      width: 40,
                      height: 40,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : IconButton(
                      onPressed: isReadOnly ? null : widget.onSend,
                      icon: const Icon(Icons.send),
                      style: IconButton.styleFrom(
                        backgroundColor: Theme.of(context).colorScheme.primary,
                        foregroundColor:
                            Theme.of(context).colorScheme.onPrimary,
                      ),
                    ),
            ],
          ),
        ],
      ),
    );
  }
}
