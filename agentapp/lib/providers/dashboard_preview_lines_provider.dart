import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Persisted dashboard preview line count (1-5, default 3).
///
/// Hydration is lazy: call [hydrate] explicitly (DashboardScreen does this
/// in initState) or rely on [set] which writes prefs synchronously.
final dashboardPreviewLinesProvider =
    StateNotifierProvider<DashboardPreviewLinesNotifier, int>((ref) {
  return DashboardPreviewLinesNotifier();
});

class DashboardPreviewLinesNotifier extends StateNotifier<int> {
  DashboardPreviewLinesNotifier() : super(3);

  static const _key = 'dashboard_preview_lines';
  static const _default = 3;
  static const _min = 1;
  static const _max = 5;

  bool _hydrated = false;

  /// Load persisted value from SharedPreferences. Safe to call multiple
  /// times; only the first successful read mutates state.
  Future<void> hydrate() async {
    if (_hydrated) return;
    final prefs = await SharedPreferences.getInstance();
    final stored = prefs.getInt(_key);
    _hydrated = true;
    if (stored != null) {
      state = stored.clamp(_min, _max);
    }
  }

  Future<void> set(int lines) async {
    final clamped = lines.clamp(_min, _max);
    state = clamped;
    _hydrated = true;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_key, clamped);
  }
}
