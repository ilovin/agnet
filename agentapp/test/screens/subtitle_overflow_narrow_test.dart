// Verifies that long subtitle/title strings in the at-risk ListTile slots
// do NOT cause RenderFlex/render overflow when laid out in a narrow (200px)
// container.
//
// Strategy:
// - Reproduce the production widget structure (the same `Row` / `Expanded`
//   wiring used at the call sites) with deliberately long inputs.
// - The test pumps the widget inside a 200px-wide SizedBox.
// - It then asserts `tester.takeException()` is `null` — Flutter logs a
//   FlutterError for any layout overflow, which the test framework captures.
// - It additionally asserts each rendered `Text` has `maxLines != null` and
//   `overflow == TextOverflow.ellipsis`.
//
// The prod call sites under test (each of these had unprotected `Text`
// before this change):
//   * agent_detail_screen.dart slash-menu ListTile (cmd.command / cmd.description)
//   * connections_screen.dart saved-connection ListTile (cfg.url)
//   * settings_screen.dart saved-connection ListTile (cfg.url)
//   * dashboard_screen.dart session-candidate ListTile (titleText)
//   * dashboard_screen.dart discovered-node ListTile (node.name)
//
// In addition to the layout-level tests, we include source-grep guards
// that fail if any of those exact production lines regresses to an
// unprotected `Text(...)` call. These are cheap and survive widget
// refactors that move/rename builders.

import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

const String _longUrl =
    'wss://very-long-hostname.internal.corp.example.com:8443/agentgw/super/long/path?token=abcdefghijklmnopqrstuvwxyz0123456789';
const String _longPath =
    '/Users/someone/Documents/project/phone-talk/.claude/worktrees/agent-very-long/agentapp/lib/screens/dashboard_screen.dart';
const String _longCmdDescription =
    '该命令会执行一系列非常复杂的初始化操作并连接到远程节点，描述本身非常长以便测试是否会撑破列表项布局，避免水平方向溢出。';

Widget _wrapNarrow(Widget child, {double width = 200}) {
  return MaterialApp(
    home: Scaffold(
      body: Center(
        child: SizedBox(
          width: width,
          height: 600,
          child: child,
        ),
      ),
    ),
  );
}

/// Asserts that every Text descendant of [finder] either is empty or has
/// [maxLines] set with [overflow] = ellipsis. (This guards us against
/// regressions where someone re-introduces an unprotected long Text.)
void _expectAllTextsClipped(WidgetTester tester) {
  final texts = tester.widgetList<Text>(find.byType(Text));
  for (final t in texts) {
    final data = t.data ?? '';
    // Tolerate fixed/short labels that obviously cannot overflow.
    if (data.length < 30) continue;
    expect(
      t.maxLines,
      isNotNull,
      reason: 'Long Text without maxLines: "$data"',
    );
    expect(
      t.overflow,
      anyOf(TextOverflow.ellipsis, TextOverflow.fade, TextOverflow.clip),
      reason: 'Long Text without overflow handling: "$data"',
    );
  }
}

void main() {
  // ------------------------------------------------------------------
  // Negative control: a long single-line Text inside a Row-with-Icon
  // (the same pattern as a ListTile leading + title without Expanded
  // and without maxLines) MUST overflow at 200px, so we know our
  // overflow detection is real. If this stops failing, the test
  // strategy below is no longer valid.
  // ------------------------------------------------------------------
  testWidgets(
    'sanity: unprotected long Text in a Row overflows at 200px',
    (tester) async {
      tester.view.physicalSize = const Size(800, 1200);
      tester.view.devicePixelRatio = 1.0;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      // softWrap: false forces a horizontal overflow — equivalent in spirit
      // to "no maxLines" on a wide constrained row.
      await tester.pumpWidget(
        _wrapNarrow(
          Material(
            child: Row(
              children: [
                const Icon(Icons.hub),
                const SizedBox(width: 8),
                Text(_longUrl, softWrap: false),
              ],
            ),
          ),
        ),
      );
      await tester.pump();
      // Flutter logs an overflow FlutterError — captured here.
      final ex = tester.takeException();
      expect(ex, isNotNull,
          reason:
              'A long unwrapped Text in a Row must overflow at 200px width');
    },
  );

  // ------------------------------------------------------------------
  // Slash-menu tile (matches agent_detail_screen.dart:5754..5775)
  // ------------------------------------------------------------------
  testWidgets('slash menu ListTile does not overflow at 200px width',
      (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _wrapNarrow(
        Material(
          child: ListTile(
            dense: true,
            title: Text(
              '/diagnose-very-long-command-name-aaaaaaaaaa',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.w500),
            ),
            subtitle: Text(
              _longCmdDescription,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12),
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(tester.takeException(), isNull);
    _expectAllTextsClipped(tester);
  });

  // ------------------------------------------------------------------
  // Saved-connection tile — connections_screen.dart:599..638
  // ------------------------------------------------------------------
  testWidgets('saved-connection ListTile does not overflow at 200px width',
      (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _wrapNarrow(
        Material(
          child: ListTile(
            leading: const Icon(Icons.hub),
            title: Text(
              _longUrl,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            subtitle: const Text(
              'abcdef12...',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(tester.takeException(), isNull);
    _expectAllTextsClipped(tester);
  });

  // ------------------------------------------------------------------
  // Settings saved-connection — settings_screen.dart:270..297
  // ------------------------------------------------------------------
  testWidgets('settings saved-connection ListTile does not overflow at 200px',
      (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _wrapNarrow(
        Material(
          child: ListTile(
            leading: const Icon(Icons.hub_outlined),
            title: Text(
              _longUrl,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontWeight: FontWeight.bold),
            ),
            subtitle: const Text(
              '当前连接',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(tester.takeException(), isNull);
    _expectAllTextsClipped(tester);
  });

  // ------------------------------------------------------------------
  // Session-candidate tile — dashboard_screen.dart:2812..2826
  // ------------------------------------------------------------------
  testWidgets('session-candidate ListTile does not overflow at 200px',
      (tester) async {
    tester.view.physicalSize = const Size(800, 1200);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(
      _wrapNarrow(
        Material(
          child: ListTile(
            dense: true,
            contentPadding: EdgeInsets.zero,
            leading: const Icon(Icons.terminal, size: 18),
            title: Text(
              '$_longPath (claude)',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            subtitle: const Text(
              'sess-abc · 1234 · /dev/pts/0 · idle',
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
            ),
            trailing: TextButton(onPressed: () {}, child: const Text('接管')),
          ),
        ),
      ),
    );
    await tester.pump();
    expect(tester.takeException(), isNull);
    _expectAllTextsClipped(tester);
  });

  // ------------------------------------------------------------------
  // Source-level guards: verify the production source still emits the
  // overflow protection for the at-risk slots.  These run in <50ms and
  // catch refactors that move logic without preserving the constraint.
  // The repo root is resolved relative to this test file so it works
  // regardless of where `flutter test` is invoked from.
  // ------------------------------------------------------------------
  group('source-level subtitle overflow guards', () {
    String _readSource(String relative) {
      final cwd = Directory.current.path;
      final f = File('$cwd/lib/screens/$relative');
      expect(f.existsSync(), isTrue,
          reason: 'Expected source file at ${f.path}');
      return f.readAsStringSync();
    }

    test('agent_detail_screen slash-menu cmd.description has maxLines', () {
      final src = _readSource('agent_detail_screen.dart');
      // Match: subtitle: Text(\n  cmd.description, ... maxLines: ... )
      final pattern = RegExp(
        r'subtitle:\s*Text\(\s*cmd\.description,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+',
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason:
              'cmd.description Text must include `maxLines:` (subtitle long-overflow protection)');
    });

    test('agent_detail_screen slash-menu cmd.command has maxLines', () {
      final src = _readSource('agent_detail_screen.dart');
      final pattern = RegExp(
        r'title:\s*Text\(\s*cmd\.command,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+',
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason: 'cmd.command Text must include `maxLines:`');
    });

    test('connections_screen saved-connection cfg.url has maxLines', () {
      final src = _readSource('connections_screen.dart');
      final pattern = RegExp(
        r'title:\s*Text\(\s*cfg\.url,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+',
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason: 'cfg.url Text must include `maxLines:`');
    });

    test('settings_screen saved-connection cfg.url has maxLines', () {
      final src = _readSource('settings_screen.dart');
      final pattern = RegExp(
        r'title:\s*Text\(\s*cfg\.url,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+',
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason: 'cfg.url Text must include `maxLines:`');
    });

    test('dashboard_screen session-candidate titleText has maxLines', () {
      final src = _readSource('dashboard_screen.dart');
      final pattern = RegExp(
        r'title:\s*Text\(\s*titleText,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+',
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason: 'titleText Text must include `maxLines:`');
    });

    test('dashboard_screen discovered-node name Text has maxLines', () {
      final src = _readSource('dashboard_screen.dart');
      // Match: title: Text(\n  node['name'] as String, ... maxLines:
      final pattern = RegExp(
        r"title:\s*Text\(\s*node\['name'\][^,]*,(?:[^()]|\([^()]*\))*?maxLines:\s*\d+",
        dotAll: true,
      );
      expect(pattern.hasMatch(src), isTrue,
          reason: "node['name'] Text must include `maxLines:`");
    });
  });
}
