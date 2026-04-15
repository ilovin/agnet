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

  List<NodeModel> get nodeList => nodes.values.toList();
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
    // New agent discovered — trigger full refresh so it appears in the list
    if (idx == -1) {
      _refreshAgents(nodeId);
      return;
    }
    final name = params['name'] as String?;
    agentList[idx] = agentList[idx].copyWith(
      status: _parseAgentStatus(params['status'] as String? ?? ''),
      name: name,
      runtimeState: params['runtimeState'] as String?,
      sessionState: params['sessionState'] as String?,
      sessionStateReason: params['sessionStateReason'] as String?,
      sessionControl: params['sessionControl'] as String?,
      providerState: params['providerState'] as String?,
      providerScope: params['providerScope'] as String?,
      providerWriteMode: params['providerWriteMode'] as String?,
      providerReadOnlyReason: params['providerReadOnlyReason'] as String?,
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
