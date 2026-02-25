package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/config"
)

type recordedRequest struct {
	Method string
	Path   string
	Body   string
	Auth   string
}

type testServer struct {
	server   *httptest.Server
	requests []recordedRequest
}

func newTestServer(t *testing.T, responses map[string]string) *testServer {
	t.Helper()
	ts := &testServer{}

	ts.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bytes.Buffer
		body.ReadFrom(r.Body)

		ts.requests = append(ts.requests, recordedRequest{
			Method: r.Method,
			Path:   r.URL.RequestURI(),
			Body:   body.String(),
			Auth:   r.Header.Get("Authorization"),
		})

		key := r.Method + " " + r.URL.Path
		if resp, ok := responses[key]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(resp))
			return
		}

		w.WriteHeader(404)
		w.Write([]byte(`{"error":{"message":"not found","type":"not_found"}}`))
	}))

	t.Cleanup(ts.server.Close)
	return ts
}

func (ts *testServer) client() *apiClient {
	return &apiClient{
		baseURL:    ts.server.URL,
		token:      "test-token",
		httpClient: ts.server.Client(),
	}
}

var ctx = context.Background()

func TestIngestCommand_Text(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"POST /ingest": `{"id":"doc-123","status":"queued"}`,
	})

	client := ts.client()

	req := map[string]any{
		"source":  "cli",
		"type":    "text",
		"content": "hello world",
		"tags":    []string{"foo"},
	}

	resp, err := client.post(ctx, "/ingest", req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]string
	if err := decodeJSON(resp, &result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if result["status"] != "queued" {
		t.Errorf("status = %q, want %q", result["status"], "queued")
	}
	if result["id"] != "doc-123" {
		t.Errorf("id = %q, want %q", result["id"], "doc-123")
	}

	if len(ts.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(ts.requests))
	}

	r := ts.requests[0]
	if r.Method != "POST" {
		t.Errorf("method = %q, want POST", r.Method)
	}
	if r.Path != "/ingest" {
		t.Errorf("path = %q, want /ingest", r.Path)
	}
	if r.Auth != "Bearer test-token" {
		t.Errorf("auth = %q, want Bearer test-token", r.Auth)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatalf("body parse error: %v", err)
	}
	if body["source"] != "cli" {
		t.Errorf("body.source = %v, want cli", body["source"])
	}
	if body["content"] != "hello world" {
		t.Errorf("body.content = %v, want hello world", body["content"])
	}
}

func TestIngestCommand_MissingArgs(t *testing.T) {
	defer rootCmd.SetArgs(nil)

	rootCmd.SetArgs([]string{"ingest"})
	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing args")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error = %q, want it to mention 'required'", err.Error())
	}
}

func TestProfileShow(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /profile": `{"identity":{"role":"developer"},"communication":{"tone":"direct"}}`,
	})

	client := ts.client()
	resp, err := client.get(ctx, "/profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var profile map[string]any
	if err := decodeJSON(resp, &profile); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	identity, ok := profile["identity"].(map[string]any)
	if !ok {
		t.Fatal("expected identity to be a map")
	}
	if identity["role"] != "developer" {
		t.Errorf("role = %v, want developer", identity["role"])
	}
}

func TestProfileSet(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"PATCH /profile": `{"status":"updated"}`,
	})

	client := ts.client()
	body := map[string]any{"communication.tone": "direct"}
	resp, err := client.patch(ctx, "/profile", body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result map[string]string
	if err := decodeJSON(resp, &result); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if result["status"] != "updated" {
		t.Errorf("status = %q, want updated", result["status"])
	}

	if len(ts.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(ts.requests))
	}

	var sentBody map[string]any
	if err := json.Unmarshal([]byte(ts.requests[0].Body), &sentBody); err != nil {
		t.Fatalf("body parse error: %v", err)
	}
	if sentBody["communication.tone"] != "direct" {
		t.Errorf("body key = %v, want direct", sentBody["communication.tone"])
	}
}

func TestRecallCommand(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /recall": `[{"id":"v1","source_id":"doc1","source_type":"context_doc","text":"I prefer Go","score":0.95,"tags":"[\"preference\"]"}]`,
	})

	client := ts.client()
	resp, err := client.get(ctx, "/recall?q=go+preferences&limit=5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var results []struct {
		ID    string  `json:"id"`
		Text  string  `json:"text"`
		Score float32 `json:"score"`
	}
	if err := decodeJSON(resp, &results); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Text != "I prefer Go" {
		t.Errorf("text = %q, want 'I prefer Go'", results[0].Text)
	}
	if results[0].Score < 0.9 {
		t.Errorf("score = %f, want > 0.9", results[0].Score)
	}
}

func TestRecallCommand_URLEncoding(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /recall": `[]`,
	})

	client := ts.client()
	query := "go & python preferences"
	path := fmt.Sprintf("/recall?q=%s&limit=5", url.QueryEscape(query))
	resp, err := client.get(ctx, path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if len(ts.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(ts.requests))
	}

	reqPath := ts.requests[0].Path
	if strings.Contains(reqPath, "& python") {
		t.Errorf("query not URL-encoded: %q", reqPath)
	}
	if !strings.Contains(reqPath, "q=go+%26+python+preferences") {
		t.Errorf("unexpected encoded path: %q", reqPath)
	}
}

func TestStatusCommand_Running(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /health": `{"status":"ok"}`,
	})

	client := ts.client()
	resp, err := client.get(ctx, "/health")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status code = %d, want 200", resp.StatusCode)
	}
}

func TestStatusCommand_Stopped(t *testing.T) {
	ts := newTestServer(t, map[string]string{})
	ts.server.Close()

	client := ts.client()
	_, err := client.get(ctx, "/health")
	if err == nil {
		t.Fatal("expected error for stopped server")
	}
	if !strings.Contains(err.Error(), "not reachable") {
		t.Errorf("error = %q, want it to mention 'not reachable'", err.Error())
	}
}

func TestNoColorFlag(t *testing.T) {
	old := noColor
	defer func() { noColor = old }()

	noColor = true
	result := colorize(colorGreen, "test message")
	if strings.Contains(result, "\033[") {
		t.Errorf("colorize with noColor=true should not contain ANSI codes, got %q", result)
	}
	if result != "test message" {
		t.Errorf("result = %q, want %q", result, "test message")
	}

	noColor = false
	result = colorize(colorGreen, "test message")
	if !strings.Contains(result, "\033[") {
		t.Errorf("colorize with noColor=false should contain ANSI codes, got %q", result)
	}
}

func TestInteractionsList(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /interactions": `[{"id":"ix-001","created_at":"2025-01-01T00:00:00Z","user_query":"hello","status":"completed"}]`,
	})

	client := ts.client()
	resp, err := client.get(ctx, "/interactions?limit=20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var interactions []struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(resp, &interactions); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	if len(interactions) != 1 {
		t.Fatalf("expected 1 interaction, got %d", len(interactions))
	}
	if interactions[0].ID != "ix-001" {
		t.Errorf("id = %q, want ix-001", interactions[0].ID)
	}
}

func TestDataExportFormat(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /context-docs": `[{"id":"doc-1","title":"test","content":"hello"}]`,
		"GET /interactions": `[]`,
	})

	client := ts.client()

	resp, err := client.get(ctx, "/context-docs?limit=100&offset=0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var docs []any
	if err := decodeJSON(resp, &docs); err != nil {
		t.Fatalf("decode error: %v", err)
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, doc := range docs {
		record := map[string]any{"type": "context_doc", "data": doc}
		if err := enc.Encode(record); err != nil {
			t.Fatalf("encode error: %v", err)
		}
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("expected 1 JSONL line, got %d", len(lines))
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("invalid JSONL: %v", err)
	}
	if record["type"] != "context_doc" {
		t.Errorf("type = %v, want context_doc", record["type"])
	}
}

func TestAPIClientAuth(t *testing.T) {
	ts := newTestServer(t, map[string]string{
		"GET /health": `{"status":"ok"}`,
	})

	client := ts.client()
	client.token = "my-secret-token"

	_, err := client.get(ctx, "/health")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ts.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(ts.requests))
	}
	if ts.requests[0].Auth != "Bearer my-secret-token" {
		t.Errorf("auth = %q, want 'Bearer my-secret-token'", ts.requests[0].Auth)
	}
}

func TestDecodeJSON_ErrorResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"unauthorized","type":"auth_error"}}`))
	}))
	defer ts.Close()

	client := &apiClient{
		baseURL:    ts.URL,
		token:      "bad-token",
		httpClient: ts.Client(),
	}

	resp, err := client.get(ctx, "/profile")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}

	var result any
	err = decodeJSON(resp, &result)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want it to contain '401'", err.Error())
	}
}

func TestConfigShowAll(t *testing.T) {
	cfg := config.Config{}
	cfg.Server.Port = 4000
	cfg.Ollama.FastModel = "phi3.5"

	keys := config.ShowAll(cfg)
	if len(keys) == 0 {
		t.Fatal("expected non-empty keys from ShowAll")
	}

	found := false
	for _, k := range keys {
		if k.Key == "server.port" && k.Value == "4000" {
			found = true
		}
	}
	if !found {
		t.Error("expected to find server.port=4000 in ShowAll output")
	}
}

func TestPurgeEndpoint_CollectsFailures(t *testing.T) {
	callCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("Content-Type", "application/json")
			if callCount == 0 {
				callCount++
				w.Write([]byte(`[{"id":"doc-1"},{"id":"doc-2"}]`))
			} else {
				w.Write([]byte(`[]`))
			}
			return
		}
		if r.Method == "DELETE" {
			if strings.HasSuffix(r.URL.Path, "doc-1") {
				w.WriteHeader(500)
				w.Write([]byte(`{"error":{"message":"internal error"}}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"deleted"}`))
			return
		}
	}))
	defer ts.Close()

	client := &apiClient{
		baseURL:    ts.URL,
		token:      "test",
		httpClient: ts.Client(),
	}

	failures, err := purgeEndpoint(ctx, client, "/items")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}
}

func TestCountLabel(t *testing.T) {
	tests := []struct {
		count, limit int
		want         string
	}{
		{5, 100, "5"},
		{0, 100, "0"},
		{100, 100, "100+"},
		{150, 100, "150+"},
	}
	for _, tt := range tests {
		got := countLabel(tt.count, tt.limit)
		if got != tt.want {
			t.Errorf("countLabel(%d, %d) = %q, want %q", tt.count, tt.limit, got, tt.want)
		}
	}
}
