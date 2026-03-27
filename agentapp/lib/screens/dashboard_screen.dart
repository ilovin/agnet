import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';

class DashboardScreen extends ConsumerWidget {
  const DashboardScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final nodeState = ref.watch(nodesProvider);
    final client = ref.watch(connectionProvider);
    final nodes = nodeState.nodeList;

    return Scaffold(
      appBar: AppBar(
        title: const Text('仪表盘'),
        actions: [
          // Reconnection status indicator
          if (client != null)
            StreamBuilder<bool>(
              stream: client.onConnectionChanged,
              initialData: client.isConnected,
              builder: (_, snap) {
                final connected = snap.data ?? false;
                return Padding(
                  padding: const EdgeInsets.only(right: 12),
                  child: Icon(
                    connected ? Icons.wifi : Icons.wifi_off,
                    color: connected ? Colors.green : Colors.red,
                  ),
                );
              },
            ),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: nodes.isEmpty
          ? const Center(child: Text('暂无节点', style: TextStyle(color: Colors.grey)))
          : ListView.builder(
              itemCount: nodes.length,
              padding: const EdgeInsets.all(12),
              itemBuilder: (_, i) => NodeCard(node: nodes[i]),
            ),
    );
  }
}

class NodeCard extends ConsumerWidget {
  final NodeModel node;
  const NodeCard({super.key, required this.node});

  Color get _statusColor {
    switch (node.status) {
      case NodeStatus.connected:
        return Colors.green;
      case NodeStatus.connecting:
      case NodeStatus.deploying:
        return Colors.orange;
      case NodeStatus.error:
        return Colors.red;
      case NodeStatus.disconnected:
        return Colors.grey;
    }
  }

  String get _statusLabel {
    switch (node.status) {
      case NodeStatus.connected:
        return '已连接';
      case NodeStatus.connecting:
        return '连接中…';
      case NodeStatus.deploying:
        return '部署中…';
      case NodeStatus.error:
        return '错误';
      case NodeStatus.disconnected:
        return '未连接';
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final agents = ref.watch(nodesProvider).agentsFor(node.id);

    return Card(
      margin: const EdgeInsets.only(bottom: 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ListTile(
            leading: Icon(Icons.computer, color: _statusColor),
            title: Text(node.name, style: const TextStyle(fontWeight: FontWeight.bold)),
            subtitle: Text('${node.host}  ·  $_statusLabel'),
            trailing: Icon(Icons.circle, color: _statusColor, size: 12),
          ),
          if (agents.isNotEmpty) const Divider(height: 1),
          ...agents.map((a) => AgentRow(agent: a, nodeId: node.id)),
          // New agent button and discover opencode
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
            child: Row(
              children: [
                TextButton.icon(
                  onPressed: () => _showCreateAgentDialog(context, ref),
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('新建 Agent'),
                ),
                const SizedBox(width: 8),
                TextButton.icon(
                  onPressed: () => _discoverOpencodeSessions(context, ref),
                  icon: const Icon(Icons.search, size: 18),
                  label: const Text('发现 OpenCode'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  void _showCreateAgentDialog(BuildContext context, WidgetRef ref) {
    final cwdCtrl = TextEditingController();
    final nameCtrl = TextEditingController();
    final sessionCtrl = TextEditingController();
    String provider = 'claude';
    showDialog(
      context: context,
      builder: (_) => StatefulBuilder(
        builder: (context, setState) => AlertDialog(
          title: Text('在 ${node.name} 上新建 Agent'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextField(
                controller: nameCtrl,
                decoration: const InputDecoration(labelText: '名称', hintText: 'claude-1'),
              ),
              TextField(
                controller: cwdCtrl,
                decoration: const InputDecoration(labelText: '工作目录', hintText: '/home/user/proj'),
              ),
              const SizedBox(height: 8),
              SegmentedButton<String>(
                segments: const [
                  ButtonSegment(value: 'claude', label: Text('Claude')),
                  ButtonSegment(value: 'opencode', label: Text('OpenCode')),
                ],
                selected: {provider},
                onSelectionChanged: (s) => setState(() => provider = s.first),
              ),
              if (provider == 'opencode') ...[
                const SizedBox(height: 8),
                TextField(
                  controller: sessionCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Session ID (可选)',
                    hintText: 'ses_xxx',
                    helperText: '留空创建新 session',
                  ),
                ),
              ],
            ],
          ),
          actions: [
            TextButton(onPressed: () => Navigator.pop(context), child: const Text('取消')),
            FilledButton(
              onPressed: () async {
                Navigator.pop(context);
                final client = ref.read(connectionProvider);
                if (client == null) return;
                final params = {
                  'nodeId': node.id,
                  'name': nameCtrl.text.trim(),
                  'workDir': cwdCtrl.text.trim(),
                  'provider': provider,
                };
                if (provider == 'opencode' && sessionCtrl.text.trim().isNotEmpty) {
                  params['sessionId'] = sessionCtrl.text.trim();
                }
                try {
                  await client.call('agent.create', params);
                  // Refresh agent list
                  final result = await client.call('agent.list', {'nodeId': node.id});
                  final agents = (result is List ? result : (result['agents'] as List?) ?? []);
                  ref.read(nodesProvider.notifier).loadAgents(node.id, agents);
                } catch (e) {
                  debugPrint('Create agent error: $e');
                }
              },
              child: const Text('创建'),
            ),
          ],
        ),
      ),
    );
  }

  void _discoverOpencodeSessions(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      final result = await client.call('opencode.discover', {'nodeId': node.id});
      final sessions = (result['sessions'] as List?) ?? [];
      if (sessions.isEmpty) {
        if (context.mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('未发现 OpenCode sessions')),
          );
        }
        return;
      }
      if (context.mounted) {
        showDialog(
          context: context,
          builder: (context) => AlertDialog(
            title: const Text('发现 OpenCode Sessions'),
            content: SizedBox(
              width: double.maxFinite,
              child: ListView.builder(
                shrinkWrap: true,
                itemCount: sessions.length,
                itemBuilder: (context, index) {
                  final s = sessions[index] as Map<String, dynamic>;
                  return ListTile(
                    dense: true,
                    title: Text(s['id'] as String? ?? 'Unknown'),
                    subtitle: Text('Size: ${s['size'] ?? 0} bytes'),
                    trailing: TextButton(
                      onPressed: () async {
                        Navigator.pop(context);
                        // Create agent for this session
                        try {
                          await client.call('agent.create', {
                            'nodeId': node.id,
                            'name': 'opencode-${s['id']}'.substring(0, 20),
                            'provider': 'opencode',
                            'workDir': '/home/fengming.xie',
                            'sessionId': s['id'],
                          });
                          // Refresh
                          final ar = await client.call('agent.list', {'nodeId': node.id});
                          final agents = (ar is List ? ar : (ar['agents'] as List?) ?? []);
                          ref.read(nodesProvider.notifier).loadAgents(node.id, agents);
                        } catch (e) {
                          debugPrint('Attach session error: $e');
                        }
                      },
                      child: const Text('附加'),
                    ),
                  );
                },
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(context),
                child: const Text('关闭'),
              ),
            ],
          ),
        );
      }
    } catch (e) {
      debugPrint('Discover error: $e');
    }
  }
}

class AgentRow extends ConsumerWidget {
  final AgentModel agent;
  final String nodeId;
  const AgentRow({super.key, required this.agent, required this.nodeId});

  Color get _statusColor {
    switch (agent.status) {
      case AgentStatus.working:
        return Colors.blue;
      case AgentStatus.idle:
        return Colors.yellow.shade700;
      case AgentStatus.starting:
        return Colors.orange;
      case AgentStatus.stopped:
        return Colors.grey;
      case AgentStatus.crashed:
        return Colors.red;
    }
  }

  String get _statusLabel {
    switch (agent.status) {
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

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 24),
      leading: Icon(Icons.smart_toy, color: _statusColor, size: 20),
      title: Text(agent.name),
      subtitle: Text('${agent.provider} · $_statusLabel', style: TextStyle(color: _statusColor, fontSize: 12)),
      trailing: agent.status == AgentStatus.working
          ? const SizedBox(
              width: 16,
              height: 16,
              child: CircularProgressIndicator(strokeWidth: 2),
            )
          : null,
      onTap: () => context.push('/agent/${nodeId}/${agent.id}'),
    );
  }
}
