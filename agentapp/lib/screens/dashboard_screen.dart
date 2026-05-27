import 'dart:async';
import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

import '../models/node_model.dart';
import '../models/agent_model.dart';
import '../models/message_model.dart';
import '../providers/nodes_provider.dart';
import '../providers/connection_provider.dart';
import '../providers/conversation_provider.dart';
import '../providers/dashboard_detail_panel_provider.dart';
import '../providers/unread_provider.dart';
import '../providers/health_provider.dart';
import '../providers/session_logo_provider.dart';
import '../services/ws_client.dart';
import '../theme/agent_status_theme.dart';
import '../theme/app_spacing.dart';
import '../theme/app_text_styles.dart';
import '../widgets/agent_status_indicator.dart';
import '../widgets/app_bar/mission_control_app_bar.dart';
import '../widgets/empty_states/empty_state.dart';
import '../widgets/loaders/oscilloscope_loader.dart';
import 'agent_detail_screen.dart'
    show
        buildCollapsedPreview,
        collapseConsecutiveActivityBlocks,
        convertEventsToMessages,
        normalizeHistoryEvent;

String shortSessionId(String? value) {
  final sessionId = value?.trim() ?? '';
  if (sessionId.isEmpty) return '';
  if (sessionId.length <= 8) return sessionId;
  return sessionId.substring(0, 8);
}

String _dirname(String path) {
  if (path.isEmpty) return '';
  final sep = path.contains('/') ? '/' : '\\';
  final parts = path.split(sep).where((p) => p.isNotEmpty).toList();
  return parts.isNotEmpty ? parts.last : path;
}

List<String> buildSessionPreviewLines(
  List<String> texts, {
  int maxLines = 2,
  int maxCharsPerLine = 80,
}) {
  // Find the last non-empty text entry only; do not span across multiple texts.
  String? lastText;
  for (var i = texts.length - 1; i >= 0; i--) {
    if (texts[i].trim().isNotEmpty) {
      lastText = texts[i];
      break;
    }
  }
  if (lastText == null) return const [];

  final lines = <String>[];
  final normalized = lastText.replaceAll('\r', '\n');
  for (final line in normalized.split('\n')) {
    final trimmed = line.trim();
    if (trimmed.isEmpty) {
      // Preserve paragraph breaks so the UI can add visual spacing.
      if (lines.isNotEmpty && lines.last.isNotEmpty) {
        lines.add('');
      }
      continue;
    }
    lines.add(buildCollapsedPreview(trimmed, maxChars: maxCharsPerLine));
  }
  // Trim trailing empty markers.
  while (lines.isNotEmpty && lines.last.isEmpty) {
    lines.removeLast();
  }
  if (lines.length <= maxLines) return lines;
  return lines.sublist(lines.length - maxLines);
}

List<String> sessionPreviewLinesFromMessages(
  List<MessageModel> messages, {
  int maxLines = 2,
}) {
  if (messages.isEmpty) return const [];
  final recentTexts = messages
      .where((m) => m.text.trim().isNotEmpty)
      .map((m) => m.text)
      .toList();
  return buildSessionPreviewLines(recentTexts, maxLines: maxLines);
}

/// Renders simple inline markdown (bold, italic, code, strikethrough, links)
/// using Text.rich.  Safe inside ListTile subtitles and tight layouts.
/// Builds inline [TextSpan] list from a markdown-lite [data] string.
///
/// Supports: `**bold**`, `*italic*`, `_italic_`, `` `code` ``,
/// `~~strikethrough~~`, `[link](url)`.
///
/// Exposed for testing via [visibleForTesting].
@visibleForTesting
List<InlineSpan> buildMarkdownSpans(
    String data, TextStyle style, BuildContext context) {
  final spans = <InlineSpan>[];
  var lastEnd = 0;
  for (final match in _MarkdownText._pattern.allMatches(data)) {
    if (match.start > lastEnd) {
      spans.add(
          TextSpan(text: data.substring(lastEnd, match.start), style: style));
    }
    final raw = match.group(0)!;
    String content;
    TextStyle spanStyle;
    if (raw.startsWith('**') && raw.endsWith('**')) {
      content = raw.substring(2, raw.length - 2);
      spanStyle = style.copyWith(fontWeight: FontWeight.bold);
    } else if (raw.startsWith('~~') && raw.endsWith('~~')) {
      content = raw.substring(2, raw.length - 2);
      spanStyle = style.copyWith(decoration: TextDecoration.lineThrough);
    } else if ((raw.startsWith('*') && raw.endsWith('*')) ||
        (raw.startsWith('_') && raw.endsWith('_'))) {
      content = raw.substring(1, raw.length - 1);
      spanStyle = style.copyWith(fontStyle: FontStyle.italic);
    } else if (raw.startsWith('`') && raw.endsWith('`')) {
      content = raw.substring(1, raw.length - 1);
      spanStyle = style.copyWith(
        fontFamily: 'monospace',
        fontFamilyFallback: const ['Noto Sans SC', 'Roboto', 'sans-serif'],
        color: style.color ?? Theme.of(context).colorScheme.onSurface,
      );
    } else if (raw.startsWith('[')) {
      // link: [text](url)
      final textEnd = raw.indexOf(']');
      content = raw.substring(1, textEnd);
      spanStyle = style.copyWith(
        color: Theme.of(context).colorScheme.primary,
        decoration: TextDecoration.underline,
      );
    } else {
      content = raw;
      spanStyle = style;
    }
    spans.add(TextSpan(text: content, style: spanStyle));
    lastEnd = match.end;
  }
  if (lastEnd < data.length) {
    spans.add(TextSpan(text: data.substring(lastEnd), style: style));
  }
  return spans;
}

class _MarkdownText extends StatelessWidget {
  final String data;
  final TextStyle style;
  final int? maxLines;
  final TextOverflow? overflow;

  const _MarkdownText(
    this.data, {
    required this.style,
    this.maxLines,
    this.overflow,
  });

  static final _pattern = RegExp(
    r'(\*\*[^*]+\*\*|\*[^*]+\*|_[^_]+_|`[^`]+`|~~[^~]+~~|\[[^\]]+\]\([^)]+\))',
  );

  @override
  Widget build(BuildContext context) {
    final spans = buildMarkdownSpans(data, style, context);
    return Text.rich(
      TextSpan(children: spans),
      maxLines: maxLines,
      overflow: overflow,
      softWrap: true,
    );
  }
}


/// Minimal markdown renderer for preview snippets.
class _MarkdownPreview extends StatelessWidget {
  final String data;
  final Color color;

  const _MarkdownPreview(this.data, {required this.color});

  @override
  Widget build(BuildContext context) {
    return _MarkdownText(
      data,
      style: AppTextStyles.caption.copyWith(color: color),
      maxLines: 3,
      overflow: TextOverflow.ellipsis,
    );
  }
}

/// Renders preview lines with visual paragraph spacing.
/// Empty strings in [lines] are treated as paragraph breaks.
class _PreviewParagraphs extends StatelessWidget {
  final List<String> lines;
  final Color color;

  const _PreviewParagraphs(this.lines, {required this.color});

  @override
  Widget build(BuildContext context) {
    final paragraphs = <List<String>>[];
    var current = <String>[];
    for (final line in lines) {
      if (line.isEmpty) {
        if (current.isNotEmpty) {
          paragraphs.add(current);
          current = <String>[];
        }
      } else {
        current.add(line);
      }
    }
    if (current.isNotEmpty) paragraphs.add(current);

    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        for (var i = 0; i < paragraphs.length; i++) ...[
          if (i > 0) const SizedBox(height: 4),
          _MarkdownPreview(
            paragraphs[i].join('\n'),
            color: color,
          ),
        ],
      ],
    );
  }
}

class _ResizeHandle extends StatefulWidget {
  final ValueChanged<double> onDrag;

  const _ResizeHandle({required this.onDrag});

  @override
  State<_ResizeHandle> createState() => _ResizeHandleState();
}

class _ResizeHandleState extends State<_ResizeHandle> {
  double _accumulator = 0;

  @override
  Widget build(BuildContext context) {
    return GestureDetector(
      behavior: HitTestBehavior.translucent,
      onPanStart: (_) => _accumulator = 0,
      onPanUpdate: (details) {
        _accumulator += details.delta.dx;
        if (_accumulator.abs() >= 4) {
          widget.onDrag(_accumulator);
          _accumulator = 0;
        }
      },
      child: MouseRegion(
        cursor: SystemMouseCursors.resizeColumn,
        child: Container(
          width: 8,
          color: Colors.transparent,
          child: Center(
            child: Container(
              width: 2,
              height: 40,
              decoration: BoxDecoration(
                color: Theme.of(context).dividerColor,
                borderRadius: BorderRadius.circular(1),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

Color providerColor(String provider) {
  switch (provider.toLowerCase()) {
    case 'claude':
      return const Color(0xFFF97316); // Claude orange
    case 'opencode':
      return const Color(0xFF3B82F6); // OpenCode blue
    case 'hermes':
      return const Color(0xFF8B5CF6); // Hermes purple
    default:
      return Colors.grey;
  }
}

IconData providerIcon(String provider) {
  switch (provider.toLowerCase()) {
    case 'hermes':
      return Icons.flutter_dash;
    case 'opencode':
      return Icons.terminal;
    default:
      return Icons.smart_toy;
  }
}

List<Map<String, String>> getDefaultModels(String provider) {
  final defaultModels = {
    'claude': [
      {'id': 'claude-sonnet-4-6', 'name': 'Sonnet 4.6'},
      {'id': 'claude-opus-4-6', 'name': 'Opus 4.6'},
      {'id': 'claude-haiku-4-5-20251001', 'name': 'Haiku 4.5'},
    ],
    'claude-bedrock': [
      {
        'id': 'anthropic.claude-3-5-sonnet-20241022-v2:0',
        'name': 'Claude 3.5 Sonnet (Bedrock)',
      },
      {
        'id': 'anthropic.claude-3-5-haiku-20241022-v1:0',
        'name': 'Claude 3.5 Haiku (Bedrock)',
      },
    ],
    'claude-vertex': [
      {
        'id': 'claude-3-5-sonnet@20241022',
        'name': 'Claude 3.5 Sonnet (Vertex)',
      },
      {'id': 'claude-3-5-haiku@20241022', 'name': 'Claude 3.5 Haiku (Vertex)'},
    ],
    'hermes': [
      {'id': 'default', 'name': 'Hermes (default)'},
    ],
  };

  return defaultModels[provider] ?? defaultModels['claude']!;
}


class SessionCandidate {
  final int? pid;
  final String provider;
  final String workDir;
  final String? sessionId;
  final String? terminal;
  final String attachMode;
  final bool isReadOnly;
  final String readOnlyReason;
  final String? projectName;

  const SessionCandidate({
    required this.pid,
    required this.provider,
    required this.workDir,
    this.sessionId,
    this.terminal,
    this.attachMode = '',
    this.isReadOnly = false,
    this.readOnlyReason = '',
    this.projectName,
  });

  factory SessionCandidate.fromJson(Map<String, dynamic> json) =>
      SessionCandidate(
        pid: (json['pid'] as num?)?.toInt(),
        provider: (json['provider'] as String? ?? 'unknown').toLowerCase(),
        workDir: json['workDir'] as String? ?? '',
        sessionId:
            json['sessionId'] as String? ??
            json['session'] as String? ??
            _sessionIdFromPath(json['sessionFile'] as String?),
        terminal: json['terminal'] as String?,
        attachMode: json['attachMode'] as String? ?? '',
        isReadOnly: json['readOnly'] as bool? ?? false,
        readOnlyReason: json['readOnlyReason'] as String? ?? '',
        projectName: json['projectName'] as String?,
      );

  static String? _sessionIdFromPath(String? path) {
    if (path == null || path.isEmpty) return null;
    final normalized = path.replaceAll('\\', '/');
    final name = normalized.split('/').last;
    if (name.endsWith('.jsonl')) return name.substring(0, name.length - 6);
    if (name.endsWith('.json')) return name.substring(0, name.length - 5);
    return null;
  }
}

bool isLiveSessionCandidate(SessionCandidate s) => (s.pid ?? 0) > 0;

String _sessionCandidateSortTitle(SessionCandidate s) {
  if (s.projectName != null && s.projectName!.isNotEmpty) {
    return '${s.projectName} (${s.provider})';
  }
  if (s.sessionId != null && s.sessionId!.isNotEmpty) {
    return '${shortSessionId(s.sessionId)} (${s.provider})';
  }
  if (s.pid != null && s.pid! > 0) {
    return '${s.provider} ${s.pid}';
  }
  return s.provider;
}

bool isWritableAttachSession(SessionCandidate s) {
  return isLiveSessionCandidate(s) && s.attachMode == 'tmux' && !s.isReadOnly;
}

int sessionCandidateSortPriority(SessionCandidate s) {
  if (isWritableAttachSession(s)) return 0;
  if (isLiveSessionCandidate(s) && !s.isReadOnly) return 1;
  if (isLiveSessionCandidate(s)) return 2;
  return 3;
}

SessionCandidate? pickPreferredAutoAttachCandidate(
  Iterable<SessionCandidate> sessions,
) {
  final writable = sessions.where(isWritableAttachSession).toList();
  if (writable.isEmpty) return null;
  writable.sort((a, b) => (b.pid ?? 0).compareTo(a.pid ?? 0));
  return writable.first;
}

List<SessionCandidate> parseSessionCandidates(dynamic result) {
  final rawAttachable = result is List
      ? result
      : (result is Map<String, dynamic>
            ? ((result['attachable'] as List?) ??
                  (result['processes'] as List?) ??
                  const [])
            : const []);

  final attachable = rawAttachable.whereType<Map>().map(
    (e) => SessionCandidate.fromJson(Map<String, dynamic>.from(e)),
  );

  return [...attachable];
}

String managedVisibilityKey(AgentModel agent) {
  // Use PID as the primary key when available so multiple live processes
  // sharing one session remain independently visible on the dashboard.
  if (agent.pid != null && agent.pid! > 0) {
    return '${agent.provider.trim().toLowerCase()}|pid:${agent.pid}';
  }
  return sessionIdentityKey(
    provider: agent.provider,
    sessionId: agent.sessionId,
    pid: agent.pid,
    agentId: agent.id,
  );
}

bool _preferManagedAgent(AgentModel candidate, AgentModel? existing) {
  if (existing == null) return true;

  int priority(AgentStatus status) {
    switch (status) {
      case AgentStatus.working:
        return 0;
      case AgentStatus.starting:
        return 1;
      case AgentStatus.idle:
        return 2;
      case AgentStatus.stopped:
        return 3;
      case AgentStatus.crashed:
        return 4;
    }
  }

  final candidatePriority = priority(candidate.status);
  final existingPriority = priority(existing.status);
  if (candidatePriority != existingPriority) {
    return candidatePriority < existingPriority;
  }

  final candidatePid = candidate.pid ?? 0;
  final existingPid = existing.pid ?? 0;
  if (candidatePid != existingPid) {
    return candidatePid > existingPid;
  }

  return false;
}

String sessionIdentityKey({
  required String provider,
  String? sessionId,
  int? pid,
  String? agentId,
}) {
  final lowerProvider = provider.trim().toLowerCase();
  final normalizedSessionId = (sessionId ?? '').trim().toLowerCase();
  if (normalizedSessionId.isNotEmpty) {
    return '$lowerProvider|session:$normalizedSessionId';
  }
  if (pid != null && pid > 0) {
    return '$lowerProvider|pid:$pid';
  }
  final normalizedAgentId = (agentId ?? '').trim().toLowerCase();
  if (normalizedAgentId.isNotEmpty) {
    return '$lowerProvider|agent:$normalizedAgentId';
  }
  return lowerProvider;
}

List<AgentModel> visibleManagedAgentsForNode(List<AgentModel> agents) {
  final bySignature = <String, AgentModel>{};
  for (final a in agents) {
    final isActive =
        a.status == AgentStatus.working ||
        a.status == AgentStatus.starting ||
        a.status == AgentStatus.idle;
    if (!isActive) continue;
    final key = managedVisibilityKey(a);
    final existing = bySignature[key];
    if (_preferManagedAgent(a, existing)) {
      bySignature[key] = a;
    }
  }

  final visibleAgents = bySignature.values.toList()
    ..sort((a, b) {
      final cmp = managedAgentSortTitle(a).compareTo(managedAgentSortTitle(b));
      if (cmp != 0) return cmp;
      return a.id.compareTo(b.id);
    });
  return visibleAgents;
}

class DashboardSessionTarget {
  final String nodeId;
  final String nodeName;
  final AgentModel agent;

  const DashboardSessionTarget({
    required this.nodeId,
    required this.nodeName,
    required this.agent,
  });

  String get key => '$nodeId:${agent.id}';
}

List<DashboardSessionTarget> buildVisibleDashboardSessions(NodeState state) {
  final sessions = <DashboardSessionTarget>[];
  for (final node in state.nodeList) {
    final visibleAgents = visibleManagedAgentsForNode(state.agentsFor(node.id));
    for (final agent in visibleAgents) {
      sessions.add(
        DashboardSessionTarget(nodeId: node.id, nodeName: node.name, agent: agent),
      );
    }
  }

  sessions.sort((a, b) {
    final nodeCmp = a.nodeName.toLowerCase().compareTo(b.nodeName.toLowerCase());
    if (nodeCmp != 0) return nodeCmp;
    final titleCmp = managedAgentSortTitle(a.agent).compareTo(managedAgentSortTitle(b.agent));
    if (titleCmp != 0) return titleCmp;
    return a.agent.id.compareTo(b.agent.id);
  });
  return sessions;
}



class DashboardScreen extends ConsumerStatefulWidget {
  const DashboardScreen({super.key});

  @override
  ConsumerState<DashboardScreen> createState() => _DashboardScreenState();
}

class _DashboardScreenState extends ConsumerState<DashboardScreen> {
  Timer? _refreshTimer;
  bool _wsConnected = true;
  bool _showSessionPreview = true;
  List<String> _canvasPanelOrder = const [];
  final Map<String, int> _canvasPanelFlex = {};
  final Map<String, TextEditingController> _canvasInputControllers = {};
  final Map<String, bool> _canvasSending = {};
  String? _canvasPickerSelectionKey;
  bool _showDetails = true;
  bool _canvasSelectionMode = false;
  EventCallback? _eventHandler;
  WsClient? _eventClient;
  StreamSubscription<WsConnectionState>? _connSub;

  bool _isLargeScreen(BuildContext context) =>
      MediaQuery.of(context).size.width >= 900;

  Future<void> _prefetchVisibleAgentPreviews() async {
    final shouldLoadPreview = _showSessionPreview;
    if (!shouldLoadPreview) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final nodeState = ref.read(nodesProvider);
    final futures = <Future<void>>[];
    for (final node in nodeState.nodeList) {
      for (final agent in nodeState.agentsFor(node.id)) {
        futures.add(_loadAgentPreview(client, node.id, agent.id));
      }
    }
    if (futures.isEmpty) return;
    await Future.wait(futures);
  }

  Future<void> _loadAgentPreview(
    WsClient client,
    String nodeId,
    String agentId,
  ) async {
    final notifier = ref.read(conversationProvider.notifier);
    try {
      final result = await client.call('conversation.history', {
        'nodeId': nodeId,
        'agentId': agentId,
        'limit': 12,
      }, timeout: const Duration(seconds: 3));
      final raw = result is Map ? result : <String, dynamic>{};
      // Resolve sessionId for the three-key cache: prefer the response's
      // sessionId (matches the events we just fetched), fall back to the
      // agent's current resume session in the local store.
      final responseSessionId = (raw['sessionId'] as String?) ?? '';
      final agent = ref
          .read(nodesProvider)
          .agentsFor(nodeId)
          .where((a) => a.id == agentId)
          .firstOrNull;
      final sessionId =
          responseSessionId.isNotEmpty ? responseSessionId : (agent?.sessionId ?? '');
      final events = ((raw['events'] as List?) ?? const [])
          .map((e) => normalizeHistoryEvent(e as Map))
          .toList();
      final messages =
          collapseConsecutiveActivityBlocks(convertEventsToMessages(events))
              .where(
                (m) => !m.isThinking && !m.isActivityBlock && !m.isToolCall,
              )
              .toList();
      notifier.mergeHistory(
        nodeId,
        agentId,
        sessionId,
        messages.map((m) {
          final role = m.role == 'user' ? 'user' : 'assistant';
          return {
            'nodeId': nodeId,
            'agentId': agentId,
            'sessionId': sessionId,
            'role': role,
            'text': m.text,
            'seq': m.seq,
          };
        }).toList(),
      );
    } catch (_) {}
  }

  @override
  void initState() {
    super.initState();
    _subscribeEvents();
    _startAutoRefresh();
    // Hydrate persisted detail-panel expansion preference (lazy load avoids
    // a race with widget tests that don't await the constructor).
    Future<void>.microtask(() {
      if (!mounted) return;
      ref.read(dashboardDetailPanelExpandedProvider.notifier).hydrate();
    });
    // Listen for connection state changes
    final notifier = ref.read(connectionProvider.notifier);
    _connSub = notifier.onStateChanged.listen((state) {
      if (!mounted) return;
      setState(() {
        _wsConnected = state == WsConnectionState.connected;
      });
      // Reconnect successful — refresh all data immediately
      if (state == WsConnectionState.connected) {
        _refreshAllNodes();
        _prefetchVisibleAgentPreviews();
        // Re-subscribe to events on the new connection
        _subscribeEvents();
      }
    });
  }

  @override
  void dispose() {
    _connSub?.cancel();
    if (_eventClient != null && _eventHandler != null) {
      _eventClient!.offEvent(_eventHandler!);
    }
    _refreshTimer?.cancel();
    for (final controller in _canvasInputControllers.values) {
      controller.dispose();
    }
    _canvasInputControllers.clear();
    super.dispose();
  }

  void _startAutoRefresh() {
    // Refresh every 10 seconds (was 3s, increased for slower networks)
    _refreshTimer = Timer.periodic(const Duration(seconds: 10), (_) {
      _refreshAllNodes();
    });
    // Initial refresh
    Future.delayed(const Duration(milliseconds: 100), _refreshAllNodes);
  }

  Future<void> _refreshAllNodes() async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('node.list', {});
      final nodes = (result is List
          ? result
          : (result['nodes'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadNodes(nodes);

      for (final n in nodes) {
        final nodeId = (n as Map<String, dynamic>)['id'] as String;
        await _refreshNodeAgents(nodeId);
      }
      await _prefetchVisibleAgentPreviews();
    } catch (e) {
      debugPrint('Auto refresh error: $e');
    }
  }

  void _onWsEvent(WsMessage event) {
    if (event.method == 'node.status_changed') {
      final params = event.params as Map<String, dynamic>?;
      if (params != null) {
        final nodeId = params['nodeId'] as String? ?? '';
        final status = params['status'] as String? ?? '';
        if (status == 'connected' && nodeId.isNotEmpty) {
          _refreshNodeAgents(nodeId);
        }
      }
    }
    ref.read(nodesProvider.notifier).handleEvent(event);
    ref.read(conversationProvider.notifier).handleEvent(event);
    if (ref.read(unreadSettingProvider)) {
      ref.read(unreadProvider.notifier).handleEvent(event);
    }
  }

  void _subscribeEvents() {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    if (_eventHandler != null && _eventClient != null) {
      _eventClient!.offEvent(_eventHandler!);
    }
    _eventHandler = _onWsEvent;
    _eventClient = client;
    client.onEvent(_eventHandler!);
  }

  Future<void> _refreshNodeAgents(String nodeId) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      final result = await client.call('agent.list', {'nodeId': nodeId});
      final agents = (result is List
          ? result
          : (result['agents'] as List?) ?? []);
      ref.read(nodesProvider.notifier).loadAgents(nodeId, agents);
    } catch (e) {
      debugPrint('Failed to refresh agents for node $nodeId: $e');
    }
  }

  Future<void> _discoverNodes(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => const AlertDialog(
        title: Text('发现节点'),
        content: SizedBox(
          height: 100,
          child: Center(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                OscilloscopeLoader(),
                SizedBox(height: 16),
                Text('正在扫描 SSH 配置...'),
              ],
            ),
          ),
        ),
      ),
    );

    try {
      final result = await client.call(
        'node.discover',
        {},
        timeout: const Duration(seconds: 15),
      );
      if (!context.mounted) return;
      Navigator.pop(context);

      final scanned = (result['scanned'] as num?)?.toInt() ?? 0;
      final found = (result['found'] as List?) ?? [];

      if (found.isEmpty) {
        if (!context.mounted) return;
        showDialog(
          context: context,
          builder: (ctx) => AlertDialog(
            title: const Text('发现节点'),
            content: Text('扫描了 $scanned 个 SSH 主机，未发现运行 agentd 的节点。'),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('确定'),
              ),
            ],
          ),
        );
        return;
      }

      // Show discovered nodes
      if (!context.mounted) return;
      final added = await showDialog<List<String>>(
        context: context,
        builder: (ctx) => StatefulBuilder(
          builder: (ctx, setState) {
            final selected = <String>{};
            for (final node in found) {
              selected.add(node['id'] as String);
            }

            return AlertDialog(
              title: Text('发现 ${found.length} 个节点'),
              content: SizedBox(
                width: double.maxFinite,
                child: ListView.builder(
                  shrinkWrap: true,
                  itemCount: found.length,
                  itemBuilder: (_, i) {
                    final node = found[i] as Map<String, dynamic>;
                    final id = node['id'] as String;
                    return CheckboxListTile(
                      title: Text(node['name'] as String),
                      subtitle: Text(node['host'] as String),
                      value: selected.contains(id),
                      onChanged: (v) {
                        setState(() {
                          if (v == true) {
                            selected.add(id);
                          } else {
                            selected.remove(id);
                          }
                        });
                      },
                    );
                  },
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx),
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(ctx, selected.toList()),
                  child: const Text('添加选中'),
                ),
              ],
            );
          },
        ),
      );

      // Add selected nodes
      if (added != null && added.isNotEmpty && context.mounted) {
        for (final id in added) {
          final node =
              found.firstWhere((n) => n['id'] == id) as Map<String, dynamic>;
          try {
            await client.call('node.add', {
              'id': node['id'],
              'name': node['name'],
              'host': node['host'],
              'sshAlias': node['sshAlias'],
              'sshPort': node['sshPort'],
              'agentdPort': 7373,
              'token': client.token,
            });
          } catch (e) {
            debugPrint('Failed to add node ${node['name']}: $e');
          }
        }
        // Refresh node list
        _refreshAllNodes();
      }
    } catch (e) {
      if (!context.mounted) return;
      Navigator.pop(context);
      showDialog(
        context: context,
        builder: (ctx) => AlertDialog(
          title: const Text('错误'),
          content: Text('扫描失败: $e'),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx),
              child: const Text('确定'),
            ),
          ],
        ),
      );
    }
  }

  Future<void> _restartGateway(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重启 Gateway'),
        content: const Text('确定要重启 Gateway 吗？WebSocket 连接将会断开并在重启完成后自动恢复。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('gateway.restart', {});
      if (!mounted) return;
      messenger.showSnackBar(const SnackBar(content: Text('Gateway 正在重启…')));
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('重启 Gateway 失败: $e')));
    }
  }

  Future<void> _showAddNodeDialog(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final nameCtrl = TextEditingController();
    final hostCtrl = TextEditingController();
    final aliasCtrl = TextEditingController();
    final tokenCtrl = TextEditingController();
    final dirCtrl = TextEditingController(text: r'$HOME/bin');
    bool deployNow = true;

    final client = ref.read(connectionProvider);
    if (client == null) return;
    // Default token to the current connection token for convenience
    tokenCtrl.text = client.token;

    final added = await showDialog<bool>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) => AlertDialog(
          title: const Text('添加远程节点'),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称',
                    hintText: 'ws',
                  ),
                ),
                TextField(
                  controller: hostCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Host',
                    hintText: '192.168.1.10',
                  ),
                ),
                TextField(
                  controller: aliasCtrl,
                  decoration: const InputDecoration(
                    labelText: 'SSH 别名 (可选)',
                    hintText: 'ssh config 中的别名',
                  ),
                ),
                TextField(
                  controller: tokenCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Token',
                    hintText: 'agentd 认证 token',
                  ),
                ),
                TextField(
                  controller: dirCtrl,
                  decoration: const InputDecoration(
                    labelText: '远程目录',
                    hintText: r'$HOME/bin',
                  ),
                ),
                const SizedBox(height: 8),
                CheckboxListTile(
                  title: const Text('立即部署 agentd'),
                  value: deployNow,
                  onChanged: (v) => setState(() => deployNow = v ?? true),
                  contentPadding: EdgeInsets.zero,
                ),
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('添加'),
            ),
          ],
        ),
      ),
    );

    if (added != true) return;
    final name = nameCtrl.text.trim();
    final host = hostCtrl.text.trim();
    if (host.isEmpty) return;

    try {
      final addResult = await client.call('node.add', {
        'name': name.isEmpty ? host : name,
        'host': host,
        'sshAlias': aliasCtrl.text.trim(),
        'token': tokenCtrl.text.trim(),
      });
      final nodeId = (addResult is Map<String, dynamic>
          ? addResult['nodeId'] as String?
          : null);
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('节点已添加: ${name.isEmpty ? host : name}')),
      );
      if (deployNow && nodeId != null) {
        await client.call('node.deploy', {
          'nodeId': nodeId,
          'remoteDir': dirCtrl.text.trim(),
        });
        if (!mounted) return;
        messenger.showSnackBar(const SnackBar(content: Text('部署已启动')));
      }
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('添加/部署失败: $e')));
    }
  }

  TextEditingController _canvasControllerFor(String panelKey) {
    return _canvasInputControllers.putIfAbsent(
      panelKey,
      () => TextEditingController(),
    );
  }

  int _canvasFlexFor(String panelKey) {
    return _canvasPanelFlex[panelKey] ?? 1;
  }

  Future<void> _addSessionToCanvas(DashboardSessionTarget target) async {
    final panelKey = target.key;
    if (_canvasPanelOrder.contains(panelKey)) return;
    setState(() {
      _canvasPanelOrder = [..._canvasPanelOrder, panelKey];
      _canvasPanelFlex.putIfAbsent(panelKey, () => 1);
    });

    final client = ref.read(connectionProvider);
    if (client != null) {
      await _loadAgentPreview(client, target.nodeId, target.agent.id);
    }
  }

  void _removeCanvasPanel(String panelKey) {
    setState(() {
      _canvasPanelOrder =
          _canvasPanelOrder.where((key) => key != panelKey).toList();
      _canvasPanelFlex.remove(panelKey);
      _canvasSending.remove(panelKey);
      if (_canvasPickerSelectionKey == panelKey) {
        _canvasPickerSelectionKey = null;
      }
    });
    _canvasInputControllers.remove(panelKey)?.dispose();
  }

  Future<void> _sendCanvasReply(DashboardSessionTarget target) async {
    final panelKey = target.key;
    final controller = _canvasControllerFor(panelKey);
    final text = controller.text.trim();
    if (text.isEmpty || (_canvasSending[panelKey] ?? false)) return;

    if (target.agent.isReadOnly) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('当前会话是只读附加会话，无法在画布内回复')),
        );
      }
      return;
    }

    final client = ref.read(connectionProvider);
    if (client == null) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('连接未就绪，无法发送')));
      }
      return;
    }

    controller.clear();
    setState(() {
      _canvasSending[panelKey] = true;
    });

    try {
      await client.call('conversation.send', {
        'nodeId': target.nodeId,
        'agentId': target.agent.id,
        'message': text,
        'raw': false,
      }, timeout: const Duration(seconds: 30));
      ref.read(unreadProvider.notifier).markAsRead(target.nodeId, target.agent.id);
      await _loadAgentPreview(client, target.nodeId, target.agent.id);
    } catch (e) {
      controller.text = text;
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('发送失败: $e')));
      }
    } finally {
      if (mounted) {
        setState(() {
          _canvasSending[panelKey] = false;
        });
      }
    }
  }

  Future<void> _showCanvasLogoPicker(String sessionKey, IconData current) async {
    final picked = await showDialog<IconData>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('选择会话图标'),
        content: SizedBox(
          width: double.maxFinite,
          child: GridView.builder(
            shrinkWrap: true,
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 6,
              mainAxisSpacing: 8,
              crossAxisSpacing: 8,
            ),
            itemCount: availableSessionLogos.length,
            itemBuilder: (context, index) {
              final icon = availableSessionLogos[index];
              final isSelected = icon == current;
              return InkWell(
                onTap: () => Navigator.pop(ctx, icon),
                borderRadius: BorderRadius.circular(8),
                child: Container(
                  decoration: BoxDecoration(
                    color: isSelected
                        ? Theme.of(context).colorScheme.primaryContainer
                        : null,
                    borderRadius: BorderRadius.circular(8),
                    border: isSelected
                        ? Border.all(
                            color: Theme.of(context).colorScheme.primary,
                            width: 2,
                          )
                        : null,
                  ),
                  child: Icon(
                    icon,
                    color: isSelected
                        ? Theme.of(context).colorScheme.primary
                        : Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              );
            },
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () {
              ref.read(sessionLogoProvider.notifier).resetLogo(sessionKey);
              Navigator.pop(ctx);
            },
            child: const Text('恢复默认'),
          ),
        ],
      ),
    );
    if (picked != null) {
      ref.read(sessionLogoProvider.notifier).setLogo(sessionKey, picked);
    }
  }

  Widget _buildDashboardListPane(
    List<NodeModel> nodes,
    Set<String> canvasPanelKeys,
    Map<String, DashboardSessionTarget> sessionByKey,
  ) {
    return ListView.builder(
      itemCount: nodes.length,
      padding: const EdgeInsets.all(8),
      itemBuilder: (_, i) => NodeCard(
        node: nodes[i],
        showSessionPreview: _showSessionPreview,
        isLargeScreen: true,
        showDetails: _showDetails,
        canvasSelectionMode: _canvasSelectionMode,
        canvasPanelKeys: canvasPanelKeys,
        onToggleCanvas: (agent) {
          final key = '${nodes[i].id}:${agent.id}';
          if (_canvasPanelOrder.contains(key)) {
            _removeCanvasPanel(key);
          } else {
            final target = sessionByKey[key];
            if (target != null) {
              _addSessionToCanvas(target);
            }
          }
        },
      ),
    );
  }

  Widget _buildCanvasPane(
    List<DashboardSessionTarget> sessions,
    Map<String, DashboardSessionTarget> sessionByKey,
  ) {
    final activePanelKeys = _canvasPanelOrder
        .where((key) => sessionByKey.containsKey(key))
        .toList();
    if (activePanelKeys.length != _canvasPanelOrder.length) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        setState(() {
          _canvasPanelOrder = activePanelKeys;
        });
      });
    }

    return Padding(
      padding: const EdgeInsets.all(6),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                '详情画布',
                style: Theme.of(context).textTheme.titleMedium?.copyWith(
                  fontWeight: FontWeight.w600,
                ),
              ),
              if (_canvasSelectionMode) ...[
                const SizedBox(width: AppSpacing.md),
                Expanded(
                  child: Text(
                    '点击左侧 + 添加 Agent，- 移除 Agent',
                    style: Theme.of(context).textTheme.labelSmall?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
                ),
              ] else
                const Spacer(),
              const SizedBox(width: AppSpacing.sm),
              FilledButton.icon(
                onPressed: () {
                  setState(() {
                    _canvasSelectionMode = !_canvasSelectionMode;
                  });
                },
                icon: Icon(_canvasSelectionMode ? Icons.check : Icons.add),
                label: Text(_canvasSelectionMode ? '完成' : '添加'),
              ),
            ],
          ),
          const SizedBox(height: 12),
          Expanded(
            child: activePanelKeys.isEmpty
                ? const Center(
                    child: Text(
                      '暂无画布面板\n请从上方选择 Agent 并添加',
                      textAlign: TextAlign.center,
                      style: TextStyle(color: Colors.grey),
                    ),
                  )
                : Row(
                    children: () {
                      final children = <Widget>[];
                      for (int i = 0; i < activePanelKeys.length; i++) {
                        final panelKey = activePanelKeys[i];
                        final target = sessionByKey[panelKey]!;
                        final logoKey =
                            '${target.nodeId}:${sessionIdentityKey(
                          provider: target.agent.provider,
                          sessionId: target.agent.sessionId,
                          pid: target.agent.pid,
                          agentId: target.agent.id,
                        )}';
                        final logo = ref
                            .watch(sessionLogoProvider.notifier)
                            .iconFor(logoKey);
                        children.add(
                          Expanded(
                            flex: _canvasFlexFor(panelKey),
                            child: _CanvasSessionPanel(
                              panelKey: panelKey,
                              target: target,
                              messages:
                                  ref.watch(conversationProvider)[
                                      (target.nodeId, target.agent.id, target.agent.sessionId ?? '')] ??
                                  const [],
                              controller: _canvasControllerFor(panelKey),
                              sending: _canvasSending[panelKey] ?? false,
                              onSend: () => _sendCanvasReply(target),
                              onOpenDetail: () {
                                context.push(
                                  '/agent/${target.nodeId}/${target.agent.id}',
                                );
                              },
                              onRemove: () => _removeCanvasPanel(panelKey),
                              logo: logo,
                              onLogoTap: () => _showCanvasLogoPicker(
                                logoKey,
                                logo,
                              ),
                            ),
                          ),
                        );
                        if (i < activePanelKeys.length - 1) {
                          children.add(
                            _ResizeHandle(
                              onDrag: (dx) {
                                setState(() {
                                  final leftKey = activePanelKeys[i];
                                  final rightKey = activePanelKeys[i + 1];
                                  final leftFlex = _canvasFlexFor(leftKey);
                                  final rightFlex = _canvasFlexFor(rightKey);
                                  final delta = (dx / 40).round();
                                  if (delta == 0) return;
                                  final newLeft =
                                      (leftFlex + delta).clamp(1, 20);
                                  final newRight =
                                      (rightFlex - delta).clamp(1, 20);
                                  _canvasPanelFlex[leftKey] = newLeft;
                                  _canvasPanelFlex[rightKey] = newRight;
                                });
                              },
                            ),
                          );
                        }
                      }
                      return children;
                    }(),
                  ),
          ),
        ],
      ),
    );
  }

  /// 48px collapsed rail with a chevron toggle (no canvas content).
  Widget _buildDetailPaneRail(bool expanded) {
    final scheme = Theme.of(context).colorScheme;
    return Container(
      color: scheme.surface,
      child: Column(
        children: [
          const SizedBox(height: 8),
          IconButton(
            key: const Key('dashboard_detail_panel_toggle'),
            tooltip: expanded ? '折叠详情画布' : '展开详情画布',
            icon: Icon(
              expanded ? Icons.chevron_right : Icons.chevron_left,
              color: scheme.onSurfaceVariant,
            ),
            onPressed: () => ref
                .read(dashboardDetailPanelExpandedProvider.notifier)
                .toggle(),
          ),
          const SizedBox(height: 4),
          RotatedBox(
            quarterTurns: 3,
            child: Text(
              '详情画布',
              style: Theme.of(context).textTheme.labelSmall?.copyWith(
                    color: scheme.onSurfaceVariant,
                    letterSpacing: 1.2,
                  ),
            ),
          ),
        ],
      ),
    );
  }

  /// Detail pane content for narrow-screen overlay mode: combines the rail
  /// (with toggle) and the actual canvas in one container.
  Widget _buildDetailPaneContent(
    bool expanded,
    List<DashboardSessionTarget> sessions,
    Map<String, DashboardSessionTarget> sessionByKey,
  ) {
    if (!expanded) {
      return _buildDetailPaneRail(expanded);
    }
    return Row(
      children: [
        SizedBox(
          width: 48,
          child: _buildDetailPaneRail(expanded),
        ),
        Expanded(
          child: DecoratedBox(
            decoration: BoxDecoration(
              border: Border(
                left: BorderSide(
                  color: Theme.of(context).dividerColor,
                  width: 1,
                ),
              ),
            ),
            child: _buildCanvasPane(sessions, sessionByKey),
          ),
        ),
      ],
    );
  }

  @override
  Widget build(BuildContext context) {
    final nodeState = ref.watch(nodesProvider);
    final nodes = nodeState.nodeList;

    // Dashboard subtitle stats
    final connectedNodes = nodes.where((n) => n.status == NodeStatus.connected).length;
    final activeAgents = nodes.fold<int>(
      0,
      (sum, n) =>
          sum +
          nodeState
              .agentsFor(n.id)
              .where(
                (a) =>
                    a.status == AgentStatus.working ||
                    a.status == AgentStatus.starting ||
                    a.status == AgentStatus.idle,
              )
              .length,
    );
    final subtitleParts = <String>[
      '${nodes.length} 节点',
      if (connectedNodes != nodes.length) '$connectedNodes 已连接',
      '$activeAgents 活跃',
    ];
    final subtitle = subtitleParts.join(' · ');

    return Scaffold(
      appBar: MissionControlAppBar(
        toolbarHeight: 64,
        leading: const IconButton(
          icon: Icon(Icons.dashboard),
          tooltip: '仪表盘',
          onPressed: null,
        ),
        titleWidget: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Flexible(
                  child: Text(
                    '仪表盘',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: Theme.of(context).textTheme.titleLarge?.copyWith(
                          fontWeight: FontWeight.bold,
                        ),
                  ),
                ),
                if (!_wsConnected) ...[
                  const SizedBox(width: 8),
                  SizedBox(
                    width: 12,
                    height: 12,
                    child: CircularProgressIndicator(
                      strokeWidth: 2,
                      color: Theme.of(context).colorScheme.onSurfaceVariant,
                    ),
                  ),
                ],
              ],
            ),
            Text(
              subtitle,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: Theme.of(context).textTheme.bodySmall?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
            ),
          ],
        ),
        actions: [
          if (_showDetails) const _HealthIndicator(),
          IconButton(
            icon: Icon(_showDetails ? Icons.expand_less : Icons.expand_more),
            tooltip: _showDetails ? '折叠详情' : '展开详情',
            onPressed: () {
              setState(() {
                _showDetails = !_showDetails;
              });
            },
          ),
          IconButton(
            icon: const Icon(Icons.search),
            tooltip: '发现节点',
            onPressed: () => _discoverNodes(context, ref),
          ),
          IconButton(
            icon: const Icon(Icons.add_circle_outline),
            tooltip: '添加节点',
            onPressed: () => _showAddNodeDialog(ref),
          ),
          IconButton(
            icon: const Icon(Icons.restart_alt),
            tooltip: '重启网关',
            onPressed: _wsConnected ? () => _restartGateway(ref) : null,
          ),
          IconButton(
            icon: Icon(_showSessionPreview ? Icons.notes_outlined : Icons.notes),
            tooltip: _showSessionPreview ? '隐藏会话预览' : '显示会话预览',
            onPressed: () {
              setState(() {
                _showSessionPreview = !_showSessionPreview;
              });
              if (_showSessionPreview) {
                _prefetchVisibleAgentPreviews();
              }
            },
          ),
          IconButton(
            icon: const Icon(Icons.settings),
            onPressed: () => context.push('/settings'),
          ),
        ],
      ),
      body: nodes.isEmpty
          ? const EmptyState(
              message: '等待信号...',
              subMessage: '尚无 Agent 节点',
            )
          : LayoutBuilder(
              builder: (context, constraints) {
                final largeScreen = _isLargeScreen(context);
                if (!largeScreen) {
                  return ListView.builder(
                    itemCount: nodes.length,
                    padding: const EdgeInsets.all(8),
                    itemBuilder: (_, i) => NodeCard(
                      node: nodes[i],
                      showSessionPreview: _showSessionPreview,
                      isLargeScreen: false,
                    ),
                  );
                }

                final sessions = buildVisibleDashboardSessions(nodeState);
                final sessionByKey = {
                  for (final session in sessions) session.key: session,
                };

                final canvasPanelKeys = Set<String>.from(_canvasPanelOrder);

                final detailExpanded =
                    ref.watch(dashboardDetailPanelExpandedProvider);
                // Narrow split (<800) → use Drawer-style overlay so the rail
                // doesn't crowd the list pane.
                final overlayMode = constraints.maxWidth < 800;

                if (overlayMode) {
                  return Stack(
                    children: [
                      _buildDashboardListPane(
                        nodes,
                        canvasPanelKeys,
                        sessionByKey,
                      ),
                      if (detailExpanded)
                        Positioned.fill(
                          child: GestureDetector(
                            behavior: HitTestBehavior.opaque,
                            onTap: () => ref
                                .read(dashboardDetailPanelExpandedProvider
                                    .notifier)
                                .setExpanded(false),
                            child: Container(
                              color: Colors.black.withValues(alpha: 0.30),
                            ),
                          ),
                        ),
                      Align(
                        alignment: Alignment.centerRight,
                        child: AnimatedContainer(
                          key: const Key('dashboard_detail_panel'),
                          duration: const Duration(milliseconds: 220),
                          curve: Curves.easeInOut,
                          width: detailExpanded
                              ? constraints.maxWidth.clamp(320.0, 520.0)
                              : 48,
                          child: Material(
                            elevation: detailExpanded ? 4 : 0,
                            color: Theme.of(context).colorScheme.surface,
                            child: _buildDetailPaneContent(
                              detailExpanded,
                              sessions,
                              sessionByKey,
                            ),
                          ),
                        ),
                      ),
                    ],
                  );
                }

                // Wide layout: list pane on the left, detail panel on the
                // right. The detail panel itself collapses to a 48px rail
                // and grows wide enough to host the canvas inline — keeping
                // its WIDTH the source of truth for the toggle (testable
                // and intent-clear).
                final expandedDetailWidth =
                    (constraints.maxWidth * 0.55).clamp(320.0, 720.0);
                return Row(
                  children: [
                    Expanded(
                      child: _buildDashboardListPane(
                          nodes, canvasPanelKeys, sessionByKey),
                    ),
                    const VerticalDivider(width: 1),
                    AnimatedContainer(
                      key: const Key('dashboard_detail_panel'),
                      duration: const Duration(milliseconds: 220),
                      curve: Curves.easeInOut,
                      width: detailExpanded ? expandedDetailWidth : 48,
                      child: _buildDetailPaneContent(
                        detailExpanded,
                        sessions,
                        sessionByKey,
                      ),
                    ),
                  ],
                );
              },
            ),
    );
  }
}

class _CanvasSessionPanel extends StatelessWidget {
  final String panelKey;
  final DashboardSessionTarget target;
  final List<MessageModel> messages;
  final TextEditingController controller;
  final bool sending;
  final VoidCallback onSend;
  final VoidCallback onOpenDetail;
  final VoidCallback onRemove;
  final IconData logo;
  final VoidCallback? onLogoTap;

  const _CanvasSessionPanel({
    required this.panelKey,
    required this.target,
    required this.messages,
    required this.controller,
    required this.sending,
    required this.onSend,
    required this.onOpenDetail,
    required this.onRemove,
    required this.logo,
    this.onLogoTap,
  });


  @override
  Widget build(BuildContext context) {
    final title = managedAgentTitle(target.agent);
    final subtitleParts = <String>[
      target.nodeName,
      if ((target.agent.sessionId ?? '').trim().isNotEmpty)
        shortSessionId(target.agent.sessionId),
      AgentStatusTheme.getLabel(target.agent.status),
    ];
    final displayMessages =
        messages.where((m) => m.text.trim().isNotEmpty).toList();

    final inputEnabled = !sending && !target.agent.isReadOnly;

    return Card(
      margin: EdgeInsets.zero,
      child: Padding(
        padding: const EdgeInsets.all(6),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                InkWell(
                  onTap: onLogoTap,
                  borderRadius: BorderRadius.circular(16),
                  child: Padding(
                    padding: const EdgeInsets.all(2),
                    child: Icon(
                      logo,
                      color: providerColor(target.agent.provider),
                      size: 16,
                    ),
                  ),
                ),
                const SizedBox(width: 6),
                Expanded(
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Text(
                        title,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                      Text(
                        subtitleParts.join(' · '),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: Theme.of(context).textTheme.labelMedium?.copyWith(
                              color: Theme.of(context).colorScheme.onSurfaceVariant,
                            ),
                      ),
                    ],
                  ),
                ),
                IconButton(
                  tooltip: '打开详情',
                  icon: const Icon(Icons.open_in_new, size: 16),
                  constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
                  padding: EdgeInsets.zero,
                  onPressed: onOpenDetail,
                ),
                IconButton(
                  tooltip: '移除面板',
                  icon: const Icon(Icons.close, size: 16),
                  constraints: const BoxConstraints(minWidth: 32, minHeight: 32),
                  padding: EdgeInsets.zero,
                  onPressed: onRemove,
                ),
              ],
            ),
            const SizedBox(height: 4),
            Expanded(
              child: Container(
                width: double.infinity,
                padding: const EdgeInsets.all(6),
                decoration: BoxDecoration(
                  color: Theme.of(context).colorScheme.surfaceContainerHighest,
                  borderRadius: BorderRadius.circular(6),
                ),
                child: displayMessages.isEmpty
                  ? Center(
                      child: Text(
                        '暂无会话内容',
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                      ),
                    )
                  : ListView.builder(
                      reverse: true,
                      itemCount: displayMessages.length,
                      itemBuilder: (context, index) {
                        final msg = displayMessages[displayMessages.length - 1 - index];
                        final isUser = msg.role == MessageRole.user;
                        return Padding(
                          padding: const EdgeInsets.only(bottom: 3),
                          child: Row(
                            crossAxisAlignment: CrossAxisAlignment.start,
                            children: [
                              Text(
                                isUser ? '你: ' : 'AI: ',
                                style: Theme.of(context).textTheme.labelSmall?.copyWith(
                                  fontWeight: FontWeight.w600,
                                  color: isUser
                                      ? Theme.of(context).colorScheme.primary
                                      : Theme.of(context).colorScheme.onSurface,
                                ),
                              ),
                              Expanded(
                                child: _MarkdownText(
                                  msg.text,
                                  style: AppTextStyles.labelSmall.copyWith(
                                    color: Theme.of(context).colorScheme.onSurface,
                                    height: 1.3,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        );
                      },
                    ),
              ),
            ),
            const SizedBox(height: 4),
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: controller,
                    enabled: inputEnabled,
                    minLines: 1,
                    maxLines: 3,
                    textInputAction: TextInputAction.send,
                    onSubmitted: (_) => onSend(),
                    decoration: InputDecoration(
                      isDense: true,
                      border: const OutlineInputBorder(),
                      hintText: target.agent.isReadOnly
                          ? '只读会话，无法回复'
                          : '输入回复…',
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: inputEnabled ? onSend : null,
                  child: sending
                      ? const SizedBox(
                          width: 16,
                          height: 16,
                          child: CircularProgressIndicator(strokeWidth: 2),
                        )
                      : const Icon(Icons.send, size: 16),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}

class NodeCard extends ConsumerStatefulWidget {
  final NodeModel node;
  final bool showSessionPreview;
  final bool isLargeScreen;
  final bool showDetails;
  final bool canvasSelectionMode;
  final Set<String> canvasPanelKeys;
  final ValueChanged<AgentModel>? onToggleCanvas;
  const NodeCard({
    super.key,
    required this.node,
    this.showSessionPreview = false,
    this.isLargeScreen = false,
    this.showDetails = false,
    this.canvasSelectionMode = false,
    this.canvasPanelKeys = const {},
    this.onToggleCanvas,
  });

  @override
  ConsumerState<NodeCard> createState() => _NodeCardState();
}

class _NodeCardState extends ConsumerState<NodeCard> {
  Color get _statusColor {
    switch (widget.node.status) {
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
    switch (widget.node.status) {
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

  List<Widget> _buildSummaryChips(
    BuildContext context,
    List<AgentModel> agents,
  ) {
    // Summary chips removed per UX feedback: the "会话 N" / "活跃 N" badges
    // duplicated information already visible in the agent list itself and
    // were carrying no actionable signal. Method retained as a no-op so the
    // call sites can stay unchanged for now (subtitle falls back to the
    // location/status text path).
    return const <Widget>[];
  }

  @override
  Widget build(BuildContext context) {
    final agents = ref.watch(nodesProvider).agentsFor(widget.node.id);
    final nodeDisplay = widget.node.isLocal
        ? widget.node.name
        : widget.node.name;
    final isRemote = !widget.node.isLocal;
    final nodeReady = widget.node.status == NodeStatus.connected;

    // Choose icon based on local/remote status
    final nodeIcon = widget.node.isLocal ? Icons.computer : Icons.cloud;

    // Only show active agents. Stopped/crashed agents are hidden from the main list
    // and should be managed through the session manager instead.
    final visibleAgents = visibleManagedAgentsForNode(agents);
    final summaryChips = _buildSummaryChips(context, visibleAgents);

    return Card(
      margin: const EdgeInsets.only(bottom: 8),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          ListTile(
            dense: true,
            minLeadingWidth: 0,
            leading: Icon(nodeIcon, color: _statusColor, size: 20),
            title: Text(
              nodeDisplay,
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
            subtitle: widget.isLargeScreen && widget.showDetails && summaryChips.isNotEmpty
                ? Wrap(spacing: 6, runSpacing: 6, children: summaryChips)
                : Text(
                    '${widget.node.location.displayLocation}  ·  $_statusLabel',
                    style: Theme.of(context).textTheme.labelMedium?.copyWith(
                          color: Theme.of(context).colorScheme.onSurfaceVariant,
                        ),
                  ),
            trailing: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                IconButton(
                  icon: const Icon(Icons.edit, size: 18),
                  tooltip: '重命名节点',
                  onPressed: () => _renameNode(context, ref),
                ),
                Icon(Icons.circle, color: _statusColor, size: 12),
              ],
            ),
          ),
          if (visibleAgents.isNotEmpty) const Divider(height: 1),
          ...visibleAgents.map(
            (a) {
              final key = '${widget.node.id}:${a.id}';
              return AgentRow(
                key: ValueKey(key),
                agent: a,
                nodeId: widget.node.id,
                showPreview: widget.showSessionPreview,
                isLargeScreen: widget.isLargeScreen,
                showDetails: widget.showDetails,
                canvasSelectionMode: widget.canvasSelectionMode,
                isInCanvas: widget.canvasPanelKeys.contains(key),
                onToggleCanvas: widget.onToggleCanvas != null
                    ? () => widget.onToggleCanvas!(a)
                    : null,
              );
            },
          ),
          if (visibleAgents.isEmpty)
            const Padding(
              padding: EdgeInsets.all(12),
              child: Text('暂无活跃 Agent', style: TextStyle(color: Colors.grey)),
            ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 6),
            child: Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                if (isRemote &&
                    (widget.node.status == NodeStatus.disconnected ||
                        widget.node.status == NodeStatus.error)) ...[
                  TextButton.icon(
                    onPressed: () => _connectNode(context, ref),
                    icon: const Icon(Icons.link, size: 18),
                    label: const Text('连接'),
                  ),
                  TextButton.icon(
                    onPressed: () => _deployNode(ref),
                    icon: const Icon(Icons.cloud_upload, size: 18),
                    label: const Text('部署'),
                  ),
                ],
                if (isRemote && widget.node.status == NodeStatus.connecting)
                  TextButton.icon(
                    onPressed: null,
                    icon: const Icon(Icons.sync, size: 18),
                    label: const Text('连接中…'),
                  ),
                if (isRemote && widget.node.status == NodeStatus.connected)
                  TextButton.icon(
                    onPressed: () => _restartNode(context, ref),
                    icon: const Icon(Icons.restart_alt, size: 18),
                    label: const Text('重启节点'),
                  ),
                if (isRemote && widget.node.status == NodeStatus.deploying)
                  TextButton.icon(
                    onPressed: null,
                    icon: const Icon(Icons.sync, size: 18),
                    label: const Text('重启中…'),
                  ),
                TextButton.icon(
                  onPressed: nodeReady
                      ? () => _showCreateAgentDialog(context, ref)
                      : null,
                  icon: const Icon(Icons.add, size: 18),
                  label: const Text('新建 Agent'),
                ),
                TextButton.icon(
                  onPressed: nodeReady
                      ? () => _showSessionManager(context, ref)
                      : null,
                  icon: const Icon(Icons.manage_search, size: 18),
                  label: const Text('管理 Agent'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

  Future<void> _connectNode(BuildContext context, WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      await client.call('node.connect', {'nodeId': widget.node.id});
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('正在连接 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('连接节点失败: $e')));
    }
  }

  Future<void> _restartNode(BuildContext context, WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final client = ref.read(connectionProvider);
    if (client == null) return;

    try {
      await client.call('node.restart', {'nodeId': widget.node.id});
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('正在重启 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('重启节点失败: $e')));
    }
  }

  Future<void> _deployNode(WidgetRef ref) async {
    final messenger = ScaffoldMessenger.of(context);
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final dirCtrl = TextEditingController(text: r'$HOME/bin');
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('部署 ${widget.node.name}'),
        content: TextField(
          controller: dirCtrl,
          decoration: const InputDecoration(
            labelText: '远程目录',
            hintText: r'$HOME/bin',
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('部署'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    try {
      await client.call('node.deploy', {
        'nodeId': widget.node.id,
        'remoteDir': dirCtrl.text.trim(),
      });
      if (!mounted) return;
      messenger.showSnackBar(
        SnackBar(content: Text('正在部署 ${widget.node.name}…')),
      );
    } catch (e) {
      if (!mounted) return;
      messenger.showSnackBar(SnackBar(content: Text('部署失败: $e')));
    }
  }

  Future<void> _refreshAgents(WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;
    final result = await client.call('agent.list', {'nodeId': widget.node.id});
    final agents = (result is List
        ? result
        : (result['agents'] as List?) ?? []);
    ref.read(nodesProvider.notifier).loadAgents(widget.node.id, agents);
  }

  Future<void> _renameNode(BuildContext context, WidgetRef ref) async {
    final controller = TextEditingController(text: widget.node.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名节点'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '输入新名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (newName != null && newName.isNotEmpty) {
      final client = ref.read(connectionProvider);
      if (client == null) return;
      try {
        await client.call('node.rename', {
          'nodeId': widget.node.id,
          'name': newName,
        });
        ref.read(nodesProvider.notifier).renameNode(widget.node.id, newName);
      } catch (e) {
        debugPrint('rename node error: $e');
      }
    }
  }

  void _showCreateAgentDialog(BuildContext context, WidgetRef ref) {
    final cwdCtrl = TextEditingController();
    final nameCtrl = TextEditingController();
    final sessionCtrl = TextEditingController();
    final modelCtrl = TextEditingController();
    String provider = 'claude';
    final defaultModels = getDefaultModels(provider);
    if (defaultModels.isNotEmpty) {
      modelCtrl.text = defaultModels.first['id'] ?? '';
    }

    // Suggested directories from agentd
    List<Map<String, dynamic>> suggestedDirs = [];
    String homeDir = '';
    bool dirsLoaded = false;

    Future<void> fetchSuggestedDirs() async {
      final client = ref.read(connectionProvider);
      if (client == null) return;
      try {
        final result = await client.call('system.suggest_dirs', {
          'nodeId': widget.node.id,
        });
        if (result is Map<String, dynamic>) {
          homeDir = result['homeDir'] as String? ?? '';
          final dirs = result['dirs'] as List? ?? [];
          suggestedDirs = dirs
              .map((d) => Map<String, dynamic>.from(d as Map))
              .toList();
          dirsLoaded = true;
          if (cwdCtrl.text.isEmpty && homeDir.isNotEmpty) {
            cwdCtrl.text = homeDir;
          }
        }
      } catch (e) {
        debugPrint('Failed to fetch suggested dirs: $e');
        // Fallback: try system.info for home dir
        try {
          final result = await client.call('system.info', {
            'nodeId': widget.node.id,
          });
          if (result is Map<String, dynamic>) {
            homeDir = result['homeDir'] as String? ?? '';
            if (cwdCtrl.text.isEmpty && homeDir.isNotEmpty) {
              cwdCtrl.text = homeDir;
            }
          }
        } catch (_) {}
      }
    }

    // Fetch dirs when dialog opens
    fetchSuggestedDirs();

    showDialog(
      context: context,
      builder: (_) => StatefulBuilder(
        builder: (context, setState) => AlertDialog(
          title: Text(
            '在 ${(widget.node.host.isNotEmpty && widget.node.host != 'localhost' && widget.node.host != '127.0.0.1') ? widget.node.host : widget.node.name} 上新建 Agent',
          ),
          content: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  controller: nameCtrl,
                  decoration: const InputDecoration(
                    labelText: '名称',
                    hintText: 'claude-1',
                  ),
                ),
                Autocomplete<String>(
                  initialValue: TextEditingValue(text: cwdCtrl.text),
                  fieldViewBuilder: (context, fieldCtrl, focusNode, onSubmitted) {
                    // Sync back to cwdCtrl
                    fieldCtrl.addListener(() {
                      cwdCtrl.text = fieldCtrl.text;
                    });
                    return TextField(
                      controller: fieldCtrl,
                      focusNode: focusNode,
                      decoration: InputDecoration(
                        labelText: '工作目录',
                        hintText: homeDir.isNotEmpty ? homeDir : '/home/user/proj',
                        suffixIcon: dirsLoaded
                            ? const Icon(Icons.folder_open, size: 20)
                            : const SizedBox(
                                width: 16,
                                height: 16,
                                child: CircularProgressIndicator(strokeWidth: 2),
                              ),
                      ),
                    );
                  },
                  optionsBuilder: (textEditingValue) {
                    if (textEditingValue.text.isEmpty) {
                      return suggestedDirs.map((d) => d['path'] as String);
                    }
                    final input = textEditingValue.text.toLowerCase();
                    return suggestedDirs
                        .map((d) => d['path'] as String)
                        .where((p) => p.toLowerCase().contains(input));
                  },
                  onSelected: (selection) {
                    cwdCtrl.text = selection;
                  },
                  displayStringForOption: (option) {
                    final dir = suggestedDirs.firstWhere(
                      (d) => d['path'] == option,
                      orElse: () => {'display': option, 'hasGit': false},
                    );
                    final display = dir['display'] as String? ?? option;
                    final hasGit = dir['hasGit'] as bool? ?? false;
                    return hasGit ? '$display (git)' : display;
                  },
                ),
                const SizedBox(height: 8),
                SegmentedButton<String>(
                  segments: const [
                    ButtonSegment(value: 'claude', label: Text('Claude')),
                    ButtonSegment(value: 'opencode', label: Text('OpenCode')),
                    ButtonSegment(value: 'hermes', label: Text('Hermes')),
                  ],
                  selected: {provider},
                  onSelectionChanged: (s) => setState(() {
                    provider = s.first;
                    final models = getDefaultModels(provider);
                    modelCtrl.text = models.first['id'] ?? '';
                  }),
                ),
                const SizedBox(height: 8),
                TextField(
                  controller: sessionCtrl,
                  decoration: const InputDecoration(
                    labelText: 'Session ID (可选)',
                    hintText: 'ses_xxx / uuid',
                    helperText: '留空创建新 session',
                  ),
                ),
                if (provider == 'claude') ...[
                  const SizedBox(height: 8),
                  TextField(
                    controller: modelCtrl,
                    decoration: InputDecoration(
                      labelText: 'Model (可选)',
                      hintText:
                          defaultModels.first['name'] ??
                          defaultModels.first['id'] ??
                          '',
                    ),
                  ),
                ],
              ],
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () async {
                final client = ref.read(connectionProvider);
                if (client == null) return;
                final workDir = cwdCtrl.text.trim();
                if (workDir.isEmpty) return;

                // Ensure work directory exists
                try {
                  await client.call('system.mkdir', {
                    'nodeId': widget.node.id,
                    'path': workDir,
                  });
                } catch (e) {
                  debugPrint('mkdir result: $e');
                }

                Navigator.pop(context);

                final params = <String, dynamic>{
                  'nodeId': widget.node.id,
                  'name': nameCtrl.text.trim(),
                  'workDir': workDir,
                  'provider': provider,
                };
                final sessionId = sessionCtrl.text.trim();
                final model = modelCtrl.text.trim();
                if (sessionId.isNotEmpty) params['sessionId'] = sessionId;
                if (provider == 'claude' && model.isNotEmpty) {
                  params['model'] = model;
                }

                try {
                  await client.call('session.create', params);
                  await _refreshAgents(ref);
                } catch (e) {
                  debugPrint('Create session error: $e');
                }
              },
              child: const Text('创建'),
            ),
          ],
        ),
      ),
    );
  }

  Future<void> _showSessionManager(BuildContext context, WidgetRef ref) async {
    final client = ref.read(connectionProvider);
    if (client == null) return;

    List<AgentModel> managedAgents = [];
    List<SessionCandidate> sessions = [];
    bool autoAttachDone = false;

    List<AgentModel> normalizeManaged(List<AgentModel> input) {
      final byName = <String, AgentModel>{};
      for (final a in input) {
        // Filter logic must match NodeCard.build():
        // - Always show working/starting/idle agents
        // - For stopped agents: only show if hasHistory AND has sessionId (resumable)
        // - crashed agents are hidden entirely
        final hasResumeCapability =
            a.sessionId != null && a.sessionId!.isNotEmpty;
        final isActive =
            a.status == AgentStatus.working ||
            a.status == AgentStatus.starting ||
            a.status == AgentStatus.idle;
        final isResumableStopped =
            a.status == AgentStatus.stopped &&
            a.hasHistory &&
            hasResumeCapability;
        if (!isActive && !isResumableStopped) {
          continue;
        }
        final key = managedVisibilityKey(a);
        final existing = byName[key];
        if (_preferManagedAgent(a, existing)) {
          byName[key] = a;
        }
      }
      final list = byName.values.toList();
      list.sort((a, b) {
        final at = managedAgentSortTitle(a);
        final bt = managedAgentSortTitle(b);
        final cmp = at.compareTo(bt);
        if (cmp != 0) return cmp;
        return a.id.compareTo(b.id);
      });
      return list;
    }

    String sigFromManaged(AgentModel a) {
      return sessionIdentityKey(
        provider: a.provider,
        sessionId: a.sessionId,
        pid: a.pid,
        agentId: a.id,
      );
    }

    String sigFromCandidate(SessionCandidate s) {
      return sessionIdentityKey(
        provider: s.provider,
        sessionId: s.sessionId,
        pid: s.pid,
      );
    }

    // Check if a session candidate matches a managed agent
    // Uses sessionId as primary key, PID as fallback
    bool managedContains(SessionCandidate s, List<AgentModel> managed) {
      final candidateSig = sigFromCandidate(s);
      final sigs = managed.map(sigFromManaged).toSet();
      return sigs.contains(candidateSig);
    }

    String sessionActionLabel(SessionCandidate s) {
      return (s.pid != null && s.pid! > 0) ? '附加' : '恢复';
    }

    String sessionStateText(SessionCandidate s) {
      if (s.pid == null || s.pid! <= 0) return '可恢复';
      if (s.attachMode == 'tmux') return '可交互';
      return '可附加';
    }

    String sessionConfirmTitle(SessionCandidate s) {
      return sessionActionLabel(s) == '恢复' ? '恢复会话' : '附加会话';
    }

    String sessionConfirmMessage(SessionCandidate s) {
      final target = '${s.provider} ${s.sessionId ?? 'pid:${s.pid ?? 0}'}';
      if (sessionActionLabel(s) == '恢复') {
        return '将从 $target 恢复为托管会话，是否继续？';
      }
      if (s.isReadOnly && s.readOnlyReason.trim().isNotEmpty) {
        return '将附加到 $target。\n\n注意：该会话当前只读。\n原因：${s.readOnlyReason.trim()}\n\n是否继续？';
      }
      if (s.attachMode == 'tmux') {
        return '将附加到 $target，并通过 tmux 转发输入，是否继续？';
      }
      return '将附加到 $target，是否继续？';
    }

    Future<bool> confirmAction(
      BuildContext ctx,
      String title,
      String content,
    ) async {
      final ok = await showDialog<bool>(
        context: ctx,
        builder: (_) => AlertDialog(
          title: Text(title),
          content: Text(content),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(ctx, false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(ctx, true),
              child: const Text('确认'),
            ),
          ],
        ),
      );
      return ok ?? false;
    }

    Future<List<SessionCandidate>> visibleSessions(
      List<SessionCandidate> all,
      List<AgentModel> managed,
    ) async {
      final unique = <String, SessionCandidate>{};
      for (final s in all) {
        unique[sigFromCandidate(s)] = s;
      }
      final list = unique.values
          .where((s) => (s.sessionId ?? '').isNotEmpty || ((s.pid ?? 0) > 0))
          .where((s) => !managedContains(s, managed))
          .toList();
      list.sort((a, b) {
        final at = _sessionCandidateSortTitle(a).toLowerCase();
        final bt = _sessionCandidateSortTitle(b).toLowerCase();
        final cmp = at.compareTo(bt);
        if (cmp != 0) return cmp;
        final sidCmp = (a.sessionId ?? '').compareTo(b.sessionId ?? '');
        if (sidCmp != 0) return sidCmp;
        return (a.pid ?? 0).compareTo(b.pid ?? 0);
      });
      return list;
    }

    Future<void> applyCatalog(
      Map<String, dynamic> map,
      StateSetter setState,
    ) async {
      final fetchedManaged = normalizeManaged(
        ((map['managed'] as List?) ?? const [])
            .whereType<Map>()
            .map(
              (e) => AgentModel.fromJson({
                ...Map<String, dynamic>.from(e),
                'nodeId': widget.node.id,
              }),
            )
            .toList(),
      );
      final fetchedSessions = await visibleSessions(
        parseSessionCandidates(map),
        fetchedManaged,
      );

      setState(() {
        managedAgents = fetchedManaged;
        sessions = fetchedSessions;
      });
    }

    Future<void> fetchCatalog(
      StateSetter setState,
      BuildContext ctx, {
      bool runAutoAttach = false,
    }) async {
      try {
        print('[SessionManager] fetchCatalog called for node=${widget.node.id}, runAutoAttach=$runAutoAttach');
        final result = await client.call('session.catalog', {
          'nodeId': widget.node.id,
        });
        print('[SessionManager] fetchCatalog result type=${result.runtimeType}');
        final map = result is Map
            ? Map<String, dynamic>.from(result)
            : <String, dynamic>{};

        final rawManaged = (map['managed'] as List?) ?? const [];
        print('[SessionManager] fetchCatalog raw managed count=${rawManaged.length}');
        final fetchedManaged = normalizeManaged(
          rawManaged
              .whereType<Map>()
              .map(
                (e) => AgentModel.fromJson({
                  ...Map<String, dynamic>.from(e),
                  'nodeId': widget.node.id,
                }),
              )
              .toList(),
        );
        print('[SessionManager] fetchCatalog normalized managed count=${fetchedManaged.length}');

        final fetchedSessions = await visibleSessions(
          parseSessionCandidates(map),
          fetchedManaged,
        );
        print('[SessionManager] fetchCatalog visible sessions count=${fetchedSessions.length}');

        if (!ctx.mounted) return;

        if (runAutoAttach && !autoAttachDone) {
          autoAttachDone = true;

          final candidate = pickPreferredAutoAttachCandidate(fetchedSessions);
          if (candidate != null) {
            final err = await _attachSessionCandidate(
              ref,
              candidate,
              refreshAgents: false,
            );
            if (err != null && ctx.mounted) {
              ScaffoldMessenger.of(
                ctx,
              ).showSnackBar(SnackBar(content: Text('自动附加失败：$err')));
            }

            final refreshResult = await client.call('session.catalog', {
              'nodeId': widget.node.id,
            });
            final refreshMap = refreshResult is Map
                ? Map<String, dynamic>.from(refreshResult)
                : <String, dynamic>{};

            if (!ctx.mounted) return;
            await applyCatalog(refreshMap, setState);
            return;
          }
        }

        if (!ctx.mounted) return;
        await applyCatalog(map, setState);
      } catch (e) {
        debugPrint('session manager load error: $e');
      }
    }

    try {
      print('[SessionManager] calling session.catalog for node=${widget.node.id}');
      final result = await client.call('session.catalog', {
        'nodeId': widget.node.id,
      });
      print('[SessionManager] result type=${result.runtimeType}');
      final map = result is Map
          ? Map<String, dynamic>.from(result)
          : <String, dynamic>{};
      final rawManaged = (map['managed'] as List?) ?? const [];
      print('[SessionManager] raw managed count=${rawManaged.length}');
      final parsed = rawManaged
          .whereType<Map>()
          .map(
            (e) => AgentModel.fromJson({
              ...Map<String, dynamic>.from(e),
              'nodeId': widget.node.id,
            }),
          )
          .toList();
      print('[SessionManager] parsed agent count=${parsed.length}');
      managedAgents = normalizeManaged(parsed);
      print('[SessionManager] normalized managed count=${managedAgents.length}');
      sessions = await visibleSessions(
        parseSessionCandidates(map),
        managedAgents,
      );
      print('[SessionManager] visible sessions count=${sessions.length}');
    } catch (e, st) {
      print('[SessionManager] init error: $e');
      print('[SessionManager] stack: $st');
    }

    if (!context.mounted) return;

    showDialog(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setState) {
          if (!autoAttachDone) {
            WidgetsBinding.instance.addPostFrameCallback((_) {
              if (!ctx.mounted || autoAttachDone) return;
              fetchCatalog(setState, ctx, runAutoAttach: true);
            });
          }
          return AlertDialog(
            title: Text(
              '${(widget.node.host.isNotEmpty && widget.node.host != 'localhost' && widget.node.host != '127.0.0.1') ? widget.node.host : widget.node.name} 会话管理',
            ),
            content: SizedBox(
              width: double.maxFinite,
              child:
                  (managedAgents.isEmpty &&
                      sessions.isEmpty)
                  ? const Padding(
                      padding: EdgeInsets.symmetric(vertical: 16),
                      child: Text('暂无会话', style: TextStyle(color: Colors.grey)),
                    )
                  : SingleChildScrollView(
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.start,
                        children: [
                          if (managedAgents.isNotEmpty) ...[
                            const Text(
                              '已管理会话',
                              style: TextStyle(fontWeight: FontWeight.w600),
                            ),
                            const SizedBox(height: 8),
                            ...managedAgents.map((a) {
                              return Dismissible(
                                key: ValueKey(a.id),
                                direction: DismissDirection.endToStart,
                                background: Container(
                                  alignment: Alignment.centerRight,
                                  padding: const EdgeInsets.only(right: 16),
                                  color: Colors.red,
                                  child: const Text(
                                    '取消管理',
                                    style: TextStyle(color: Colors.white),
                                  ),
                                ),
                                confirmDismiss: (_) async {
                                  try {
                                    await client.call('agent.remove', {'agentId': a.id});
                                    return true;
                                  } catch (e) {
                                    if (ctx.mounted) {
                                      ScaffoldMessenger.of(ctx).showSnackBar(
                                        SnackBar(content: Text('取消管理失败: $e')),
                                      );
                                    }
                                    return false;
                                  }
                                },
                                onDismissed: (_) {
                                  setState(() {
                                    managedAgents.removeWhere((m) => m.id == a.id);
                                  });
                                },
                                child: ListTile(
                                  dense: true,
                                  contentPadding: EdgeInsets.zero,
                                  leading: Icon(
                                    providerIcon(a.provider),
                                    size: 18,
                                    color: providerColor(a.provider),
                                  ),
                                  title: Text(
                                    _agentDisplayTitle(a),
                                    maxLines: 1,
                                    overflow: TextOverflow.ellipsis,
                                  ),
                                  subtitle: Text(
                                    _agentSubtitleText(a),
                                    style: Theme.of(context).textTheme.labelMedium?.copyWith(
                                      fontFamily: AppTextStyles.monoFontFamily,
                                      fontWeight: FontWeight.w600,
                                      color: AgentStatusTheme.getColor(a.status),
                                    ),
                                  ),
                                  trailing: const Text('进入'),
                                  onTap: () {
                                    Navigator.pop(ctx);
                                    context.push(
                                      '/agent/${widget.node.id}/${a.id}',
                                    );
                                  },
                                ),
                              );
                            }),
                          ],
                          if (managedAgents.isNotEmpty && sessions.isNotEmpty)
                            const Padding(
                              padding: EdgeInsets.symmetric(vertical: 8),
                              child: Divider(height: 1),
                            ),
                          if (sessions.isNotEmpty) ...[
                            const Text(
                              '可接管 / 可恢复会话',
                              style: TextStyle(fontWeight: FontWeight.w600),
                            ),
                            const SizedBox(height: 8),
                            ...sessions.map((s) {
                              final secondaryParts = [
                                if (s.sessionId != null &&
                                    s.sessionId!.isNotEmpty)
                                  shortSessionId(s.sessionId),
                                if (s.pid != null && s.pid! > 0) '${s.pid}',
                                if (s.terminal != null &&
                                    s.terminal!.isNotEmpty)
                                  s.terminal!,
                                sessionStateText(s),
                              ];
                              final secondary = secondaryParts.join(' · ');
                              final actionLabel = sessionActionLabel(s);
                              final titleText = _sessionCandidateSortTitle(s);

                              return ListTile(
                                dense: true,
                                contentPadding: EdgeInsets.zero,
                                leading: Icon(
                                  providerIcon(s.provider),
                                  size: 18,
                                  color: providerColor(s.provider),
                                ),
                                title: Text(titleText),
                                subtitle: secondary.isEmpty
                                    ? null
                                    : Text(secondary),
                                trailing: TextButton(
                                  onPressed: () async {
                                    final ok = await confirmAction(
                                      ctx,
                                      sessionConfirmTitle(s),
                                      sessionConfirmMessage(s),
                                    );
                                    if (!ok || !ctx.mounted) return;
                                    final err = await _attachSessionCandidate(
                                      ref,
                                      s,
                                    );
                                    if (!ctx.mounted) return;
                                    ScaffoldMessenger.of(ctx).showSnackBar(
                                      SnackBar(
                                        content: Text(
                                          err == null
                                              ? '$actionLabel成功'
                                              : '$actionLabel失败: $err',
                                        ),
                                      ),
                                    );
                                    await fetchCatalog(setState, ctx);
                                  },
                                  child: Text(actionLabel),
                                ),
                              );
                            }),
                          ],
                        ],
                      ),
                    ),
            ),
            actions: [
              TextButton(
                onPressed: () => fetchCatalog(setState, ctx),
                child: const Text('刷新'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('关闭'),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<String?> _attachSessionCandidate(
    WidgetRef ref,
    SessionCandidate candidate, {
    bool refreshAgents = true,
  }) async {
    final client = ref.read(connectionProvider);
    if (client == null) return 'not connected';

    final params = <String, dynamic>{
      'nodeId': widget.node.id,
      'provider': candidate.provider,
      'name': '${candidate.provider}-attached',
    };

    if (candidate.pid != null && candidate.pid! > 0) {
      params['pid'] = candidate.pid;
      if (candidate.workDir.isNotEmpty) {
        params['workDir'] = candidate.workDir;
      }
    } else if (candidate.sessionId != null && candidate.sessionId!.isNotEmpty) {
      params['sessionId'] = candidate.sessionId;
    }

    if (!params.containsKey('sessionId') && !params.containsKey('pid')) {
      return 'missing sessionId/pid';
    }

    try {
      await client.call('session.attach', params);
      if (refreshAgents) {
        await _refreshAgents(ref);
      }
      return null;
    } catch (e) {
      debugPrint('session.attach error: $e');
      return e.toString();
    }
  }
}

class AgentRow extends ConsumerStatefulWidget {
  final AgentModel agent;
  final String nodeId;
  final bool showPreview;
  final bool isLargeScreen;
  final bool showDetails;
  final bool canvasSelectionMode;
  final bool isInCanvas;
  final VoidCallback? onToggleCanvas;
  const AgentRow({
    super.key,
    required this.agent,
    required this.nodeId,
    this.showPreview = false,
    this.isLargeScreen = false,
    this.showDetails = false,
    this.canvasSelectionMode = false,
    this.isInCanvas = false,
    this.onToggleCanvas,
  });

  @override
  ConsumerState<AgentRow> createState() => _AgentRowState();
}

class _AgentRowState extends ConsumerState<AgentRow> {
  final GlobalKey _tileKey = GlobalKey();

  String get _statusLabel => AgentStatusTheme.getLabel(widget.agent.status);
  Color get _statusColor => AgentStatusTheme.getColor(widget.agent.status);

  AgentIndicatorStatus get _indicatorStatus {
    switch (widget.agent.status) {
      case AgentStatus.working:
        return AgentIndicatorStatus.running;
      case AgentStatus.starting:
        return AgentIndicatorStatus.thinking;
      case AgentStatus.crashed:
        return AgentIndicatorStatus.error;
      case AgentStatus.idle:
      case AgentStatus.stopped:
        return AgentIndicatorStatus.idle;
    }
  }

  Widget _buildUnreadBadge() {
    final unreadCount = ref.watch(unreadProvider)[(widget.nodeId, widget.agent.id)] ?? 0;
    if (unreadCount == 0) return const SizedBox.shrink();
    return Container(
      width: 10,
      height: 10,
      decoration: const BoxDecoration(
        color: Colors.red,
        shape: BoxShape.circle,
      ),
    );
  }

  Widget _buildStatusTime(BuildContext context) {
    final agent = widget.agent;
    final statusText =
        _statusLabel + (agent.status == AgentStatus.crashed ? '.异常' : '');
    final timeText = agent.lastMessageTime != null
        ? _formatRelativeTime(agent.lastMessageTime!)
        : null;
    return Row(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        AgentStatusIndicator(
          status: _indicatorStatus,
          size: 10,
          color: _statusColor,
        ),
        const SizedBox(width: 6),
        Text.rich(
          TextSpan(
            style: Theme.of(context).textTheme.labelMedium,
            children: [
              TextSpan(
                text: statusText,
                style: TextStyle(
                    fontWeight: FontWeight.w600, color: _statusColor),
              ),
              if (timeText != null)
                TextSpan(
                  text: '  $timeText',
                  style: TextStyle(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
                ),
            ],
          ),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
        ),
      ],
    );
  }

  Widget _buildTitleRow(BuildContext context, String displayTitle) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.center,
      children: [
        Expanded(
          child: Text(
            displayTitle,
            maxLines: 3,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontWeight: FontWeight.w600),
          ),
        ),
        const SizedBox(width: 8),
        _buildStatusTime(context),
      ],
    );
  }

  Future<void> _showAgentActions() async {
    final tileContext = _tileKey.currentContext;
    final overlay = Overlay.of(context).context.findRenderObject() as RenderBox;
    final tileBox = tileContext?.findRenderObject() as RenderBox?;
    if (tileBox == null) return;
    final tileRect = Rect.fromPoints(
      tileBox.localToGlobal(Offset.zero, ancestor: overlay),
      tileBox.localToGlobal(
        tileBox.size.bottomRight(Offset.zero),
        ancestor: overlay,
      ),
    );
    final agent = widget.agent;
    final items = <PopupMenuEntry<String>>[
      const PopupMenuItem<String>(
        value: 'rename',
        child: Row(
          children: [
            Icon(Icons.edit, size: 18),
            SizedBox(width: 8),
            Text('重命名'),
          ],
        ),
      ),
    ];

    final details = <PopupMenuEntry<String>>[];
    if ((agent.sessionStateReason ?? '').trim().isNotEmpty) {
      details.add(
        _detailMenuItem(Icons.info_outline, agent.sessionStateReason!.trim()),
      );
    }

    if (details.isNotEmpty) {
      items.add(const PopupMenuDivider());
      items.addAll(details);
    }

    final value = await showMenu<String>(
      context: context,
      position: RelativeRect.fromRect(
        Rect.fromLTWH(tileRect.left, tileRect.center.dy, tileRect.width, 1),
        Offset.zero & overlay.size,
      ),
      items: items,
    );
    if (!mounted || value == null) return;
    if (value == 'rename') _renameAgent();
  }

  Future<void> _renameAgent() async {
    final controller = TextEditingController(text: widget.agent.name);
    final newName = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('重命名 Agent'),
        content: TextField(
          controller: controller,
          autofocus: true,
          decoration: const InputDecoration(hintText: '输入新名称'),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, controller.text),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (newName == null || newName.isEmpty) return;
    final client = ref.read(connectionProvider);
    if (client == null) return;
    try {
      await client.call('agent.rename', {
        'nodeId': widget.nodeId,
        'agentId': widget.agent.id,
        'name': newName,
      });
      ref
          .read(nodesProvider.notifier)
          .renameAgent(widget.nodeId, widget.agent.id, newName);
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('已重命名为 $newName')));
      }
    } catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('重命名失败: $e')));
      }
    }
  }

  Future<void> _showLogoPicker(String sessionKey, IconData current) async {
    final picked = await showDialog<IconData>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('选择会话图标'),
        content: SizedBox(
          width: double.maxFinite,
          child: GridView.builder(
            shrinkWrap: true,
            gridDelegate: const SliverGridDelegateWithFixedCrossAxisCount(
              crossAxisCount: 6,
              mainAxisSpacing: 8,
              crossAxisSpacing: 8,
            ),
            itemCount: availableSessionLogos.length,
            itemBuilder: (context, index) {
              final icon = availableSessionLogos[index];
              final isSelected = icon == current;
              return InkWell(
                onTap: () => Navigator.pop(ctx, icon),
                borderRadius: BorderRadius.circular(8),
                child: Container(
                  decoration: BoxDecoration(
                    color: isSelected
                        ? Theme.of(context).colorScheme.primaryContainer
                        : null,
                    borderRadius: BorderRadius.circular(8),
                    border: isSelected
                        ? Border.all(
                            color: Theme.of(context).colorScheme.primary,
                            width: 2,
                          )
                        : null,
                  ),
                  child: Icon(
                    icon,
                    color: isSelected
                        ? Theme.of(context).colorScheme.primary
                        : Theme.of(context).colorScheme.onSurface,
                  ),
                ),
              );
            },
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () {
              ref.read(sessionLogoProvider.notifier).resetLogo(sessionKey);
              Navigator.pop(ctx);
            },
            child: const Text('恢复默认'),
          ),
        ],
      ),
    );
    if (picked != null) {
      ref.read(sessionLogoProvider.notifier).setLogo(sessionKey, picked);
    }
  }

  PopupMenuItem<String> _detailMenuItem(IconData icon, String text) {
    return PopupMenuItem<String>(
      enabled: false,
      child: Row(
        children: [
          Icon(
            icon,
            size: 16,
            color: Theme.of(context).colorScheme.onSurfaceVariant,
          ),
          const SizedBox(width: AppSpacing.sm),
          Expanded(
            child: Text(
              text,
              style: Theme.of(context).textTheme.labelMedium?.copyWith(
                    color: Theme.of(context).colorScheme.onSurfaceVariant,
                  ),
            ),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final agent = widget.agent;
    final displayTitle = _agentDisplayTitle(agent);
    final previewLines = widget.showPreview
        ? sessionPreviewLinesFromMessages(
            ref.watch(conversationProvider)[(widget.nodeId, agent.id, agent.sessionId ?? '')] ??
                const [],
          )
        : const <String>[];

    final sessionKey = '${widget.nodeId}:${sessionIdentityKey(
      provider: agent.provider,
      sessionId: agent.sessionId,
      pid: agent.pid,
      agentId: agent.id,
    )}';
    final sessionLogo = ref.watch(sessionLogoProvider.notifier).iconFor(sessionKey);

    final tile = ListTile(
      key: _tileKey,
      dense: true,
      minLeadingWidth: 0,
      contentPadding: const EdgeInsets.symmetric(horizontal: 16, vertical: 0),
      leading: widget.canvasSelectionMode
          ? InkWell(
              onTap: widget.onToggleCanvas,
              borderRadius: BorderRadius.circular(16),
              child: Padding(
                padding: const EdgeInsets.all(2),
                child: Icon(
                  widget.isInCanvas ? Icons.remove_circle : Icons.add_circle,
                  color: widget.isInCanvas
                      ? Theme.of(context).colorScheme.error
                      : Theme.of(context).colorScheme.primary,
                  size: 18,
                ),
              ),
            )
          : Stack(
              clipBehavior: Clip.none,
              children: [
                InkWell(
                  onTap: () {
                    final t = agent.lastMessageTime;
                    if (t != null) {
                      ScaffoldMessenger.of(context).showSnackBar(
                        SnackBar(
                          content: Text('最后消息：${_formatRelativeTime(t)}'),
                          duration: const Duration(seconds: 2),
                        ),
                      );
                    }
                  },
                  onLongPress: () => _showLogoPicker(sessionKey, sessionLogo),
                  borderRadius: BorderRadius.circular(16),
                  child: Padding(
                    padding: const EdgeInsets.all(2),
                    child: Icon(sessionLogo, color: providerColor(agent.provider), size: 18),
                  ),
                ),
                Positioned(
                  right: -2,
                  top: -2,
                  child: _buildUnreadBadge(),
                ),
              ],
            ),
      title: _buildTitleRow(context, displayTitle),
      subtitle: previewLines.isNotEmpty
          ? Padding(
              padding: const EdgeInsets.only(top: 4),
              child: _PreviewParagraphs(
                previewLines,
                color: Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            )
          : null,
      onTap: widget.canvasSelectionMode
          ? null
          : () => context.push('/agent/${widget.nodeId}/${agent.id}'),
      onLongPress: widget.canvasSelectionMode ? null : _showAgentActions,
    );

    if (widget.canvasSelectionMode) {
      return tile;
    }

    return Dismissible(
      key: ValueKey('${widget.nodeId}:${agent.id}'),
      direction: DismissDirection.startToEnd,
      confirmDismiss: (_) async {
        context.push('/agent/${widget.nodeId}/${agent.id}');
        return false;
      },
      background: Container(
        color: Theme.of(context).colorScheme.primaryContainer,
        alignment: Alignment.centerLeft,
        padding: const EdgeInsets.only(left: 24),
        child: Icon(
          Icons.arrow_forward,
          color: Theme.of(context).colorScheme.onPrimaryContainer,
        ),
      ),
      child: tile,
    );
  }
}

String _agentSubtitleText(AgentModel agent) {
  final parts = <String>[
    if ((agent.pid ?? 0) > 0) '${agent.pid}',
    if ((agent.sessionId ?? '').trim().isNotEmpty)
      shortSessionId(agent.sessionId),
    AgentStatusTheme.getLabel(agent.status),
  ];
  if (agent.status == AgentStatus.crashed) {
    parts.add('异常');
  }
  return parts.join('.');
}

String _agentDisplayTitle(AgentModel agent) {
  final name = agent.name.trim();
  if (name.isNotEmpty) return name;
  return _agentFallbackTitle(agent);
}

/// Returns a human-readable relative time string for a Unix-ms timestamp.
String _formatRelativeTime(int unixMs) {
  final now = DateTime.now().millisecondsSinceEpoch;
  final diffMs = now - unixMs;
  if (diffMs < 0) return '刚刚';
  final diffSec = diffMs ~/ 1000;
  if (diffSec < 60) return '${diffSec}秒前';
  final diffMin = diffSec ~/ 60;
  if (diffMin < 60) return '${diffMin}分钟前';
  final diffHour = diffMin ~/ 60;
  if (diffHour < 24) return '${diffHour}小时前';
  final diffDay = diffHour ~/ 24;
  return '${diffDay}天前';
}

String managedAgentTitle(AgentModel agent) {
  final name = agent.name.trim();
  if (name.isNotEmpty) return name;
  return _agentFallbackTitle(agent);
}

String managedAgentSortTitle(AgentModel agent) {
  return managedAgentTitle(agent).trim().toLowerCase();
}

String _agentFallbackTitle(AgentModel agent) {
  final name = agent.name.trim();
  if (name.isNotEmpty) {
    return name;
  }
  final sessionId = agent.sessionId?.trim() ?? '';
  if (sessionId.isNotEmpty) {
    return shortSessionId(sessionId);
  }
  if (agent.projectName?.isNotEmpty == true) {
    return agent.projectName!;
  }
  if (agent.workDir.isNotEmpty) {
    return _dirname(agent.workDir);
  }
  return agent.provider;
}

class _HealthIndicator extends ConsumerWidget {
  const _HealthIndicator();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final health = ref.watch(healthProvider);

    final status = health?.status ?? HealthStatus.unknown;
    final color = switch (status) {
      HealthStatus.healthy => Colors.green,
      HealthStatus.degraded => Colors.orange,
      HealthStatus.down => Colors.red,
      HealthStatus.unknown => Colors.grey,
    };

    final label = switch (status) {
      HealthStatus.healthy => 'Healthy',
      HealthStatus.degraded => 'Degraded',
      HealthStatus.down => 'Down',
      HealthStatus.unknown => 'Checking...',
    };

    final nodeDetails = <String>[];
    if (health?.nodes.isNotEmpty ?? false) {
      for (final entry in health!.nodes.entries) {
        final n = entry.value;
        final latencyStr = n.latencyMs > 0 ? '${n.latencyMs}ms' : '';
        final errStr = n.error != null ? ' (${n.error})' : '';
        nodeDetails.add('${entry.key}: ${n.status}$latencyStr$errStr');
      }
    }

    final tooltip = nodeDetails.isNotEmpty
        ? '$label\n${nodeDetails.join('\n')}'
        : label;

    return Padding(
      padding: const EdgeInsets.only(right: 8),
      child: Tooltip(
        message: tooltip,
        child: Icon(Icons.circle, color: color, size: 14, key: const Key('healthIndicator')),
      ),
    );
  }
}
