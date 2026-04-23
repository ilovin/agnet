import 'dart:convert';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';
import 'package:package_info_plus/package_info_plus.dart';

import '../providers/connection_provider.dart';
import '../providers/theme_provider.dart';
import '../providers/color_mode_provider.dart';
import '../providers/unread_provider.dart';
import '../models/connection_config.dart';
import '../services/apk_downloader.dart';

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
    configs.sort((a, b) => a.url.toLowerCase().compareTo(b.url.toLowerCase()));
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

  /// ws://host:port/ws  ->  http://host:port
  String? _httpBase(String wsUrl) {
    try {
      final uri = Uri.parse(wsUrl);
      final scheme = uri.scheme == 'wss' ? 'https' : 'http';
      final defaultPort = uri.scheme == 'wss' ? 443 : 80;
      if (uri.port > 0 && uri.port != defaultPort) {
        return '$scheme://${uri.host}:${uri.port}';
      }
      return '$scheme://${uri.host}';
    } catch (_) {
      return null;
    }
  }

  Future<void> _checkForUpdate(BuildContext context) async {
    final client = ref.read(connectionProvider);
    if (client == null) {
      _snack('请先连接到网关');
      return;
    }

    final base = _httpBase(client.url);
    if (base == null) {
      _snack('无法解析网关地址');
      return;
    }

    // Fetch version info first
    if (!context.mounted) return;
    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (_) => const AlertDialog(
        content: Row(
          children: [
            CircularProgressIndicator(),
            SizedBox(width: 16),
            Text('正在检查更新...'),
          ],
        ),
      ),
    );

    try {
      final versionUrl = Uri.parse('$base/apk/version?token=${Uri.encodeComponent(client.token)}');
      final resp = await http.get(versionUrl).timeout(const Duration(seconds: 10));

      if (!context.mounted) return;
      Navigator.pop(context); // dismiss checking dialog

      if (resp.statusCode != 200) {
        _snack(resp.statusCode == 404 ? '网关上没有 APK 文件' : 'HTTP ${resp.statusCode}');
        return;
      }

      final info = jsonDecode(resp.body) as Map<String, dynamic>;
      final size = info['size'] as int? ?? 0;
      final modifiedAt = info['modifiedAt'] as String? ?? '';

      if (!context.mounted) return;
      _showDownloadDialog(context, base, client.token, size, modifiedAt);
    } catch (e) {
      if (context.mounted) Navigator.pop(context);
      _snack('检查更新失败: $e');
    }
  }

  void _showDownloadDialog(
    BuildContext context,
    String base,
    String token,
    int totalSize,
    String modifiedAt,
  ) {
    showDialog(
      context: context,
      barrierDismissible: false,
      builder: (ctx) => _DownloadDialog(
        apkUrl: '$base/apk?token=${Uri.encodeComponent(token)}',
        totalSize: totalSize,
        modifiedAt: modifiedAt,
      ),
    );
  }

  void _snack(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context) {
    final client = ref.watch(connectionProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('设置')),
      body: ListView(
        children: [
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
            child: Text('外观', style: TextStyle(color: Colors.grey, fontSize: 13)),
          ),
          Consumer(builder: (context, ref, _) {
            final mode = ref.watch(themeModeProvider);
            return Column(
              children: [
                RadioListTile<ThemeModeSetting>(
                  title: const Text('跟随系统'),
                  secondary: const Icon(Icons.brightness_auto),
                  value: ThemeModeSetting.system,
                  groupValue: mode,
                  onChanged: (v) => ref.read(themeModeProvider.notifier).set(v!),
                ),
                RadioListTile<ThemeModeSetting>(
                  title: const Text('浅色模式'),
                  secondary: const Icon(Icons.light_mode),
                  value: ThemeModeSetting.light,
                  groupValue: mode,
                  onChanged: (v) => ref.read(themeModeProvider.notifier).set(v!),
                ),
                RadioListTile<ThemeModeSetting>(
                  title: const Text('深色模式'),
                  secondary: const Icon(Icons.dark_mode),
                  value: ThemeModeSetting.dark,
                  groupValue: mode,
                  onChanged: (v) => ref.read(themeModeProvider.notifier).set(v!),
                ),
              ],
            );
          }),
          Consumer(builder: (context, ref, _) {
            final cmode = ref.watch(colorModeProvider);
            return Column(
              children: [
                ListTile(
                  leading: const Icon(Icons.palette),
                  title: const Text('配色模式'),
                  subtitle: Text(cmode == ColorMode.rich ? 'Rich — 彩色语法高亮' : 'Naive — 简洁默认配色'),
                ),
                RadioListTile<ColorMode>(
                  title: const Text('Rich（彩色高亮）'),
                  subtitle: const Text('ANSI 彩色、代码语法高亮、工具颜色标记'),
                  value: ColorMode.rich,
                  groupValue: cmode,
                  onChanged: (v) => ref.read(colorModeProvider.notifier).set(v!),
                ),
                RadioListTile<ColorMode>(
                  title: const Text('Naive（简洁）'),
                  subtitle: const Text('默认 Markdown 渲染，无额外颜色'),
                  value: ColorMode.naive,
                  groupValue: cmode,
                  onChanged: (v) => ref.read(colorModeProvider.notifier).set(v!),
                ),
              ],
            );
          }),
          const Divider(),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('通知', style: TextStyle(color: Colors.grey, fontSize: 13)),
          ),
          Consumer(builder: (context, ref, _) {
            final enabled = ref.watch(unreadSettingProvider);
            return SwitchListTile(
              secondary: const Icon(Icons.notifications),
              title: const Text('未读消息小红点'),
              subtitle: const Text('Agent 输出新消息时显示提醒'),
              value: enabled,
              onChanged: (v) => ref.read(unreadSettingProvider.notifier).set(v),
            );
          }),
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
            subtitle: Text('v0.2.2 — Multi-AI-Agent Remote Manager'),
          ),
        ],
      ),
    );
  }
}

// ─── Download Dialog with progress ───────────────────────────────────

class _DownloadDialog extends StatefulWidget {
  final String apkUrl;
  final int totalSize;
  final String modifiedAt;

  const _DownloadDialog({
    required this.apkUrl,
    required this.totalSize,
    required this.modifiedAt,
  });

  @override
  State<_DownloadDialog> createState() => _DownloadDialogState();
}

enum _DlState { idle, downloading, paused, done, error }

class _DownloadDialogState extends State<_DownloadDialog> {
  _DlState _state = _DlState.idle;
  ApkDownloader? _downloader;
  String? _savePath;
  String _errorMsg = '';
  String _versionLabel = '';

  int _received = 0;
  int _total = -1;
  double _speed = 0;

  @override
  void initState() {
    super.initState();
    _total = widget.totalSize;
    _initVersionLabel();
  }

  Future<void> _initVersionLabel() async {
    final info = await PackageInfo.fromPlatform();
    if (mounted) {
      setState(() {
        _versionLabel = info.version;
      });
    }
  }

  @override
  void dispose() {
    _downloader?.cancel();
    super.dispose();
  }

  /// Download to external storage Download directory with version-stamped filename.
  Future<String> _getSavePath() async {
    if (_savePath != null) return _savePath!;
    // Use external storage Downloads directory
    final dir = await getExternalStorageDirectory();
    final downloadsDir = dir != null
        ? Directory('${dir.parent.parent.parent.parent.path}/Download')
        : await getTemporaryDirectory();
    if (!await downloadsDir.exists()) {
      await downloadsDir.create(recursive: true);
    }
    // Clean up old agentapp APKs first
    try {
      await for (final f in downloadsDir.list()) {
        if (f.path.contains('agentapp') && f.path.endsWith('.apk')) {
          await f.delete();
        }
      }
    } catch (_) {}
    final timestamp = DateTime.now().millisecondsSinceEpoch;
    _savePath =
        '${downloadsDir.path}/agentapp-v${_versionLabel.isNotEmpty ? _versionLabel : 'unknown'}-$timestamp.apk';
    return _savePath!;
  }

  Future<void> _startDownload() async {
    final path = await _getSavePath();
    setState(() {
      _state = _DlState.downloading;
      _errorMsg = '';
    });

    _downloader = ApkDownloader(
      url: widget.apkUrl,
      savePath: path,
      onProgress: (p) {
        if (mounted) {
          setState(() {
            _received = p.received;
            _total = p.total;
            _speed = p.speed;
          });
        }
      },
    );

    try {
      await _downloader!.download();
      if (mounted) setState(() => _state = _DlState.done);
    } catch (e) {
      if (mounted) {
        final msg = e.toString();
        if (msg.contains('取消')) {
          setState(() => _state = _DlState.paused);
        } else {
          setState(() {
            _state = _DlState.error;
            _errorMsg = msg;
          });
        }
      }
    }
  }

  void _pause() {
    _downloader?.cancel();
    _downloader = null;
    if (mounted) setState(() => _state = _DlState.paused);
  }

  void _resume() => _startDownload(); // ApkDownloader checks existing bytes

  Future<void> _install() async {
    if (_savePath == null) return;
    final file = File(_savePath!);
    if (!await file.exists()) return;

    // Make file world-readable so PackageInstaller can access it
    try {
      await Process.run('chmod', ['644', _savePath!]);
    } catch (_) {}

    // Trigger APK install via Android Intent
    // Uses platform channel: open_file or direct method channel
    try {
      final result = await _installChannel.invokeMethod('installApk', {'path': _savePath});
      if (!mounted) return;
      if (result == true) {
        Navigator.pop(context);
      } else {
        // Fallback: tell user where the file is
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('APK 已保存到: $_savePath')),
        );
        Navigator.pop(context);
      }
    } catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('安装失败: $e\n文件路径: $_savePath')),
      );
      Navigator.pop(context);
    }
  }

  static final _installChannel = const MethodChannel('com.phonetalk.agentapp/install');

  String _formatBytes(int bytes) {
    if (bytes < 1024) return '$bytes B';
    if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KB';
    return '${(bytes / 1024 / 1024).toStringAsFixed(1)} MB';
  }

  String _formatSpeed(double bps) {
    if (bps <= 0) return '--';
    if (bps < 1024) return '${bps.toStringAsFixed(0)} B/s';
    if (bps < 1024 * 1024) return '${(bps / 1024).toStringAsFixed(1)} KB/s';
    return '${(bps / 1024 / 1024).toStringAsFixed(1)} MB/s';
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('更新 APK'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (widget.modifiedAt.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(bottom: 8),
              child: Text('更新时间: ${widget.modifiedAt}',
                  style: const TextStyle(fontSize: 13, color: Colors.grey)),
            ),
          Text('大小: ${_formatBytes(widget.totalSize)}',
              style: const TextStyle(fontSize: 13, color: Colors.grey)),
          const SizedBox(height: 16),

          // Progress bar
          if (_state == _DlState.downloading || _state == _DlState.paused || _state == _DlState.done)
            Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                LinearProgressIndicator(
                  value: _total > 0 ? _received / _total : null,
                  minHeight: 6,
                ),
                const SizedBox(height: 8),
                Row(
                  mainAxisAlignment: MainAxisAlignment.spaceBetween,
                  children: [
                    Text('${_formatBytes(_received)} / ${_total > 0 ? _formatBytes(_total) : "?"}',
                        style: const TextStyle(fontSize: 12)),
                    if (_state == _DlState.downloading)
                      Text(_formatSpeed(_speed), style: const TextStyle(fontSize: 12)),
                    if (_state == _DlState.paused)
                      const Text('已暂停', style: TextStyle(fontSize: 12, color: Colors.orange)),
                    if (_state == _DlState.done)
                      const Text('完成', style: TextStyle(fontSize: 12, color: Colors.green)),
                  ],
                ),
              ],
            ),

          if (_state == _DlState.error)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(_errorMsg,
                  style: const TextStyle(color: Colors.red, fontSize: 13)),
            ),
        ],
      ),
      actions: _buildActions(),
    );
  }

  List<Widget> _buildActions() {
    switch (_state) {
      case _DlState.idle:
        return [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('取消')),
          FilledButton(onPressed: _startDownload, child: const Text('开始下载')),
        ];
      case _DlState.downloading:
        return [
          TextButton(onPressed: _pause, child: const Text('暂停')),
        ];
      case _DlState.paused:
        return [
          TextButton(
            onPressed: () {
              // Delete partial file and close
              if (_savePath != null) {
                File(_savePath!).delete().catchError((_) => File(_savePath!));
              }
              Navigator.pop(context);
            },
            child: const Text('取消'),
          ),
          FilledButton(onPressed: _resume, child: const Text('继续下载')),
        ];
      case _DlState.done:
        return [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('关闭')),
          FilledButton(onPressed: _install, child: const Text('安装')),
        ];
      case _DlState.error:
        return [
          TextButton(onPressed: () => Navigator.pop(context), child: const Text('关闭')),
          FilledButton(onPressed: _startDownload, child: const Text('重试')),
        ];
    }
  }
}
