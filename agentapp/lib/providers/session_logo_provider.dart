import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// Predefined icon pool for session logos.
/// Chosen to be visually distinct and recognizable.
const _iconPool = <IconData>[
  Icons.terminal,
  Icons.code,
  Icons.bug_report,
  Icons.lightbulb,
  Icons.search,
  Icons.build,
  Icons.computer,
  Icons.cloud,
  Icons.memory,
  Icons.storage,
  Icons.security,
  Icons.analytics,
  Icons.language,
  Icons.layers,
  Icons.settings,
  Icons.extension,
  Icons.integration_instructions,
  Icons.vpn_key,
  Icons.api,
  Icons.dns,
  Icons.web,
  Icons.data_object,
  Icons.schema,
  Icons.model_training,
  Icons.auto_fix_high,
  Icons.smart_toy,
  Icons.rocket_launch,
  Icons.psychology,
  Icons.bolt,
  Icons.cable,
  Icons.hub,
  Icons.forklift,
  Icons.precision_manufacturing,
  Icons.biotech,
  Icons.science,
  Icons.calculate,
  Icons.translate,
  Icons.article,
  Icons.feedback,
  Icons.support_agent,
  Icons.task_alt,
  Icons.check_circle,
  Icons.flag,
  Icons.bookmark,
  Icons.label,
  Icons.tag,
  Icons.folder,
  Icons.inventory,
  Icons.dashboard,
  Icons.grid_view,
  Icons.view_module,
  Icons.widgets,
  Icons.palette,
  Icons.brush,
  Icons.camera,
  Icons.mic,
  Icons.videocam,
  Icons.headset,
  Icons.gamepad,
  Icons.sports_esports,
  Icons.fitness_center,
  Icons.directions_run,
  Icons.local_fire_department,
  Icons.water_drop,
  Icons.eco,
  Icons.nature,
  Icons.park,
  Icons.pets,
  Icons.emoji_nature,
  Icons.brightness_high,
  Icons.nights_stay,
  Icons.wb_sunny,
  Icons.ac_unit,
  Icons.filter_drama,
  Icons.thunderstorm,
  Icons.waves,
  Icons.landscape,
  Icons.terrain,
  Icons.map,
  Icons.explore,
  Icons.navigation,
  Icons.my_location,
  Icons.location_on,
  Icons.home,
  Icons.apartment,
  Icons.business,
  Icons.account_balance,
  Icons.local_cafe,
  Icons.local_dining,
  Icons.local_pizza,
  Icons.fastfood,
  Icons.bakery_dining,
  Icons.coffee,
  Icons.wine_bar,
  Icons.celebration,
  Icons.card_giftcard,
  Icons.shopping_cart,
  Icons.local_mall,
  Icons.storefront,
  Icons.attach_money,
  Icons.savings,
  Icons.account_balance_wallet,
  Icons.credit_card,
  Icons.receipt,
  Icons.inventory_2,
  Icons.local_shipping,
  Icons.flight,
  Icons.train,
  Icons.directions_car,
  Icons.directions_bike,
  Icons.electric_car,
  Icons.electric_bolt,
  Icons.solar_power,
  Icons.battery_full,
  Icons.power,
 Icons.energy_savings_leaf,
  Icons.recycling,
  Icons.compost,
  Icons.forest,
  Icons.agriculture,
  Icons.anchor,
  Icons.flare,
  Icons.star,
  Icons.favorite,
  Icons.heart_broken,
  Icons.sentiment_satisfied,
  Icons.sentiment_dissatisfied,
  Icons.sentiment_neutral,
  Icons.mood,
  Icons.mood_bad,
  Icons.face,
  Icons.face_2,
  Icons.face_3,
  Icons.face_4,
  Icons.face_5,
  Icons.face_6,
];

/// Mapping between IconData and a stable string name for persistence.
final _iconNameMap = <IconData, String>{
  Icons.terminal: 'terminal',
  Icons.code: 'code',
  Icons.bug_report: 'bug_report',
  Icons.lightbulb: 'lightbulb',
  Icons.search: 'search',
  Icons.build: 'build',
  Icons.computer: 'computer',
  Icons.cloud: 'cloud',
  Icons.memory: 'memory',
  Icons.storage: 'storage',
  Icons.security: 'security',
  Icons.analytics: 'analytics',
  Icons.language: 'language',
  Icons.layers: 'layers',
  Icons.settings: 'settings',
  Icons.extension: 'extension',
  Icons.integration_instructions: 'integration_instructions',
  Icons.vpn_key: 'vpn_key',
  Icons.api: 'api',
  Icons.dns: 'dns',
  Icons.web: 'web',
  Icons.data_object: 'data_object',
  Icons.schema: 'schema',
  Icons.model_training: 'model_training',
  Icons.auto_fix_high: 'auto_fix_high',
  Icons.smart_toy: 'smart_toy',
  Icons.rocket_launch: 'rocket_launch',
  Icons.psychology: 'psychology',
  Icons.bolt: 'bolt',
  Icons.cable: 'cable',
  Icons.hub: 'hub',
  Icons.forklift: 'forklift',
  Icons.precision_manufacturing: 'precision_manufacturing',
  Icons.biotech: 'biotech',
  Icons.science: 'science',
  Icons.calculate: 'calculate',
  Icons.translate: 'translate',
  Icons.article: 'article',
  Icons.feedback: 'feedback',
  Icons.support_agent: 'support_agent',
  Icons.task_alt: 'task_alt',
  Icons.check_circle: 'check_circle',
  Icons.flag: 'flag',
  Icons.bookmark: 'bookmark',
  Icons.label: 'label',
  Icons.tag: 'tag',
  Icons.folder: 'folder',
  Icons.inventory: 'inventory',
  Icons.dashboard: 'dashboard',
  Icons.grid_view: 'grid_view',
  Icons.view_module: 'view_module',
  Icons.widgets: 'widgets',
  Icons.palette: 'palette',
  Icons.brush: 'brush',
  Icons.camera: 'camera',
  Icons.mic: 'mic',
  Icons.videocam: 'videocam',
  Icons.headset: 'headset',
  Icons.gamepad: 'gamepad',
  Icons.sports_esports: 'sports_esports',
  Icons.fitness_center: 'fitness_center',
  Icons.directions_run: 'directions_run',
  Icons.local_fire_department: 'local_fire_department',
  Icons.water_drop: 'water_drop',
  Icons.eco: 'eco',
  Icons.nature: 'nature',
  Icons.park: 'park',
  Icons.pets: 'pets',
  Icons.emoji_nature: 'emoji_nature',
  Icons.brightness_high: 'brightness_high',
  Icons.nights_stay: 'nights_stay',
  Icons.wb_sunny: 'wb_sunny',
  Icons.ac_unit: 'ac_unit',
  Icons.filter_drama: 'filter_drama',
  Icons.thunderstorm: 'thunderstorm',
  Icons.waves: 'waves',
  Icons.landscape: 'landscape',
  Icons.terrain: 'terrain',
  Icons.map: 'map',
  Icons.explore: 'explore',
  Icons.navigation: 'navigation',
  Icons.my_location: 'my_location',
  Icons.location_on: 'location_on',
  Icons.home: 'home',
  Icons.apartment: 'apartment',
  Icons.business: 'business',
  Icons.account_balance: 'account_balance',
  Icons.local_cafe: 'local_cafe',
  Icons.local_dining: 'local_dining',
  Icons.local_pizza: 'local_pizza',
  Icons.fastfood: 'fastfood',
  Icons.bakery_dining: 'bakery_dining',
  Icons.coffee: 'coffee',
  Icons.wine_bar: 'wine_bar',
  Icons.celebration: 'celebration',
  Icons.card_giftcard: 'card_giftcard',
  Icons.shopping_cart: 'shopping_cart',
  Icons.local_mall: 'local_mall',
  Icons.storefront: 'storefront',
  Icons.attach_money: 'attach_money',
  Icons.savings: 'savings',
  Icons.account_balance_wallet: 'account_balance_wallet',
  Icons.credit_card: 'credit_card',
  Icons.receipt: 'receipt',
  Icons.inventory_2: 'inventory_2',
  Icons.local_shipping: 'local_shipping',
  Icons.flight: 'flight',
  Icons.train: 'train',
  Icons.directions_car: 'directions_car',
  Icons.directions_bike: 'directions_bike',
  Icons.electric_car: 'electric_car',
  Icons.electric_bolt: 'electric_bolt',
  Icons.solar_power: 'solar_power',
  Icons.battery_full: 'battery_full',
  Icons.power: 'power',
  Icons.energy_savings_leaf: 'energy_savings_leaf',
  Icons.recycling: 'recycling',
  Icons.compost: 'compost',
  Icons.forest: 'forest',
  Icons.agriculture: 'agriculture',
  Icons.anchor: 'anchor',
  Icons.flare: 'flare',
  Icons.star: 'star',
  Icons.favorite: 'favorite',
  Icons.heart_broken: 'heart_broken',
  Icons.sentiment_satisfied: 'sentiment_satisfied',
  Icons.sentiment_dissatisfied: 'sentiment_dissatisfied',
  Icons.sentiment_neutral: 'sentiment_neutral',
  Icons.mood: 'mood',
  Icons.mood_bad: 'mood_bad',
  Icons.face: 'face',
  Icons.face_2: 'face_2',
  Icons.face_3: 'face_3',
  Icons.face_4: 'face_4',
  Icons.face_5: 'face_5',
  Icons.face_6: 'face_6',
};

final _nameToIconMap = <String, IconData>{
  for (final entry in _iconNameMap.entries) entry.value: entry.key,
};

int _toSigned32(int value) {
  value = value & 0xFFFFFFFF;
  return value > 0x7FFFFFFF ? value - 0x100000000 : value;
}

/// Cross-platform string hash (Dart VM and JS produce identical results).
int _crossPlatformStringHash(String input) {
  var hash = 0;
  for (var i = 0; i < input.length; i++) {
    hash = _toSigned32(hash * 31 + input.codeUnitAt(i));
  }
  return hash;
}

/// Returns a deterministic but well-distributed default icon for a session key.
IconData defaultIconForSession(String sessionKey) {
  // Mix hash bits to reduce clustering for similar keys
  var hash = _crossPlatformStringHash(sessionKey);
  hash = _toSigned32(((hash >> 16) ^ hash) * 0x45d9f3b);
  hash = _toSigned32(((hash >> 16) ^ hash) * 0x45d9f3b);
  hash = _toSigned32((hash >> 16) ^ hash);
  final index = hash.abs() % _iconPool.length;
  return _iconPool[index];
}

class SessionLogoNotifier extends StateNotifier<Map<String, IconData>> {
  SessionLogoNotifier() : super(const {}) {
    _load();
  }

  static const _prefsKey = 'session_logos';

  Future<void> _load() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final raw = prefs.getString(_prefsKey);
      if (raw == null || raw.isEmpty) return;
      final json = jsonDecode(raw) as Map<String, dynamic>;
      final loaded = <String, IconData>{};
      for (final entry in json.entries) {
        final icon = _nameToIconMap[entry.value as String];
        if (icon != null) {
          loaded[entry.key] = icon;
        }
      }
      state = loaded;
    } catch (_) {
      // Ignore load errors; fall back to defaults.
    }
  }

  Future<void> _save() async {
    try {
      final prefs = await SharedPreferences.getInstance();
      final json = <String, String>{};
      for (final entry in state.entries) {
        final name = _iconNameMap[entry.value];
        if (name != null) {
          json[entry.key] = name;
        }
      }
      await prefs.setString(_prefsKey, jsonEncode(json));
    } catch (_) {
      // Ignore save errors.
    }
  }

  /// Returns the effective icon for a session, using a stable default if none
  /// has been explicitly set.
  IconData iconFor(String sessionKey) {
    return state[sessionKey] ?? defaultIconForSession(sessionKey);
  }

  /// Explicitly set a session logo.
  void setLogo(String sessionKey, IconData icon) {
    if (!_iconNameMap.containsKey(icon)) return;
    if (state[sessionKey] == icon) return;
    state = {...state, sessionKey: icon};
    _save();
  }

  /// Reset a session logo to its default.
  void resetLogo(String sessionKey) {
    if (!state.containsKey(sessionKey)) return;
    final next = Map<String, IconData>.from(state);
    next.remove(sessionKey);
    state = next;
    _save();
  }
}

final sessionLogoProvider =
    StateNotifierProvider<SessionLogoNotifier, Map<String, IconData>>(
  (_) => SessionLogoNotifier(),
);

/// All available icons that users can pick from, grouped loosely by category
/// for the picker UI.
List<IconData> get availableSessionLogos => List.unmodifiable(_iconPool);
