package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/proxy"
)

// mockUpstream returns an httptest.Server that mimics a subset of the OpenRouter API.
func mockUpstream(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *proxy.Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := proxy.NewClientWithBaseURL("test-key", srv.URL)
	return srv, c
}

func TestHealth(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h := NewOpenAIHandler(c)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("body = %v, want status=ok", body)
	}
}

func TestChatCompletions_Streaming(t *testing.T) {
	sseData := "data: {\"id\":\"gen-1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\ndata: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})
	h := NewOpenAIHandler(c)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream")
	}

	got := rr.Body.String()
	if !strings.Contains(got, `"choices"`) {
		t.Errorf("body does not contain expected SSE data: %q", got)
	}
}

func TestChatCompletions_NonStreaming(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})
	h := NewOpenAIHandler(c)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}],"stream":false}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	if rr.Body.String() != respJSON {
		t.Errorf("body = %q, want %q", rr.Body.String(), respJSON)
	}
}

func TestChatCompletions_InvalidBody(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h := NewOpenAIHandler(c)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("{invalid"))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestChatCompletions_MissingMessages(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {})
	h := NewOpenAIHandler(c)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"test","messages":[]}`))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestModels(t *testing.T) {
	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		list := proxy.ModelList{
			Object: "list",
			Data: []proxy.Model{
				{ID: "anthropic/claude-opus-4", Object: "model"},
				{ID: "openai/gpt-4o", Object: "model"},
			},
		}
		json.NewEncoder(w).Encode(list)
	})
	h := NewOpenAIHandler(c)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var list proxy.ModelList
	json.NewDecoder(rr.Body).Decode(&list)

	if len(list.Data) != 2 {
		t.Fatalf("got %d models, want 2", len(list.Data))
	}
	if list.Data[0].ID != "anthropic/claude-opus-4" {
		t.Errorf("models[0].ID = %q", list.Data[0].ID)
	}
}

