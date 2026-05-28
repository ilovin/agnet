import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/providers/dashboard_preview_lines_provider.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('default preview lines is 3 before hydration', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    expect(container.read(dashboardPreviewLinesProvider), 3);
  });

  test('hydrate keeps default when no stored value', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .hydrate();
    expect(container.read(dashboardPreviewLinesProvider), 3);
  });

  test('setting preview lines updates the provider', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .set(5);
    expect(container.read(dashboardPreviewLinesProvider), 5);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .set(1);
    expect(container.read(dashboardPreviewLinesProvider), 1);
  });

  test('values are clamped to 1-5 range', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .set(0);
    expect(container.read(dashboardPreviewLinesProvider), 1);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .set(10);
    expect(container.read(dashboardPreviewLinesProvider), 5);
  });

  test('preview lines persist to SharedPreferences', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .set(4);

    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getInt('dashboard_preview_lines'), 4);
  });

  test('preview lines restore from SharedPreferences via hydrate', () async {
    SharedPreferences.setMockInitialValues({
      'dashboard_preview_lines': 2,
    });

    final container = ProviderContainer();
    addTearDown(container.dispose);

    await container
        .read(dashboardPreviewLinesProvider.notifier)
        .hydrate();
    expect(container.read(dashboardPreviewLinesProvider), 2);
  });
}
