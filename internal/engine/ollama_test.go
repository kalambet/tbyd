package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func tagsJSON(names ...string) []byte {
	type entry struct {
		Name string `json:"name"`
	}
	type resp struct {
		Models []entry `json:"models"`
	}
	r := resp{}
	for _, n := range names {
		r.Models = append(r.Models, entry{Name: n})
	}
	b, _ := json.Marshal(r)
	return b
}

func TestOllamaEngine_Chat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]string{"role": "assistant", "content": "hello from ollama"},
		})
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	result, err := e.Chat(context.Background(), "phi3.5", []Message{
		{Role: "user", Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if result != "hello from ollama" {
		t.Errorf("got %q, want %q", result, "hello from ollama")
	}
}

func TestOllamaEngine_Embed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float32{{0.1, 0.2, 0.3}},
		})
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	vec, err := e.Embed(context.Background(), "nomic-embed-text", "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 3 {
		t.Fatalf("got %d floats, want 3", len(vec))
	}
}

func TestOllamaEngine_IsRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("phi3.5:latest"))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	if !e.IsRunning(context.Background()) {
		t.Error("IsRunning() = false, want true")
	}
}

func TestOllamaEngine_IsRunning_Down(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	e := NewOllamaEngine(srv.URL)
	if e.IsRunning(context.Background()) {
		t.Error("IsRunning() = true, want false")
	}
}

func TestOllamaEngine_HasModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("phi3.5:latest", "mistral-nemo:latest"))
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	if !e.HasModel(context.Background(), "phi3.5") {
		t.Error("HasModel(phi3.5) = false, want true")
	}
	if e.HasModel(context.Background(), "llama3") {
		t.Error("HasModel(llama3) = true, want false")
	}
}

func TestOllamaEngine_PullModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			http.NotFound(w, r)
			return
		}
		enc := json.NewEncoder(w)
		enc.Encode(map[string]any{"status": "downloading", "total": 1000, "completed": 500})
		enc.Encode(map[string]any{"status": "downloading", "total": 1000, "completed": 1000})
		enc.Encode(map[string]any{"status": "success"})
	}))
	defer srv.Close()

	e := NewOllamaEngine(srv.URL)
	var progressCount int
	err := e.PullModel(context.Background(), "phi3.5", func(p PullProgress) {
		progressCount++
	})
	if err != nil {
		t.Fatalf("PullModel: %v", err)
	}
	if progressCount != 3 {
		t.Errorf("received %d progress updates, want 3", progressCount)
	}
}
