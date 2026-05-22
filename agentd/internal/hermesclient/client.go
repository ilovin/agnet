package hermesclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ResponseChunk struct {
	Text      string
	Done      bool
	SessionID string
	Error     error
}

type Event struct {
	Role string
	Text string
}

type Client struct {
	BaseURL      string
	APIServerKey string
	HTTPClient   *http.Client
}

func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8642"
	}
	return &Client{
		BaseURL:      baseURL,
		APIServerKey: apiKey,
		HTTPClient:   &http.Client{},
	}
}

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatCompletionChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
}

func (c *Client) ChatCompletion(ctx context.Context, sessionID string, messages []Message) (<-chan ResponseChunk, error) {
	body := chatRequest{
		Model:    "default",
		Messages: messages,
		Stream:   true,
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if sessionID != "" {
		req.Header.Set("X-Hermes-Session-Id", sessionID)
	}
	if c.APIServerKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIServerKey)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	chunks := make(chan ResponseChunk, 10)
	sessionIDFromHeader := resp.Header.Get("X-Hermes-Session-Id")

	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		events, errs := decodeSSE(resp.Body)
		var text strings.Builder

		for ev := range events {
			if ev.Data == "[DONE]" {
				chunks <- ResponseChunk{
					Text:      text.String(),
					Done:      true,
					SessionID: sessionIDFromHeader,
				}
				return
			}
			var chunk chatCompletionChunk
			if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
				continue
			}
			for _, choice := range chunk.Choices {
				if choice.Delta.Content != "" {
					text.WriteString(choice.Delta.Content)
					chunks <- ResponseChunk{
						Text:      choice.Delta.Content,
						Done:      false,
						SessionID: sessionIDFromHeader,
					}
				}
				if choice.FinishReason != nil {
					chunks <- ResponseChunk{
						Text:      text.String(),
						Done:      true,
						SessionID: sessionIDFromHeader,
					}
					return
				}
			}
		}

		// events exhausted, check for transport error
		select {
		case err := <-errs:
			if err != nil {
				chunks <- ResponseChunk{Error: err}
				return
			}
		default:
		}

		chunks <- ResponseChunk{
			Text:      text.String(),
			Done:      true,
			SessionID: sessionIDFromHeader,
		}
	}()

	return chunks, nil
}

func (c *Client) IsHealthy(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/capabilities", nil)
	if err != nil {
		return false
	}
	if c.APIServerKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIServerKey)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Client) GetHistory(ctx context.Context, sessionID string) ([]Event, error) {
	return nil, fmt.Errorf("GetHistory not implemented yet")
}
