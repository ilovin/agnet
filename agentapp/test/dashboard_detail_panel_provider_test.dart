import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'package:agentapp/providers/dashboard_detail_panel_provider.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    SharedPreferences.setMockInitialValues({});
  });

  test('default state is collapsed (false) without hydration', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    expect(container.read(dashboardDetailPanelExpandedProvider), isFalse);
  });

  test('hydrate() restores persisted true', () async {
    SharedPreferences.setMockInitialValues({
      DashboardDetailPanelNotifier.prefsKey: true,
    });
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await container
        .read(dashboardDetailPanelExpandedProvider.notifier)
        .hydrate();
    expect(container.read(dashboardDetailPanelExpandedProvider), isTrue);
  });

  test('hydrate() with no stored value keeps default', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await container
        .read(dashboardDetailPanelExpandedProvider.notifier)
        .hydrate();
    expect(container.read(dashboardDetailPanelExpandedProvider), isFalse);
  });

  test('setExpanded(true) updates state and persists', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    await container
        .read(dashboardDetailPanelExpandedProvider.notifier)
        .setExpanded(true);
    expect(container.read(dashboardDetailPanelExpandedProvider), isTrue);
    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getBool(DashboardDetailPanelNotifier.prefsKey), isTrue);
  });

  test('toggle() flips the state and persists', () async {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final notifier =
        container.read(dashboardDetailPanelExpandedProvider.notifier);

    await notifier.toggle();
    expect(container.read(dashboardDetailPanelExpandedProvider), isTrue);

    await notifier.toggle();
    expect(container.read(dashboardDetailPanelExpandedProvider), isFalse);

    final prefs = await SharedPreferences.getInstance();
    expect(prefs.getBool(DashboardDetailPanelNotifier.prefsKey), isFalse);
  });
}
