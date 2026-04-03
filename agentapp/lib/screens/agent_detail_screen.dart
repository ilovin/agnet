import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

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
/// Each message is displayed separately (like PC terminal)
class ChatMessage {
  final String role;
  final String text;
  final int seq;
  final bool isRaw;
  final bool isPermissionPrompt;

  ChatMessage({
    required this.role,
    required this.text,
    required this.seq,
    this.isRaw = false,
    this.isPermissionPrompt = false,
  });
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
  if (normalized.contains('bypass') && normalized.contains('permission')) return true;
  if (normalized.contains('permission') && normalized.contains('shift+tab')) return true;
  if (normalized.contains('bypass') && normalized.contains('shift+tab')) return true;
  if (normalized.contains('shift+tab') && normalized.contains('cycle')) return true;

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
  if (cleaned.length < 10 || cleaned.replaceAll(RegExp(r'[^a-zA-Z]'), '').length < 5) {
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
  if (RegExp(r'^(Sautéing|Doing|Working)[\s…]*$').hasMatch(normalized)) return true;
  // Box drawing only (after stripping ANSI)
  if (RegExp(r'^[─│╭╰╮╯\s]+$').hasMatch(normalized)) return true;
  return false;
}

/// Converts raw events to display messages.
/// Handles both structured stream-json events and legacy PTY raw output.
List<ChatMessage> convertEventsToMessages(List<Map<String, dynamic>> events) {
  final messages = <ChatMessage>[];

  // Buffer for merging consecutive raw assistant chunks
  final rawBuf = StringBuffer();
  int rawBufSeq = 0;

  void flushRawBuf() {
    if (rawBuf.isEmpty) return;
    final merged = rawBuf.toString().trim();
    rawBuf.clear();
    if (!isNoiseOnlyText(merged)) {
      messages.add(ChatMessage(
        role: 'assistant',
        text: merged,
        seq: rawBufSeq,
        isRaw: true,
      ));
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

    // User messages: flush any pending raw buffer first
    if (role == 'user') {
      flushRawBuf();
      if (!isNoiseOnlyText(cleaned)) {
        messages.add(ChatMessage(
          role: role,
          text: cleaned,
          seq: seq,
          isRaw: raw,
        ));
      }
      continue;
    }

    // Raw assistant output: buffer and merge
    if (raw) {
      if (isNoiseOnlyText(cleaned)) continue;
      if (cleaned.isNotEmpty) {
        if (rawBuf.isEmpty) rawBufSeq = seq;
        if (rawBuf.isNotEmpty) rawBuf.write('\n');
        rawBuf.write(cleaned);
        // Flush when we see sentence-ending content or significant newlines
        if (cleaned.contains('？') || cleaned.contains('。') || cleaned.contains('！') ||
            cleaned.endsWith('?') || cleaned.endsWith('.') || cleaned.endsWith('!') ||
            cleaned.length > 100) {
          flushRawBuf();
        }
      }
      continue;
    }

    // Structured message from watcher/stream-json (not raw)
    flushRawBuf();
    if (isNoiseOnlyText(cleaned)) continue;
    messages.add(ChatMessage(
      role: role,
      text: cleaned,
      seq: seq,
      isRaw: false,
    ));
  }

  // Flush any remaining raw buffer
  flushRawBuf();

  return messages;
}

class AgentDetailScreen extends ConsumerStatefulWidget {
  final String nodeId;
  final String agentId;

  const AgentDetailScreen({super.key, required this.nodeId, required this.agentId});

  @override
  ConsumerState<AgentDetailScreen> createState() => _AgentDetailScreenState();
}

class _AgentDetailScreenState extends ConsumerState<AgentDetailScreen> {
  final _inputCtrl = TextEditingController();
  final _scrollCtrl = ScrollController();
  bool _loading = false;
  bool _initialLoading = true;
  bool _rawMode = false;
  bool _stopping = false;
  bool _stickToBottom = true;
  bool _showJumpToLatest = false;
  String? _lastError;

  // Raw events from EventBuffer
  List<Map<String, dynamic>> _rawEvents = [];
  int _lastSeq = 0;
  Timer? _pollTimer;

  // Permission handling mode (mobile-friendly option)
  // true = auto-resolve (default for mobile), false = manual confirmation
  bool _autoResolvePermissions = true;

  @override
  void initState() {
    super.initState();
    _scrollCtrl.addListener(_handleScroll);
    _loadHistory();
    // Poll every 1s for new events
    _pollTimer = Timer.periodic(const Duration(seconds: 1), (_) => _pollNewEvents());
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
      });
      final raw = result is Map ? result : <String, dynamic>{};
      final events = (raw['events'] as List?) ?? [];
      final lastSeq = (raw['lastSeq'] as num?)?.toInt() ?? 0;
      if (mounted) {
        setState(() {
          // API returns flattened events: {seq, role, text, raw}
          _rawEvents = events.map((e) {
            final map = Map<String, dynamic>.from(e as Map);
            // Ensure all fields are present
            return {
              'seq': map['seq'] ?? 0,
              'role': map['role'] ?? 'assistant',
              'text': map['text'] ?? '',
              'raw': map['raw'] ?? false,
            };
          }).toList();
          _lastSeq = lastSeq;
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

  Future<void> _pollNewEvents() async {
    if (_initialLoading) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
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

      if (events.isNotEmpty) {
        setState(() {
          // API returns flattened events: {seq, role, text, raw}
          _rawEvents.addAll(events.map((e) {
            final map = Map<String, dynamic>.from(e as Map);
            return {
              'seq': map['seq'] ?? 0,
              'role': map['role'] ?? 'assistant',
              'text': map['text'] ?? '',
              'raw': map['raw'] ?? false,
            };
          }));
          _lastSeq = lastSeq;
          _lastError = null;
        });
        _scrollToBottom();
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
    }
  }

  Future<void> _sendPermissionResponse(String requestId, String behavior) async {
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
    if (text.isEmpty || _loading) return;
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
    if (agent.status == AgentStatus.stopped || agent.status == AgentStatus.crashed) {
      setState(() {
        _lastError = '会话未运行，请先启动';
      });
      return;
    }

    _inputCtrl.clear();
    setState(() {
      _loading = true;
      _lastError = null;
    });
    try {
      await client.call('conversation.send', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'message': text,
        'raw': _rawMode,
      });
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
      final listResult = await client.call('agent.list', {'nodeId': widget.nodeId});
      final agents = (listResult is List ? listResult : (listResult['agents'] as List?) ?? []);
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

      final map = result is Map ? Map<String, dynamic>.from(result) : <String, dynamic>{};
      final newId = map['id'] as String?;

      final listResult = await client.call('agent.list', {'nodeId': widget.nodeId});
      final agents = (listResult is List ? listResult : (listResult['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(widget.nodeId, agents);

      if (newId != null && newId.isNotEmpty && mounted && newId != widget.agentId) {
        context.go('/agent/${widget.nodeId}/$newId');
      }
    } catch (e) {
      debugPrint('switchModel error: $e');
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

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;

    final messages = convertEventsToMessages(_rawEvents);
    final showPermissionOverlay = hasPendingPermissionPrompt(_rawEvents);

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(agent?.name ?? widget.agentId, style: const TextStyle(fontSize: 16)),
            if (agent != null)
              Text(
                '${agent.provider} · ${_statusLabel(agent.status)}',
                style: TextStyle(fontSize: 12, color: _statusColor(agent.status)),
              ),
          ],
        ),
        actions: [
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
                _initialLoading = true;
              });
              _loadHistory();
            },
          ),
        ],
      ),
      floatingActionButton: _showJumpToLatest
          ? FloatingActionButton.small(
              onPressed: () {
                setState(() {
                  _stickToBottom = true;
                  _showJumpToLatest = false;
                });
                _scrollToBottom(force: true);
              },
              child: const Icon(Icons.arrow_downward),
            )
          : null,
      body: Column(
        children: [
          if (_lastError != null)
            MaterialBanner(
              backgroundColor: Theme.of(context).colorScheme.errorContainer,
              content: Text(
                _lastError!,
                style: TextStyle(color: Theme.of(context).colorScheme.onErrorContainer),
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
              stopping: _stopping,
              onControl: _control,
              onSwitchModel: _switchModel,
            ),
          // Messages
          Expanded(
            child: _initialLoading
                ? const Center(child: CircularProgressIndicator())
                : messages.isEmpty
                    ? const Center(child: Text('暂无对话', style: TextStyle(color: Colors.grey)))
                    : ListView.builder(
                        controller: _scrollCtrl,
                        padding: const EdgeInsets.all(12),
                        itemCount: messages.length,
                        itemBuilder: (_, i) => _MessageBubble(
                          message: messages[i],
                          onResolvePermissionPrompt: messages[i].isPermissionPrompt ? _resolvePermissionPrompt : null,
                        ),
                      ),
          ),
          // Permission prompt overlay (above input bar)
          if (showPermissionOverlay)
            _PermissionPromptOverlay(
              onResolve: _resolvePermissionPrompt,
            ),
          // Input bar
          _InputBar(
            controller: _inputCtrl,
            loading: _loading,
            rawMode: _rawMode,
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
      case AgentStatus.working: return Colors.blue;
      case AgentStatus.idle: return Colors.green;
      case AgentStatus.starting: return Colors.orange;
      case AgentStatus.stopped: return Colors.grey;
      case AgentStatus.crashed: return Colors.red;
    }
  }

  String _statusLabel(AgentStatus s) {
    switch (s) {
      case AgentStatus.working: return 'Working';
      case AgentStatus.idle: return 'Standby';
      case AgentStatus.starting: return 'Starting…';
      case AgentStatus.stopped: return 'Stopped';
      case AgentStatus.crashed: return 'Crashed';
    }
  }
}

class _ControlBar extends StatelessWidget {
  final AgentModel agent;
  final bool stopping;
  final Future<void> Function(String action) onControl;
  final Future<void> Function(String model) onSwitchModel;

  const _ControlBar({
    required this.agent,
    required this.stopping,
    required this.onControl,
    required this.onSwitchModel,
  });

  @override
  Widget build(BuildContext context) {
    final stopped = agent.status == AgentStatus.stopped || agent.status == AgentStatus.crashed;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surfaceContainerHighest,
        border: Border(bottom: BorderSide(color: Colors.grey.shade300)),
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.end,
        children: [
          if (agent.provider == 'claude')
            PopupMenuButton<String>(
              tooltip: '切换模型',
              onSelected: onSwitchModel,
              itemBuilder: (_) => const [
                PopupMenuItem(value: 'claude-sonnet-4-6', child: Text('Sonnet 4.6')),
                PopupMenuItem(value: 'claude-opus-4-6', child: Text('Opus 4.6')),
                PopupMenuItem(value: 'claude-haiku-4-5-20251001', child: Text('Haiku 4.5')),
              ],
              child: const Padding(
                padding: EdgeInsets.symmetric(horizontal: 8),
                child: Row(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    Icon(Icons.psychology, size: 18),
                    SizedBox(width: 4),
                    Text('模型'),
                  ],
                ),
              ),
            ),
          if (stopped)
            TextButton.icon(
              onPressed: () => onControl('restart'),
              icon: const Icon(Icons.play_arrow, size: 18),
              label: const Text('启动'),
            ),
          if (!stopped)
            TextButton.icon(
              onPressed: stopping ? null : () => onControl('stop'),
              icon: stopping
                  ? const SizedBox(
                      width: 14,
                      height: 14,
                      child: CircularProgressIndicator(strokeWidth: 2),
                    )
                  : const Icon(Icons.stop, size: 18),
              label: Text(stopping ? '停止中…' : '停止'),
            ),
          const SizedBox(width: 8),
          TextButton.icon(
            onPressed: () => onControl('restart'),
            icon: const Icon(Icons.refresh, size: 18),
            label: const Text('重启'),
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
        mainAxisAlignment: isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
        children: [
          if (!isUser) ...[
            CircleAvatar(
              radius: isRaw ? 12 : 16,
              backgroundColor: isRaw ? scheme.surfaceContainerHighest : scheme.primaryContainer,
              child: Icon(
                isRaw ? Icons.terminal : Icons.smart_toy,
                size: isRaw ? 14 : 18,
                color: isRaw ? scheme.onSurfaceVariant : scheme.onPrimaryContainer,
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
                  SelectableText(
                    message.text,
                    style: TextStyle(
                      fontSize: isRaw ? 12 : 14,
                      color: textColor,
                      height: 1.4,
                    ),
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

class _KeyButton extends StatelessWidget {
  final String label;
  final String displayLabel;
  final VoidCallback onTap;
  final bool isArrow;

  const _KeyButton({
    required this.label,
    required this.onTap,
    this.displayLabel = '',
    this.isArrow = false,
  });

  @override
  Widget build(BuildContext context) {
    final display = displayLabel.isNotEmpty ? displayLabel : label;
    final isDark = Theme.of(context).brightness == Brightness.dark;

    return Material(
      color: Colors.transparent,
      child: InkWell(
        onTap: onTap,
        borderRadius: BorderRadius.circular(8),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          decoration: BoxDecoration(
            color: isArrow
                ? (isDark ? Colors.blue.shade900.withValues(alpha: 0.3) : Colors.blue.shade50)
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
            child: Icon(
              Icons.security,
              size: 20,
              color: scheme.secondary,
            ),
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

class _InputBar extends StatelessWidget {
  final TextEditingController controller;
  final bool loading;
  final bool rawMode;
  final ValueChanged<bool> onToggleRaw;
  final VoidCallback onSend;
  final Future<void> Function(String key) onKey;

  const _InputBar({
    required this.controller,
    required this.loading,
    required this.rawMode,
    required this.onToggleRaw,
    required this.onSend,
    required this.onKey,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.only(
        left: 12, right: 12, top: 8,
        bottom: MediaQuery.of(context).padding.bottom + 8,
      ),
      decoration: BoxDecoration(
        color: Theme.of(context).colorScheme.surface,
        border: Border(top: BorderSide(color: Colors.grey.shade300)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Wrap(
                spacing: 6,
                runSpacing: 6,
                crossAxisAlignment: WrapCrossAlignment.center,
                children: [
                  _KeyButton(label: 'up', displayLabel: '↑', isArrow: true, onTap: () => onKey('up')),
                  _KeyButton(label: 'down', displayLabel: '↓', isArrow: true, onTap: () => onKey('down')),
                  _KeyButton(label: 'left', displayLabel: '←', isArrow: true, onTap: () => onKey('left')),
                  _KeyButton(label: 'right', displayLabel: '→', isArrow: true, onTap: () => onKey('right')),
                  _KeyButton(label: 'enter', displayLabel: '⏎', onTap: () => onKey('enter')),
                ],
              ),
              const Spacer(),
              Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  const Text('Raw', style: TextStyle(fontSize: 12)),
                  Switch(value: rawMode, onChanged: onToggleRaw),
                ],
              ),
            ],
          ),
          const SizedBox(height: 8),
          Row(
            children: [
              Expanded(
                child: TextField(
                  controller: controller,
                  decoration: const InputDecoration(
                    hintText: '输入消息…',
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.all(Radius.circular(24)),
                    ),
                    contentPadding: EdgeInsets.symmetric(horizontal: 16, vertical: 10),
                    isDense: true,
                  ),
                  textInputAction: TextInputAction.send,
                  onSubmitted: (_) => onSend(),
                  maxLines: 4,
                  minLines: 1,
                ),
              ),
              const SizedBox(width: 8),
              loading
                  ? const SizedBox(width: 40, height: 40, child: CircularProgressIndicator(strokeWidth: 2))
                  : IconButton(
                      onPressed: onSend,
                      icon: const Icon(Icons.send),
                      style: IconButton.styleFrom(
                        backgroundColor: Theme.of(context).colorScheme.primary,
                        foregroundColor: Theme.of(context).colorScheme.onPrimary,
                      ),
                    ),
            ],
          ),
        ],
      ),
    );
  }
}
