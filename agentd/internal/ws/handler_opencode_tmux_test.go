package ws

import (
	"strings"
	"testing"
)

func TestDiffPaneContentSimpleAppend(t *testing.T) {
	old := "Hello world"
	newContent := "Hello world\nThis is new"
	got := diffPaneContent(old, newContent)
	want := "This is new"
	if got != want {
		t.Fatalf("diffPaneContent(%q, %q) = %q, want %q", old, newContent, got, want)
	}
}

func TestDiffPaneContentOldInsideNew(t *testing.T) {
	old := "middle"
	newContent := "prefixmiddle\nsuffix"
	got := diffPaneContent(old, newContent)
	want := "prefixmiddle\nsuffix"
	if got != want {
		t.Fatalf("diffPaneContent(%q, %q) = %q, want %q", old, newContent, got, want)
	}
}

func TestDiffPaneContentEmptyOld(t *testing.T) {
	got := diffPaneContent("", "all new")
	if got != "all new" {
		t.Fatalf("diffPaneContent(\"\", \"all new\") = %q, want \"all new\"", got)
	}
}

func TestDiffPaneContentNoChange(t *testing.T) {
	old := "same"
	got := diffPaneContent(old, "same")
	if got != "" {
		t.Fatalf("diffPaneContent(\"same\", \"same\") = %q, want empty", got)
	}
}

func TestDiffPaneContentScrolled(t *testing.T) {
	// Simulate terminal scroll: old content starts with scrolled-out lines,
	// but shares a suffix with the start of new content.
	old := "line1\nline2\nline3"
	newContent := "line2\nline3\nline4"
	got := diffPaneContent(old, newContent)
	want := "line2\nline3\nline4"
	if got != want {
		t.Fatalf("diffPaneContent scrolled = %q, want %q", got, want)
	}
}

func TestDiffPaneContentANSIReRender(t *testing.T) {
	// Simulate TUI re-render where ANSI sequences change but visible text is identical.
	old := "\x1b[31mHello world\x1b[0m"
	newContent := "\x1b[32mHello world\x1b[0m"
	got := diffPaneContent(old, newContent)
	if got != "" {
		t.Fatalf("diffPaneContent ANSI re-render = %q, want empty", got)
	}
}

func TestDiffPaneContentANSINewLine(t *testing.T) {
	// New line appended with different ANSI styling; should return only the new line.
	old := "\x1b[31mHello world\x1b[0m"
	newContent := "\x1b[31mHello world\x1b[0m\n\x1b[32mNew line\x1b[0m"
	got := diffPaneContent(old, newContent)
	want := "\x1b[32mNew line\x1b[0m"
	if got != want {
		t.Fatalf("diffPaneContent ANSI new line = %q, want %q", got, want)
	}
}

func TestDiffPaneContentANSIScrolled(t *testing.T) {
	// Terminal scroll with ANSI codes on shared suffix lines.
	old := "line1\n\x1b[31mline2\x1b[0m\nline3"
	newContent := "\x1b[31mline2\x1b[0m\nline3\n\x1b[32mline4\x1b[0m"
	got := diffPaneContent(old, newContent)
	want := "\x1b[31mline2\x1b[0m\nline3\n\x1b[32mline4\x1b[0m"
	if got != want {
		t.Fatalf("diffPaneContent ANSI scrolled = %q, want %q", got, want)
	}
}

func TestDiffPaneContentMultiLineANSISame(t *testing.T) {
	// Multi-line content where ANSI changes on every line but text is identical.
	old := "\x1b[31mline1\x1b[0m\n\x1b[31mline2\x1b[0m"
	newContent := "\x1b[32mline1\x1b[0m\n\x1b[32mline2\x1b[0m"
	got := diffPaneContent(old, newContent)
	if got != "" {
		t.Fatalf("diffPaneContent multi-line ANSI same = %q, want empty", got)
	}
}

func TestDiffPaneContentPartialSuffixMatch(t *testing.T) {
	// Only the last line is shared; first lines differ.
	old := "aaa\nbbb\nccc"
	newContent := "xxx\nbbb\nccc"
	got := diffPaneContent(old, newContent)
	want := "xxx"
	if got != want {
		t.Fatalf("diffPaneContent partial suffix = %q, want %q", got, want)
	}
}

func TestStripANSI(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"\x1b[31mred\x1b[0m", "red"},
		{"\x1b[0;1;32mbold green\x1b[0m", "bold green"},
		{"no ansi", "no ansi"},
		{"\x1b[2Kclear line", "clear line"},
		{"mixed \x1b[31mred\x1b[0m text", "mixed red text"},
	}
	for _, c := range cases {
		got := stripANSI(c.in)
		if got != c.want {
			t.Fatalf("stripANSI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStripUserMessageFromBaseline(t *testing.T) {
	baseline := "prompt> hello world"
	got := stripUserMessageFromBaseline(baseline, "hello world")
	want := "prompt> "
	if got != want {
		t.Fatalf("stripUserMessageFromBaseline = %q, want %q", got, want)
	}
}

func TestStripUserMessageNotFound(t *testing.T) {
	baseline := "some content"
	got := stripUserMessageFromBaseline(baseline, "missing")
	if got != baseline {
		t.Fatalf("stripUserMessageFromBaseline = %q, want %q", got, baseline)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("truncate(\"short\", 10) = %q, want \"short\"", got)
	}
	if got := truncate("this is a long string", 5); got != "this ..." {
		t.Fatalf("truncate(\"this is a long string\", 5) = %q, want \"this ...\"", got)
	}
}

func TestDiffPaneContentSpinnerOnly(t *testing.T) {
	// Simulate 62-line pane where only the last line (spinner) changes.
	oldLines := make([]string, 62)
	newLines := make([]string, 62)
	for i := 0; i < 61; i++ {
		oldLines[i] = "line content"
		newLines[i] = "line content"
	}
	oldLines[61] = "   \u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d  esc interrupt                   tab agents  ctrl+p commands"
	newLines[61] = "   \u25a0\u25a0\u25a0\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d  esc interrupt                   tab agents  ctrl+p commands"

	old := strings.Join(oldLines, "\n")
	newContent := strings.Join(newLines, "\n")
	got := diffPaneContent(old, newContent)
	if got != "" {
		t.Fatalf("diffPaneContent spinner only = %q, want empty", got)
	}
}

func TestDiffPaneContentSingleLineDiffMiddle(t *testing.T) {
	old := "line1\nline2\nline3\nline4\nline5"
	newContent := "line1\nline2\nCHANGED\nline4\nline5"
	got := diffPaneContent(old, newContent)
	want := "CHANGED"
	if got != want {
		t.Fatalf("diffPaneContent single line diff middle = %q, want %q", got, want)
	}
}

func TestDiffPaneContentBottomAppend(t *testing.T) {
	old := "line1\nline2\nline3"
	newContent := "line1\nline2\nline3\nline4\nline5"
	got := diffPaneContent(old, newContent)
	want := "line4\nline5"
	if got != want {
		t.Fatalf("diffPaneContent bottom append = %q, want %q", got, want)
	}
}

func TestDiffPaneContentNoChangeMultiLine(t *testing.T) {
	old := "line1\nline2\nline3"
	got := diffPaneContent(old, "line1\nline2\nline3")
	if got != "" {
		t.Fatalf("diffPaneContent no change multi-line = %q, want empty", got)
	}
}

func TestDiffPaneContentAllSameExceptSpinner(t *testing.T) {
	old := "header\nbody line 1\nbody line 2\n   \u2b9d\u2b9d\u2b9d  esc interrupt"
	newContent := "header\nbody line 1\nbody line 2\n   \u25a0\u25a0\u25a0  esc interrupt"
	got := diffPaneContent(old, newContent)
	if got != "" {
		t.Fatalf("diffPaneContent all same except spinner = %q, want empty", got)
	}
}

func TestDiffPaneContentMixedSpinnerAndReal(t *testing.T) {
	old := "header\nbody line 1\n   \u2b9d\u2b9d\u2b9d  esc interrupt"
	newContent := "header\nbody line 1\nnew real text\n   \u25a0\u25a0\u25a0  esc interrupt"
	got := diffPaneContent(old, newContent)
	want := "new real text"
	if got != want {
		t.Fatalf("diffPaneContent mixed spinner and real = %q, want %q", got, want)
	}
}

func TestIsSpinnerLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"   \u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d  esc interrupt                   tab agents  ctrl+p commands", true},
		{"\u25a0\u25a0\u25a0\u2b9d\u2b9d\u2b9d", true},
		{"\x1b[31m\u2b9d\u2b9d\u2b9d\u2b9d\u2b9d\x1b[0m", true},
		{"this is real text", false},
		{"", false},
		{"   ", false},
		{"\u2b9d real text", false}, // only 1 spinner char out of many non-space
	}
	for _, c := range cases {
		got := isSpinnerLine(c.line)
		if got != c.want {
			t.Fatalf("isSpinnerLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
