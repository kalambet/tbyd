package ollama

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// tagsJSON builds a /api/tags response with the given model names.
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

func TestIsRunning_Up(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("phi3.5:latest"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if !c.IsRunning(context.Background()) {
		t.Error("IsRunning() = false, want true")
	}
}

func TestIsRunning_Down(t *testing.T) {
	// Point at a closed server to simulate connection refused.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := New(srv.URL)
	if c.IsRunning(context.Background()) {
		t.Error("IsRunning() = true, want false")
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("phi3.5:latest", "mistral-nemo:latest", "nomic-embed-text:latest"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	models, err := c.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}

	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}

	want := []string{"phi3.5:latest", "mistral-nemo:latest", "nomic-embed-text:latest"}
	for i, w := range want {
		if models[i] != w {
			t.Errorf("models[%d] = %q, want %q", i, models[i], w)
		}
	}
}

func TestHasModel_Present(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("phi3.5:latest", "mistral-nemo:latest"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if !c.HasModel(context.Background(), "phi3.5") {
		t.Error("HasModel(phi3.5) = false, want true")
	}
}

func TestHasModel_Absent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(tagsJSON("mistral-nemo:latest"))
	}))
	defer srv.Close()

	c := New(srv.URL)
	if c.HasModel(context.Background(), "phi3.5") {
		t.Error("HasModel(phi3.5) = true, want false")
	}
}

func TestChat_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		resp := chatResponse{
			Message: Message{Role: "assistant", Content: "Go is great!"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL)
	result, err := c.Chat(context.Background(), "phi3.5", []Message{
		{Role: "user", Content: "Tell me about Go"},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	if result != "Go is great!" {
		t.Errorf("result = %q, want %q", result, "Go is great!")
	}
}

func TestChat_JSONSchema(t *testing.T) {
	var capturedFormat any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}

		var reqBody chatRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		capturedFormat = reqBody.Format

		resp := chatResponse{
			Message: Message{Role: "assistant", Content: `{"intent":"code","confidence":0.95}`},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL)
	schema := &Schema{
		Type: "object",
		Properties: map[string]SchemaProperty{
			"intent":     {Type: "string"},
			"confidence": {Type: "number"},
		},
		Required: []string{"intent", "confidence"},
	}

	result, err := c.Chat(context.Background(), "phi3.5", []Message{
		{Role: "user", Content: "classify this"},
	}, schema)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	formatMap, ok := capturedFormat.(map[string]any)
	if !ok {
		t.Fatalf("format = %T, want map (schema object)", capturedFormat)
	}
	if formatMap["type"] != "object" {
		t.Errorf("format.type = %v, want %q", formatMap["type"], "object")
	}

	// Verify response is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal([]byte(result), &parsed); err != nil {
		t.Errorf("response is not valid JSON: %v", err)
	}
}

func TestEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.NotFound(w, r)
			return
		}
		resp := embedResponse{
			Embeddings: [][]float32{{0.1, 0.2, 0.3}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := New(srv.URL)
	vec, err := c.Embed(context.Background(), "nomic-embed-text", "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}

	if len(vec) != 3 {
		t.Fatalf("got %d floats, want 3", len(vec))
	}

	want := []float32{0.1, 0.2, 0.3}
	for i, w := range want {
		if vec[i] != w {
			t.Errorf("vec[%d] = %f, want %f", i, vec[i], w)
		}
	}
}

func TestPullModel_Progress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/pull" {
			http.NotFound(w, r)
			return
		}

		// Verify request body.
		var reqBody pullRequest
		json.NewDecoder(r.Body).Decode(&reqBody)
		if reqBody.Name != "phi3.5" {
			t.Errorf("pull model = %q, want %q", reqBody.Name, "phi3.5")
		}

		// Stream progress lines as newline-delimited JSON.
		enc := json.NewEncoder(w)
		enc.Encode(PullProgress{Status: "downloading", Total: 1000, Completed: 500})
		enc.Encode(PullProgress{Status: "downloading", Total: 1000, Completed: 1000})
		enc.Encode(PullProgress{Status: "success"})
	}))
	defer srv.Close()

	c := New(srv.URL)
	var progressCount int
	err := c.PullModel(context.Background(), "phi3.5", func(p PullProgress) {
		progressCount++
	})
	if err != nil {
		t.Fatalf("PullModel: %v", err)
	}

	if progressCount != 3 {
		t.Errorf("received %d progress updates, want 3", progressCount)
	}
}

func TestEnsureReady_OllamaDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close()

	c := New(srv.URL)
	err := EnsureReady(context.Background(), c, "phi3.5", "nomic-embed-text", io.Discard)
	if err == nil {
		t.Fatal("expected error when Ollama is down")
	}

	want := "Ollama is not running"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error = %q, want it to contain %q", got, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
