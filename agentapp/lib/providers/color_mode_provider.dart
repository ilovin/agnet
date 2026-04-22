import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

enum ColorMode { rich, naive }

final colorModeProvider = StateNotifierProvider<ColorModeNotifier, ColorMode>((ref) {
  return ColorModeNotifier();
});

class ColorModeNotifier extends StateNotifier<ColorMode> {
  ColorModeNotifier() : super(ColorMode.rich) {
    _load();
  }

  static const _key = 'color_mode';

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final index = prefs.getInt(_key) ?? 0;
    state = ColorMode.values[index.clamp(0, 1)];
  }

  Future<void> set(ColorMode mode) async {
    state = mode;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_key, mode.index);
  }
}
