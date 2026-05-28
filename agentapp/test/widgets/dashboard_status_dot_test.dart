import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'package:agentapp/models/agent_model.dart';
import 'package:agentapp/widgets/app_bar/dashboard_status_dot.dart';

Widget _wrap(Widget child) {
  return MaterialApp(
    home: Scaffold(
      body: Center(child: child),
    ),
  );
}

void main() {
  group('DashboardStatusDot', () {
    testWidgets('renders nothing when status is null', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: null)),
      );
      // Should render a SizedBox.shrink() — no Container with decoration.
      expect(find.byType(Container), findsNothing);
    });

    testWidgets('renders a circle for idle (green)', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.idle)),
      );

      final container = tester.widget<Container>(find.byType(Container));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.shape, BoxShape.circle);
      expect(decoration.color, Colors.green);
    });

    testWidgets('renders orange for working', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.working)),
      );

      final container = tester.widget<Container>(find.byType(Container));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.blue);
    });

    testWidgets('renders orange for starting', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.starting)),
      );

      final container = tester.widget<Container>(find.byType(Container));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.orange);
    });

    testWidgets('renders grey for stopped', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.stopped)),
      );

      final container = tester.widget<Container>(find.byType(Container));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.grey);
    });

    testWidgets('renders red for crashed', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.crashed)),
      );

      final container = tester.widget<Container>(find.byType(Container));
      final decoration = container.decoration! as BoxDecoration;
      expect(decoration.color, Colors.red);
    });

    testWidgets('respects custom size', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(
          status: AgentStatus.idle,
          size: 16,
        )),
      );

      final size = tester.getSize(find.byType(DashboardStatusDot));
      expect(size.width, 16);
      expect(size.height, 16);
    });

    testWidgets('default size is 10x10', (tester) async {
      await tester.pumpWidget(
        _wrap(const DashboardStatusDot(status: AgentStatus.idle)),
      );

      final size = tester.getSize(find.byType(DashboardStatusDot));
      expect(size.width, 10);
      expect(size.height, 10);
    });
  });
}
