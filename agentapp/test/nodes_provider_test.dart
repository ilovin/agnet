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
