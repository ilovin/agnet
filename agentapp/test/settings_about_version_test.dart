import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/screens/settings_screen.dart';

/// 关于 / About 区版本号必须随 pubspec.yaml 自动同步。
///
/// 历史问题：v0.2.2 → v1.0.x 多次发版后，"关于 — Agnet" 副标题仍然显示
/// 'v0.2.2 — Multi-AI-Agent Remote Manager'。
/// 原因是该副标题在 settings_screen.dart 是 const 字符串，bump-version.sh
/// 不会触达这一行。
///
/// 修复：副标题改成从 PackageInfo.fromPlatform() 异步读取，
/// 由 Flutter 构建管线注入 pubspec.yaml 中的 version。
void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  Widget _wrap() {
    return const ProviderScope(
      child: MaterialApp(home: SettingsScreen()),
    );
  }

  testWidgets(
      'About subtitle reflects PackageInfo version (e.g. 1.0.1) instead of legacy v0.2.2',
      (tester) async {
    PackageInfo.setMockInitialValues(
      appName: 'Agnet',
      packageName: 'com.example.agentapp',
      version: '1.0.1',
      buildNumber: '7',
      buildSignature: '',
    );

    await tester.pumpWidget(_wrap());
    // initState fires _loadAppVersion(); pump once for the future, once for setState.
    await tester.pump();
    await tester.pump();

    // The about ListTile sits inside a SingleChildScrollView; it may need a
    // scroll to become visible. Use ensureVisible-style scroll only if find
    // by text fails — but since SingleChildScrollView lays out all children
    // off-screen they're still in the tree and findable.
    expect(
      find.text('v1.0.1+7 — Multi-AI-Agent Remote Manager'),
      findsOneWidget,
      reason:
          'About subtitle must use PackageInfo.version (1.0.1) + buildNumber (7), '
          'not the hardcoded v0.2.2 legacy string',
    );

    // Negative assertion: the legacy hardcoded string must be gone.
    expect(
      find.text('v0.2.2 — Multi-AI-Agent Remote Manager'),
      findsNothing,
      reason: 'legacy hardcoded v0.2.2 must no longer appear',
    );
  });

  testWidgets('About subtitle falls back gracefully when version is empty',
      (tester) async {
    PackageInfo.setMockInitialValues(
      appName: 'Agnet',
      packageName: 'com.example.agentapp',
      version: '',
      buildNumber: '',
      buildSignature: '',
    );

    await tester.pumpWidget(_wrap());
    await tester.pump();
    await tester.pump();

    // When PackageInfo returns an empty version, the subtitle must still show
    // the tagline rather than 'v — Multi-AI-Agent Remote Manager' or a crash.
    expect(find.text('Multi-AI-Agent Remote Manager'), findsOneWidget);
  });

  testWidgets(
      'About subtitle omits build number when buildNumber is empty (iOS-style)',
      (tester) async {
    PackageInfo.setMockInitialValues(
      appName: 'Agnet',
      packageName: 'com.example.agentapp',
      version: '1.2.0',
      buildNumber: '',
      buildSignature: '',
    );

    await tester.pumpWidget(_wrap());
    await tester.pump();
    await tester.pump();

    expect(
      find.text('v1.2.0 — Multi-AI-Agent Remote Manager'),
      findsOneWidget,
    );
  });
}
