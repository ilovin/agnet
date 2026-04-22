import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

enum ThemeModeSetting { system, light, dark }

ThemeMode themeModeFromSetting(ThemeModeSetting s) {
  switch (s) {
    case ThemeModeSetting.light:
      return ThemeMode.light;
    case ThemeModeSetting.dark:
      return ThemeMode.dark;
    case ThemeModeSetting.system:
      return ThemeMode.system;
  }
}

final themeModeProvider = StateNotifierProvider<ThemeModeNotifier, ThemeModeSetting>((ref) {
  return ThemeModeNotifier();
});

class ThemeModeNotifier extends StateNotifier<ThemeModeSetting> {
  ThemeModeNotifier() : super(ThemeModeSetting.system) {
    _load();
  }

  static const _key = 'theme_mode';

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    final index = prefs.getInt(_key) ?? 0;
    state = ThemeModeSetting.values[index.clamp(0, 2)];
  }

  Future<void> set(ThemeModeSetting mode) async {
    state = mode;
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt(_key, mode.index);
  }
}
