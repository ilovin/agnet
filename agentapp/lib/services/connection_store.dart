import 'dart:convert';

import 'package:shared_preferences/shared_preferences.dart';

import '../models/connection_config.dart';

const _kKey = 'saved_connections';

/// Persists a list of [ConnectionConfig] to SharedPreferences.
class ConnectionStore {
  Future<List<ConnectionConfig>> load() async {
    final prefs = await SharedPreferences.getInstance();
    final raw = prefs.getString(_kKey);
    if (raw == null) return [];
    final list = jsonDecode(raw) as List<dynamic>;
    return list
        .map((e) => ConnectionConfig.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  Future<void> save(List<ConnectionConfig> configs) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setString(_kKey, jsonEncode(configs.map((c) => c.toJson()).toList()));
  }

  Future<void> clear() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove(_kKey);
  }
}
