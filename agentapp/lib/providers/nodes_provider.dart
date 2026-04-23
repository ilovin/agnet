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
  void loadNodes(List<dynamic> rawNodes) {
    final map = <String, NodeModel>{};
    for (final n in rawNodes) {
      final node = NodeModel.fromJson(n as Map<String, dynamic>);
      map[node.id] = node;
    }
    state = state.copyWith(nodes: map);
  }

  /// Load agents for a given node from [agent.list] response.
  void loadAgents(String nodeId, List<dynamic> rawAgents) {
    final list = rawAgents.map((a) {
      final json = a as Map<String, dynamic>;
      json['nodeId'] = nodeId; // inject nodeId
      return AgentModel.fromJson(json);
    }).toList();
    final updated = Map<String, List<AgentModel>>.from(state.agents);
    updated[nodeId] = list;
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
    if (nodeId.isEmpty || agentId == null || agentId.isEmpty) return;
    final agentList = List<AgentModel>.from(state.agents[nodeId] ?? []);
    final idx = agentList.indexWhere((a) => a.id == agentId);
    if (idx == -1) {
      _refreshAgents(nodeId);
      return;
    }
    final status = _parseAgentStatus(params['status'] as String? ?? '');
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
