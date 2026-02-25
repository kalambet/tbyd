package api

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

// --- mocks ---

type mockMCPRetriever struct {
	chunks []retrieval.ContextChunk
	err    error
}

func (m *mockMCPRetriever) Retrieve(_ context.Context, _ string, _ int) ([]retrieval.ContextChunk, error) {
	return m.chunks, m.err
}

type mockMCPEngine struct {
	response string
	err      error
}

func (m *mockMCPEngine) Chat(_ context.Context, _ string, _ []MCPMessage, _ *MCPSchema) (string, error) {
	return m.response, m.err
}

type mockProfileStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newMockProfileStore() *mockProfileStore {
	return &mockProfileStore{data: make(map[string]string)}
}

func (m *mockProfileStore) SetProfileKey(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *mockProfileStore) GetProfileKey(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

func (m *mockProfileStore) GetAllProfileKeys() (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]string, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, nil
}

// --- helpers ---

func newTestMCPDeps(t *testing.T) (MCPDeps, *storage.Store) {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	profileStore := newMockProfileStore()
	profileMgr := profile.NewManager(profileStore)

	return MCPDeps{
		Store:     store,
		Profile:   profileMgr,
		Retriever: &mockMCPRetriever{},
		Engine:    &mockMCPEngine{response: "test summary"},
		DeepModel: "test-model",
	}, store
}

func toolText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("no content in result")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return tc.Text
}

func makeCallToolRequest(name string, args map[string]interface{}) mcp.CallToolRequest {
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	}
}

func makeReadResourceRequest(uri string) mcp.ReadResourceRequest {
	return mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: uri,
		},
	}
}

// --- tests ---

func TestMCPTool_AddContext(t *testing.T) {
	deps, store := newTestMCPDeps(t)
	handler := mcpAddContext(deps)

	req := makeCallToolRequest("add_context", map[string]interface{}{
		"title":   "Go preference",
		"content": "I prefer Go for backend services",
		"tags":    []string{"preference", "go"},
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(t, result))
	}

	text := toolText(t, result)
	if len(text) < 20 {
		t.Fatalf("expected doc ID in response, got: %s", text)
	}

	// Verify doc was saved.
	docs, err := store.ListContextDocsPaginated(10, 0)
	if err != nil {
		t.Fatalf("listing docs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Content != "I prefer Go for backend services" {
		t.Fatalf("unexpected content: %s", docs[0].Content)
	}
	if docs[0].Source != "mcp" {
		t.Fatalf("expected source 'mcp', got %s", docs[0].Source)
	}
}

func TestMCPTool_Recall_ReturnsChunks(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	deps.Retriever = &mockMCPRetriever{
		chunks: []retrieval.ContextChunk{
			{ID: "c1", SourceID: "s1", SourceType: "context_doc", Text: "Go is great", Score: 0.95},
			{ID: "c2", SourceID: "s2", SourceType: "context_doc", Text: "Prefer short answers", Score: 0.8},
		},
	}
	handler := mcpRecall(deps)

	req := makeCallToolRequest("recall", map[string]interface{}{
		"query": "go preferences",
		"limit": 5,
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(t, result))
	}

	text := toolText(t, result)
	var chunks []json.RawMessage
	if err := json.Unmarshal([]byte(text), &chunks); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
}

func TestMCPTool_Recall_EmptyResult(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	deps.Retriever = &mockMCPRetriever{chunks: nil}
	handler := mcpRecall(deps)

	req := makeCallToolRequest("recall", map[string]interface{}{
		"query": "nonexistent topic",
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(t, result))
	}

	text := toolText(t, result)
	if text != "[]" {
		t.Fatalf("expected empty array, got: %s", text)
	}
}

func TestMCPTool_SetPreference(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	handler := mcpSetPreference(deps)

	req := makeCallToolRequest("set_preference", map[string]interface{}{
		"key":   "communication.tone",
		"value": "direct",
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(t, result))
	}

	text := toolText(t, result)
	if text != "Set communication.tone = direct" {
		t.Fatalf("unexpected response: %s", text)
	}

	// Verify profile was updated.
	p, err := deps.Profile.GetProfile()
	if err != nil {
		t.Fatalf("getting profile: %v", err)
	}
	if p.Communication.Tone != "direct" {
		t.Fatalf("expected tone 'direct', got '%s'", p.Communication.Tone)
	}
}

func TestMCPResource_Profile(t *testing.T) {
	deps, _ := newTestMCPDeps(t)

	// Set a profile field first.
	deps.Profile.SetField("identity.role", "engineer")

	handler := mcpResourceProfile(deps)
	req := makeReadResourceRequest("user://profile")

	contents, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content, got %d", len(contents))
	}

	tc, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}

	var p profile.Profile
	if err := json.Unmarshal([]byte(tc.Text), &p); err != nil {
		t.Fatalf("failed to parse profile JSON: %v", err)
	}
	if p.Identity.Role != "engineer" {
		t.Fatalf("expected role 'engineer', got '%s'", p.Identity.Role)
	}
}

func TestMCPServer_ConcurrentCalls(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	deps.Retriever = &mockMCPRetriever{
		chunks: []retrieval.ContextChunk{
			{ID: "c1", Text: "test", Score: 0.9},
		},
	}

	addHandler := mcpAddContext(deps)
	recallHandler := mcpRecall(deps)

	var wg sync.WaitGroup
	errs := make(chan error, 20)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := makeCallToolRequest("add_context", map[string]interface{}{
				"content": "concurrent content",
			})
			_, err := addHandler(context.Background(), req)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := makeCallToolRequest("recall", map[string]interface{}{
				"query": "test",
			})
			_, err := recallHandler(context.Background(), req)
			if err != nil {
				errs <- err
			}
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
}

func TestMCPTool_SummarizeSession(t *testing.T) {
	deps, store := newTestMCPDeps(t)
	deps.Engine = &mockMCPEngine{response: "The user discussed Go preferences and coding style."}
	handler := mcpSummarizeSession(deps)

	messages := []map[string]string{
		{"role": "user", "content": "I prefer Go for backend"},
		{"role": "assistant", "content": "Noted! Go is excellent for backend services."},
	}
	messagesJSON, _ := json.Marshal(messages)

	req := makeCallToolRequest("summarize_session", map[string]interface{}{
		"messages": string(messagesJSON),
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", toolText(t, result))
	}

	text := toolText(t, result)
	if text != "The user discussed Go preferences and coding style." {
		t.Fatalf("unexpected summary: %s", text)
	}

	// Verify summary was stored as context doc.
	docs, err := store.ListContextDocsPaginated(10, 0)
	if err != nil {
		t.Fatalf("listing docs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Source != "session_summary" {
		t.Fatalf("expected source 'session_summary', got %s", docs[0].Source)
	}
}

func TestMCPTool_SummarizeSession_NoEngine(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	deps.Engine = nil
	handler := mcpSummarizeSession(deps)

	req := makeCallToolRequest("summarize_session", map[string]interface{}{
		"messages": `[{"role":"user","content":"hello"}]`,
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error when engine is nil")
	}
}

func TestMCPTool_Recall_Error(t *testing.T) {
	deps, _ := newTestMCPDeps(t)
	deps.Retriever = &mockMCPRetriever{err: errors.New("embed failed")}
	handler := mcpRecall(deps)

	req := makeCallToolRequest("recall", map[string]interface{}{
		"query": "test",
	})

	result, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
}

func TestMCPResource_Recent(t *testing.T) {
	deps, store := newTestMCPDeps(t)

	// Save an interaction.
	err := store.SaveInteraction(storage.Interaction{
		ID:        "int-1",
		CreatedAt: time.Now().UTC(),
		UserQuery: "What is Go?",
		Status:    "completed",
	})
	if err != nil {
		t.Fatalf("saving interaction: %v", err)
	}

	handler := mcpResourceRecent(deps)
	req := makeReadResourceRequest("user://recent")

	contents, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	tc, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("expected TextResourceContents, got %T", contents[0])
	}

	var summaries []json.RawMessage
	if err := json.Unmarshal([]byte(tc.Text), &summaries); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected 1 interaction, got %d", len(summaries))
	}
}
