package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
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
	// Response is "Stored context doc <uuid>"; extract and validate UUID.
	const prefix = "Stored context doc "
	if !strings.HasPrefix(text, prefix) {
		t.Fatalf("expected response starting with %q, got: %s", prefix, text)
	}
	docIDStr := strings.TrimPrefix(text, prefix)
	if _, err := uuid.Parse(docIDStr); err != nil {
		t.Fatalf("expected valid UUID in response, got %q: %v", docIDStr, err)
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
	var successCount atomic.Int32
	errs := make(chan error, 20)

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := makeCallToolRequest("add_context", map[string]interface{}{
				"content": "concurrent content",
			})
			result, err := addHandler(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if result.IsError {
				errs <- errors.New("add_context returned error: " + toolText(t, result))
				return
			}
			successCount.Add(1)
		}(i)
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := makeCallToolRequest("recall", map[string]interface{}{
				"query": "test",
			})
			result, err := recallHandler(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			if result.IsError {
				errs <- errors.New("recall returned error: " + toolText(t, result))
				return
			}
			successCount.Add(1)
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}

	if got := successCount.Load(); got != 10 {
		t.Fatalf("expected 10 successful completions, got %d", got)
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
	messagesJSON, err := json.Marshal(messages)
	if err != nil {
		t.Fatalf("marshaling messages: %v", err)
	}

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
	err := store.SaveInteraction(context.Background(), storage.Interaction{
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

func TestPrintMCPSetupSnippet(t *testing.T) {
	const token = "my-secret-token"
	const port = 4001
	var buf strings.Builder
	PrintMCPSetupSnippet(&buf, port, token)
	out := buf.String()

	if !strings.Contains(out, "4001") {
		t.Errorf("expected port 4001 in snippet, got: %s", out)
	}
	if !strings.Contains(out, token) {
		t.Errorf("expected token %q in snippet, got: %s", token, out)
	}
	if !strings.Contains(out, "claude mcp add tbyd") {
		t.Errorf("expected claude mcp add command in snippet, got: %s", out)
	}
	if !strings.Contains(out, "Authorization") {
		t.Errorf("expected Authorization header in settings.json snippet, got: %s", out)
	}
}

func TestPrintMCPSetupSnippet_SpecialCharsInToken(t *testing.T) {
	// Tokens with JSON-special characters must produce valid embedded JSON.
	const token = `tok"en\with"specials`
	const port = 4001
	var buf strings.Builder
	PrintMCPSetupSnippet(&buf, port, token)
	out := buf.String()

	// The raw token must not appear unescaped in the output.
	if strings.Contains(out, `tok"en`) {
		t.Errorf("token was not JSON-escaped in snippet: %s", out)
	}
}

// newTestMCPHTTPServer creates an httptest.Server backed by NewMCPHTTPHandler.
func newTestMCPHTTPServer(t *testing.T, token string) (*httptest.Server, *storage.Store) {
	t.Helper()
	deps, store := newTestMCPDeps(t)
	mcpSrv := NewMCPServer(deps)
	ts := httptest.NewServer(NewMCPHTTPHandler(mcpSrv, token))
	t.Cleanup(ts.Close)
	return ts, store
}

// newMCPClient creates an authenticated MCP client for the given test server URL.
func newMCPClient(t *testing.T, serverURL, token string) *mcpclient.Client {
	t.Helper()
	c, err := mcpclient.NewStreamableHttpClient(serverURL,
		transport.WithHTTPHeaders(map[string]string{
			"Authorization": "Bearer " + token,
		}),
	)
	if err != nil {
		t.Fatalf("creating MCP client: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("starting MCP client: %v", err)
	}
	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "test-client", Version: "1.0.0"},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("initializing MCP client: %v", err)
	}
	return c
}

func TestMCPHTTP_AddContext(t *testing.T) {
	const token = "test-http-token"
	ts, store := newTestMCPHTTPServer(t, token)

	c := newMCPClient(t, ts.URL, token)

	callReq := mcp.CallToolRequest{}
	callReq.Params.Name = "add_context"
	callReq.Params.Arguments = map[string]interface{}{
		"content": "HTTP transport test content",
		"title":   "HTTP test",
	}

	result, err := c.CallTool(context.Background(), callReq)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %+v", result)
	}

	docs, err := store.ListContextDocsPaginated(10, 0)
	if err != nil {
		t.Fatalf("listing docs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if docs[0].Content != "HTTP transport test content" {
		t.Fatalf("unexpected content: %s", docs[0].Content)
	}
}

func TestMCPHTTP_Unauthorized(t *testing.T) {
	const token = "correct-token"
	ts, _ := newTestMCPHTTPServer(t, token)

	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`)

	// No Authorization header — expect 401.
	resp, err := http.Post(ts.URL+"/mcp", "application/json", body)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with no auth, got %d", resp.StatusCode)
	}

	// Wrong token — expect 401.
	req, _ := http.NewRequest("POST", ts.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`))
	req.Header.Set("Authorization", "Bearer wrong-token")
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request with wrong token failed: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 with wrong token, got %d", resp2.StatusCode)
	}
}

func TestMCPHTTP_ConcurrentCalls(t *testing.T) {
	const token = "concurrent-token"
	ts, _ := newTestMCPHTTPServer(t, token)

	var wg sync.WaitGroup
	var successCount atomic.Int32
	errs := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			c, err := mcpclient.NewStreamableHttpClient(ts.URL,
				transport.WithHTTPHeaders(map[string]string{
					"Authorization": "Bearer " + token,
				}),
			)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: creating client: %v", i, err)
				return
			}
			ctx := context.Background()
			if err := c.Start(ctx); err != nil {
				errs <- fmt.Errorf("goroutine %d: start: %v", i, err)
				return
			}
			initReq := mcp.InitializeRequest{
				Params: mcp.InitializeParams{
					ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
					ClientInfo:      mcp.Implementation{Name: "test", Version: "1.0"},
				},
			}
			if _, err := c.Initialize(ctx, initReq); err != nil {
				errs <- fmt.Errorf("goroutine %d: initialize: %v", i, err)
				return
			}

			callReq := mcp.CallToolRequest{}
			callReq.Params.Name = "add_context"
			callReq.Params.Arguments = map[string]interface{}{
				"content": fmt.Sprintf("concurrent content %d", i),
			}
			result, err := c.CallTool(ctx, callReq)
			if err != nil {
				errs <- fmt.Errorf("goroutine %d: CallTool: %v", i, err)
				return
			}
			if result.IsError {
				errs <- fmt.Errorf("goroutine %d: tool error", i)
				return
			}
			successCount.Add(1)
		}(i)
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
	if got := successCount.Load(); got != 10 {
		t.Fatalf("expected 10 successful calls, got %d", got)
	}
}
