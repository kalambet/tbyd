//go:build integration

package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/proxy"
)

func TestPassthroughRoundTrip(t *testing.T) {
	// Simulate an OpenRouter backend that returns a streaming SSE response.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/chat/completions" {
			// Verify the request was forwarded correctly.
			var req proxy.ChatRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("upstream decode error: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			if req.Model != "anthropic/claude-haiku-4-5-20251001" {
				t.Errorf("upstream model = %q, want %q", req.Model, "anthropic/claude-haiku-4-5-20251001")
			}

			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)

			chunks := []string{
				`data: {"id":"gen-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"}}]}`,
				`data: {"id":"gen-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"}}]}`,
				`data: {"id":"gen-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "%s\n\n", chunk)
			}
			return
		}

		http.NotFound(w, r)
	}))
	defer upstream.Close()

	// Point our proxy client at the mock upstream.
	c := proxy.NewClientWithBaseURL("test-key", upstream.URL)
	handler := NewOpenAIHandler(c, nil)

	// Start the tbyd server.
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// Send a streaming chat request through the full stack.
	reqBody := `{"model":"anthropic/claude-haiku-4-5-20251001","messages":[{"role":"user","content":"hello"}],"stream":true}`
	resp, err := http.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("reading error body: %v", err)
		}
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	// Read the full streamed response and verify it contains expected data.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	bodyStr := string(body)
	if !strings.Contains(bodyStr, "Hello") {
		t.Errorf("response missing 'Hello': %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "world") {
		t.Errorf("response missing 'world': %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "[DONE]") {
		t.Errorf("response missing '[DONE]': %q", bodyStr)
	}
}
