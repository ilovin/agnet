package ws

// sanitizeStatusLine replaces unicode characters that no agentapp-bundled
// font supports with ASCII equivalents, so they don't render as tofu (□)
// in the Flutter client.
//
// Background: agentapp ships with five font families (Noto Sans SC,
// Noto Sans Symbols 2, Noto Color Emoji, JetBrainsMono, Source Han
// Sans CN). Box-drawing characters from the U+2500..U+257F block, the
// carriage-return symbol U+21B5, and the TUI sub-tree mark U+23BF are
// missing from all five. They commonly appear in TUI status / progress
// lines that agentd captures from Claude or OpenCode and broadcasts as
// `conversation.message` events; the client renders them as tofu.
//
// We sanitize at the gateway boundary (rather than in agentd) for two
// reasons:
//
//  1. agentd may have other consumers (CLI, tests) that rely on the
//     raw byte stream; the gateway is the bridge to a UI that has the
//     font-fallback constraint.
//  2. Centralising the rule here makes it trivial to extend.
//
// Characters that ARE covered by the bundled fonts (arrows ↑↓←→ in
// Noto Sans SC, spinner stars ✻✳ in Symbols 2, emoji 🔧 in
// Color Emoji, all CJK in Noto Sans SC / Source Han Sans CN) are left
// untouched so that real content is preserved.
func sanitizeStatusLine(s string) string {
	if s == "" {
		return s
	}
	// Fast path: bail out if the string is pure ASCII. ASCII never
	// hits any of the replacements, and most status text is mixed CJK
	// or ASCII anyway, so this just avoids the rune-by-rune loop on
	// the common case.
	allASCII := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			allASCII = false
			break
		}
	}
	if allASCII {
		return s
	}

	// We can produce up to ~4 ASCII bytes per replaced rune (e.g. "<CR>"),
	// so size the builder generously to avoid re-allocation.
	var b []byte
	b = make([]byte, 0, len(s)+8)
	for _, r := range s {
		if rep, ok := statusLineReplacement(r); ok {
			b = append(b, rep...)
			continue
		}
		// Encode rune unchanged.
		// (utf8.AppendRune introduced in 1.18+; Go module here is 1.21+.)
		b = appendRune(b, r)
	}
	return string(b)
}

// statusLineReplacement returns the ASCII replacement for a rune that
// agentapp's bundled fonts cannot render, or (_, false) if the rune
// can be passed through unchanged.
func statusLineReplacement(r rune) (string, bool) {
	switch {
	// Box drawing: light/heavy horizontals → "-"
	case r == 0x2500 || r == 0x2501 ||
		r == 0x2504 || r == 0x2505 ||
		r == 0x2508 || r == 0x2509 ||
		r == 0x254C || r == 0x254D:
		return "-", true
	// Box drawing: light/heavy verticals → "|"
	case r == 0x2502 || r == 0x2503 ||
		r == 0x2506 || r == 0x2507 ||
		r == 0x250A || r == 0x250B ||
		r == 0x254E || r == 0x254F:
		return "|", true
	// Box drawing: light corners (┌┐└┘) and tees (├┤┬┴┼) → "+"
	case r >= 0x250C && r <= 0x254B:
		return "+", true
	// Box drawing: rounded corners (╭╮╯╰) and diagonals → "+"
	case r >= 0x256D && r <= 0x2573:
		return "+", true
	// Box drawing: double horizontals → "="
	case r == 0x2550 ||
		r == 0x2552 || r == 0x2555 ||
		r == 0x2558 || r == 0x255B ||
		r == 0x2564 || r == 0x2567:
		return "=", true
	// Box drawing: double vertical → "|"
	case r == 0x2551 ||
		r == 0x2553 || r == 0x2556 ||
		r == 0x2559 || r == 0x255C ||
		r == 0x2565 || r == 0x2568:
		return "|", true
	// Box drawing: double corners and tees → "+"
	case r == 0x2554 || r == 0x2557 ||
		r == 0x255A || r == 0x255D ||
		r == 0x2560 || r == 0x2563 ||
		r == 0x2566 || r == 0x2569 ||
		r == 0x256A || r == 0x256B || r == 0x256C:
		return "+", true
	// Carriage-return symbol → "<CR>"
	case r == 0x21B5:
		return "<CR>", true
	// TUI sub-tree mark (└── style) → "L"
	case r == 0x23BF:
		return "L", true
	// Record button symbol → filled circle
	case r == 0x23FA:
		return "●", true
	// Arrows — sometimes render as tofu in CanvasKit even though
	// Noto Sans SC claims coverage; sanitize to ASCII for safety.
	case r == 0x2190: // ←
		return "<-", true
	case r == 0x2191: // ↑
		return "^", true
	case r == 0x2192: // →
		return "->", true
	case r == 0x2193: // ↓
		return "v", true
	// Ballot box with X (U+2612 ☒) — Noto Sans SC claims coverage for
	// the Miscellaneous Symbols block (OS/2 ulUnicodeRange1 bit 29) but
	// does not actually contain this glyph. CanvasKit sees the range bit
	// and skips the fallback chain, rendering tofu. Sanitize to ASCII.
	case r == 0x2612: // ☒
		return "[X]", true
	}
	return "", false
}

// appendRune is a tiny helper so we don't depend on utf8.AppendRune,
// which makes this file easier to backport if ever needed.
func appendRune(b []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(b, byte(r))
	case r < 0x800:
		return append(b,
			byte(0xC0|(r>>6)),
			byte(0x80|(r&0x3F)))
	case r < 0x10000:
		// Surrogate halves get encoded as the U+FFFD replacement char to
		// avoid producing invalid UTF-8.
		if r >= 0xD800 && r <= 0xDFFF {
			return append(b, 0xEF, 0xBF, 0xBD)
		}
		return append(b,
			byte(0xE0|(r>>12)),
			byte(0x80|((r>>6)&0x3F)),
			byte(0x80|(r&0x3F)))
	default:
		if r > 0x10FFFF {
			return append(b, 0xEF, 0xBF, 0xBD)
		}
		return append(b,
			byte(0xF0|(r>>18)),
			byte(0x80|((r>>12)&0x3F)),
			byte(0x80|((r>>6)&0x3F)),
			byte(0x80|(r&0x3F)))
	}
}

// sanitizedTextEvents is the set of broadcast event method names whose
// `params.text` field carries TUI-captured content that may include
// box-drawing / control glyphs. Other event types (status changes,
// permission updates, etc.) carry structured data and are passed
// through unchanged.
var sanitizedTextEvents = map[string]struct{}{
	"conversation.message":        {},
	"conversation.message_update": {},
}

// SanitizeEventInPlace mutates a node-push event in place, rewriting
// `params.text` (when present and a string) for the event types listed
// in sanitizedTextEvents. All other fields, and all other event types,
// are left untouched.
//
// Safe to call with a nil event or with a non-map params payload — both
// are no-ops.
//
// Exported so that the gateway main loop (cmd/agentgw) can invoke it
// inside its OnEvent callback right before broadcasting downstream.
func SanitizeEventInPlace(ev map[string]any) {
	sanitizeEventInPlace(ev)
}

// sanitizeEventInPlace is the package-private implementation. The
// exported wrapper `SanitizeEventInPlace` is the public boundary; the
// package-internal name keeps existing tests stable.
func sanitizeEventInPlace(ev map[string]any) {
	if ev == nil {
		return
	}
	method, _ := ev["method"].(string)
	if _, ok := sanitizedTextEvents[method]; !ok {
		return
	}
	params, ok := ev["params"].(map[string]any)
	if !ok {
		return
	}
	if text, ok := params["text"].(string); ok {
		params["text"] = sanitizeStatusLine(text)
	}
}
