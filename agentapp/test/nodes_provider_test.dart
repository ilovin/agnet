import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/services/ws_client.dart';

void main() {
  group('NodesNotifier', () {
    late ProviderContainer container;
    late NodesNotifier notifier;

    setUp(() {
      container = ProviderContainer();
      notifier = container.read(nodesProvider.notifier);
    });

    tearDown(() => container.dispose());

    test('loadNodes populates node map', () {
      notifier.loadNodes([
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'connected',
        },
        {
          'id': 'n2',
          'name': 'remote2',
          'host': '10.0.0.2',
          'status': 'disconnected',
        },
      ]);
      final state = container.read(nodesProvider);
      expect(state.nodeList.length, equals(2));
      expect(state.nodes['n1']?.status, equals(NodeStatus.connected));
    });

    test('loadAgents populates agents for node', () {
      notifier.loadNodes([
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'connected',
        },
      ]);
      notifier.loadAgents('n1', [
        {
          'id': 'a1',
          'name': 'claude-1',
          'status': 'idle',
          'workDir': '/home',
          'nodeId': 'n1',
        },
      ]);
      final state = container.read(nodesProvider);
      expect(state.agentsFor('n1').length, equals(1));
      expect(state.agentsFor('n1')[0].status, equals(AgentStatus.idle));
    });

    test('handleEvent node.status_changed updates node status', () {
      notifier.loadNodes([
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'disconnected',
        },
      ]);
      notifier.handleEvent(
        WsMessage(
          method: 'node.status_changed',
          params: {'nodeId': 'n1', 'status': 'connected'},
        ),
      );
      final state = container.read(nodesProvider);
      expect(state.nodes['n1']?.status, equals(NodeStatus.connected));
    });

    test('handleEvent node.status_changed supports deploying state', () {
      notifier.loadNodes([
        {
          'id': 'n1',
          'name': 'remote1',
          'host': '10.0.0.1',
          'status': 'disconnected',
        },
      ]);
      notifier.handleEvent(
        WsMessage(
          method: 'node.status_changed',
          params: {'nodeId': 'n1', 'status': 'deploying'},
        ),
      );
      final state = container.read(nodesProvider);
      expect(state.nodes['n1']?.status, equals(NodeStatus.deploying));
    });

    test('handleEvent agent.status_changed updates agent status', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
      ]);
      notifier.loadAgents('n1', [
        {
          'id': 'a1',
          'name': 'claude-1',
          'status': 'idle',
          'workDir': '/home',
          'nodeId': 'n1',
        },
      ]);
      notifier.handleEvent(
        WsMessage(
          method: 'agent.status_changed',
          params: {'nodeId': 'n1', 'agentId': 'a1', 'status': 'working'},
        ),
      );
      final state = container.read(nodesProvider);
      expect(state.agentsFor('n1')[0].status, equals(AgentStatus.working));
    });

    test('handleEvent agent.status_changed removes removed agent', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
      ]);
      notifier.loadAgents('n1', [
        {
          'id': 'a1',
          'name': 'claude-1',
          'status': 'idle',
          'workDir': '/home',
          'nodeId': 'n1',
        },
      ]);

      notifier.handleEvent(
        WsMessage(
          method: 'agent.status_changed',
          params: {'nodeId': 'n1', 'agentId': 'a1', 'status': 'removed'},
        ),
      );

      expect(container.read(nodesProvider).agentsFor('n1'), isEmpty);
    });

    // ---- R-013: loadNodes merge (preserve WS-updated status) ----

    test('loadNodes merge: WS-updated status preserved when RPC returns stale data', () {
      // 1. Initial load from RPC
      notifier.loadNodes([
        {'id': 'n1', 'name': 'remote1', 'host': '10.0.0.1', 'status': 'disconnected'},
      ]);
      // 2. WS event sets status to connected
      notifier.handleEvent(WsMessage(
        method: 'node.status_changed',
        params: {'nodeId': 'n1', 'status': 'connected'},
      ));
      expect(container.read(nodesProvider).nodes['n1']?.status,
          equals(NodeStatus.connected));

      // 3. Polling RPC returns stale 'disconnected' status
      notifier.loadNodes([
        {'id': 'n1', 'name': 'remote1-renamed', 'host': '10.0.0.1', 'status': 'disconnected'},
      ]);
      final state = container.read(nodesProvider);
      // Status should be preserved from WS event (connected), not overwritten by RPC (disconnected)
      expect(state.nodes['n1']?.status, equals(NodeStatus.connected));
      // Static fields should be updated from RPC
      expect(state.nodes['n1']?.name, equals('remote1-renamed'));
    });

    test('loadNodes merge: new node from RPC is added, missing node is removed', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'node1', 'host': '10.0.0.1', 'status': 'connected'},
        {'id': 'n2', 'name': 'node2', 'host': '10.0.0.2', 'status': 'connected'},
      ]);

      // RPC now returns n2 + n3 (n1 is gone, n3 is new)
      notifier.loadNodes([
        {'id': 'n2', 'name': 'node2', 'host': '10.0.0.2', 'status': 'connected'},
        {'id': 'n3', 'name': 'node3', 'host': '10.0.0.3', 'status': 'disconnected'},
      ]);

      final state = container.read(nodesProvider);
      expect(state.nodes.containsKey('n1'), isFalse, reason: 'n1 should be removed');
      expect(state.nodes.containsKey('n2'), isTrue, reason: 'n2 should remain');
      expect(state.nodes.containsKey('n3'), isTrue, reason: 'n3 should be added');
    });

    // ---- R-013: loadAgents merge (preserve WS-updated dynamic fields) ----

    test('loadAgents merge: WS-updated dynamic fields preserved when RPC returns stale data', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
      ]);
      notifier.loadAgents('n1', [
        {'id': 'a1', 'name': 'claude-1', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1'},
      ]);

      // WS event updates dynamic fields
      notifier.handleEvent(WsMessage(
        method: 'agent.status_changed',
        params: {
          'nodeId': 'n1', 'agentId': 'a1', 'status': 'working',
          'runtimeState': 'live',
          'sessionState': 'active',
          'sessionStateReason': 'agent is working',
          'sessionControl': 'managed',
          'providerState': 'drifted',
          'providerScope': 'inherited',
          'providerWriteMode': 'read_only',
          'providerReadOnlyReason': 'test reason',
          'permissionMode': 'plan',
          'lastMessageTime': 1700000000000,
        },
      ));
      final wsAgent = container.read(nodesProvider).agentsFor('n1')[0];
      expect(wsAgent.status, equals(AgentStatus.working));
      expect(wsAgent.runtimeState, equals('live'));
      expect(wsAgent.sessionState, equals('active'));
      expect(wsAgent.providerState, equals('drifted'));
      expect(wsAgent.lastMessageTime, equals(1700000000000));

      // Polling RPC returns stale data
      notifier.loadAgents('n1', [
        {
          'id': 'a1', 'name': 'claude-1-renamed', 'status': 'idle',
          'workDir': '/new-home', 'nodeId': 'n1',
          'pid': 1234,
          'runtimeState': null,
          'sessionState': null,
        },
      ]);

      final agent = container.read(nodesProvider).agentsFor('n1')[0];
      // Dynamic fields preserved from WS
      expect(agent.status, equals(AgentStatus.working), reason: 'status from WS');
      expect(agent.runtimeState, equals('live'), reason: 'runtimeState from WS');
      expect(agent.sessionState, equals('active'), reason: 'sessionState from WS');
      expect(agent.lastMessageTime, equals(1700000000000), reason: 'lastMessageTime from WS');
      // Static fields updated from RPC
      expect(agent.name, equals('claude-1-renamed'), reason: 'name from RPC');
      expect(agent.workDir, equals('/new-home'), reason: 'workDir from RPC');
      expect(agent.pid, equals(1234), reason: 'pid from RPC');
    });

    test('loadAgents merge: new agent added, missing agent removed', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
      ]);
      notifier.loadAgents('n1', [
        {'id': 'a1', 'name': 'claude-1', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1'},
        {'id': 'a2', 'name': 'claude-2', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1'},
      ]);

      // RPC returns a2 + a3 (a1 gone, a3 new)
      notifier.loadAgents('n1', [
        {'id': 'a2', 'name': 'claude-2', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1'},
        {'id': 'a3', 'name': 'claude-3', 'status': 'starting', 'workDir': '/new', 'nodeId': 'n1'},
      ]);

      final agents = container.read(nodesProvider).agentsFor('n1');
      expect(agents.length, equals(2));
      expect(agents.any((a) => a.id == 'a1'), isFalse, reason: 'a1 should be removed');
      expect(agents.any((a) => a.id == 'a2'), isTrue, reason: 'a2 should remain');
      expect(agents.any((a) => a.id == 'a3'), isTrue, reason: 'a3 should be added');
    });

    test('loadAgents merge: preserves WS-updated isReadOnly and readOnlyReason', () {
      notifier.loadNodes([
        {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
      ]);
      notifier.loadAgents('n1', [
        {'id': 'a1', 'name': 'claude-1', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1'},
      ]);

      // WS event sets isReadOnly = true with reason
      notifier.handleEvent(WsMessage(
        method: 'agent.status_changed',
        params: {
          'nodeId': 'n1', 'agentId': 'a1', 'status': 'working',
          'readOnly': true,
          'readOnlyReason': 'provider scope is inherited',
        },
      ));
      expect(container.read(nodesProvider).agentsFor('n1')[0].isReadOnly, isTrue);

      // RPC returns isReadOnly = false (stale)
      notifier.loadAgents('n1', [
        {'id': 'a1', 'name': 'claude-1', 'status': 'idle', 'workDir': '/home', 'nodeId': 'n1', 'readOnly': false},
      ]);

      final agent = container.read(nodesProvider).agentsFor('n1')[0];
      expect(agent.isReadOnly, isTrue, reason: 'isReadOnly from WS should be preserved');
      expect(agent.readOnlyReason, equals('provider scope is inherited'));
    });

    test(
      'handleEvent agent.status_changed updates session and provider fields',
      () {
        notifier.loadNodes([
          {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'connected'},
        ]);
        notifier.loadAgents('n1', [
          {
            'id': 'a1',
            'name': 'claude-1',
            'status': 'idle',
            'workDir': '/home',
            'nodeId': 'n1',
          },
        ]);
        notifier.handleEvent(
          WsMessage(
            method: 'agent.status_changed',
            params: {
              'nodeId': 'n1',
              'agentId': 'a1',
              'status': 'working',
              'pid': 0,
              'sessionId': '',
              'projectName': '',
              'runtimeState': 'live',
              'sessionState': 'active',
              'sessionStateReason': 'agent is currently producing output',
              'sessionControl': 'managed',
              'providerState': 'drifted',
              'providerScope': 'inherited',
              'providerWriteMode': 'read_only',
              'providerReadOnlyReason':
                  'provider scope is inherited from root session',
            },
          ),
        );
        final agent = container.read(nodesProvider).agentsFor('n1')[0];
        expect(agent.status, equals(AgentStatus.working));
        expect(agent.pid, equals(0));
        expect(agent.sessionId, equals(''));
        expect(agent.projectName, equals(''));
        expect(agent.runtimeState, equals('live'));
        expect(agent.sessionState, equals('active'));
        expect(
          agent.sessionStateReason,
          equals('agent is currently producing output'),
        );
        expect(agent.sessionControl, equals('managed'));
        expect(agent.providerState, equals('drifted'));
        expect(agent.providerScope, equals('inherited'));
        expect(agent.providerWriteMode, equals('read_only'));
        expect(
          agent.providerReadOnlyReason,
          equals('provider scope is inherited from root session'),
        );
      },
    );
  });
}
