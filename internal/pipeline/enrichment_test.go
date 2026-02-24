package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/composer"
	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/intent"
	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/retrieval"
)

// --- mock chatter (for intent.Extractor) ---

type mockChatter struct {
	chatFn func(ctx context.Context, model string, messages []ollama.Message, schema *ollama.Schema) (string, error)
}

func (m *mockChatter) Chat(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, model, msgs, schema)
	}
	return "", nil
}

// --- mock engine (for retrieval.Embedder) ---

type mockEngine struct {
	embedFn func(ctx context.Context, model string, text string) ([]float32, error)
}

func (m *mockEngine) Chat(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
	return "", nil
}

func (m *mockEngine) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, model, text)
	}
	return make([]float32, 768), nil
}

func (m *mockEngine) IsRunning(ctx context.Context) bool                                     { return true }
func (m *mockEngine) ListModels(ctx context.Context) ([]string, error)                       { return nil, nil }
func (m *mockEngine) HasModel(ctx context.Context, name string) bool                         { return true }
func (m *mockEngine) PullModel(ctx context.Context, name string, fn func(engine.PullProgress)) error {
	return nil
}

// --- mock vector store ---

type mockVectorStore struct {
	searchResults []retrieval.ScoredRecord
	searchErr     error
}

func (m *mockVectorStore) Insert(table string, records []retrieval.Record) error { return nil }
func (m *mockVectorStore) Search(table string, vector []float32, topK int, filter string) ([]retrieval.ScoredRecord, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}
	return m.searchResults, nil
}
func (m *mockVectorStore) GetByIDs(ctx context.Context, table string, ids []string) ([]retrieval.Record, error) {
	return nil, nil
}
func (m *mockVectorStore) Delete(table string, id string) error { return nil }
func (m *mockVectorStore) CreateTable(name string) error        { return nil }
func (m *mockVectorStore) ExportAll(table string) ([]retrieval.Record, error) {
	return nil, nil
}
func (m *mockVectorStore) Count(table string) (int, error) { return 0, nil }

// --- mock profile store ---

type mockProfileStore struct {
	keys map[string]string
	err  error
}

func (m *mockProfileStore) SetProfileKey(key, value string) error {
	if m.err != nil {
		return m.err
	}
	if m.keys == nil {
		m.keys = make(map[string]string)
	}
	m.keys[key] = value
	return nil
}

func (m *mockProfileStore) GetProfileKey(key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.keys[key], nil
}

func (m *mockProfileStore) GetAllProfileKeys() (map[string]string, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.keys == nil {
		return map[string]string{}, nil
	}
	return m.keys, nil
}

// --- helpers ---

func makeReq(userMsg string) proxy.ChatRequest {
	msgs, _ := json.Marshal([]map[string]string{
		{"role": "user", "content": userMsg},
	})
	return proxy.ChatRequest{
		Model:    "test-model",
		Messages: msgs,
	}
}

func buildEnricher(chatter *mockChatter, eng *mockEngine, vs *mockVectorStore, ps *mockProfileStore) *Enricher {
	extractor := intent.NewExtractor(chatter, "test-fast")
	embedder := retrieval.NewEmbedder(eng, "test-embed")
	ret := retrieval.NewRetriever(embedder, vs)
	profileMgr := profile.NewManager(ps)
	comp := composer.New(4000)
	return NewEnricher(extractor, ret, profileMgr, comp, 5)
}

// --- tests ---

func TestEnrich_FullPipeline(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return `{"intent_type":"question","entities":["Go"],"topics":["programming"],"context_needs":["docs"],"is_private":false}`, nil
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			return make([]float32, 768), nil
		},
	}

	vs := &mockVectorStore{
		searchResults: []retrieval.ScoredRecord{
			{Record: retrieval.Record{ID: "chunk-1", SourceID: "src-1", SourceType: "doc", TextChunk: "Go is great"}, Score: 0.9},
			{Record: retrieval.Record{ID: "chunk-2", SourceID: "src-2", SourceType: "doc", TextChunk: "Concurrency"}, Score: 0.8},
		},
	}

	ps := &mockProfileStore{
		keys: map[string]string{
			"identity.role":      "software engineer",
			"communication.tone": "direct",
		},
	}

	enricher := buildEnricher(chatter, eng, vs, ps)
	enriched, meta, err := enricher.Enrich(context.Background(), makeReq("tell me about Go"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if !meta.IntentExtracted {
		t.Error("expected IntentExtracted to be true")
	}
	if len(meta.ChunksUsed) != 2 {
		t.Errorf("ChunksUsed = %d, want 2", len(meta.ChunksUsed))
	}

	// Verify the enriched request contains profile and context.
	var msgs []map[string]string
	json.Unmarshal(enriched.Messages, &msgs)
	if len(msgs) == 0 {
		t.Fatal("enriched request has no messages")
	}
	sysContent := msgs[0]["content"]
	if !strings.Contains(sysContent, "User Profile") {
		t.Error("system message missing profile section")
	}
	if !strings.Contains(sysContent, "Go is great") {
		t.Error("system message missing context chunk")
	}
}

func TestEnrich_IntentExtractorFails(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return "", errors.New("ollama down")
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			return make([]float32, 768), nil
		},
	}

	vs := &mockVectorStore{
		searchResults: []retrieval.ScoredRecord{
			{Record: retrieval.Record{ID: "c1", SourceID: "s1", TextChunk: "some context"}, Score: 0.85},
		},
	}

	ps := &mockProfileStore{
		keys: map[string]string{"identity.role": "engineer"},
	}

	enricher := buildEnricher(chatter, eng, vs, ps)
	enriched, meta, err := enricher.Enrich(context.Background(), makeReq("test query"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if meta.IntentExtracted {
		t.Error("expected IntentExtracted to be false when extractor fails")
	}

	// Profile should still be injected.
	var msgs []map[string]string
	json.Unmarshal(enriched.Messages, &msgs)
	if len(msgs) == 0 {
		t.Fatal("enriched request has no messages")
	}
	if !strings.Contains(msgs[0]["content"], "User Profile") {
		t.Error("profile should still be injected when intent extraction fails")
	}
}

func TestEnrich_RetrievalFails(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":false}`, nil
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			return nil, errors.New("embed failed")
		},
	}

	vs := &mockVectorStore{}

	ps := &mockProfileStore{
		keys: map[string]string{"communication.tone": "direct"},
	}

	enricher := buildEnricher(chatter, eng, vs, ps)
	enriched, meta, err := enricher.Enrich(context.Background(), makeReq("test query"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if len(meta.ChunksUsed) != 0 {
		t.Errorf("ChunksUsed = %d, want 0", len(meta.ChunksUsed))
	}

	// Profile should still be injected.
	var msgs []map[string]string
	json.Unmarshal(enriched.Messages, &msgs)
	if len(msgs) > 0 && strings.Contains(msgs[0]["content"], "User Profile") {
		// Good — profile still present.
	} else {
		t.Error("profile should still be injected when retrieval fails")
	}
}

func TestEnrich_ProfileEmpty(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return `{"intent_type":"task","entities":[],"topics":[],"context_needs":[],"is_private":false}`, nil
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			return make([]float32, 768), nil
		},
	}

	vs := &mockVectorStore{
		searchResults: []retrieval.ScoredRecord{
			{Record: retrieval.Record{ID: "c1", SourceID: "s1", TextChunk: "chunk text"}, Score: 0.9},
		},
	}

	ps := &mockProfileStore{} // empty

	enricher := buildEnricher(chatter, eng, vs, ps)
	_, meta, err := enricher.Enrich(context.Background(), makeReq("do something"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if !meta.IntentExtracted {
		t.Error("expected IntentExtracted to be true")
	}
}

func TestEnrich_MetadataPopulated(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return `{"intent_type":"recall","entities":["db"],"topics":["data"],"context_needs":["past"],"is_private":false}`, nil
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			return make([]float32, 768), nil
		},
	}

	vs := &mockVectorStore{
		searchResults: []retrieval.ScoredRecord{
			{Record: retrieval.Record{ID: "id-aaa", SourceID: "s1", TextChunk: "data"}, Score: 0.9},
			{Record: retrieval.Record{ID: "id-bbb", SourceID: "s2", TextChunk: "more"}, Score: 0.8},
		},
	}

	ps := &mockProfileStore{}

	enricher := buildEnricher(chatter, eng, vs, ps)
	_, meta, err := enricher.Enrich(context.Background(), makeReq("recall db schema"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if len(meta.ChunksUsed) != 2 {
		t.Fatalf("ChunksUsed = %d, want 2", len(meta.ChunksUsed))
	}
	if meta.ChunksUsed[0] != "id-aaa" || meta.ChunksUsed[1] != "id-bbb" {
		t.Errorf("ChunksUsed = %v, want [id-aaa, id-bbb]", meta.ChunksUsed)
	}
}

func TestEnrich_DurationTracked(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			return `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":false}`, nil
		},
	}

	eng := &mockEngine{
		embedFn: func(ctx context.Context, model string, text string) ([]float32, error) {
			time.Sleep(50 * time.Millisecond)
			return make([]float32, 768), nil
		},
	}

	vs := &mockVectorStore{
		searchResults: []retrieval.ScoredRecord{
			{Record: retrieval.Record{ID: "c1", SourceID: "s1", TextChunk: "text"}, Score: 0.9},
		},
	}

	ps := &mockProfileStore{}

	enricher := buildEnricher(chatter, eng, vs, ps)
	_, meta, err := enricher.Enrich(context.Background(), makeReq("test"))
	if err != nil {
		t.Fatalf("Enrich returned error: %v", err)
	}

	if meta.EnrichmentDurationMs < 50 {
		t.Errorf("EnrichmentDurationMs = %d, want >= 50", meta.EnrichmentDurationMs)
	}
}

func TestEnrich_ContextCancelled(t *testing.T) {
	chatter := &mockChatter{
		chatFn: func(ctx context.Context, model string, msgs []ollama.Message, schema *ollama.Schema) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}

	eng := &mockEngine{}
	vs := &mockVectorStore{}
	ps := &mockProfileStore{}

	enricher := buildEnricher(chatter, eng, vs, ps)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan struct{})
	go func() {
		enricher.Enrich(ctx, makeReq("test"))
		close(done)
	}()

	select {
	case <-done:
		// Good — returned promptly.
	case <-time.After(2 * time.Second):
		t.Fatal("Enrich did not return promptly after context cancellation")
	}
}

func TestExtractLastUserMessage(t *testing.T) {
	tests := []struct {
		name string
		msgs json.RawMessage
		want string
	}{
		{
			name: "single user message",
			msgs: json.RawMessage(`[{"role":"user","content":"hello"}]`),
			want: "hello",
		},
		{
			name: "multiple messages, last user",
			msgs: json.RawMessage(`[{"role":"user","content":"first"},{"role":"assistant","content":"reply"},{"role":"user","content":"second"}]`),
			want: "second",
		},
		{
			name: "no user messages",
			msgs: json.RawMessage(`[{"role":"system","content":"sys"}]`),
			want: "",
		},
		{
			name: "invalid JSON",
			msgs: json.RawMessage(`{invalid`),
			want: "",
		},
		{
			name: "empty array",
			msgs: json.RawMessage(`[]`),
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLastUserMessage(tt.msgs)
			if got != tt.want {
				t.Errorf("extractLastUserMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
