import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../services/ws_client.dart';

class NodeState {
  final Map<String, NodeModel> nodes;
  final Map<String, List<AgentModel>> agents; // keyed by nodeId

  const NodeState({this.nodes = const {}, this.agents = const {}});

  NodeState copyWith({
    Map<String, NodeModel>? nodes,
    Map<String, List<AgentModel>>? agents,
  }) => NodeState(nodes: nodes ?? this.nodes, agents: agents ?? this.agents);

  List<NodeModel> get nodeList {
    final list = nodes.values.toList();
    list.sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase()));
    return list;
  }
  List<AgentModel> agentsFor(String nodeId) => agents[nodeId] ?? [];
}

class NodesNotifier extends StateNotifier<NodeState> {
  Future<void> Function(String nodeId)? onAgentsRefresh;

  NodesNotifier() : super(const NodeState());

  /// Load initial state from [node.list] response.
  /// Merges with existing state: preserves WS-updated status for known nodes,
  /// adds new nodes, removes nodes not returned by RPC.
  void loadNodes(List<dynamic> rawNodes) {
    final map = <String, NodeModel>{};
    for (final n in rawNodes) {
      final rpcNode = NodeModel.fromJson(n as Map<String, dynamic>);
      final existing = state.nodes[rpcNode.id];
      if (existing != null) {
        // Preserve WS-updated status (more real-time), update static fields from RPC
        map[rpcNode.id] = existing.copyWith(
          name: rpcNode.name,
          host: rpcNode.host,
          location: rpcNode.location,
          agentCount: rpcNode.agentCount,
        );
      } else {
        map[rpcNode.id] = rpcNode;
      }
    }
    state = state.copyWith(nodes: map);
  }

  /// Load agents for a given node from [agent.list] response.
  /// Merges with existing state: preserves WS-updated dynamic fields for known agents,
  /// adds new agents, removes agents not returned by RPC.
  void loadAgents(String nodeId, List<dynamic> rawAgents) {
    final rpcAgents = rawAgents.map((a) {
      final json = a as Map<String, dynamic>;
      json['nodeId'] = nodeId; // inject nodeId
      return AgentModel.fromJson(json);
    }).toList();

    final existing = <String, AgentModel>{
      for (final a in state.agents[nodeId] ?? const <AgentModel>[]) a.id: a,
    };

    final merged = <AgentModel>[];
    for (final rpcAgent in rpcAgents) {
      final prev = existing[rpcAgent.id];
      if (prev != null) {
        // Preserve WS-updated dynamic fields, update static fields from RPC
        merged.add(AgentModel(
          id: rpcAgent.id,
          nodeId: rpcAgent.nodeId,
          // Static fields from RPC
          name: rpcAgent.name,
          workDir: rpcAgent.workDir,
          provider: rpcAgent.provider,
          pid: rpcAgent.pid,
          hasHistory: rpcAgent.hasHistory,
          attachMode: rpcAgent.attachMode,
          projectName: rpcAgent.projectName,
          sessionId: rpcAgent.sessionId,
          // Dynamic fields preserved from WS (local state)
          status: prev.status,
          runtimeState: prev.runtimeState,
          sessionState: prev.sessionState,
          sessionStateReason: prev.sessionStateReason,
          sessionControl: prev.sessionControl,
          providerState: prev.providerState,
          providerScope: prev.providerScope,
          providerWriteMode: prev.providerWriteMode,
          providerReadOnlyReason: prev.providerReadOnlyReason,
          permissionMode: prev.permissionMode,
          isReadOnly: prev.isReadOnly,
          readOnlyReason: prev.readOnlyReason,
          lastMessageTime: (rpcAgent.lastMessageTime != null &&
                  (prev.lastMessageTime == null || rpcAgent.lastMessageTime! > prev.lastMessageTime!))
              ? rpcAgent.lastMessageTime
              : prev.lastMessageTime,
        ));
      } else {
        // New agent from RPC
        merged.add(rpcAgent);
      }
    }

    final updated = Map<String, List<AgentModel>>.from(state.agents);
    updated[nodeId] = merged;
    state = state.copyWith(agents: updated);
  }

  /// Handle a push event from the server.
  void handleEvent(WsMessage event) {
    switch (event.method) {
      case 'node.status_changed':
        _handleNodeStatus(event.params as Map<String, dynamic>);
      case 'agent.status_changed':
        _handleAgentStatus(event.params as Map<String, dynamic>);
    }
  }

  void _handleNodeStatus(Map<String, dynamic> params) {
    final nodeId = params['nodeId'] as String;
    final existing = state.nodes[nodeId];
    if (existing == null) return;
    final updated = Map<String, NodeModel>.from(state.nodes);
    updated[nodeId] = existing.copyWith(
      status: _parseNodeStatus(params['status'] as String? ?? ''),
    );
    state = state.copyWith(nodes: updated);
  }

  void _handleAgentStatus(Map<String, dynamic> params) {
    final nodeId = params['nodeId'] as String? ?? '';
    final agentId = params['agentId'] as String?;
    print('[WS] agent.status_changed nodeId=$nodeId agentId=$agentId status=${params['status']}');
    if (nodeId.isEmpty || agentId == null || agentId.isEmpty) {
      print('[WS] agent.status_changed: missing nodeId or agentId, skipping');
      return;
    }
    final agentList = List<AgentModel>.from(state.agents[nodeId] ?? []);
    final idx = agentList.indexWhere((a) => a.id == agentId);
    print('[WS] agent.status_changed: found ${agentList.length} agents, idx=$idx');
    if (idx == -1) {
      print('[WS] agent.status_changed: agent not found, refreshing');
      _refreshAgents(nodeId);
      return;
    }
    final status = _parseAgentStatus(params['status'] as String? ?? '');
    print('[WS] agent.status_changed: updating status to $status');
    if ((params['status'] as String? ?? '') == 'removed') {
      agentList.removeAt(idx);
      final updated = Map<String, List<AgentModel>>.from(state.agents);
      updated[nodeId] = agentList;
      state = state.copyWith(agents: updated);
      return;
    }
    final current = agentList[idx];
    agentList[idx] = current.copyWith(
      status: status,
      name: (params['name'] as String?) ?? current.name,
      workDir: (params['workDir'] as String?) ?? current.workDir,
      pid: params.containsKey('pid')
          ? ((params['pid'] as num?)?.toInt() ?? 0)
          : current.pid,
      sessionId: params.containsKey('sessionId')
          ? ((params['sessionId'] as String?) ?? '')
          : current.sessionId,
      projectName: params.containsKey('projectName')
          ? ((params['projectName'] as String?) ?? '')
          : current.projectName,
      isReadOnly: params['readOnly'] as bool?,
      readOnlyReason: params['readOnlyReason'] as String?,
      attachMode: params['attachMode'] as String?,
      runtimeState: params['runtimeState'] as String?,
      sessionState: params['sessionState'] as String?,
      sessionStateReason: params['sessionStateReason'] as String?,
      sessionControl: params['sessionControl'] as String?,
      providerState: params['providerState'] as String?,
      providerScope: params['providerScope'] as String?,
      providerWriteMode: params['providerWriteMode'] as String?,
      providerReadOnlyReason: params['providerReadOnlyReason'] as String?,
      permissionMode: params['permissionMode'] as String?,
      // Defensive: never let an older WS lastMessageTime overwrite a newer one.
      lastMessageTime: (() {
        final wsTime = params.containsKey('lastMessageTime')
            ? (params['lastMessageTime'] as num?)?.toInt()
            : null;
        if (wsTime == null) return current.lastMessageTime;
        if (current.lastMessageTime == null || wsTime > current.lastMessageTime!) {
          return wsTime;
        }
        return current.lastMessageTime;
      })(),
    );
    final updated = Map<String, List<AgentModel>>.from(state.agents);
    updated[nodeId] = agentList;
    state = state.copyWith(agents: updated);
  }

  void _refreshAgents(String nodeId) {
    onAgentsRefresh?.call(nodeId);
  }

  /// Rename a node locally (after a successful node.rename RPC call).
  void renameNode(String nodeId, String name) {
    final existing = state.nodes[nodeId];
    if (existing == null) return;
    final updated = Map<String, NodeModel>.from(state.nodes);
    updated[nodeId] = existing.copyWith(name: name);
    state = state.copyWith(nodes: updated);
  }

  /// Rename an agent locally (after a successful agent.rename RPC call).
  void renameAgent(String nodeId, String agentId, String name) {
    final agentList = state.agents[nodeId];
    if (agentList == null) return;
    final updated = agentList.map((a) {
      if (a.id == agentId) return a.copyWith(name: name);
      return a;
    }).toList();
    final agentsMap = Map<String, List<AgentModel>>.from(state.agents);
    agentsMap[nodeId] = updated;
    state = state.copyWith(agents: agentsMap);
  }

  NodeStatus _parseNodeStatus(String s) {
    switch (s) {
      case 'connected':
        return NodeStatus.connected;
      case 'connecting':
        return NodeStatus.connecting;
      case 'deploying':
      case 'deployed':
        return NodeStatus.deploying;
      case 'error':
        return NodeStatus.error;
      default:
        return NodeStatus.disconnected;
    }
  }

  AgentStatus _parseAgentStatus(String s) {
    switch (s) {
      case 'working':
        return AgentStatus.working;
      case 'stopped':
        return AgentStatus.stopped;
      case 'crashed':
        return AgentStatus.crashed;
      case 'starting':
        return AgentStatus.starting;
      default:
        return AgentStatus.idle;
    }
  }
}

final nodesProvider = StateNotifierProvider<NodesNotifier, NodeState>(
  (_) => NodesNotifier(),
);
