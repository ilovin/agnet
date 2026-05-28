package ws

import (
	"strings"
	"testing"
)

// TestFormatTmuxMessageWithImages verifies that when image paths are present,
// they are appended to the message text in a Claude-readable way for tmux-attached
// sessions (which cannot accept dynamic --file flags). Without this, image
// uploads on tmux agents silently fail or surface a "not supported" error.
func TestFormatTmuxMessageWithImages(t *testing.T) {
	tests := []struct {
		name    string
		message string
		paths   []string
		// substrings that MUST appear in the formatted output
		wantContains []string
	}{
		{
			name:         "no images returns message unchanged",
			message:      "hello",
			paths:        nil,
			wantContains: []string{"hello"},
		},
		{
			name:         "empty image list returns message unchanged",
			message:      "hello",
			paths:        []string{},
			wantContains: []string{"hello"},
		},
		{
			name:         "single image appends path",
			message:      "请看这张图片",
			paths:        []string{"/tmp/agentd-img-abc-0.png"},
			wantContains: []string{"请看这张图片", "/tmp/agentd-img-abc-0.png"},
		},
		{
			name:    "multiple images appends all paths",
			message: "Compare these",
			paths: []string{
				"/tmp/agentd-img-abc-0.png",
				"/tmp/agentd-img-abc-1.jpg",
			},
			wantContains: []string{
				"Compare these",
				"/tmp/agentd-img-abc-0.png",
				"/tmp/agentd-img-abc-1.jpg",
			},
		},
		{
			name:    "image-only message with placeholder text still includes path",
			message: "请看这张图片",
			paths:   []string{"/tmp/foo.webp"},
			wantContains: []string{
				"请看这张图片",
				"/tmp/foo.webp",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatTmuxMessageWithImages(tc.message, tc.paths)
			for _, sub := range tc.wantContains {
				if !strings.Contains(got, sub) {
					t.Errorf("formatTmuxMessageWithImages(%q, %v) = %q, want to contain %q",
						tc.message, tc.paths, got, sub)
				}
			}
		})
	}
}

// TestFormatTmuxMessageWithImagesNoPathsIsIdempotent ensures the helper is a
// no-op when there are no images, so the existing tmux send path is unchanged
// for text-only messages.
func TestFormatTmuxMessageWithImagesNoPathsIsIdempotent(t *testing.T) {
	got := formatTmuxMessageWithImages("plain text", nil)
	if got != "plain text" {
		t.Fatalf("formatTmuxMessageWithImages with nil paths = %q, want %q", got, "plain text")
	}
	got = formatTmuxMessageWithImages("plain text", []string{})
	if got != "plain text" {
		t.Fatalf("formatTmuxMessageWithImages with empty paths = %q, want %q", got, "plain text")
	}
}

// TestFormatTmuxMessageWithImagesAvoidsEmbeddedNewlines guards against a real
// hazard in tmux send-keys: sendTmuxInput translates every '\n' into the Enter
// key, which Claude's interactive REPL interprets as "submit prompt". Embedding
// newlines between message and image paths would cause Claude to submit a
// half-built prompt and never see the image paths. We assert the formatter
// keeps everything on a single line so the prompt is submitted exactly once.
func TestFormatTmuxMessageWithImagesAvoidsEmbeddedNewlines(t *testing.T) {
	got := formatTmuxMessageWithImages("hello world", []string{
		"/tmp/a.png",
		"/tmp/b.jpg",
	})
	if strings.ContainsRune(got, '\n') {
		t.Fatalf("formatted prompt must not contain newlines (each \\n becomes Enter and submits the prompt early); got %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("formatted prompt must not contain CR; got %q", got)
	}
}
