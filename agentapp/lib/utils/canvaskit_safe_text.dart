/// Runtime CanvasKit-safe rewrites for runes that bundled fonts claim to
/// cover but actually render as `.notdef` tofu under Flutter Web CanvasKit.
///
/// Background: agentapp ships `Noto Sans SC` as the primary CJK + Latin
/// family. Its OS/2 `ulUnicodeRange1` advertises coverage for the
/// "Arrows" (U+2190..U+21FF) and "Miscellaneous Symbols" blocks even
/// though several individual glyphs in those blocks are missing. CanvasKit
/// reads the OS/2 bitmap to decide whether the family supplies a code
/// point and short-circuits the `fontFamilyFallback` chain when it
/// believes the primary covers it. The result: `→`, `←`, `↑`, `↓`, `☒`,
/// etc. render as tofu boxes — even though `Noto Sans Symbols 2` (which
/// IS in the fallback chain) carries the glyph.
///
/// The agentgw [`ws.SanitizeEventInPlace`] already rewrites these runes
/// for the live broadcast path (`conversation.message` /
/// `conversation.message_update`). Conversation history loaded via
/// `conversation.history` is proxied through agentgw without
/// sanitization, so a user re-opening an old session still sees tofu.
///
/// Rather than thread sanitization through every history path on the
/// gateway side, this helper performs the same rewrite at the Flutter
/// rendering boundary. The rewrite mirrors `agentgw/internal/ws/sanitize.go`
/// 1:1 so the user-visible text is identical regardless of which path
/// (live broadcast or history replay) it travelled through.
library;

/// Replace runes that CanvasKit cannot render reliably with their
/// gateway-equivalent ASCII fallback. Returns the input unchanged when
/// no replacement applies (fast path) so this is safe to call on every
/// markdown render without measurable cost on the common case.
///
/// Keep this list in sync with `statusLineReplacement` in
/// `agentgw/internal/ws/sanitize.go`. The gateway is still considered
/// authoritative for live messages; this helper is the second line of
/// defense for replayed history and any other text that bypasses the
/// gateway.
String canvaskitSafeText(String input) {
  if (input.isEmpty) return input;

  // Fast path: bail out if the string has no characters above 0x7F.
  // Pure ASCII never needs rewriting.
  var needsRewrite = false;
  for (var i = 0; i < input.length; i++) {
    if (input.codeUnitAt(i) >= 0x80) {
      needsRewrite = true;
      break;
    }
  }
  if (!needsRewrite) return input;

  final buf = StringBuffer();
  for (final rune in input.runes) {
    final replacement = _replacement(rune);
    if (replacement != null) {
      buf.write(replacement);
    } else {
      buf.writeCharCode(rune);
    }
  }
  return buf.toString();
}

String? _replacement(int rune) {
  switch (rune) {
    // Arrows — Noto Sans SC claims coverage but CanvasKit renders tofu.
    case 0x2190:
      return '<-';
    case 0x2191:
      return '^';
    case 0x2192:
      return '->';
    case 0x2193:
      return 'v';
    // Ballot box with X — same bogus coverage situation.
    case 0x2612:
      return '[X]';
  }
  return null;
}
