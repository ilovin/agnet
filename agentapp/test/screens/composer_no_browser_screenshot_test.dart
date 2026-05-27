import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

/// Source-level guard: ensures the input toolbar no longer surfaces a
/// "浏览器截图" affordance after the BOLD UX cleanup. The
/// [BrowserScreenshotWidget] code itself is intentionally retained
/// (file under lib/widgets/browser_screenshot_widget.dart), so we scope
/// the negative assertion to the screen file and its toolbar slot.
void main() {
  test('agent_detail_screen no longer wires a browser-screenshot button',
      () async {
    final file = File('lib/screens/agent_detail_screen.dart');
    expect(await file.exists(), isTrue,
        reason: 'expected agent_detail_screen.dart to exist');
    final src = await file.readAsString();

    // The visible button used Icons.web with tooltip '浏览器截图'.
    // After the cleanup the toolbar must not display that tooltip.
    final toolTipMatches =
        RegExp(r"tooltip:\s*'浏览器截图'").allMatches(src).length;
    expect(toolTipMatches, 0,
        reason: '"浏览器截图" tooltip should be removed from the input toolbar');

    // The icon entry-point should also be gone from the toolbar slot;
    // the only remaining Icons.web reference (if any) belongs to the
    // dialog header inside browser_screenshot_widget.dart, not this file.
    final iconWebMatches =
        RegExp(r"Icon\(Icons\.web\b").allMatches(src).length;
    expect(iconWebMatches, 0,
        reason: 'Icons.web button should not appear in agent_detail_screen');
  });
}
