// Source-level guard ensuring the dead UX scaffolding from the BOLD
// mission-control cleanup stays purged. The four artefacts below were
// either documented `SizedBox.shrink()` placeholders (`_buildModeButton`,
// `_showConfigSheet`), an `// ignore: unused_element` orphan
// (`_takeBrowserScreenshot`), or an entire abandoned widget file
// (`browser_screenshot_widget.dart`). None of them must reappear in
// agentapp/lib/ without an explicit re-introduction.

import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('dead UX scaffolding stays purged from agentapp/lib/', () async {
    final libDir = Directory('lib');
    expect(await libDir.exists(), isTrue,
        reason: 'expected agentapp/lib/ to exist');

    const purgedTokens = <String>[
      '_takeBrowserScreenshot',
      'browser_screenshot_widget',
      '_buildModeButton',
      '_showConfigSheet',
    ];

    final offenders = <String, List<String>>{};

    await for (final entity in libDir.list(recursive: true)) {
      if (entity is! File) continue;
      if (!entity.path.endsWith('.dart')) continue;
      final src = await entity.readAsString();
      for (final token in purgedTokens) {
        if (src.contains(token)) {
          offenders.putIfAbsent(token, () => <String>[]).add(entity.path);
        }
      }
    }

    expect(offenders, isEmpty,
        reason: 'dead UX scaffolding must not reappear in lib/. '
            'Offenders: $offenders');
  });
}
