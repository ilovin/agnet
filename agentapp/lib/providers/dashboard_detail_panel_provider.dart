import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Persisted "dashboard right-side detail panel expanded?" preference.
///
/// Default: collapsed (false). When false the right panel only shows a 48px
/// rail with a chevron toggle. When true it expands to take its share of the
/// dashboard.
///
/// Hydration is **lazy**: the constructor does not fire-and-forget an async
/// load (which races with widget tests). Use [hydrate] explicitly or rely on
/// [setExpanded]/[toggle] which read prefs synchronously after the first
/// successful load.
final dashboardDetailPanelExpandedProvider =
    StateNotifierProvider<DashboardDetailPanelNotifier, bool>((ref) {
  return DashboardDetailPanelNotifier();
});

class DashboardDetailPanelNotifier extends StateNotifier<bool> {
  DashboardDetailPanelNotifier({bool initial = false}) : super(initial);

  static const prefsKey = 'dashboard_detail_panel_expanded';

  bool _hydrated = false;

  /// Load persisted value from SharedPreferences. Safe to call multiple
  /// times; only the first successful read mutates state.
  Future<void> hydrate() async {
    if (_hydrated) return;
    final prefs = await SharedPreferences.getInstance();
    final stored = prefs.getBool(prefsKey);
    _hydrated = true;
    if (stored != null && stored != state) {
      state = stored;
    }
  }

  Future<void> setExpanded(bool value) async {
    state = value;
    _hydrated = true;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool(prefsKey, value);
  }

  Future<void> toggle() => setExpanded(!state);
}
