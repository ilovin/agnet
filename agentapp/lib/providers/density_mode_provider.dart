import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../theme/density_mode.dart';

/// Persisted information-density preference (compact / standard / comfortable).
final densityModeProvider =
    StateNotifierProvider<DensityModeNotifier, DensityMode>((ref) {
  return DensityModeNotifier();
});

class DensityModeNotifier extends StateNotifier<DensityMode> {
  DensityModeNotifier() : super(DensityMode.standard) {
    _load();
  }

  static const _key = 'density_mode';

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final stored = prefs.getInt(_key);
    if (stored == null) return;
    final clamped = stored.clamp(0, DensityMode.values.length - 1);
    state = DensityMode.values[clamped];
  }

  Future<void> set(DensityMode mode) async {
    state = mode;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_key, mode.index);
  }
}
