package ws

import (
	"strings"
	"testing"
)

// TestSanitizeStatusLineRunes guards a regression:
//
// agentd captures TUI status / progress lines (Claude / OpenCode) that
// contain characters not covered by any of agentapp's bundled fonts:
//
//   - Box drawing horizontals/verticals/corners (U+2500..U+257F)
//   - Carriage-return symbol "↵" (U+21B5)
//   - Misc TUI marks (e.g. "⎿" U+23BF)
//
// Because agentapp's font fallback chain (Noto Sans SC / Symbols 2 /
// Color Emoji / JetBrainsMono / SourceHanSansCN) does NOT cover these
// codepoints, they render as tofu (□) on Chrome.
//
// The fix lives in agentgw's broadcast forwarder: before relaying a
// node push event downstream, we sanitize "text" fields in
// `conversation.message` payloads, replacing the unsupported characters
// with ASCII equivalents that every font supports.
//
// Spinner / box-drawing characters that CAN render (e.g. emoji 🔧, or
// arrows ↑↓←→ on Noto Sans SC) MUST be left intact; we never strip
// content that already paints correctly.
func TestSanitizeStatusLineRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "box-drawing horizontal becomes dash",
			in:   "─── status ───",
			want: "--- status ---",
		},
		{
			name: "box-drawing thick horizontal becomes dash",
			in:   "━━━ go ━━━",
			want: "--- go ---",
		},
		{
			name: "box-drawing vertical becomes pipe",
			in:   "│ left │ right │",
			want: "| left | right |",
		},
		{
			name: "box-drawing corners become plus",
			in:   "┌─┐\n│ │\n└─┘",
			want: "+-+\n| |\n+-+",
		},
		{
			name: "rounded corners become plus",
			in:   "╭─╮\n│ │\n╰─╯",
			want: "+-+\n| |\n+-+",
		},
		{
			name: "double box-drawing becomes equivalents",
			in:   "╔═╗║ hi ║╚═╝",
			want: "+=+| hi |+=+",
		},
		{
			name: "carriage-return symbol becomes lf token",
			in:   "press↵ to send",
			want: "press<CR> to send",
		},
		{
			name: "tui sub-tree mark becomes ASCII L",
			in:   "  ⎿ found 3 files",
			want: "  L found 3 files",
		},
		{
			name: "leaves arrow keys intact (covered by Noto Sans SC)",
			in:   "↑↓←→",
			want: "↑↓←→",
		},
		{
			name: "leaves emoji intact",
			in:   "🔧 Bash: ls",
			want: "🔧 Bash: ls",
		},
		{
			name: "leaves spinner stars intact (covered by Symbols 2)",
			in:   "✻ Sautéing…",
			want: "✻ Sautéing…",
		},
		{
			name: "leaves Chinese intact",
			in:   "状态：运行中",
			want: "状态：运行中",
		},
		{
			name: "empty string passes through",
			in:   "",
			want: "",
		},
		{
			name: "ascii passes through unchanged",
			in:   "hello world 123",
			want: "hello world 123",
		},
		{
			name: "mixed real-world status line",
			in:   "╭─ Bash ─╮\n│ ls ↵  │\n╰───────╯",
			want: "+- Bash -+\n| ls <CR>  |\n+-------+",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeStatusLine(tc.in)
			if got != tc.want {
				t.Fatalf("sanitizeStatusLine(%q)\n  got  = %q\n  want = %q",
					tc.in, got, tc.want)
			}
		})
	}
}

// TestSanitizeEventConversationMessageText verifies the wrapper that
// walks an RPC event params map and rewrites the `text` field for
// `conversation.message` events. Other event types must be untouched.
func TestSanitizeEventConversationMessageText(t *testing.T) {
	t.Run("rewrites text for conversation.message", func(t *testing.T) {
		ev := map[string]any{
			"method": "conversation.message",
			"params": map[string]any{
				"agentId": "ag1",
				"role":    "assistant",
				"text":    "─── progress ───",
			},
		}
		sanitizeEventInPlace(ev)

		params, ok := ev["params"].(map[string]any)
		if !ok {
			t.Fatalf("expected params map, got %T", ev["params"])
		}
		got, _ := params["text"].(string)
		want := "--- progress ---"
		if got != want {
			t.Fatalf("text = %q, want %q", got, want)
		}
	})

	t.Run("leaves non-conversation events untouched", func(t *testing.T) {
		ev := map[string]any{
			"method": "agent.status_changed",
			"params": map[string]any{
				"agentId": "ag1",
				"status":  "working",
				// even if the payload happened to contain a box-drawing
				// rune, we should NOT mutate non-conversation events.
				"note": "─ keep me ─",
			},
		}
		sanitizeEventInPlace(ev)

		params := ev["params"].(map[string]any)
		got, _ := params["note"].(string)
		want := "─ keep me ─"
		if got != want {
			t.Fatalf("note = %q, want %q (must not be sanitized)", got, want)
		}
	})

	t.Run("conversation.message_update is also sanitized", func(t *testing.T) {
		ev := map[string]any{
			"method": "conversation.message_update",
			"params": map[string]any{
				"agentId": "ag1",
				"text":    "press↵",
			},
		}
		sanitizeEventInPlace(ev)
		params := ev["params"].(map[string]any)
		got, _ := params["text"].(string)
		if !strings.Contains(got, "<CR>") {
			t.Fatalf("expected sanitized text containing <CR>, got %q", got)
		}
	})

	t.Run("missing or non-string text is a no-op", func(t *testing.T) {
		ev := map[string]any{
			"method": "conversation.message",
			"params": map[string]any{
				"agentId": "ag1",
				// no text field
			},
		}
		// must not panic
		sanitizeEventInPlace(ev)
	})

	t.Run("nil event is a no-op", func(t *testing.T) {
		// must not panic
		sanitizeEventInPlace(nil)
	})

	t.Run("non-map params is a no-op", func(t *testing.T) {
		ev := map[string]any{
			"method": "conversation.message",
			"params": "not a map",
		}
		// must not panic, must leave params untouched
		sanitizeEventInPlace(ev)
		if ev["params"] != "not a map" {
			t.Fatalf("params got mutated: %v", ev["params"])
		}
	})
}
