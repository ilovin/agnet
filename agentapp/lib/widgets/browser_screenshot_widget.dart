import 'dart:async';
import 'dart:convert';
import 'dart:ui' as ui;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:webview_flutter/webview_flutter.dart';

/// A widget that allows users to browse a webpage and capture a screenshot.
/// The screenshot is returned as base64-encoded PNG data.
class BrowserScreenshotWidget extends StatefulWidget {
  final String? initialUrl;
  final Function(String base64Data, String mimeType) onScreenshotCaptured;

  const BrowserScreenshotWidget({
    super.key,
    this.initialUrl,
    required this.onScreenshotCaptured,
  });

  @override
  State<BrowserScreenshotWidget> createState() => _BrowserScreenshotWidgetState();
}

class _BrowserScreenshotWidgetState extends State<BrowserScreenshotWidget> {
  late final WebViewController _controller;
  final TextEditingController _urlController = TextEditingController();
  final GlobalKey _webviewKey = GlobalKey();
  bool _isLoading = true;
  bool _canGoBack = false;
  bool _canGoForward = false;
  double _progress = 0;

  @override
  void initState() {
    super.initState();
    _controller = WebViewController()
      ..setJavaScriptMode(JavaScriptMode.unrestricted)
      ..setBackgroundColor(const Color(0xFFFFFFFF))
      ..setNavigationDelegate(
        NavigationDelegate(
          onProgress: (int progress) {
            setState(() {
              _progress = progress / 100;
            });
          },
          onPageStarted: (String url) {
            setState(() {
              _isLoading = true;
              _urlController.text = url;
            });
          },
          onPageFinished: (String url) async {
            setState(() {
              _isLoading = false;
            });
            _updateNavigationState();
          },
          onWebResourceError: (WebResourceError error) {
            setState(() {
              _isLoading = false;
            });
          },
        ),
      );

    final initialUrl = widget.initialUrl ?? 'https://www.google.com';
    _urlController.text = initialUrl;
    _controller.loadRequest(Uri.parse(initialUrl));
  }

  Future<void> _updateNavigationState() async {
    final canGoBack = await _controller.canGoBack();
    final canGoForward = await _controller.canGoForward();
    setState(() {
      _canGoBack = canGoBack;
      _canGoForward = canGoForward;
    });
  }

  Future<void> _navigateToUrl() async {
    var url = _urlController.text.trim();
    if (url.isEmpty) return;
    
    // Add https:// if no scheme is present
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      url = 'https://$url';
    }
    
    await _controller.loadRequest(Uri.parse(url));
  }

  Future<void> _takeScreenshot() async {
    try {
      final renderObject = _webviewKey.currentContext?.findRenderObject();
      if (renderObject == null) {
        _showError('截图失败：无法找到渲染对象');
        return;
      }
      
      final boundary = renderObject as RenderRepaintBoundary;
      final image = await boundary.toImage(pixelRatio: 2.0);
      final byteData = await image.toByteData(format: ui.ImageByteFormat.png);
      
      if (byteData == null) {
        _showError('截图失败：无法获取图像数据');
        return;
      }
      
      final bytes = byteData.buffer.asUint8List();
      final b64 = base64Encode(bytes);
      
      widget.onScreenshotCaptured(b64, 'image/png');
      if (mounted) {
        Navigator.pop(context);
      }
    } catch (e) {
      _showError('截图失败：$e');
    }
  }

  void _showError(String message) {
    if (mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text(message)),
      );
    }
  }

  @override
  void dispose() {
    _urlController.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('浏览器截图'),
        actions: [
          IconButton(
            icon: const Icon(Icons.camera_alt),
            tooltip: '截图',
            onPressed: _isLoading ? null : _takeScreenshot,
          ),
        ],
      ),
      body: Column(
        children: [
          // URL bar
          Padding(
            padding: const EdgeInsets.all(8.0),
            child: Row(
              children: [
                IconButton(
                  icon: const Icon(Icons.arrow_back),
                  onPressed: _canGoBack ? () => _controller.goBack() : null,
                ),
                IconButton(
                  icon: const Icon(Icons.arrow_forward),
                  onPressed: _canGoForward ? () => _controller.goForward() : null,
                ),
                IconButton(
                  icon: const Icon(Icons.refresh),
                  onPressed: () => _controller.reload(),
                ),
                Expanded(
                  child: TextField(
                    controller: _urlController,
                    decoration: InputDecoration(
                      hintText: '输入网址',
                      suffixIcon: IconButton(
                        icon: const Icon(Icons.navigate_next),
                        onPressed: _navigateToUrl,
                      ),
                      border: const OutlineInputBorder(),
                      contentPadding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
                    ),
                    onSubmitted: (_) => _navigateToUrl(),
                  ),
                ),
              ],
            ),
          ),
          // Progress indicator
          if (_isLoading && _progress > 0 && _progress < 1)
            LinearProgressIndicator(value: _progress),
          // WebView
          Expanded(
            child: RepaintBoundary(
              key: _webviewKey,
              child: WebViewWidget(controller: _controller),
            ),
          ),
        ],
      ),
    );
  }
}

/// Shows the browser screenshot widget as a modal bottom sheet or dialog.
/// Returns the captured screenshot data.
Future<Map<String, String>?> showBrowserScreenshot(BuildContext context, {String? initialUrl}) async {
  final completer = Completer<Map<String, String>?>();
  
  await Navigator.push(
    context,
    MaterialPageRoute(
      builder: (context) => BrowserScreenshotWidget(
        initialUrl: initialUrl,
        onScreenshotCaptured: (base64Data, mimeType) {
          completer.complete({
            'data': base64Data,
            'mimeType': mimeType,
          });
        },
      ),
    ),
  );
  
  // If the user closes the browser without taking a screenshot, complete with null
  if (!completer.isCompleted) {
    completer.complete(null);
  }
  
  return completer.future;
}
