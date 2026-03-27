import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/app.dart';

void main() {
  testWidgets('AgentApp smoke test — renders without crash', (WidgetTester tester) async {
    await tester.pumpWidget(const ProviderScope(child: AgentApp()));
    await tester.pumpAndSettle();
    // ConnectionsScreen should be shown initially
    expect(find.text('Agent Manager'), findsOneWidget);
  });
}
