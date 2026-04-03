import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/connection_config.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

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

  group('SessionCandidate', () {
    test('fromJson parses fields and lowercases provider', () {
      final c = SessionCandidate.fromJson({
        'pid': 123,
        'provider': 'Claude',
        'workDir': '/tmp/work',
        'session': 'ses_abc',
        'terminal': 'ttys001',
      });

      expect(c.pid, equals(123));
      expect(c.provider, equals('claude'));
      expect(c.workDir, equals('/tmp/work'));
      expect(c.sessionId, equals('ses_abc'));
      expect(c.terminal, equals('ttys001'));
    });

    test('fromJson derives sessionId from sessionFile path', () {
      final c = SessionCandidate.fromJson({
        'provider': 'opencode',
        'workDir': '/tmp',
        'sessionFile': '/home/user/.claude/projects/ses_file123.jsonl',
      });

      expect(c.sessionId, equals('ses_file123'));
    });
  });

  group('parseSessionCandidates', () {
    test('parses both list and wrapped map response', () {
      final fromList = parseSessionCandidates([
        {'pid': 1, 'provider': 'claude', 'workDir': '/a'},
      ]);
      final fromMap = parseSessionCandidates({
        'processes': [
          {'pid': 2, 'provider': 'opencode', 'workDir': '/b'},
        ],
      });

      expect(fromList.length, equals(1));
      expect(fromList.first.pid, equals(1));
      expect(fromMap.length, equals(1));
      expect(fromMap.first.pid, equals(2));
    });
  });
}
