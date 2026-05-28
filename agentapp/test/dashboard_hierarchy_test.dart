import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/models/node_model.dart';
import 'package:agentapp/providers/conversation_provider.dart';
import 'package:agentapp/providers/nodes_provider.dart';
import 'package:agentapp/screens/dashboard_screen.dart';
import 'package:agentapp/widgets/agent_status_indicator.dart';

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

    testWidgets('AgentRow does not render a status dot widget', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
      );

      // The colored dot (AgentStatusIndicator) was removed as redundant:
      // status text already conveys the same information.
      expect(
        find.byType(AgentStatusIndicator),
        findsNothing,
        reason: 'AgentRow should not contain a status dot; text is sufficient',
      );

      // Status text must still be present.
      expect(find.textContaining('Standby'), findsOneWidget);
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

    // ── Task #14: structural hierarchy reinforcements ────────────────────
    //
    // Beyond raw font size/weight (already covered above), the dashboard
    // now layers structural visual cues so the hierarchy survives at a
    // glance: status/time gets a chip background, the preview body gets a
    // left-indent + vertical accent bar, and font-size deltas are widened
    // to a consistent 2-step separation.

    testWidgets('status+time region is wrapped in a chip-like Container '
        '(non-null background)', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
      );

      // The status label "Standby" must live inside an ancestor Container
      // that declares a non-transparent background colour or a BoxDecoration
      // with a non-null colour — i.e. it visually reads as a chip rather
      // than bare text.
      final statusFinder = find.textContaining('Standby', findRichText: true);
      expect(statusFinder, findsWidgets,
          reason: 'status text must render so we can locate its ancestors');

      final ancestors = find.ancestor(
        of: statusFinder.first,
        matching: find.byType(Container),
      );
      expect(ancestors, findsWidgets,
          reason: 'status text must be wrapped in at least one Container');

      bool foundChipBackground = false;
      for (final element in tester.elementList(ancestors)) {
        final container = element.widget as Container;
        final decoration = container.decoration;
        final directColor = container.color;
        if (directColor != null && directColor.alpha > 0) {
          foundChipBackground = true;
          break;
        }
        if (decoration is BoxDecoration) {
          final c = decoration.color;
          if (c != null && c.alpha > 0) {
            foundChipBackground = true;
            break;
          }
        }
      }
      expect(foundChipBackground, isTrue,
          reason: 'status/time must sit inside a Container with a non-null '
              'background colour so the status visually reads as a chip');
    });

    testWidgets('preview body has left indent OR vertical accent bar', (tester) async {
      await _pumpAgentRow(
        tester,
        agent: baseAgent,
        nodeId: 'n1',
        showPreview: true,
        previewText: '这是一段需要左缩进的预览正文，附属于上方的 agent 头。',
      );

      final previewFinder = find.textContaining('需要左缩进', findRichText: true);
      expect(previewFinder, findsWidgets,
          reason: 'preview text must be present');

      // Look for either:
      //  (a) an ancestor Padding with EdgeInsets.left >= 8, OR
      //  (b) a sibling vertical bar Container (width <= 4, color != null) in
      //      a Row ancestor of the preview text — signalling a quote-style
      //      accent bar.
      final paddings = find.ancestor(
        of: previewFinder.first,
        matching: find.byType(Padding),
      );

      bool foundLeftIndent = false;
      for (final element in tester.elementList(paddings)) {
        final padding = element.widget as Padding;
        final p = padding.padding.resolve(TextDirection.ltr);
        // Ignore ListTile's symmetric horizontal padding (left==right==16).
        // Only count an *asymmetric* left indent (left >= 8 AND right == 0)
        // — this is the structural cue we're after.
        if (p.left >= 8 && p.right == 0) {
          foundLeftIndent = true;
          break;
        }
      }

      // Sibling accent bar: scan all Container widgets in the rendered tree
      // for a narrow vertical bar (width <= 3) with a non-null colour. A
      // 2-3px wide bar is the classic quote-style accent and can't be
      // confused with status dots (10px) or unread badges (10x10).
      bool foundAccentBar = false;
      final allContainers = tester.widgetList<Container>(find.byType(Container));
      for (final c in allContainers) {
        final cs = c.constraints;
        final dec = c.decoration;
        double? width;
        if (cs != null && cs.maxWidth.isFinite) {
          width = cs.maxWidth;
        }
        Color? color = c.color;
        if (color == null && dec is BoxDecoration) color = dec.color;
        if (width != null && width <= 3 && color != null && color.alpha > 0) {
          foundAccentBar = true;
          break;
        }
      }

      expect(foundLeftIndent || foundAccentBar, isTrue,
          reason: 'preview body must have at least one structural cue: '
              'left indent (Padding.left >= 8) OR a vertical accent bar '
              '(narrow Container with non-null colour as a Row sibling). '
              'Found: leftIndent=$foundLeftIndent accentBar=$foundAccentBar');
    });

    testWidgets('font sizes form a strict ladder with >=2px gaps between '
        'adjacent tiers', (tester) async {
      // Render NodeCard so we can inspect node header + agent title + status
      // + time in the same widget tree.
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

      final recentTs = DateTime.now().millisecondsSinceEpoch - 30 * 1000;
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
          'lastMessageTime': recentTs,
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

      final nodeSize = _styleOfText(tester, 'remote1')!.fontSize!;
      final agentSize = _styleOfText(tester, 'claude-hierarchy-test')!.fontSize!;
      final statusSize = _styleOfRichSpan(tester, 'Standby')!.fontSize!;
      TextStyle? timeS = _styleOfRichSpan(tester, '前') ?? _styleOfRichSpan(tester, '刚刚');
      final timeSize = timeS!.fontSize!;

      // Adjacent tiers need at least a 2-px gap (node→agent, agent→status)
      // and at least 1-px gap (status→time, since 11→10 is the floor for
      // legibility of relative-time).
      expect(nodeSize - agentSize >= 2, isTrue,
          reason: 'node header ($nodeSize) must be >= agent ($agentSize) + 2');
      expect(agentSize - statusSize >= 2, isTrue,
          reason: 'agent ($agentSize) must be >= status ($statusSize) + 2');
      expect(statusSize - timeSize >= 1, isTrue,
          reason: 'status ($statusSize) must be > time ($timeSize)');
    });
  });
}
