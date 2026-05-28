import 'package:flutter/material.dart';

class _AnsiStyle {
  Color? foreground;
  Color? background;
  bool bold;
  bool dim;
  bool italic;
  bool underline;
  bool strikethrough;
  bool reverse;

  _AnsiStyle()
      : bold = false,
        dim = false,
        italic = false,
        underline = false,
        strikethrough = false,
        reverse = false;

  _AnsiStyle copy() => _AnsiStyle()
    ..foreground = foreground
    ..background = background
    ..bold = bold
    ..dim = dim
    ..italic = italic
    ..underline = underline
    ..strikethrough = strikethrough
    ..reverse = reverse;

  void reset() {
    foreground = null;
    background = null;
    bold = false;
    dim = false;
    italic = false;
    underline = false;
    strikethrough = false;
    reverse = false;
  }
}

// VS Code-style terminal colors
const _standardColors = [
  Color(0xFF000000), // 0 black
  Color(0xFFCD3131), // 1 red
  Color(0xFF0DBC79), // 2 green
  Color(0xFFE5E510), // 3 yellow
  Color(0xFF2472C8), // 4 blue
  Color(0xFFBC3FBC), // 5 magenta
  Color(0xFF11A8CD), // 6 cyan
  Color(0xFFE5E5E5), // 7 white (light gray)
  Color(0xFF666666), // 8 bright black
  Color(0xFFF14C4C), // 9 bright red
  Color(0xFF23D18B), // 10 bright green
  Color(0xFFF5F543), // 11 bright yellow
  Color(0xFF3B8EEA), // 12 bright blue
  Color(0xFFD670D6), // 13 bright magenta
  Color(0xFF29B8DB), // 14 bright cyan
  Color(0xFFFFFFFF), // 15 bright white
];

Color _color256(int n) {
  if (n < 16) return _standardColors[n];
  if (n < 232) {
    // 6x6x6 color cube (16-231)
    final v = n - 16;
    final b = v % 6;
    final g = (v ~/ 6) % 6;
    final r = v ~/ 36;
    return Color.fromARGB(
      255,
      r == 0 ? 0 : r * 40 + 55,
      g == 0 ? 0 : g * 40 + 55,
      b == 0 ? 0 : b * 40 + 55,
    );
  }
  // Grayscale ramp (232-255)
  final v = 8 + (n - 232) * 10;
  return Color.fromARGB(255, v, v, v);
}

/// Parses a string containing ANSI SGR escape sequences into a list of
/// [TextSpan]s with appropriate colors and styles.
///
/// Non-SGR sequences (cursor movement, etc.) are stripped.
/// Terminal control characters (CR, backspace, BEL) are processed.
TextSpan parseAnsiToSpan(
  String input, {
  Color? defaultColor,
  double fontSize = 14,
  String fontFamily = 'Noto Sans SC',
}) {
  final spans = <TextSpan>[];
  final buf = StringBuffer();
  final style = _AnsiStyle();
  final defaultFg = defaultColor ?? const Color(0xFFE5E5E5);

  void flush() {
    if (buf.isEmpty) return;
    final text = buf.toString();
    buf.clear();

    Color fg = style.foreground ?? defaultFg;
    Color? bg = style.background;

    if (style.reverse) {
      final tmp = fg;
      fg = bg ?? const Color(0xFF000000);
      bg = tmp;
    }
    if (style.dim) {
      fg = Color.fromARGB(
        (fg.a * 0.6 * 255).round().clamp(0, 255),
        (fg.r * 255).round().clamp(0, 255),
        (fg.g * 255).round().clamp(0, 255),
        (fg.b * 255).round().clamp(0, 255),
      );
    }

    spans.add(TextSpan(
      text: text,
      style: TextStyle(
        color: fg,
        backgroundColor: bg,
        fontFamily: fontFamily,
        fontFamilyFallback: const [
          'Noto Sans SC',
          'Noto Sans Symbols 2',
          'Noto Color Emoji',
          'PingFang SC',
          'Microsoft YaHei',
          'sans-serif',
        ],
        fontSize: fontSize,
        height: 1.4,
        fontWeight: style.bold ? FontWeight.bold : FontWeight.normal,
        fontStyle: style.italic ? FontStyle.italic : FontStyle.normal,
        decoration: TextDecoration.combine([
          if (style.underline) TextDecoration.underline,
          if (style.strikethrough) TextDecoration.lineThrough,
        ]),
      ),
    ));
  }

  int i = 0;
  while (i < input.length) {
    final char = input[i];
    final code = char.codeUnitAt(0);

    // ESC sequence
    if (code == 0x1B) {
      i++;
      if (i >= input.length) break;

      final next = input[i];
      if (next == '[') {
        // CSI sequence: collect params + final byte
        i++;
        final paramBuf = StringBuffer();
        while (i < input.length) {
          final c = input[i];
          final cv = c.codeUnitAt(0);
          if (cv >= 0x30 && cv <= 0x3F) {
            // digit or ; or ? or >
            paramBuf.write(c);
            i++;
          } else if (cv >= 0x20 && cv <= 0x2F) {
            // intermediate bytes (space, !, ", etc.)
            i++;
          } else if (cv >= 0x40 && cv <= 0x7E) {
            // final byte
            if (c == 'm') {
              // SGR — process color/style
              _applySgr(paramBuf.toString(), style);
            }
            // else: non-SGR CSI (cursor move etc.) — skip
            i++;
            break;
          } else {
            i++;
            break;
          }
        }
        continue;
      } else if (next == ']' ) {
        // OSC sequence — skip until BEL or ST
        i++;
        while (i < input.length) {
          if (input[i].codeUnitAt(0) == 0x07) {
            i++;
            break;
          }
          if (input[i] == '\x1B' && i + 1 < input.length && input[i + 1] == '\\') {
            i += 2;
            break;
          }
          i++;
        }
        continue;
      } else {
        // ESC + single char (>, =, <, O, N, etc.) — skip
        i++;
        continue;
      }
    }

    // Handle control characters
    if (code == 0x08) {
      // Backspace — remove last char from buffer
      if (buf.isNotEmpty) {
        final s = buf.toString();
        buf.clear();
        buf.write(s.substring(0, s.length - 1));
      }
      i++;
      continue;
    }
    if (code == 0x0D) {
      // CR — treat as newline unless followed by LF
      if (i + 1 < input.length && input[i + 1].codeUnitAt(0) == 0x0A) {
        i += 2;
        buf.write('\n');
        continue;
      }
      i++;
      buf.write('\n');
      continue;
    }
    if (code == 0x00 || code == 0x07) {
      i++;
      continue;
    }
    if (code < 0x20 && code != 0x0A && code != 0x09) {
      i++;
      continue;
    }

    // Printable character — accumulate
    buf.write(char);
    i++;
  }

  flush();
  return TextSpan(children: spans);
}

void _applySgr(String params, _AnsiStyle style) {
  if (params.isEmpty || params == '0') {
    style.reset();
    return;
  }

  // Split params on semicolons, filter out empty and '?'-prefixed
  final parts = params.split(';');
  final nums = <int>[];
  for (final p in parts) {
    final cleaned = p.replaceAll(RegExp(r'[?>]'), '');
    if (cleaned.isEmpty) {
      nums.add(0);
    } else {
      nums.add(int.tryParse(cleaned) ?? 0);
    }
  }

  int j = 0;
  while (j < nums.length) {
    final n = nums[j];
    switch (n) {
      case 0:
        style.reset();
      case 1:
        style.bold = true;
      case 2:
        style.dim = true;
      case 3:
        style.italic = true;
      case 4:
        style.underline = true;
      case 7:
        style.reverse = true;
      case 9:
        style.strikethrough = true;
      case 22:
        style.bold = false;
        style.dim = false;
      case 23:
        style.italic = false;
      case 24:
        style.underline = false;
      case 27:
        style.reverse = false;
      case 29:
        style.strikethrough = false;
      case _ when n >= 30 && n <= 37:
        style.foreground = _standardColors[n - 30];
      case 38:
        // Extended foreground
        j = _parseExtendedColor(nums, j + 1, style, true);
      case 39:
        style.foreground = null; // default
      case _ when n >= 40 && n <= 47:
        style.background = _standardColors[n - 40];
      case 48:
        // Extended background
        j = _parseExtendedColor(nums, j + 1, style, false);
      case 49:
        style.background = null; // default
      case _ when n >= 90 && n <= 97:
        style.foreground = _standardColors[n - 90 + 8];
      case _ when n >= 100 && n <= 107:
        style.background = _standardColors[n - 100 + 8];
    }
    j++;
  }
}

/// Parse extended color (256-color or true-color) starting at index [start].
/// Returns the new index (after consuming color params).
int _parseExtendedColor(List<int> nums, int start, _AnsiStyle style, bool fg) {
  if (start >= nums.length) return start;
  if (nums[start] == 5 && start + 1 < nums.length) {
    // 256-color: 38;5;N or 48;5;N
    final c = _color256(nums[start + 1]);
    if (fg) {
      style.foreground = c;
    } else {
      style.background = c;
    }
    return start + 2; // skip 5 and N (the caller's j++ makes it +3 total)
  }
  if (nums[start] == 2 && start + 3 < nums.length) {
    // True-color: 38;2;R;G;B or 48;2;R;G;B
    final c = Color.fromARGB(255, nums[start + 1], nums[start + 2], nums[start + 3]);
    if (fg) {
      style.foreground = c;
    } else {
      style.background = c;
    }
    return start + 4; // skip 2, R, G, B
  }
  return start;
}
