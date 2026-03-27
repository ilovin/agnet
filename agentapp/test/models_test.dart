import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/connection_config.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/message_model.dart';

void main() {
  group('ConnectionConfig', () {
    test('serializes to/from JSON', () {
      final cfg = ConnectionConfig(url: 'ws://localhost:7374', token: 'tok123');
      final json = cfg.toJson();
      final restored = ConnectionConfig.fromJson(json);
      expect(restored.url, equals('ws://localhost:7374'));
      expect(restored.token, equals('tok123'));
    });
  });

  group('NodeModel', () {
    test('fromJson parses correctly', () {
      final n = NodeModel.fromJson({
        'id': 'n1',
        'name': 'remote1',
        'host': '10.0.0.1',
        'status': 'connected',
      });
      expect(n.id, equals('n1'));
      expect(n.status, equals(NodeStatus.connected));
    });

    test('copyWith changes only specified fields', () {
      final n = NodeModel(id: 'n1', name: 'r1', host: '10.0.0.1', status: NodeStatus.disconnected);
      final n2 = n.copyWith(status: NodeStatus.connected);
      expect(n2.id, equals('n1'));
      expect(n2.status, equals(NodeStatus.connected));
    });
  });

  group('AgentModel', () {
    test('fromJson parses status correctly', () {
      final a = AgentModel.fromJson({
        'id': 'a1',
        'name': 'claude-1',
        'status': 'working',
        'workDir': '/home/user/proj',
        'nodeId': 'n1',
      });
      expect(a.status, equals(AgentStatus.working));
      expect(a.nodeId, equals('n1'));
    });
  });

  group('MessageModel', () {
    test('fromJson parses role and text', () {
      final m = MessageModel.fromJson({
        'role': 'assistant',
        'text': 'Hello!',
        'seq': 42,
        'nodeId': 'n1',
        'agentId': 'a1',
      });
      expect(m.role, equals(MessageRole.assistant));
      expect(m.text, equals('Hello!'));
      expect(m.seq, equals(42));
    });
  });
}
