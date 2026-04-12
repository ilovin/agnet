import 'dart:async';
import 'dart:io';

/// Progress info for APK download.
class DownloadProgress {
  final int received;
  final int total; // -1 if unknown
  final double speed; // bytes/sec

  DownloadProgress({required this.received, required this.total, required this.speed});

  double get fraction => total > 0 ? received / total : 0;
  bool get known => total > 0;
}

/// Downloads an APK from [url] to [savePath] with progress, resume, and cancel.
///
/// - Resume: if [savePath] already has partial data, sends Range header.
/// - Cancel: call [cancel] on the returned controller.
/// - Progress: listen to [onProgress].
class ApkDownloader {
  final String url;
  final String savePath;
  final void Function(DownloadProgress progress)? onProgress;

  HttpClient? _client;
  bool _cancelled = false;

  ApkDownloader({required this.url, required this.savePath, this.onProgress});

  /// Cancel the in-progress download.
  void cancel() {
    _cancelled = true;
    _client?.close(force: true);
    _client = null;
  }

  /// Start (or resume) the download. Returns the final file path on success.
  /// Throws on error or cancellation.
  Future<String> download() async {
    final file = File(savePath);
    int existingBytes = 0;
    if (await file.exists()) {
      existingBytes = await file.length();
    }

    _client = HttpClient();
    _client!.connectionTimeout = const Duration(seconds: 15);

    try {
      final request = await _client!.getUrl(Uri.parse(url));
      if (existingBytes > 0) {
        request.headers.set(HttpHeaders.rangeHeader, 'bytes=$existingBytes-');
      }

      final response = await request.close();

      // If server doesn't support range or file changed, start over
      if (response.statusCode == 200) {
        existingBytes = 0;
        // Truncate existing file
        if (await file.exists()) await file.delete();
      } else if (response.statusCode == 206) {
        // Partial content — resume
      } else if (response.statusCode == 416) {
        // Range not satisfiable — file already complete, re-download
        existingBytes = 0;
        if (await file.exists()) await file.delete();
        // Retry without range
        _client?.close(force: true);
        _client = HttpClient();
        final retry = await _client!.getUrl(Uri.parse(url));
        final retryResp = await retry.close();
        return _streamToFile(retryResp, file, 0, _contentLength(retryResp, 0));
      } else {
        throw HttpException('HTTP ${response.statusCode}');
      }

      final totalBytes = _contentLength(response, existingBytes);
      return _streamToFile(response, file, existingBytes, totalBytes);
    } catch (e) {
      if (_cancelled) throw Exception('下载已取消');
      rethrow;
    } finally {
      _client?.close();
      _client = null;
    }
  }

  int _contentLength(HttpClientResponse resp, int existing) {
    final cl = resp.contentLength;
    if (cl > 0) return existing + cl;
    return -1;
  }

  Future<String> _streamToFile(
    HttpClientResponse response,
    File file,
    int existingBytes,
    int totalBytes,
  ) async {
    final sink = file.openWrite(mode: existingBytes > 0 ? FileMode.append : FileMode.write);
    int received = existingBytes;
    final sw = Stopwatch()..start();
    int lastSpeedBytes = existingBytes;
    double speed = 0;

    try {
      await for (final chunk in response) {
        if (_cancelled) {
          await sink.flush();
          await sink.close();
          throw Exception('下载已取消');
        }
        sink.add(chunk);
        received += chunk.length;

        // Calculate speed every 500ms
        if (sw.elapsedMilliseconds > 500) {
          speed = (received - lastSpeedBytes) / (sw.elapsedMilliseconds / 1000);
          lastSpeedBytes = received;
          sw.reset();
        }

        onProgress?.call(DownloadProgress(
          received: received,
          total: totalBytes,
          speed: speed,
        ));
      }
      await sink.flush();
      await sink.close();
      return file.path;
    } catch (e) {
      await sink.flush();
      await sink.close();
      rethrow;
    }
  }
}
