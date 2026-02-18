package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testMessages(t *testing.T) json.RawMessage {
	t.Helper()
	msgs, err := json.Marshal([]map[string]string{{"role": "user", "content": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	return msgs
}

func TestChat_Streaming(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\ndata: [DONE]\n\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	rc, err := c.Chat(context.Background(), ChatRequest{
		Model:    "anthropic/claude-opus-4",
		Messages: testMessages(t),
		Stream:   true,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if string(body) != sseData {
		t.Errorf("body = %q, want %q", string(body), sseData)
	}
}

func TestChat_NonStreaming(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	rc, err := c.Chat(context.Background(), ChatRequest{
		Model:    "anthropic/claude-opus-4",
		Messages: testMessages(t),
		Stream:   false,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}

	if string(body) != respJSON {
		t.Errorf("body = %q, want %q", string(body), respJSON)
	}
}

func TestChat_AuthHeader(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"gen-1","choices":[]}`)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	rc, err := c.Chat(context.Background(), ChatRequest{
		Model:    "test",
		Messages: testMessages(t),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	rc.Close()

	want := "Bearer test-key"
	if gotAuth != want {
		t.Errorf("Authorization = %q, want %q", gotAuth, want)
	}
}

func TestChat_RateLimit_Retry(t *testing.T) {
	var attempt atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempt.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"gen-1","choices":[]}`)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	rc, err := c.Chat(context.Background(), ChatRequest{
		Model:    "test",
		Messages: testMessages(t),
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	rc.Close()

	if got := attempt.Load(); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

func TestChat_RateLimit_Exhausted(t *testing.T) {
	var attempt atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:    "test",
		Messages: testMessages(t),
	})
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}

	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "rate limited")
	}

	if got := attempt.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3", got)
	}
}

func TestChat_ContextCancellation(t *testing.T) {
	handlerStarted := make(chan struct{})
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		// Write headers but delay the body to simulate a slow response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until test signals us to stop (after client cancel verified).
		<-handlerDone
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		c := NewClientWithBaseURL("test-key", srv.URL)
		rc, err := c.Chat(ctx, ChatRequest{
			Model:    "test",
			Messages: testMessages(t),
			Stream:   true,
		})
		if err != nil {
			done <- err
			return
		}
		// For streaming, Chat returns immediately with body. Reading should fail on cancel.
		_, err = io.ReadAll(rc)
		rc.Close()
		done <- err
	}()

	// Wait for the handler to start, then cancel the client context.
	<-handlerStarted
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error after context cancellation")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Chat did not return promptly after context cancellation")
	}

	// Unblock the server handler so it can close cleanly.
	close(handlerDone)
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		list := ModelList{
			Object: "list",
			Data: []Model{
				{ID: "anthropic/claude-opus-4", Object: "model"},
				{ID: "openai/gpt-4o", Object: "model"},
				{ID: "meta/llama-3-70b", Object: "model"},
			},
		}
		json.NewEncoder(w).Encode(list)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}

	want := []string{"anthropic/claude-opus-4", "openai/gpt-4o", "meta/llama-3-70b"}
	for i, w := range want {
		if models[i].ID != w {
			t.Errorf("models[%d].ID = %q, want %q", i, models[i].ID, w)
		}
	}
}

func TestListModels_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		list := ModelList{Object: "list", Data: nil}
		json.NewEncoder(w).Encode(list)
	}))
	defer srv.Close()

	c := NewClientWithBaseURL("test-key", srv.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if len(models) != 0 {
		t.Errorf("got %d models, want 0", len(models))
	}
}
