import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';

import 'package:agentapp/main.dart' as app;

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('Flutter web app loads and shows Agent Manager title', (
    WidgetTester tester,
  ) async {
    // Launch the app
    app.main();

    // Wait for the app to settle (font loading, first frame, etc.)
    await tester.pumpAndSettle(const Duration(seconds: 3));

    // Verify the app loads by finding the Agent Manager title
    // The initial route is /connections which shows 'Agent Manager' in the AppBar
    expect(find.text('Agent Manager'), findsOneWidget);

    // Verify that the connections screen loaded with expected UI elements
    // The connections screen shows an IconButton (add connection button)
    expect(find.byType(IconButton), findsWidgets);
  });
}
