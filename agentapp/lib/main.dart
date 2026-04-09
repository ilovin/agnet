import 'dart:ui';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter/services.dart';

import 'app.dart';

void main() async {
  WidgetsFlutterBinding.ensureInitialized();

  // Explicitly load CJK font before first frame to ensure CanvasKit registers it
  final loader = FontLoader('Noto Sans SC');
  loader.addFont(rootBundle.load('fonts/NotoSansSC-Regular.ttf'));
  await loader.load();

  runApp(const ProviderScope(child: AgentApp()));
}
