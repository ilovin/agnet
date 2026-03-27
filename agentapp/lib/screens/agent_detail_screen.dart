import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';

/// Strips ANSI escape sequences from PTY output.
String stripAnsi(String s) {
  return s.replaceAll(RegExp(r'\x1B\[[0-9;]*[a-zA-Z]|\x1B\][^\x07]*\x07|\x1B[\(\)][AB012]|\x1B\[[\?]?[0-9;]*[hlm]|\x1B[>=<]|\x1B\[\?[0-9;]*[a-zA-Z]|\x1B\[[0-9]*[A-Za-z]|\r'), '');
}

/// Merges consecutive assistant events into logical messages.
/// User input events (role=user) stay separate.
class ChatMessage {
  final String role;
  final String text;
  final int startSeq;
  final int endSeq;

  ChatMessage({required this.role, required this.text, required this.startSeq, required this.endSeq});
}

List<ChatMessage> mergeEvents(List<Map<String, dynamic>> events) {
  final messages = <ChatMessage>[];
  StringBuffer? currentBuf;
  String? currentRole;
  int startSeq = 0;
  int endSeq = 0;

  for (final e in events) {
    final seq = (e['seq'] as num?)?.toInt() ?? 0;
    final data = e['data'] as Map<String, dynamic>? ?? {};
    final role = data['role'] as String? ?? 'assistant';
    final rawText = data['text'] as String? ?? '';
    final text = stripAnsi(rawText).trim();
    if (text.isEmpty) continue;

    if (role == 'user') {
      // Flush current assistant buffer
      if (currentBuf != null && currentBuf.isNotEmpty) {
        messages.add(ChatMessage(role: currentRole!, text: currentBuf.toString().trim(), startSeq: startSeq, endSeq: endSeq));
        currentBuf = null;
        currentRole = null;
      }
      messages.add(ChatMessage(role: 'user', text: text, startSeq: seq, endSeq: seq));
    } else {
      if (currentRole != role) {
        if (currentBuf != null && currentBuf.isNotEmpty) {
          messages.add(ChatMessage(role: currentRole!, text: currentBuf.toString().trim(), startSeq: startSeq, endSeq: endSeq));
        }
        currentBuf = StringBuffer();
        currentRole = role;
        startSeq = seq;
      }
      currentBuf!.write(text);
      endSeq = seq;
    }
  }
  if (currentBuf != null && currentBuf.isNotEmpty) {
    messages.add(ChatMessage(role: currentRole!, text: currentBuf.toString().trim(), startSeq: startSeq, endSeq: endSeq));
  }
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

  // Raw events from EventBuffer
  List<Map<String, dynamic>> _rawEvents = [];
  int _lastSeq = 0;
  Timer? _pollTimer;

  @override
  void initState() {
    super.initState();
    _loadHistory();
    // Poll every 1s for new events
    _pollTimer = Timer.periodic(const Duration(seconds: 1), (_) => _pollNewEvents());
  }

  @override
  void dispose() {
    _pollTimer?.cancel();
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
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
          _rawEvents = events.map((e) => Map<String, dynamic>.from(e as Map)).toList();
          _lastSeq = lastSeq;
          _initialLoading = false;
        });
        _scrollToBottom();
      }
    } catch (e) {
      debugPrint('loadHistory error: $e');
      if (mounted) setState(() => _initialLoading = false);
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
      if (events.isNotEmpty && mounted) {
        setState(() {
          _rawEvents.addAll(events.map((e) => Map<String, dynamic>.from(e as Map)));
          _lastSeq = lastSeq;
        });
        _scrollToBottom();
      }
    } catch (_) {}
  }

  Future<void> _sendMessage() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty) return;
    _inputCtrl.clear();
    final client = ref.read(connectionProvider);
    if (client == null) return;
    setState(() => _loading = true);
    try {
      await client.call('conversation.send', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'message': text,
      });
      // Immediately poll for the echo
      await _pollNewEvents();
    } catch (e) {
      debugPrint('sendMessage error: $e');
    }
    if (mounted) setState(() => _loading = false);
  }

  Future<void> _control(String action) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('agent.$action', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
      });
    } catch (_) {}
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollCtrl.hasClients) {
        _scrollCtrl.animateTo(
          _scrollCtrl.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;

    final messages = mergeEvents(_rawEvents);

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
      body: Column(
        children: [
          // Control bar
          if (agent != null) _ControlBar(agent: agent, onControl: _control),
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
                        itemBuilder: (_, i) => _MessageBubble(message: messages[i]),
                      ),
          ),
          // Input bar
          _InputBar(
            controller: _inputCtrl,
            loading: _loading,
            onSend: _sendMessage,
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
  final Future<void> Function(String action) onControl;

  const _ControlBar({required this.agent, required this.onControl});

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
          if (stopped)
            TextButton.icon(
              onPressed: () => onControl('restart'),
              icon: const Icon(Icons.play_arrow, size: 18),
              label: const Text('启动'),
            ),
          if (!stopped)
            TextButton.icon(
              onPressed: () => onControl('stop'),
              icon: const Icon(Icons.stop, size: 18),
              label: const Text('停止'),
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
  const _MessageBubble({required this.message});

  @override
  Widget build(BuildContext context) {
    final isUser = message.role == 'user';
    final scheme = Theme.of(context).colorScheme;

    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisAlignment: isUser ? MainAxisAlignment.end : MainAxisAlignment.start,
        children: [
          if (!isUser) ...[
            CircleAvatar(
              radius: 16,
              backgroundColor: scheme.primaryContainer,
              child: const Icon(Icons.smart_toy, size: 18),
            ),
            const SizedBox(width: 8),
          ],
          Flexible(
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
              decoration: BoxDecoration(
                color: isUser ? scheme.primaryContainer : scheme.surfaceContainerHighest,
                borderRadius: BorderRadius.only(
                  topLeft: const Radius.circular(16),
                  topRight: const Radius.circular(16),
                  bottomLeft: Radius.circular(isUser ? 16 : 4),
                  bottomRight: Radius.circular(isUser ? 4 : 16),
                ),
              ),
              child: SelectableText(
                message.text,
                style: TextStyle(
                  fontSize: 14,
                  color: isUser ? scheme.onPrimaryContainer : scheme.onSurface,
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

class _InputBar extends StatelessWidget {
  final TextEditingController controller;
  final bool loading;
  final VoidCallback onSend;

  const _InputBar({required this.controller, required this.loading, required this.onSend});

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
      child: Row(
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
    );
  }
}
