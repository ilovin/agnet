package hermesclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientIsHealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/capabilities" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "")
	if !client.IsHealthy(context.Background()) {
		t.Error("expected healthy")
	}
}

func TestClientIsUnhealthy(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "")
	if client.IsHealthy(context.Background()) {
		t.Error("expected unhealthy")
	}
}

func TestClientChatCompletion(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %s", r.URL.Path)
		}
		// Verify request body
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}
		if !req.Stream {
			t.Error("expected stream=true")
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Hermes-Session-Id", "test-session-123")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
			`data: {"id":"x","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: [DONE]`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "")
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "hi"}}

	chunks, err := client.ChatCompletion(ctx, "", messages)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	var texts []string
	var sessionID string
	var finalDone bool
	for chunk := range chunks {
		if chunk.Error != nil {
			t.Fatalf("chunk error: %v", chunk.Error)
		}
		if chunk.SessionID != "" {
			sessionID = chunk.SessionID
		}
		texts = append(texts, chunk.Text)
		if chunk.Done {
			finalDone = true
		}
	}

	if !finalDone {
		t.Error("expected done=true")
	}
	if sessionID != "test-session-123" {
		t.Errorf("expected sessionID=test-session-123, got %s", sessionID)
	}
	// Verify accumulated text
	full := strings.Join(texts, "")
	if !strings.Contains(full, "Hello") || !strings.Contains(full, "world") {
		t.Errorf("unexpected text: %v", texts)
	}
}

func TestChatCompletionWithSessionID(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hermes-Session-Id") != "existing-session" {
			t.Errorf("expected X-Hermes-Session-Id header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "")
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "hi"}}

	chunks, err := client.ChatCompletion(ctx, "existing-session", messages)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	for range chunks {
	}
}

func TestChatCompletionWithAuth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key-123" {
			t.Errorf("expected Authorization header")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "data: [DONE]\n\n")
	}))
	defer ts.Close()

	client := NewClient(ts.URL, "test-key-123")
	ctx := context.Background()
	messages := []Message{{Role: "user", Content: "hi"}}

	chunks, err := client.ChatCompletion(ctx, "", messages)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}
	for range chunks {
	}
}

func TestGetHistory(t *testing.T) {
	t.Run("fetches and maps hermes history", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Fatalf("expected GET, got %s", r.Method)
			}
			if r.URL.Path != "/v1/sessions/sess-123/history" {
				t.Fatalf("expected /v1/sessions/sess-123/history, got %s", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer test-key" {
				t.Fatalf("expected auth header")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"events":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi there"}]}`))
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "test-key")
		events, err := client.GetHistory(context.Background(), "sess-123")
		if err != nil {
			t.Fatalf("GetHistory returned error: %v", err)
		}
		if len(events) != 2 {
			t.Fatalf("expected 2 events, got %d", len(events))
		}
		if events[0].Role != "user" || events[0].Text != "hello" {
			t.Fatalf("unexpected first event: %+v", events[0])
		}
		if events[1].Role != "assistant" || events[1].Text != "hi there" {
			t.Fatalf("unexpected second event: %+v", events[1])
		}
	})

	t.Run("returns error on non-200", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := NewClient(ts.URL, "")
		_, err := client.GetHistory(context.Background(), "sess-404")
		if err == nil {
			t.Fatal("expected non-nil error")
		}
	})
}

func TestNewClientDefaultBaseURL(t *testing.T) {
	c := NewClient("", "")
	if c.BaseURL != "http://127.0.0.1:8642" {
		t.Errorf("expected default base URL, got %s", c.BaseURL)
	}
}
