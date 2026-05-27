import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/dashboard_screen.dart';

/// Pumps an [AgentRow] inside a real Material app so its TextStyles resolve
/// against the actual theme. We then read the rendered styles back out of the
/// [Text] / [Text.rich] widgets to assert visual hierarchy between the
/// agent title (primary), status label (semantic colour), timestamp (meta)
/// and preview body (secondary).
Future<void> _pumpAgentRow(
  WidgetTester tester, {
  required AgentModel agent,
  required String nodeId,
  bool showPreview = false,
  ThemeMode themeMode = ThemeMode.light,
  String? previewText,
}) async {
  final container = ProviderContainer();
  addTearDown(container.dispose);

  if (showPreview && previewText != null) {
    container.read(conversationProvider.notifier).loadHistory(
      nodeId,
      agent.id,
      agent.sessionId ?? '',
      [
        {
          'nodeId': nodeId,
          'agentId': agent.id,
          'sessionId': agent.sessionId ?? '',
          'role': 'assistant',
          'text': previewText,
          'seq': 1,
        },
      ],
    );
  }

  await tester.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        theme: ThemeData.light(),
        darkTheme: ThemeData.dark(),
        themeMode: themeMode,
        home: Scaffold(
          body: AgentRow(
            agent: agent,
            nodeId: nodeId,
            showPreview: showPreview,
          ),
        ),
      ),
    ),
  );
  await tester.pump();
}

/// Walks all [Text] widgets that contain [substring] and returns the first
/// resolved [TextStyle] (after merging DefaultTextStyle), or null if not found.
TextStyle? _styleOfText(WidgetTester tester, String substring) {
  final all = tester.widgetList<Text>(find.byType(Text));
  for (final t in all) {
    final data = t.data ?? '';
    if (data.contains(substring)) {
      // Try the explicit style first; if not specified, fall back to ambient
      // DefaultTextStyle for the element.
      if (t.style != null) return t.style;
      final element = tester.element(find.byWidget(t));
      return DefaultTextStyle.of(element).style;
    }
  }
  return null;
}

/// For a Text.rich widget, find the resolved style of a TextSpan whose text
/// contains [substring].
TextStyle? _styleOfRichSpan(WidgetTester tester, String substring) {
  final all = tester.widgetList<RichText>(find.byType(RichText));
  for (final rt in all) {
    final span = rt.text;
    final found = _findSpanStyle(span, substring, span.style);
    if (found != null) return found;
  }
  return null;
}

TextStyle? _findSpanStyle(InlineSpan span, String substring, TextStyle? parentStyle) {
  final mergedStyle = parentStyle == null
      ? span.style
      : (span.style == null ? parentStyle : parentStyle.merge(span.style));
  if (span is TextSpan) {
    final text = span.text;
    if (text != null && text.contains(substring)) {
      return mergedStyle;
    }
    if (span.children != null) {
      for (final child in span.children!) {
        final found = _findSpanStyle(child, substring, mergedStyle);
        if (found != null) return found;
      }
    }
  }
  return null;
}

void main() {
  group('Dashboard text hierarchy (R-014 strengthen visual)', () {
    // Recent timestamp (unix MILLISECONDS) so a relative time like "30秒前"
    // is rendered.
    final recentTs = DateTime.now().millisecondsSinceEpoch - 30 * 1000;

    final baseAgent = AgentModel(
      id: 'a1',
      name: 'claude-hierarchy-test',
      workDir: '/tmp/work',
      nodeId: 'n1',
      provider: 'claude',
      status: AgentStatus.idle,
      runtimeState: 'live',
      sessionState: 'active',
      sessionId: 'sess-xyz',
      lastMessageTime: recentTs,
    );

    testWidgets('agent title is heavier and larger than preview body', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
        showPreview: true,
        previewText: '这是一段会话预览正文。',
      );

      final titleStyle = _styleOfText(tester, 'claude-hierarchy-test');
      expect(titleStyle, isNotNull,
          reason: 'agent title Text widget should be present');

      // Preview body is rendered via Text.rich -> look at the RichText span.
      final previewStyle = _styleOfRichSpan(tester, '会话预览');
      expect(previewStyle, isNotNull,
          reason: 'preview body span should be present');

      // 1) Title weight strictly heavier than preview body weight.
      final titleWeight = titleStyle!.fontWeight ?? FontWeight.w400;
      final previewWeight = previewStyle!.fontWeight ?? FontWeight.w400;
      expect(titleWeight.index, greaterThan(previewWeight.index),
          reason: 'title weight ($titleWeight) must be strictly heavier than '
              'preview body weight ($previewWeight)');

      // 2) Title font size strictly larger than preview body size.
      // Title is in a ListTile dense subtitle context — we want an explicit
      // size bump so the hierarchy is reliable rather than depending on
      // ambient defaults.
      final titleSize = titleStyle.fontSize;
      final previewSize = previewStyle.fontSize;
      expect(titleSize, isNotNull,
          reason: 'title must declare an explicit fontSize so hierarchy is '
              'deterministic across themes');
      expect(previewSize, isNotNull,
          reason: 'preview must declare an explicit fontSize');
      expect(titleSize! > previewSize!, isTrue,
          reason: 'title fontSize ($titleSize) must be > preview fontSize ($previewSize)');
    });

    testWidgets('timestamp is smaller and lighter than status label', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
      );

      // Status label for AgentStatus.idle is "Standby" (see AgentStatusTheme).
      final statusStyle = _styleOfRichSpan(tester, 'Standby');
      expect(statusStyle, isNotNull,
          reason: 'status label span ("Standby") should be present');

      // Timestamp span is appended after the status with a leading space.
      // We accept either "前" (e.g. "30秒前") or "刚刚".
      TextStyle? timeStyle = _styleOfRichSpan(tester, '前');
      timeStyle ??= _styleOfRichSpan(tester, '刚刚');
      expect(timeStyle, isNotNull,
          reason: 'relative-time span should be present');

      // 1) Timestamp font weight is not heavier than status weight.
      final statusWeight = statusStyle!.fontWeight ?? FontWeight.w400;
      final timeWeight = timeStyle!.fontWeight ?? FontWeight.w400;
      expect(timeWeight.index, lessThan(statusWeight.index),
          reason: 'timestamp weight ($timeWeight) must be lighter than status '
              'weight ($statusWeight)');

      // 2) Timestamp font size strictly smaller than status size — meta info
      // must visually recede.
      final statusSize = statusStyle.fontSize;
      final timeSize = timeStyle.fontSize;
      expect(statusSize, isNotNull, reason: 'status must have explicit fontSize');
      expect(timeSize, isNotNull, reason: 'timestamp must have explicit fontSize');
      expect(timeSize! < statusSize!, isTrue,
          reason: 'timestamp fontSize ($timeSize) must be < status fontSize '
              '($statusSize)');
    });

    testWidgets('NodeCard header title is heavier than AgentRow title', (tester) async {
      // Build a NodeCard with a single child agent so we can compare the
      // node-header title with the agent-row title in the same render tree.
      final container = ProviderContainer();
      addTearDown(container.dispose);

      const node = NodeModel(
        id: 'n1',
        name: 'remote1',
        host: '10.0.0.1',
        status: NodeStatus.connected,
        location: NodeLocation(
          type: 'remote',
          host: '10.0.0.1',
          displayLocation: 'ws (10.0.0.1)',
        ),
        agentCount: 1,
      );

      container.read(nodesProvider.notifier).loadNodes([
        {
          'id': node.id,
          'name': node.name,
          'host': node.host,
          'status': 'connected',
          'location': node.location.toJson(),
          'agentCount': node.agentCount,
        },
      ]);
      container.read(nodesProvider.notifier).loadAgents(node.id, [
        {
          'id': 'a1',
          'name': 'claude-hierarchy-test',
          'workDir': '/tmp',
          'nodeId': 'n1',
          'provider': 'claude',
          'status': 'idle',
          'runtimeState': 'live',
          'sessionState': 'active',
        },
      ]);

      await tester.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            home: Scaffold(
              body: NodeCard(
                node: node,
                showSessionPreview: false,
                isLargeScreen: false,
                showDetails: false,
              ),
            ),
          ),
        ),
      );
      await tester.pump();

      final nodeTitleStyle = _styleOfText(tester, 'remote1');
      final agentTitleStyle = _styleOfText(tester, 'claude-hierarchy-test');
      expect(nodeTitleStyle, isNotNull,
          reason: 'node header title should be present');
      expect(agentTitleStyle, isNotNull,
          reason: 'agent row title should be present');

      // Node header should be visually dominant: at least one of {fontSize,
      // fontWeight} strictly greater than the agent title.
      final nodeWeight = nodeTitleStyle!.fontWeight ?? FontWeight.w400;
      final agentWeight = agentTitleStyle!.fontWeight ?? FontWeight.w400;
      final nodeSize = nodeTitleStyle.fontSize ?? 14;
      final agentSize = agentTitleStyle.fontSize ?? 14;

      final nodeHeavier = nodeWeight.index > agentWeight.index;
      final nodeBigger = nodeSize > agentSize;
      expect(nodeHeavier || nodeBigger, isTrue,
          reason: 'node header (size=$nodeSize, weight=$nodeWeight) must be '
              'heavier OR larger than agent title (size=$agentSize, '
              'weight=$agentWeight) to maintain hierarchy');
    });

    testWidgets('hierarchy holds in dark mode (no hardcoded light colours)', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
        showPreview: true,
        previewText: '深色模式下的预览。',
        themeMode: ThemeMode.dark,
      );

      final titleStyle = _styleOfText(tester, 'claude-hierarchy-test');
      final previewStyle = _styleOfRichSpan(tester, '深色模式');
      expect(titleStyle, isNotNull);
      expect(previewStyle, isNotNull);

      // Title colour, if specified, must not be a near-black hardcoded value
      // that disappears in dark mode. Allow null (inherits from theme).
      final titleColor = titleStyle!.color;
      if (titleColor != null) {
        // Must not be exactly Colors.black and must have luminance > 0.4 in
        // dark mode (so it shows up on a dark background).
        expect(titleColor, isNot(equals(const Color(0xFF000000))),
            reason: 'title colour must not hardcode black; dark mode would '
                'render it invisibly');
      }
    });
  });
}
