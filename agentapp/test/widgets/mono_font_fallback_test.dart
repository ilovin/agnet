// Audit that every widget using `fontFamily: 'monospace'` or
// `fontFamily: AppTextStyles.monoFontFamily` also declares
// `fontFamilyFallback: AppTextStyles.fontFamilyFallback`.
//
// Background: CanvasKit sometimes skips the fallback chain when the
// primary font (e.g. Noto Sans SC) falsely claims coverage for arrow
// codepoints (U+2190..U+2193) in its OS/2 table. If a TextStyle sets
// `fontFamily` without `fontFamilyFallback`, the glyph renders as tofu
// (□). This test guards every monospace text surface in the app.

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/claude_interaction_models.dart';
import 'package:agentapp/theme/app_text_styles.dart';
import 'package:agentapp/widgets/exit_plan_mode_card.dart';
import 'package:agentapp/widgets/permission_request_card.dart';
import 'package:agentapp/widgets/app_bar/bypass_indicator.dart';

/// Collect every [TextStyle] found on [Text] / [SelectableText] widgets
/// inside the widget tree rooted at [finder].
List<TextStyle> _collectTextStyles(WidgetTester tester, Finder finder) {
  final out = <TextStyle>[];
  final elements = finder.evaluate();
  for (final root in elements) {
    root.visitChildren((child) {
      // Walk the subtree
      void walk(Element e) {
        final widget = e.widget;
        if (widget is Text && widget.style != null) {
          out.add(widget.style!);
        } else if (widget is SelectableText && widget.style != null) {
          out.add(widget.style!);
        }
        e.visitChildren(walk);
      }

      walk(child);
    });
  }
  return out;
}

void main() {
  group('ExitPlanModeCard monospace fontFamilyFallback', () {
    testWidgets('plan SelectableText carries full fontFamilyFallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: ExitPlanModeCard(
              payload: ExitPlanModePayload(
                toolUseId: 'tu-1',
                plan: 'step 1 → do this\nstep 2 ← done',
              ),
            ),
          ),
        ),
      );

      final styles = _collectTextStyles(tester, find.byType(ExitPlanModeCard));
      final monoStyles = styles.where((s) =>
          s.fontFamily == 'monospace' || s.fontFamily == AppTextStyles.monoFontFamily);

      expect(monoStyles, isNotEmpty,
          reason: 'ExitPlanModeCard must render at least one monospace text');

      for (final s in monoStyles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'monospace TextStyle must declare fontFamilyFallback so arrows do not tofu');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'monospace TextStyle fallback must reach Noto Sans Symbols 2');
      }
    });
  });

  group('PermissionRequestCard monospace fontFamilyFallback', () {
    testWidgets('bash command block carries full fontFamilyFallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PermissionRequestCard(
              permissionRequest: {
                'tool_name': 'bash',
                'request_id': 'req-1',
                'input': {
                  'command': 'git log --oneline → output.txt',
                },
              },
            ),
          ),
        ),
      );

      final styles =
          _collectTextStyles(tester, find.byType(PermissionRequestCard));
      final monoStyles = styles.where((s) =>
          s.fontFamily == 'monospace' || s.fontFamily == AppTextStyles.monoFontFamily);

      expect(monoStyles, isNotEmpty,
          reason:
              'PermissionRequestCard bash mode must render command in monospace');

      for (final s in monoStyles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'monospace TextStyle must declare fontFamilyFallback so arrows do not tofu');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'monospace TextStyle fallback must reach Noto Sans Symbols 2');
      }
    });

    testWidgets('edit file preview carries full fontFamilyFallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PermissionRequestCard(
              permissionRequest: {
                'tool_name': 'edit_file',
                'request_id': 'req-2',
                'input': {
                  'file_path': '/tmp/test.txt',
                  'old_string': 'foo → bar',
                  'new_string': 'baz ← qux',
                },
              },
            ),
          ),
        ),
      );

      final styles =
          _collectTextStyles(tester, find.byType(PermissionRequestCard));
      final monoStyles = styles.where((s) =>
          s.fontFamily == 'monospace' || s.fontFamily == AppTextStyles.monoFontFamily);

      expect(monoStyles, isNotEmpty,
          reason:
              'PermissionRequestCard edit mode must render diff in monospace');

      for (final s in monoStyles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'monospace TextStyle must declare fontFamilyFallback so arrows do not tofu');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'monospace TextStyle fallback must reach Noto Sans Symbols 2');
      }
    });

    testWidgets('write file preview carries full fontFamilyFallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PermissionRequestCard(
              permissionRequest: {
                'tool_name': 'write_file',
                'request_id': 'req-3',
                'input': {
                  'file_path': '/tmp/test.txt',
                  'content': 'line 1 → ok\nline 2 ← done',
                },
              },
            ),
          ),
        ),
      );

      final styles =
          _collectTextStyles(tester, find.byType(PermissionRequestCard));
      final monoStyles = styles.where((s) =>
          s.fontFamily == 'monospace' || s.fontFamily == AppTextStyles.monoFontFamily);

      expect(monoStyles, isNotEmpty,
          reason:
              'PermissionRequestCard write mode must render content in monospace');

      for (final s in monoStyles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'monospace TextStyle must declare fontFamilyFallback so arrows do not tofu');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'monospace TextStyle fallback must reach Noto Sans Symbols 2');
      }
    });

    testWidgets('fallback tool input carries full fontFamilyFallback',
        (tester) async {
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: PermissionRequestCard(
              permissionRequest: {
                'tool_name': 'unknown_tool',
                'request_id': 'req-4',
                'input': {
                  'key': 'value → result',
                },
              },
            ),
          ),
        ),
      );

      final styles =
          _collectTextStyles(tester, find.byType(PermissionRequestCard));
      final monoStyles = styles.where((s) =>
          s.fontFamily == 'monospace' || s.fontFamily == AppTextStyles.monoFontFamily);

      expect(monoStyles, isNotEmpty,
          reason:
              'PermissionRequestCard fallback mode must render input in monospace');

      for (final s in monoStyles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'monospace TextStyle must declare fontFamilyFallback so arrows do not tofu');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'monospace TextStyle fallback must reach Noto Sans Symbols 2');
      }
    });
  });

  group('BypassIndicator fontFamilyFallback', () {
    testWidgets('chip text carries full fontFamilyFallback including Symbols 2',
        (tester) async {
      await tester.pumpWidget(
        const MaterialApp(
          home: Scaffold(
            body: BypassIndicator(modeLabel: 'Bypass'),
          ),
        ),
      );

      final styles = _collectTextStyles(tester, find.byType(BypassIndicator));
      expect(styles, isNotEmpty,
          reason: 'BypassIndicator must render text');

      for (final s in styles) {
        expect(s.fontFamilyFallback, isNotNull,
            reason:
                'BypassIndicator TextStyle must declare fontFamilyFallback');
        expect(s.fontFamilyFallback, contains('Noto Sans Symbols 2'),
            reason:
                'BypassIndicator fallback must reach Noto Sans Symbols 2');
        expect(s.fontFamilyFallback, contains('Noto Sans Math'),
            reason:
                'BypassIndicator fallback must include Noto Sans Math for completeness');
      }
    });
  });
}
