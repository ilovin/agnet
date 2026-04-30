# agentapp MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `agentapp` — a Flutter Android/iOS App that connects to agentgw via WebSocket JSON-RPC 2.0, displays a dashboard of all remote nodes and their agents, enables conversation with individual agents, and supports start/stop/restart controls with automatic reconnection.

**Architecture:** A single `WsClient` manages the WebSocket connection with exponential-backoff reconnection. Riverpod providers derive UI state from incoming JSON-RPC events and method call responses. All screens are stateless widgets driven by Riverpod watches. GoRouter handles navigation.

**Tech Stack:** Flutter 3.x, Dart, `web_socket_channel ^3.0.0`, `flutter_markdown ^0.7.0`, `flutter_riverpod ^2.6.0`, `go_router ^14.0.0`, `shared_preferences ^2.3.0`.

**Depends on:** agentgw plan — agentapp calls agentgw's JSON-RPC API:
- Requests: `node.list`, `node.add`, `agent.list`, `agent.create`, `agent.stop`, `agent.restart`, `conversation.history`, `conversation.send`
- Push events: `node.status_changed {nodeId, status}`, `agent.status_changed {nodeId, agentId, status}`, `conversation.message {nodeId, agentId, role, text, seq}`

---

## File Structure

```
phone-talk/
└── agentapp/
    ├── lib/
    │   ├── main.dart
    │   ├── app.dart                            # MaterialApp + GoRouter setup
    │   ├── models/
    │   │   ├── connection_config.dart          # {url, token}
    │   │   ├── node_model.dart                 # {id, name, host, status}
    │   │   ├── agent_model.dart                # {id, name, status, workDir, nodeId}
    │   │   └── message_model.dart              # {role, text, seq}
    │   ├── services/
    │   │   ├── ws_client.dart                  # JSON-RPC over WS + exponential reconnect
    │   │   └── connection_store.dart           # SharedPreferences save/load
    │   ├── providers/
    │   │   ├── connection_provider.dart        # current WS connection state
    │   │   ├── nodes_provider.dart             # nodes + nested agents
    │   │   └── conversation_provider.dart      # messages for (nodeId, agentId)
    │   └── screens/
    │       ├── connections_screen.dart         # connection list + AddConnectionSheet
    │       ├── dashboard_screen.dart           # NodeCard + AgentRow
    │       ├── agent_detail_screen.dart        # ConversationView + InputBar + ControlBar
    │       └── settings_screen.dart           # manage saved connections
    ├── test/
    │   ├── ws_client_test.dart
    │   ├── nodes_provider_test.dart
    │   └── conversation_provider_test.dart
    └── pubspec.yaml
```

---

## Task 1: Flutter Project Initialization

**Files:**
- Create: `agentapp/` (via flutter create)
- Modify: `agentapp/pubspec.yaml`

- [ ] **Step 1: Create Flutter project**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
flutter create agentapp --org com.phonetalk --platforms android,ios
```

Expected: agentapp directory created with Flutter project structure.

- [ ] **Step 2: Update pubspec.yaml dependencies**

Replace the `dependencies:` section in `agentapp/pubspec.yaml`:

```yaml
dependencies:
  flutter:
    sdk: flutter
  web_socket_channel: ^3.0.0
  flutter_markdown: ^0.7.4
  flutter_riverpod: ^2.6.1
  go_router: ^14.6.2
  shared_preferences: ^2.3.3
  uuid: ^4.5.1

dev_dependencies:
  flutter_test:
    sdk: flutter
  flutter_lints: ^5.0.0
  mocktail: ^1.0.4
```

- [ ] **Step 3: Install dependencies**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter pub get
```

Expected: All packages resolved, no errors.

- [ ] **Step 4: Verify project compiles**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter analyze
```

Expected: No issues found.

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/
git commit -m "feat(agentapp): initialize Flutter project with dependencies"
```

---

## Task 2: Models

**Files:**
- Create: `agentapp/lib/models/connection_config.dart`
- Create: `agentapp/lib/models/node_model.dart`
- Create: `agentapp/lib/models/agent_model.dart`
- Create: `agentapp/lib/models/message_model.dart`
- Test: `agentapp/test/models_test.dart`

- [ ] **Step 1: Write failing test**

Create `agentapp/test/models_test.dart`:

```dart
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
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/models_test.dart -v
```

Expected: compilation error — models not found.

- [ ] **Step 3: Implement models**

Create `agentapp/lib/models/connection_config.dart`:

```dart
class ConnectionConfig {
  final String url;
  final String token;

  const ConnectionConfig({required this.url, required this.token});

  Map<String, dynamic> toJson() => {'url': url, 'token': token};

  factory ConnectionConfig.fromJson(Map<String, dynamic> json) =>
      ConnectionConfig(url: json['url'] as String, token: json['token'] as String);
}
```

Create `agentapp/lib/models/node_model.dart`:

```dart
enum NodeStatus { connected, disconnected, connecting, deploying, error }

NodeStatus _parseNodeStatus(String s) {
  switch (s) {
    case 'connected': return NodeStatus.connected;
    case 'connecting': return NodeStatus.connecting;
    case 'deploying': return NodeStatus.deploying;
    case 'error': return NodeStatus.error;
    default: return NodeStatus.disconnected;
  }
}

class NodeModel {
  final String id;
  final String name;
  final String host;
  final NodeStatus status;

  const NodeModel({
    required this.id,
    required this.name,
    required this.host,
    required this.status,
  });

  factory NodeModel.fromJson(Map<String, dynamic> json) => NodeModel(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        host: json['host'] as String? ?? '',
        status: _parseNodeStatus(json['status'] as String? ?? ''),
      );

  NodeModel copyWith({String? id, String? name, String? host, NodeStatus? status}) => NodeModel(
        id: id ?? this.id,
        name: name ?? this.name,
        host: host ?? this.host,
        status: status ?? this.status,
      );
}
```

Create `agentapp/lib/models/agent_model.dart`:

```dart
enum AgentStatus { starting, idle, working, stopped, crashed }

AgentStatus _parseAgentStatus(String s) {
  switch (s) {
    case 'starting': return AgentStatus.starting;
    case 'working': return AgentStatus.working;
    case 'stopped': return AgentStatus.stopped;
    case 'crashed': return AgentStatus.crashed;
    default: return AgentStatus.idle;
  }
}

class AgentModel {
  final String id;
  final String name;
  final String workDir;
  final String nodeId;
  final AgentStatus status;

  const AgentModel({
    required this.id,
    required this.name,
    required this.workDir,
    required this.nodeId,
    required this.status,
  });

  factory AgentModel.fromJson(Map<String, dynamic> json) => AgentModel(
        id: json['id'] as String,
        name: json['name'] as String? ?? '',
        workDir: json['workDir'] as String? ?? '',
        nodeId: json['nodeId'] as String? ?? '',
        status: _parseAgentStatus(json['status'] as String? ?? ''),
      );

  AgentModel copyWith({String? id, String? name, String? workDir, String? nodeId, AgentStatus? status}) =>
      AgentModel(
        id: id ?? this.id,
        name: name ?? this.name,
        workDir: workDir ?? this.workDir,
        nodeId: nodeId ?? this.nodeId,
        status: status ?? this.status,
      );
}
```

Create `agentapp/lib/models/message_model.dart`:

```dart
enum MessageRole { user, assistant }

class MessageModel {
  final String nodeId;
  final String agentId;
  final MessageRole role;
  final String text;
  final int seq;

  const MessageModel({
    required this.nodeId,
    required this.agentId,
    required this.role,
    required this.text,
    required this.seq,
  });

  factory MessageModel.fromJson(Map<String, dynamic> json) => MessageModel(
        nodeId: json['nodeId'] as String? ?? '',
        agentId: json['agentId'] as String? ?? '',
        role: (json['role'] as String?) == 'user' ? MessageRole.user : MessageRole.assistant,
        text: json['text'] as String? ?? '',
        seq: (json['seq'] as num?)?.toInt() ?? 0,
      );
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/models_test.dart -v
```

Expected:
```
All tests passed!
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/models/ agentapp/test/models_test.dart
git commit -m "feat(agentapp): add data models (Connection, Node, Agent, Message)"
```

---

## Task 3: WsClient

**Files:**
- Create: `agentapp/lib/services/ws_client.dart`
- Test: `agentapp/test/ws_client_test.dart`

- [ ] **Step 1: Write failing test**

Create `agentapp/test/ws_client_test.dart`:

```dart
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/services/ws_client.dart';

void main() {
  group('WsClient.buildRequest', () {
    test('builds valid JSON-RPC 2.0 request', () {
      final req = WsClient.buildRequest(id: 1, method: 'agent.list', params: {'nodeId': 'n1'});
      expect(req['jsonrpc'], equals('2.0'));
      expect(req['id'], equals(1));
      expect(req['method'], equals('agent.list'));
      expect(req['params'], equals({'nodeId': 'n1'}));
    });

    test('handles null params', () {
      final req = WsClient.buildRequest(id: 2, method: 'node.list', params: null);
      expect(req['params'], isNull);
    });
  });

  group('WsClient.parseMessage', () {
    test('identifies response by id presence', () {
      final msg = jsonEncode({'jsonrpc': '2.0', 'id': 1, 'result': {'ok': true}});
      final parsed = WsClient.parseMessage(msg);
      expect(parsed.isResponse, isTrue);
      expect(parsed.isEvent, isFalse);
      expect(parsed.id, equals(1));
    });

    test('identifies event by method without id', () {
      final msg = jsonEncode({
        'jsonrpc': '2.0',
        'method': 'agent.status_changed',
        'params': {'agentId': 'a1', 'status': 'working'},
      });
      final parsed = WsClient.parseMessage(msg);
      expect(parsed.isEvent, isTrue);
      expect(parsed.isResponse, isFalse);
      expect(parsed.method, equals('agent.status_changed'));
    });

    test('identifies error response', () {
      final msg = jsonEncode({
        'jsonrpc': '2.0',
        'id': 3,
        'error': {'code': -32000, 'message': 'not found'},
      });
      final parsed = WsClient.parseMessage(msg);
      expect(parsed.isResponse, isTrue);
      expect(parsed.isError, isTrue);
      expect(parsed.errorMessage, equals('not found'));
    });
  });

  group('ReconnectBackoff', () {
    test('doubles delay up to max', () {
      final b = ReconnectBackoff();
      expect(b.nextDelay(), equals(const Duration(seconds: 1)));
      expect(b.nextDelay(), equals(const Duration(seconds: 2)));
      expect(b.nextDelay(), equals(const Duration(seconds: 4)));
      expect(b.nextDelay(), equals(const Duration(seconds: 8)));
      expect(b.nextDelay(), equals(const Duration(seconds: 16)));
      expect(b.nextDelay(), equals(const Duration(seconds: 30))); // capped
      expect(b.nextDelay(), equals(const Duration(seconds: 30))); // stays capped
    });

    test('resets to initial after reset()', () {
      final b = ReconnectBackoff();
      b.nextDelay(); b.nextDelay(); b.nextDelay();
      b.reset();
      expect(b.nextDelay(), equals(const Duration(seconds: 1)));
    });
  });
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/ws_client_test.dart -v
```

Expected: compilation error.

- [ ] **Step 3: Implement ws_client.dart**

Create `agentapp/lib/services/ws_client.dart`:

```dart
import 'dart:async';
import 'dart:convert';
import 'package:web_socket_channel/web_socket_channel.dart';

/// Parsed incoming WS message — either a response or a server-push event.
class WsMessage {
  final Map<String, dynamic> raw;

  const WsMessage(this.raw);

  bool get isResponse => raw.containsKey('id') && raw['id'] != null;
  bool get isEvent => !isResponse && raw.containsKey('method');
  bool get isError => isResponse && raw.containsKey('error');

  dynamic get id => raw['id'];
  String get method => raw['method'] as String? ?? '';
  Map<String, dynamic>? get params => raw['params'] as Map<String, dynamic>?;
  dynamic get result => raw['result'];
  String get errorMessage =>
      (raw['error'] as Map<String, dynamic>?)?['message'] as String? ?? 'unknown error';

  static WsMessage parse(String data) => WsMessage(jsonDecode(data) as Map<String, dynamic>);
}

/// Exponential backoff for reconnect delays: 1s → 2s → 4s … capped at 30s.
class ReconnectBackoff {
  static const _initial = Duration(seconds: 1);
  static const _max = Duration(seconds: 30);
  Duration _current = _initial;

  Duration nextDelay() {
    final delay = _current;
    _current = _current * 2;
    if (_current > _max) _current = _max;
    return delay;
  }

  void reset() => _current = _initial;
}

typedef EventCallback = void Function(WsMessage event);

/// WsClient manages a JSON-RPC 2.0 WebSocket connection to agentgw.
/// Automatically reconnects with exponential backoff on disconnection.
class WsClient {
  final String url;
  final String token;

  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  final _pending = <int, Completer<WsMessage>>{};
  int _nextId = 1;
  final _backoff = ReconnectBackoff();
  bool _disposed = false;
  EventCallback? _onEvent;
  final _connectionState = StreamController<bool>.broadcast();

  WsClient({required this.url, required this.token});

  Stream<bool> get connectionState => _connectionState.stream;

  void onEvent(EventCallback cb) => _onEvent = cb;

  /// Connects to agentgw. Call once; reconnects automatically.
  ///
  /// Auth: token is passed as a query parameter (?token=...) because
  /// web_socket_channel does not support custom HTTP headers on all platforms
  /// (notably mobile). The agentgw server accepts both:
  ///   1. Authorization: Bearer <token> header (for Go/desktop clients)
  ///   2. ?token=<token> query parameter (for Flutter/mobile clients)
  Future<void> connect() async {
    if (_disposed) return;
    try {
      // Append token as query parameter for cross-platform auth
      final uri = Uri.parse(url).replace(queryParameters: {'token': token});
      _channel = WebSocketChannel.connect(uri);
      await _channel!.ready;
      _sub = _channel!.stream.listen(
        _onMessage,
        onError: (_) => _scheduleReconnect(),
        onDone: () => _scheduleReconnect(),
      );
      _backoff.reset();
      _connectionState.add(true);
    } catch (_) {
      _scheduleReconnect();
    }
  }

  void _onMessage(dynamic data) {
    if (data is! String) return;
    final msg = WsMessage.parse(data);
    if (msg.isResponse) {
      final id = msg.id;
      if (id is int) {
        _pending.remove(id)?.complete(msg);
      }
    } else if (msg.isEvent) {
      _onEvent?.call(msg);
    }
  }

  void _scheduleReconnect() {
    if (_disposed) return;
    _connectionState.add(false);
    // Fail all pending requests
    for (final c in _pending.values) {
      c.completeError('disconnected');
    }
    _pending.clear();
    _sub?.cancel();
    _channel?.sink.close();

    final delay = _backoff.nextDelay();
    Future.delayed(delay, () {
      if (!_disposed) connect();
    });
  }

  /// Sends a JSON-RPC call and returns the response.
  Future<dynamic> call(String method, [Map<String, dynamic>? params]) async {
    final id = _nextId++;
    final completer = Completer<WsMessage>();
    _pending[id] = completer;

    final req = buildRequest(id: id, method: method, params: params);
    _channel?.sink.add(jsonEncode(req));

    final resp = await completer.future.timeout(
      const Duration(seconds: 30),
      onTimeout: () {
        _pending.remove(id);
        throw TimeoutException('RPC $method timed out');
      },
    );
    if (resp.isError) throw Exception(resp.errorMessage);
    return resp.result;
  }

  void dispose() {
    _disposed = true;
    _sub?.cancel();
    _channel?.sink.close();
    _connectionState.close();
  }

  // ── Static helpers (pure, testable without a real connection) ──────────

  static Map<String, dynamic> buildRequest({
    required int id,
    required String method,
    Map<String, dynamic>? params,
  }) =>
      {'jsonrpc': '2.0', 'id': id, 'method': method, if (params != null) 'params': params};

  static WsMessage parseMessage(String data) => WsMessage.parse(data);
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/ws_client_test.dart -v
```

Expected:
```
All tests passed!
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/services/ws_client.dart agentapp/test/ws_client_test.dart
git commit -m "feat(agentapp): add WsClient with JSON-RPC framing and reconnect backoff"
```

---

## Task 4: ConnectionStore

**Files:**
- Create: `agentapp/lib/services/connection_store.dart`
- Test: `agentapp/test/connection_store_test.dart`

- [ ] **Step 1: Write failing test**

Create `agentapp/test/connection_store_test.dart`:

```dart
import 'package:flutter_test/flutter_test.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:agentapp/models/connection_config.dart';
import 'package:agentapp/services/connection_store.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('save and load connection', () async {
    final store = ConnectionStore();
    final cfg = ConnectionConfig(url: 'ws://localhost:7374', token: 'secret');
    await store.save(cfg);

    final loaded = await store.load();
    expect(loaded, isNotNull);
    expect(loaded!.url, equals('ws://localhost:7374'));
    expect(loaded.token, equals('secret'));
  });

  test('load returns null when nothing saved', () async {
    final store = ConnectionStore();
    final loaded = await store.load();
    expect(loaded, isNull);
  });

  test('clear removes saved connection', () async {
    final store = ConnectionStore();
    await store.save(ConnectionConfig(url: 'ws://x:7374', token: 't'));
    await store.clear();
    expect(await store.load(), isNull);
  });
}
```

- [ ] **Step 2: Run test — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/connection_store_test.dart -v
```

Expected: compilation error.

- [ ] **Step 3: Implement connection_store.dart**

Create `agentapp/lib/services/connection_store.dart`:

```dart
import 'dart:convert';
import 'package:shared_preferences/shared_preferences.dart';
import '../models/connection_config.dart';

class ConnectionStore {
  static const _key = 'connection_config';

  Future<void> save(ConnectionConfig cfg) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_key, jsonEncode(cfg.toJson()));
  }

  Future<ConnectionConfig?> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_key);
    if (raw == null) return null;
    return ConnectionConfig.fromJson(jsonDecode(raw) as Map<String, dynamic>);
  }

  Future<void> clear() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_key);
  }
}
```

- [ ] **Step 4: Run test — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/connection_store_test.dart -v
```

Expected:
```
All tests passed!
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/services/connection_store.dart agentapp/test/connection_store_test.dart
git commit -m "feat(agentapp): add ConnectionStore with SharedPreferences persistence"
```

---

## Task 5: Riverpod Providers

**Files:**
- Create: `agentapp/lib/providers/connection_provider.dart`
- Create: `agentapp/lib/providers/nodes_provider.dart`
- Create: `agentapp/lib/providers/conversation_provider.dart`
- Test: `agentapp/test/nodes_provider_test.dart`
- Test: `agentapp/test/conversation_provider_test.dart`

- [ ] **Step 1: Write failing tests**

Create `agentapp/test/nodes_provider_test.dart`:

```dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/providers/nodes_provider.dart';

void main() {
  test('NodesNotifier starts empty', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final nodes = container.read(nodesProvider);
    expect(nodes, isEmpty);
  });

  test('NodesNotifier.updateFromNodeList populates nodes', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(nodesProvider.notifier);

    notifier.updateFromNodeList([
      {'id': 'n1', 'name': 'remote1', 'host': '10.0.0.1', 'status': 'connected'},
      {'id': 'n2', 'name': 'remote2', 'host': '10.0.0.2', 'status': 'disconnected'},
    ]);

    final nodes = container.read(nodesProvider);
    expect(nodes.length, equals(2));
    expect(nodes['n1']!.status, equals(NodeStatus.connected));
    expect(nodes['n2']!.status, equals(NodeStatus.disconnected));
  });

  test('NodesNotifier.updateNodeStatus changes status', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(nodesProvider.notifier);
    notifier.updateFromNodeList([
      {'id': 'n1', 'name': 'r1', 'host': 'h', 'status': 'disconnected'},
    ]);

    notifier.updateNodeStatus('n1', 'connected');
    expect(container.read(nodesProvider)['n1']!.status, equals(NodeStatus.connected));
  });

  test('NodesNotifier.updateAgentStatus changes agent status', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier = container.read(nodesProvider.notifier);
    notifier.upsertAgent('n1', AgentModel(
      id: 'a1', name: 'claude-1', workDir: '/tmp', nodeId: 'n1', status: AgentStatus.idle,
    ));

    notifier.updateAgentStatus('n1', 'a1', 'working');
    final agent = container.read(agentsForNodeProvider('n1'))
        .firstWhere((a) => a.id == 'a1');
    expect(agent.status, equals(AgentStatus.working));
  });
}
```

Create `agentapp/test/conversation_provider_test.dart`:

```dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:agentapp/models/message_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';

void main() {
  test('ConversationNotifier starts empty', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final key = AgentKey(nodeId: 'n1', agentId: 'a1');
    final msgs = container.read(conversationProvider(key));
    expect(msgs, isEmpty);
  });

  test('ConversationNotifier.addMessage appends messages in order', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final key = AgentKey(nodeId: 'n1', agentId: 'a1');
    final notifier = container.read(conversationProvider(key).notifier);

    notifier.addMessage(MessageModel(
      nodeId: 'n1', agentId: 'a1', role: MessageRole.user, text: 'hello', seq: 1,
    ));
    notifier.addMessage(MessageModel(
      nodeId: 'n1', agentId: 'a1', role: MessageRole.assistant, text: 'hi', seq: 2,
    ));

    final msgs = container.read(conversationProvider(key));
    expect(msgs.length, equals(2));
    expect(msgs[0].text, equals('hello'));
    expect(msgs[1].text, equals('hi'));
  });

  test('ConversationNotifier ignores duplicate seq', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final key = AgentKey(nodeId: 'n1', agentId: 'a1');
    final notifier = container.read(conversationProvider(key).notifier);

    final msg = MessageModel(nodeId: 'n1', agentId: 'a1', role: MessageRole.user, text: 'x', seq: 5);
    notifier.addMessage(msg);
    notifier.addMessage(msg); // duplicate

    expect(container.read(conversationProvider(key)).length, equals(1));
  });
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/nodes_provider_test.dart test/conversation_provider_test.dart -v
```

Expected: compilation error.

- [ ] **Step 3: Implement providers**

Create `agentapp/lib/providers/connection_provider.dart`:

```dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/connection_config.dart';
import '../services/ws_client.dart';
import '../services/connection_store.dart';
import 'nodes_provider.dart';

enum ConnectionStatus { disconnected, connecting, connected }

class ConnectionState {
  final ConnectionConfig? config;
  final ConnectionStatus status;
  const ConnectionState({this.config, this.status = ConnectionStatus.disconnected});
  ConnectionState copyWith({ConnectionConfig? config, ConnectionStatus? status}) =>
      ConnectionState(config: config ?? this.config, status: status ?? this.status);
}

class ConnectionNotifier extends StateNotifier<ConnectionState> {
  final Ref _ref;
  WsClient? _client;

  ConnectionNotifier(this._ref) : super(const ConnectionState());

  /// Connects to agentgw. Wires event forwarding to NodesNotifier via Ref.
  Future<void> connect(ConnectionConfig cfg) async {
    state = state.copyWith(config: cfg, status: ConnectionStatus.connecting);
    await ConnectionStore().save(cfg);
    _client?.dispose();
    _client = WsClient(url: cfg.url, token: cfg.token);
    _client!.onEvent((msg) => _ref.read(nodesProvider.notifier).handleEvent(msg));
    await _client!.connect();
    state = state.copyWith(status: ConnectionStatus.connected);
  }

  WsClient? get client => _client;

  void disconnect() {
    _client?.dispose();
    _client = null;
    state = const ConnectionState();
  }
}

final connectionProvider = StateNotifierProvider<ConnectionNotifier, ConnectionState>(
  (ref) => ConnectionNotifier(ref),
);
```

Create `agentapp/lib/providers/nodes_provider.dart`:

```dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../services/ws_client.dart';

// State: nodeId → NodeModel
class NodesNotifier extends StateNotifier<Map<String, NodeModel>> {
  // agentId → AgentModel per node
  final Map<String, Map<String, AgentModel>> _agents = {};

  NodesNotifier() : super({});

  void updateFromNodeList(List<dynamic> list) {
    final updated = <String, NodeModel>{};
    for (final raw in list) {
      final n = NodeModel.fromJson(raw as Map<String, dynamic>);
      updated[n.id] = n;
    }
    state = updated;
  }

  void updateNodeStatus(String nodeId, String status) {
    final n = state[nodeId];
    if (n == null) return;
    state = {...state, nodeId: n.copyWith(status: _parseNodeStatus(status))};
  }

  void upsertAgent(String nodeId, AgentModel agent) {
    _agents[nodeId] ??= {};
    _agents[nodeId]![agent.id] = agent;
    // Trigger rebuild by reassigning state (shallow copy)
    state = {...state};
  }

  void updateAgentStatus(String nodeId, String agentId, String status) {
    final agents = _agents[nodeId];
    if (agents == null) return;
    final a = agents[agentId];
    if (a == null) return;
    agents[agentId] = a.copyWith(status: _parseAgentStatus(status));
    state = {...state};
  }

  List<AgentModel> agentsForNode(String nodeId) =>
      (_agents[nodeId]?.values.toList()) ?? [];

  void handleEvent(WsMessage msg) {
    final params = msg.params ?? {};
    switch (msg.method) {
      case 'node.status_changed':
        updateNodeStatus(params['nodeId'] as String, params['status'] as String);
      case 'agent.status_changed':
        updateAgentStatus(
          params['nodeId'] as String,
          params['agentId'] as String,
          params['status'] as String,
        );
    }
  }

  NodeStatus _parseNodeStatus(String s) {
    switch (s) {
      case 'connected': return NodeStatus.connected;
      case 'connecting': return NodeStatus.connecting;
      case 'deploying': return NodeStatus.deploying;
      default: return NodeStatus.disconnected;
    }
  }

  AgentStatus _parseAgentStatus(String s) {
    switch (s) {
      case 'working': return AgentStatus.working;
      case 'stopped': return AgentStatus.stopped;
      case 'crashed': return AgentStatus.crashed;
      case 'starting': return AgentStatus.starting;
      default: return AgentStatus.idle;
    }
  }
}

final nodesProvider = StateNotifierProvider<NodesNotifier, Map<String, NodeModel>>(
  (ref) => NodesNotifier(),
);

final agentsForNodeProvider = Provider.family<List<AgentModel>, String>((ref, nodeId) {
  ref.watch(nodesProvider); // rebuild when nodes change
  return ref.read(nodesProvider.notifier).agentsForNode(nodeId);
});
```

Create `agentapp/lib/providers/conversation_provider.dart`:

```dart
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/message_model.dart';

class AgentKey {
  final String nodeId;
  final String agentId;
  const AgentKey({required this.nodeId, required this.agentId});

  @override
  bool operator ==(Object other) =>
      other is AgentKey && other.nodeId == nodeId && other.agentId == agentId;

  @override
  int get hashCode => Object.hash(nodeId, agentId);
}

class ConversationNotifier extends StateNotifier<List<MessageModel>> {
  final _seenSeqs = <int>{};

  ConversationNotifier() : super([]);

  void addMessage(MessageModel msg) {
    if (_seenSeqs.contains(msg.seq)) return;
    _seenSeqs.add(msg.seq);
    state = [...state, msg];
  }

  void addMessages(List<MessageModel> msgs) {
    for (final m in msgs) {
      addMessage(m);
    }
  }

  int get lastSeq => state.isEmpty ? 0 : state.last.seq;
}

final conversationProvider =
    StateNotifierProvider.family<ConversationNotifier, List<MessageModel>, AgentKey>(
  (ref, key) => ConversationNotifier(),
);
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test test/nodes_provider_test.dart test/conversation_provider_test.dart -v
```

Expected:
```
All tests passed!
```

- [ ] **Step 5: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/providers/ agentapp/test/nodes_provider_test.dart agentapp/test/conversation_provider_test.dart
git commit -m "feat(agentapp): add Riverpod providers for connection, nodes, conversation"
```

---

## Task 6: ConnectionsScreen

**Files:**
- Create: `agentapp/lib/screens/connections_screen.dart`

- [ ] **Step 1: Implement ConnectionsScreen**

Create `agentapp/lib/screens/connections_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../models/connection_config.dart';
import '../providers/connection_provider.dart';
import '../providers/nodes_provider.dart';

class ConnectionsScreen extends ConsumerWidget {
  const ConnectionsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final connState = ref.watch(connectionProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Agent Manager')),
      body: Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (connState.status == ConnectionStatus.connected)
                _ConnectedCard(config: connState.config!, ref: ref)
              else
                _ConnectForm(ref: ref),
            ],
          ),
        ),
      ),
    );
  }
}

class _ConnectedCard extends StatelessWidget {
  final ConnectionConfig config;
  final WidgetRef ref;
  const _ConnectedCard({required this.config, required this.ref});

  @override
  Widget build(BuildContext context) => Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(children: [
                const Icon(Icons.circle, color: Colors.green, size: 12),
                const SizedBox(width: 8),
                Text('Connected', style: Theme.of(context).textTheme.titleMedium),
              ]),
              const SizedBox(height: 8),
              Text(config.url, style: Theme.of(context).textTheme.bodySmall),
              const SizedBox(height: 16),
              Row(children: [
                FilledButton(
                  onPressed: () => context.go('/dashboard'),
                  child: const Text('Go to Dashboard'),
                ),
                const SizedBox(width: 8),
                OutlinedButton(
                  onPressed: () => ref.read(connectionProvider.notifier).disconnect(),
                  child: const Text('Disconnect'),
                ),
              ]),
            ],
          ),
        ),
      );
}

class _ConnectForm extends StatefulWidget {
  final WidgetRef ref;
  const _ConnectForm({required this.ref});

  @override
  State<_ConnectForm> createState() => _ConnectFormState();
}

class _ConnectFormState extends State<_ConnectForm> {
  final _urlCtrl = TextEditingController(text: 'ws://');
  final _tokenCtrl = TextEditingController();
  bool _loading = false;
  String? _error;

  @override
  Widget build(BuildContext context) => Card(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text('Connect to agentgw', style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 16),
              TextField(
                controller: _urlCtrl,
                decoration: const InputDecoration(labelText: 'WebSocket URL', hintText: 'ws://192.168.x.x:7374'),
              ),
              const SizedBox(height: 12),
              TextField(
                controller: _tokenCtrl,
                decoration: const InputDecoration(labelText: 'Token'),
                obscureText: true,
              ),
              if (_error != null) ...[
                const SizedBox(height: 8),
                Text(_error!, style: const TextStyle(color: Colors.red)),
              ],
              const SizedBox(height: 16),
              FilledButton(
                onPressed: _loading ? null : _connect,
                child: _loading ? const SizedBox(height: 16, width: 16, child: CircularProgressIndicator(strokeWidth: 2)) : const Text('Connect'),
              ),
            ],
          ),
        ),
      );

  Future<void> _connect() async {
    setState(() { _loading = true; _error = null; });
    try {
      final cfg = ConnectionConfig(url: _urlCtrl.text.trim(), token: _tokenCtrl.text.trim());
      await widget.ref.read(connectionProvider.notifier).connect(cfg);
      if (mounted) context.go('/dashboard');
    } catch (e) {
      setState(() { _error = e.toString(); });
    } finally {
      if (mounted) setState(() { _loading = false; });
    }
  }
}
```

- [ ] **Step 2: Verify no analysis errors**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter analyze lib/screens/connections_screen.dart
```

Expected: No issues.

- [ ] **Step 3: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/screens/connections_screen.dart
git commit -m "feat(agentapp): add ConnectionsScreen with connect form"
```

---

## Task 7: DashboardScreen

**Files:**
- Create: `agentapp/lib/screens/dashboard_screen.dart`

- [ ] **Step 1: Implement DashboardScreen**

Create `agentapp/lib/screens/dashboard_screen.dart`:

```dart
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
    final nodes = ref.watch(nodesProvider);
    final connState = ref.watch(connectionProvider);

    return Scaffold(
      appBar: AppBar(
        title: const Text('Dashboard'),
        actions: [
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.go('/settings'),
          ),
        ],
      ),
      body: Column(
        children: [
          if (connState.status != ConnectionStatus.connected)
            const MaterialBanner(
              content: Text('Reconnecting…'),
              backgroundColor: Colors.orange,
              actions: [SizedBox.shrink()],
            ),
          Expanded(
            child: nodes.isEmpty
                ? const Center(child: Text('No nodes. Add one in Settings.'))
                : ListView(
                    children: nodes.values.map((n) => _NodeCard(node: n)).toList(),
                  ),
          ),
        ],
      ),
    );
  }
}

class _NodeCard extends ConsumerWidget {
  final NodeModel node;
  const _NodeCard({required this.node});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final agents = ref.watch(agentsForNodeProvider(node.id));
    return Card(
      margin: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ListTile(
            leading: _nodeStatusIcon(node.status),
            title: Text(node.name.isEmpty ? node.host : node.name,
                style: const TextStyle(fontWeight: FontWeight.bold)),
            subtitle: Text(node.host),
            trailing: Text(node.status.name,
                style: TextStyle(color: _nodeStatusColor(node.status), fontSize: 12)),
          ),
          ...agents.map((a) => _AgentRow(agent: a, nodeId: node.id)),
          const SizedBox(height: 4),
        ],
      ),
    );
  }

  Widget _nodeStatusIcon(NodeStatus s) {
    final color = _nodeStatusColor(s);
    return Icon(Icons.circle, color: color, size: 14);
  }

  Color _nodeStatusColor(NodeStatus s) {
    switch (s) {
      case NodeStatus.connected: return Colors.green;
      case NodeStatus.connecting: return Colors.orange;
      case NodeStatus.deploying: return Colors.blue;
      case NodeStatus.error: return Colors.red;
      case NodeStatus.disconnected: return Colors.grey;
    }
  }
}

class _AgentRow extends StatelessWidget {
  final AgentModel agent;
  final String nodeId;
  const _AgentRow({required this.agent, required this.nodeId});

  @override
  Widget build(BuildContext context) => InkWell(
        onTap: () => context.push('/agent/$nodeId/${agent.id}'),
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: Row(
            children: [
              _statusDot(agent.status),
              const SizedBox(width: 10),
              Expanded(child: Text(agent.name.isEmpty ? agent.id : agent.name)),
              Text(agent.status.name,
                  style: TextStyle(color: _agentStatusColor(agent.status), fontSize: 12)),
            ],
          ),
        ),
      );

  Widget _statusDot(AgentStatus s) {
    if (s == AgentStatus.starting) {
      return const SizedBox(
          width: 12, height: 12, child: CircularProgressIndicator(strokeWidth: 2));
    }
    return Icon(Icons.circle, color: _agentStatusColor(s), size: 12);
  }

  Color _agentStatusColor(AgentStatus s) {
    switch (s) {
      case AgentStatus.working: return Colors.blue;
      case AgentStatus.idle: return Colors.amber;
      case AgentStatus.stopped: return Colors.grey;
      case AgentStatus.crashed: return Colors.red;
      case AgentStatus.starting: return Colors.orange;
    }
  }
}
```

- [ ] **Step 2: Verify no analysis errors**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter analyze lib/screens/dashboard_screen.dart
```

Expected: No issues.

- [ ] **Step 3: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/screens/dashboard_screen.dart
git commit -m "feat(agentapp): add DashboardScreen with NodeCard and AgentRow"
```

---

## Task 8: AgentDetailScreen

**Files:**
- Create: `agentapp/lib/screens/agent_detail_screen.dart`

- [ ] **Step 1: Implement AgentDetailScreen**

Create `agentapp/lib/screens/agent_detail_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_markdown/flutter_markdown.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../models/message_model.dart';
import '../models/agent_model.dart';
import '../providers/conversation_provider.dart';
import '../providers/nodes_provider.dart';
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
  bool _sending = false;

  AgentKey get _key => AgentKey(nodeId: widget.nodeId, agentId: widget.agentId);

  @override
  void dispose() {
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final messages = ref.watch(conversationProvider(_key));
    final agents = ref.watch(agentsForNodeProvider(widget.nodeId));
    final agent = agents.where((a) => a.id == widget.agentId).firstOrNull;

    return Scaffold(
      appBar: AppBar(
        title: Text(agent?.name ?? widget.agentId),
        actions: [
          if (agent != null) _StatusChip(status: agent.status),
        ],
      ),
      body: Column(
        children: [
          Expanded(
            child: messages.isEmpty
                ? const Center(child: Text('No messages yet'))
                : ListView.builder(
                    controller: _scrollCtrl,
                    padding: const EdgeInsets.all(12),
                    itemCount: messages.length,
                    itemBuilder: (_, i) => _MessageBubble(msg: messages[i]),
                  ),
          ),
          if (agent != null) _ControlBar(nodeId: widget.nodeId, agentId: widget.agentId, status: agent.status),
          _InputBar(
            controller: _inputCtrl,
            sending: _sending,
            onSend: _sendMessage,
          ),
        ],
      ),
    );
  }

  Future<void> _sendMessage() async {
    final text = _inputCtrl.text.trim();
    if (text.isEmpty || _sending) return;
    setState(() => _sending = true);
    try {
      final client = ref.read(connectionProvider.notifier).client;
      if (client == null) return;
      await client.call('conversation.send', {
        'nodeId': widget.nodeId,
        'agentId': widget.agentId,
        'message': text,
      });
      _inputCtrl.clear();
      // Optimistically add user message
      ref.read(conversationProvider(_key).notifier).addMessage(MessageModel(
        nodeId: widget.nodeId,
        agentId: widget.agentId,
        role: MessageRole.user,
        text: text,
        seq: DateTime.now().millisecondsSinceEpoch, // temp seq
      ));
      _scrollToBottom();
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollCtrl.hasClients) {
        _scrollCtrl.animateTo(
          _scrollCtrl.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }
}

class _MessageBubble extends StatelessWidget {
  final MessageModel msg;
  const _MessageBubble({required this.msg});

  @override
  Widget build(BuildContext context) {
    final isUser = msg.role == MessageRole.user;
    return Align(
      alignment: isUser ? Alignment.centerRight : Alignment.centerLeft,
      child: Container(
        margin: const EdgeInsets.symmetric(vertical: 4),
        padding: const EdgeInsets.all(12),
        constraints: BoxConstraints(maxWidth: MediaQuery.of(context).size.width * 0.8),
        decoration: BoxDecoration(
          color: isUser ? Theme.of(context).colorScheme.primary : Theme.of(context).colorScheme.surfaceVariant,
          borderRadius: BorderRadius.circular(12),
        ),
        child: isUser
            ? Text(msg.text, style: TextStyle(color: Theme.of(context).colorScheme.onPrimary))
            : MarkdownBody(data: msg.text),
      ),
    );
  }
}

class _ControlBar extends ConsumerWidget {
  final String nodeId;
  final String agentId;
  final AgentStatus status;
  const _ControlBar({required this.nodeId, required this.agentId, required this.status});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final client = ref.read(connectionProvider.notifier).client;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        children: [
          if (status == AgentStatus.stopped || status == AgentStatus.crashed)
            OutlinedButton.icon(
              icon: const Icon(Icons.play_arrow, size: 16),
              label: const Text('Start'),
              onPressed: () => client?.call('agent.restart', {'nodeId': nodeId, 'agentId': agentId}),
            ),
          if (status == AgentStatus.idle || status == AgentStatus.working) ...[
            OutlinedButton.icon(
              icon: const Icon(Icons.stop, size: 16),
              label: const Text('Stop'),
              onPressed: () => client?.call('agent.stop', {'nodeId': nodeId, 'agentId': agentId}),
            ),
            const SizedBox(width: 8),
            OutlinedButton.icon(
              icon: const Icon(Icons.restart_alt, size: 16),
              label: const Text('Restart'),
              onPressed: () => client?.call('agent.restart', {'nodeId': nodeId, 'agentId': agentId}),
            ),
          ],
        ],
      ),
    );
  }
}

class _InputBar extends StatelessWidget {
  final TextEditingController controller;
  final bool sending;
  final VoidCallback onSend;
  const _InputBar({required this.controller, required this.sending, required this.onSend});

  @override
  Widget build(BuildContext context) => SafeArea(
        child: Padding(
          padding: const EdgeInsets.fromLTRB(12, 4, 12, 8),
          child: Row(
            children: [
              Expanded(
                child: TextField(
                  controller: controller,
                  minLines: 1,
                  maxLines: 4,
                  decoration: const InputDecoration(
                    hintText: 'Message…',
                    border: OutlineInputBorder(),
                    contentPadding: EdgeInsets.symmetric(horizontal: 12, vertical: 10),
                  ),
                  onSubmitted: (_) => onSend(),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(
                icon: sending
                    ? const SizedBox(width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white))
                    : const Icon(Icons.send),
                onPressed: sending ? null : onSend,
              ),
            ],
          ),
        ),
      );
}

class _StatusChip extends StatelessWidget {
  final AgentStatus status;
  const _StatusChip({required this.status});

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(right: 12),
        child: Chip(
          label: Text(status.name, style: const TextStyle(fontSize: 11)),
          backgroundColor: _color(status).withOpacity(0.15),
          side: BorderSide(color: _color(status)),
          padding: EdgeInsets.zero,
          labelPadding: const EdgeInsets.symmetric(horizontal: 6),
        ),
      );

  Color _color(AgentStatus s) {
    switch (s) {
      case AgentStatus.working: return Colors.blue;
      case AgentStatus.idle: return Colors.amber;
      case AgentStatus.stopped: return Colors.grey;
      case AgentStatus.crashed: return Colors.red;
      case AgentStatus.starting: return Colors.orange;
    }
  }
}
```

- [ ] **Step 2: Verify no analysis errors**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter analyze lib/screens/agent_detail_screen.dart
```

Expected: No issues.

- [ ] **Step 3: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/screens/agent_detail_screen.dart
git commit -m "feat(agentapp): add AgentDetailScreen with conversation, input, and controls"
```

---

## Task 9: App Router + Wire Everything

**Files:**
- Create: `agentapp/lib/screens/settings_screen.dart`
- Modify: `agentapp/lib/app.dart`
- Modify: `agentapp/lib/main.dart`

- [ ] **Step 1: Implement settings_screen.dart**

Create `agentapp/lib/screens/settings_screen.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import '../providers/connection_provider.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final connState = ref.watch(connectionProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          ListTile(
            leading: const Icon(Icons.link),
            title: const Text('Connection'),
            subtitle: Text(connState.config?.url ?? 'Not connected'),
            trailing: connState.status == ConnectionStatus.connected
                ? TextButton(
                    onPressed: () {
                      ref.read(connectionProvider.notifier).disconnect();
                      context.go('/connections');
                    },
                    child: const Text('Disconnect'),
                  )
                : TextButton(
                    onPressed: () => context.go('/connections'),
                    child: const Text('Connect'),
                  ),
          ),
          const Divider(),
          ListTile(
            leading: const Icon(Icons.info_outline),
            title: const Text('Version'),
            subtitle: const Text('agentapp v0.1.0'),
          ),
        ],
      ),
    );
  }
}
```

- [ ] **Step 2: Implement app.dart**

Create `agentapp/lib/app.dart`:

```dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'screens/connections_screen.dart';
import 'screens/dashboard_screen.dart';
import 'screens/agent_detail_screen.dart';
import 'screens/settings_screen.dart';

final _router = GoRouter(
  initialLocation: '/connections',
  routes: [
    GoRoute(path: '/connections', builder: (_, __) => const ConnectionsScreen()),
    GoRoute(path: '/dashboard', builder: (_, __) => const DashboardScreen()),
    GoRoute(
      path: '/agent/:nodeId/:agentId',
      builder: (_, state) => AgentDetailScreen(
        nodeId: state.pathParameters['nodeId']!,
        agentId: state.pathParameters['agentId']!,
      ),
    ),
    GoRoute(path: '/settings', builder: (_, __) => const SettingsScreen()),
  ],
);

class AgentApp extends StatelessWidget {
  const AgentApp({super.key});

  @override
  Widget build(BuildContext context) => ProviderScope(
        child: MaterialApp.router(
          title: 'Agent Manager',
          theme: ThemeData(
            colorSchemeSeed: Colors.indigo,
            useMaterial3: true,
          ),
          routerConfig: _router,
        ),
      );
}
```

- [ ] **Step 3: Update main.dart**

Replace `agentapp/lib/main.dart` with:

```dart
import 'package:flutter/material.dart';
import 'app.dart';

void main() {
  runApp(const AgentApp());
}
```

- [ ] **Step 4: Run all tests**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter test -v
```

Expected: All tests passed!

- [ ] **Step 5: Run flutter analyze**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk/agentapp
flutter analyze
```

Expected: No issues found.

- [ ] **Step 6: Commit**

```bash
cd /Users/fengming.xie/Documents/project/phone-talk
git add agentapp/lib/
git commit -m "feat(agentapp): wire GoRouter, app entry point, settings screen — agentapp v0.1.0 complete"
```
