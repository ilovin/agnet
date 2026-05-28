import 'package:flutter/material.dart';

import '../theme/app_text_styles.dart';

/// Color scheme for syntax highlighting — One Dark (dark) and GitHub Light (light).
class _Dark {
  static const keyword = Color(0xFFC678DD);
  static const string = Color(0xFF98C379);
  static const number = Color(0xFFD19A66);
  static const comment = Color(0xFF5C6370);
  static const type = Color(0xFFE5C07B);
  static const operator = Color(0xFF56B6C2);
  static const punctuation = Color(0xFFABB2BF);
}

// GitHub Light — exact Primer colors
class _Light {
  static const keyword = Color(0xFFCF222E);    // red
  static const string = Color(0xFF0A3069);      // dark blue
  static const number = Color(0xFF0550AE);      // blue
  static const comment = Color(0xFF6E7781);      // gray
  static const type = Color(0xFF8250DF);         // purple
  static const operator = Color(0xFFCF222E);      // red
  static const punctuation = Color(0xFF24292F);   // near-black
}

/// Applies syntax highlighting to source code.
TextSpan highlightCode(String code, {String? language, required bool isDark}) {
  final spans = <TextSpan>[];
  final tokenRegex = RegExp(
    r"(//.*?$|/\*[\s\S]*?\*/)"
    r"|('(?:[^'\\]|\\.)*'"
    r'|"(?:[^"\\]|\\.)*"'
    r'|`(?:[^`\\]|\\.)*`'
    r"|'''[\s\S]*?'''"
    r')'
    r"|(\b(?:abstract|and|as|assert|async|await|break|case|catch|class|const|continue|default|deferred|do|done|dynamic|elif|else|enum|except|export|extends|extension|factory|false|final|finally|for|from|func|function|get|go|if|implements|import|in|interface|is|late|let|match|module|mut|new|nil|null|operator|or|override|package|pass|private|protocol|public|raise|readonly|ref|required|return|self|set|static|struct|super|switch|sync|throw|trait|true|try|type|typedef|union|var|void|when|where|while|with|yield)\b)"
    r"|(\b(?:bool|byte|char|double|float|int|long|num|rune|short|string|uint|uintptr|Map|List|Set|Object|String|Exception|Error|Future|Stream|Iterable|Duration|DateTime|Type|any|Any)\b)"
    r"|(\b\d+\.?\d*(?:e[+-]?\d+)?\b)"
    r"|([+\-*/%=<>!&|^~?:]+)"
    r"|([{}()\[\];,.])"
    ,
    multiLine: true,
  );

  int lastEnd = 0;
  final baseColor = isDark ? const Color(0xFFABB2BF) : const Color(0xFF1F2328);
  final baseStyle = TextStyle(
    fontFamily: 'Noto Sans SC',
    fontFamilyFallback: AppTextStyles.fontFamilyFallback,
    color: baseColor,
  );

  for (final match in tokenRegex.allMatches(code)) {
    if (match.start > lastEnd) {
      spans.add(TextSpan(text: code.substring(lastEnd, match.start), style: baseStyle));
    }

    Color? color;
    if (match[1] != null) {
      color = isDark ? _Dark.comment : _Light.comment;
    } else if (match[2] != null) {
      color = isDark ? _Dark.string : _Light.string;
    } else if (match[3] != null) {
      color = isDark ? _Dark.keyword : _Light.keyword;
    } else if (match[4] != null) {
      color = isDark ? _Dark.type : _Light.type;
    } else if (match[5] != null) {
      color = isDark ? _Dark.number : _Light.number;
    } else if (match[6] != null) {
      color = isDark ? _Dark.operator : _Light.operator;
    } else if (match[7] != null) {
      color = isDark ? _Dark.punctuation : _Light.punctuation;
    }

    spans.add(TextSpan(
      text: match[0],
      style: baseStyle.copyWith(color: color),
    ));
    lastEnd = match.end;
  }

  if (lastEnd < code.length) {
    spans.add(TextSpan(text: code.substring(lastEnd), style: baseStyle));
  }

  return TextSpan(children: spans);
}
