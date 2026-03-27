import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_markdown/flutter_markdown.dart';

import '../models/agent_model.dart';
import '../models/message_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/connection_provider.dart';

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

  @override
  void initState() {
    super.initState();
    _loadHistory();
  }

  Future<void> _loadHistory() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      final result = await client.call('conversation.history', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
      });
      final messages = (result['messages'] as List?) ?? [];
      ref.read(conversationProvider.notifier).loadHistory(
            widget.nodeId,
            widget.agentId,
            messages,
          );
      _scrollToBottom();
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
    } catch (_) {}
    setState(() => _loading = false);
    _scrollToBottom();
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
          duration: const Duration(milliseconds: 300),
          curve: Curves.easeOut,
        );
      }
    });
  }

  @override
  void dispose() {
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final agents = nodeState.agentsFor(widget.nodeId);
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;

    final messages = ref.watch(conversationProvider.notifier).messagesFor(
          widget.nodeId,
          widget.agentId,
        );

    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(agent?.name ?? widget.agentId),
            if (agent != null)
              Text(
                _statusLabel(agent.status),
                style: TextStyle(fontSize: 12, color: _statusColor(agent.status)),
              ),
          ],
        ),
      ),
      body: Column(
        children: [
          // Control bar
          if (agent != null) _ControlBar(agent: agent, onControl: _control),
          // Messages
          Expanded(
            child: messages.isEmpty
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
        return '● Working';
      case AgentStatus.idle:
        return '● Standby';
      case AgentStatus.starting:
        return '◎ Starting…';
      case AgentStatus.stopped:
        return '○ Stopped';
      case AgentStatus.crashed:
        return '✕ Crashed';
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
            )
          else
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
  final MessageModel message;
  const _MessageBubble({required this.message});

  @override
  Widget build(BuildContext context) {
    final isUser = message.role == MessageRole.user;
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
              child: isUser
                  ? Text(message.text)
                  : MarkdownBody(data: message.text, shrinkWrap: true),
            ),
          ),
          if (isUser) ...[
            const SizedBox(width: 8),
            CircleAvatar(
              radius: 16,
              backgroundColor: scheme.secondaryContainer,
              child: const Icon(Icons.person, size: 18),
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

  const _InputBar({
    required this.controller,
    required this.loading,
    required this.onSend,
  });

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: EdgeInsets.only(
        left: 12,
        right: 12,
        top: 8,
        bottom: MediaQuery.of(context).viewInsets.bottom + 8,
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
              minLines: 1,
              maxLines: 4,
              decoration: const InputDecoration(
                hintText: '发送消息…',
                border: OutlineInputBorder(),
                contentPadding: EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              ),
              onSubmitted: (_) => onSend(),
            ),
          ),
          const SizedBox(width: 8),
          loading
              ? const SizedBox(
                  width: 40,
                  height: 40,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : IconButton.filled(
                  onPressed: onSend,
                  icon: const Icon(Icons.send),
                ),
        ],
      ),
    );
  }
}
