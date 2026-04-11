import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:url_launcher/url_launcher.dart';

import '../providers/connection_provider.dart';
import '../models/connection_config.dart';

class SettingsScreen extends ConsumerStatefulWidget {
  const SettingsScreen({super.key});

  @override
  ConsumerState<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends ConsumerState<SettingsScreen> {
  List<ConnectionConfig> _saved = [];

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final store = ref.read(connectionStoreProvider);
    final configs = await store.load();
    if (mounted) setState(() => _saved = configs);
  }

  Future<void> _delete(int index) async {
    final store = ref.read(connectionStoreProvider);
    final updated = List<ConnectionConfig>.from(_saved)..removeAt(index);
    await store.save(updated);
    setState(() => _saved = updated);
  }

  void _disconnect() {
    ref.read(connectionProvider.notifier).disconnect();
    context.go('/connections');
  }

  /// Derive HTTP base URL from the WebSocket URL.
  /// ws://host:port/ws  ->  http://host:port
  String? _httpBase(String wsUrl) {
    try {
      final uri = Uri.parse(wsUrl);
      final scheme = uri.scheme == 'wss' ? 'https' : 'http';
      return '$scheme://${uri.host}:${uri.port}';
    } catch (_) {
      return null;
    }
  }

  Future<void> _checkForUpdate(BuildContext context) async {
    final client = ref.read(connectionProvider);
    if (client == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('请先连接到网关')),
      );
      return;
    }

    final base = _httpBase(client.url);
    if (base == null) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('无法解析网关地址')),
      );
      return;
    }

    final apkUrl = Uri.parse('$base/apk?token=${Uri.encodeComponent(client.token)}');

    if (!context.mounted) return;
    showDialog(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('检查更新'),
        content: Text('从网关下载最新 APK？\n\n$base/apk'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () async {
              Navigator.pop(ctx);
              if (await canLaunchUrl(apkUrl)) {
                await launchUrl(apkUrl, mode: LaunchMode.externalApplication);
              } else if (context.mounted) {
                ScaffoldMessenger.of(context).showSnackBar(
                  const SnackBar(content: Text('无法打开下载链接')),
                );
              }
            },
            child: const Text('下载更新'),
          ),
        ],
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final client = ref.watch(connectionProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('设置')),
      body: ListView(
        children: [
          // Connection status section
          ListTile(
            leading: Icon(
              client != null ? Icons.wifi : Icons.wifi_off,
              color: client != null ? Colors.green : Colors.grey,
            ),
            title: Text(client != null ? '已连接' : '未连接'),
            subtitle: Text(client != null ? '点击断开连接' : '前往连接页面'),
            onTap: client != null ? _disconnect : () => context.go('/connections'),
          ),
          const Divider(),
          // Saved connections
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('保存的连接', style: TextStyle(color: Colors.grey, fontSize: 13)),
          ),
          if (_saved.isEmpty)
            const ListTile(title: Text('暂无保存的连接', style: TextStyle(color: Colors.grey)))
          else
            ..._saved.asMap().entries.map(
                  (e) => ListTile(
                    leading: const Icon(Icons.hub_outlined),
                    title: Text(e.value.url),
                    trailing: IconButton(
                      icon: const Icon(Icons.delete_outline, color: Colors.red),
                      onPressed: () => _delete(e.key),
                    ),
                  ),
                ),
          const Divider(),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('开发者工具', style: TextStyle(color: Colors.grey, fontSize: 13)),
          ),
          ListTile(
            leading: const Icon(Icons.system_update_alt),
            title: const Text('检查更新'),
            subtitle: const Text('从网关下载最新 APK'),
            onTap: () => _checkForUpdate(context),
          ),
          const Divider(),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('关于', style: TextStyle(color: Colors.grey, fontSize: 13)),
          ),
          const ListTile(
            leading: Icon(Icons.info_outline),
            title: Text('Agent Manager'),
            subtitle: Text('v1.0.0 — Multi-AI-Agent Remote Manager'),
          ),
        ],
      ),
    );
  }
}
