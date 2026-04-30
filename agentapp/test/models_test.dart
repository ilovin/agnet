import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/connection_config.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/message_model.dart';
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
      final n = NodeModel(
        id: 'n1',
        name: 'r1',
        host: '10.0.0.1',
        status: NodeStatus.disconnected,
        location: const NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: '10.0.0.1',
        ),
      );
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
      expect(a.isReadOnly, isFalse);
    });

    test('fromJson parses readOnly flag', () {
      final a = AgentModel.fromJson({
        'id': 'a2',
        'name': 'claude-attached-123',
        'status': 'idle',
        'workDir': '/home/user/proj',
        'nodeId': 'n1',
        'readOnly': true,
      });

      expect(a.isReadOnly, isTrue);
    });

    test('fromJson parses state machine fields', () {
      final a = AgentModel.fromJson({
        'id': 'a3',
        'name': 'claude-root',
        'status': 'idle',
        'workDir': '/home/user/proj',
        'nodeId': 'n1',
        'runtimeState': 'exited',
        'sessionState': 'resumable',
        'sessionStateReason': 'watcher detached after process exit',
        'sessionControl': 'rebindable',
        'providerState': 'drifted',
        'providerScope': 'inherited',
        'providerWriteMode': 'read_only',
        'providerReadOnlyReason': 'provider scope is inherited from root session',
      });

      expect(a.runtimeState, equals('exited'));
      expect(a.sessionState, equals('resumable'));
      expect(a.sessionStateReason, contains('watcher detached'));
      expect(a.sessionControl, equals('rebindable'));
      expect(a.providerState, equals('drifted'));
      expect(a.providerScope, equals('inherited'));
      expect(a.providerWriteMode, equals('read_only'));
      expect(a.providerReadOnlyReason, contains('inherited'));
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

    test('fromJson parses attach metadata', () {
      final c = SessionCandidate.fromJson({
        'pid': 123,
        'provider': 'Claude',
        'workDir': '/tmp/work',
        'session': 'ses_abc',
        'terminal': 'ttys001',
        'attachMode': 'tmux',
        'readOnly': true,
        'readOnlyReason': 'no safe input route found',
      });

      expect(c.pid, equals(123));
      expect(c.provider, equals('claude'));
      expect(c.workDir, equals('/tmp/work'));
      expect(c.sessionId, equals('ses_abc'));
      expect(c.terminal, equals('ttys001'));
      expect(c.attachMode, equals('tmux'));
      expect(c.isReadOnly, isTrue);
      expect(c.readOnlyReason, equals('no safe input route found'));
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

  group('auto-attach session selection', () {
    test('prefers writable tmux session over newer read-only session', () {
      final candidate = pickPreferredAutoAttachCandidate([
        SessionCandidate.fromJson({
          'pid': 300,
          'provider': 'claude',
          'workDir': '/tmp/readonly',
          'attachMode': 'watcher',
          'readOnly': true,
        }),
        SessionCandidate.fromJson({
          'pid': 200,
          'provider': 'claude',
          'workDir': '/tmp/writable',
          'attachMode': 'tmux',
          'readOnly': false,
        }),
      ]);

      expect(candidate, isNotNull);
      expect(candidate!.pid, equals(200));
      expect(candidate.attachMode, equals('tmux'));
      expect(candidate.isReadOnly, isFalse);
    });

    test('skips auto-attach when no writable tmux session exists', () {
      final candidate = pickPreferredAutoAttachCandidate([
        SessionCandidate.fromJson({
          'pid': 300,
          'provider': 'claude',
          'workDir': '/tmp/readonly',
          'attachMode': 'watcher',
          'readOnly': true,
        }),
        SessionCandidate.fromJson({
          'pid': 200,
          'provider': 'claude',
          'workDir': '/tmp/non-tmux',
          'attachMode': 'pty',
          'readOnly': false,
        }),
      ]);

      expect(candidate, isNull);
    });
  });

  group('session identity', () {
    test('uses pid instead of workDir fallback for live claude sessions', () {
      final keyA = sessionIdentityKey(
        provider: 'claude',
        pid: 6864,
      );
      final keyB = sessionIdentityKey(
        provider: 'claude',
        pid: 6865,
      );

      expect(keyA, equals('claude|pid:6864'));
      expect(keyB, equals('claude|pid:6865'));
      expect(keyA, isNot(equals(keyB)));
    });

    test('does not fall back to workDir when pid and sessionId are missing', () {
      final key = sessionIdentityKey(
        provider: 'claude',
        agentId: 'agent-1',
      );

      expect(key, equals('claude|agent:agent-1'));
    });
  });

  group('session id display', () {
    test('uses the first 8 characters for short session labels', () {
      expect(shortSessionId('758f2876-f103-4432-a693-4098cd0ac73c'), equals('758f2876'));
      expect(shortSessionId('cea53ceb-eb6f-42a6-b061-6ba00d33c7cc'), equals('cea53ceb'));
      expect(shortSessionId('short'), equals('short'));
    });
  });

  test('parses OpenCode file sessions with first-8 project label', () {
    final sessions = parseSessionCandidates({
      'opencodeFiles': [
        {'id': 'cea53ceb-eb6f-42a6-b061-6ba00d33c7cc'},
      ],
    });

    expect(sessions.length, equals(1));
    expect(sessions.first.provider, equals('opencode'));
    expect(sessions.first.sessionId, equals('cea53ceb-eb6f-42a6-b061-6ba00d33c7cc'));
    expect(sessions.first.projectName, equals('cea53ceb'));
  });

  group('managed agent titles', () {
    test('keeps attached auto-name title instead of session id', () {
      final agent = AgentModel.fromJson({
        'id': 'a4',
        'name': 'phone-talk - 6864',
        'status': 'idle',
        'provider': 'claude',
        'workDir': '/tmp/phone-talk',
        'nodeId': 'n1',
        'pid': 6864,
        'sessionId': 'cea53ceb-eb6f-42a6-b061-6ba00d33c7cc',
      });

      expect(managedAgentTitle(agent), equals('phone-talk - 6864'));
      expect(managedAgentSortTitle(agent), equals('phone-talk - 6864'));
    });

    test('sort is stable when managedAgentSortTitle ties (uses id as tie-breaker)', () {
      final agents = [
        AgentModel.fromJson({
          'id': 'z-id',
          'name': 'same-name',
          'status': 'idle',
          'provider': 'claude',
          'workDir': '/tmp/a',
          'nodeId': 'n1',
        }),
        AgentModel.fromJson({
          'id': 'a-id',
          'name': 'same-name',
          'status': 'idle',
          'provider': 'claude',
          'workDir': '/tmp/b',
          'nodeId': 'n1',
        }),
        AgentModel.fromJson({
          'id': 'm-id',
          'name': 'same-name',
          'status': 'idle',
          'provider': 'claude',
          'workDir': '/tmp/c',
          'nodeId': 'n1',
        }),
      ];

      agents.sort((a, b) {
        final at = managedAgentSortTitle(a);
        final bt = managedAgentSortTitle(b);
        final cmp = at.compareTo(bt);
        if (cmp != 0) return cmp;
        return a.id.compareTo(b.id);
      });

      expect(agents.map((a) => a.id).toList(), equals(['a-id', 'm-id', 'z-id']));
    });
  });

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

  group('MessageModel', () {
    test('fromJson parses timestamp when present', () {
      final m = MessageModel.fromJson({
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'user',
        'text': 'hello',
        'seq': 1,
        'timestamp': 1714464000000,
      });
      expect(m.timestamp, equals(1714464000000));
    });

    test('fromJson leaves timestamp null when absent', () {
      final m = MessageModel.fromJson({
        'nodeId': 'n1',
        'agentId': 'a1',
        'role': 'assistant',
        'text': 'hi',
        'seq': 2,
      });
      expect(m.timestamp, isNull);
    });

    test('copyWith updates timestamp', () {
      final m = MessageModel(
        nodeId: 'n1',
        agentId: 'a1',
        role: MessageRole.user,
        text: 'hello',
        seq: 1,
        timestamp: 1000,
      );
      final m2 = m.copyWith(timestamp: 2000);
      expect(m2.timestamp, equals(2000));
      expect(m2.text, equals('hello'));
    });
  });
}
