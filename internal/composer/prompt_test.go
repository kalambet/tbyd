package composer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/retrieval"
)

func makeRequest(t *testing.T, msgs ...map[string]string) proxy.ChatRequest {
	t.Helper()
	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("marshal messages: %v", err)
	}
	return proxy.ChatRequest{
		Model:    "test-model",
		Messages: b,
	}
}

func decodeMessages(t *testing.T, req proxy.ChatRequest) []rawMsg {
	t.Helper()
	msgs, err := parseMessages(req.Messages)
	if err != nil {
		t.Fatalf("parsing result messages: %v", err)
	}
	return msgs
}

func TestCompose_EmptyContext(t *testing.T) {
	c := New(4000)
	req := makeRequest(t, map[string]string{"role": "user", "content": "hello"})

	out, err := c.Compose(req, nil, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	// No profile, no chunks → no system message prepended, original preserved.
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if getRole(msgs[0]) != "user" {
		t.Errorf("expected role user, got %q", getRole(msgs[0]))
	}
	if getContent(msgs[0]) != "hello" {
		t.Errorf("expected content 'hello', got %q", getContent(msgs[0]))
	}
}

func TestCompose_ProfileInjected(t *testing.T) {
	c := New(4000)
	req := makeRequest(t, map[string]string{"role": "user", "content": "hi"})

	out, err := c.Compose(req, nil, nil, "User: engineer. Prefers: direct tone.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if getRole(msgs[0]) != "system" {
		t.Errorf("expected system message first, got %q", getRole(msgs[0]))
	}
	sysContent := getContent(msgs[0])
	if !strings.Contains(sysContent, "direct tone") {
		t.Errorf("system message missing profile: %s", sysContent)
	}
	if getContent(msgs[1]) != "hi" {
		t.Errorf("user message changed: %q", getContent(msgs[1]))
	}
}

func TestCompose_ChunksAppended(t *testing.T) {
	c := New(4000)
	req := makeRequest(t, map[string]string{"role": "user", "content": "question"})

	chunks := []retrieval.ContextChunk{
		{ID: "1", SourceID: "doc1", SourceType: "manual", Text: "chunk one text", Score: 0.5},
		{ID: "2", SourceID: "doc2", SourceType: "extracted", Text: "chunk two text", Score: 0.9},
	}

	out, err := c.Compose(req, chunks, nil, "User: dev.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])
	if !strings.Contains(sysContent, "chunk one text") || !strings.Contains(sysContent, "chunk two text") {
		t.Errorf("system message missing chunks: %s", sysContent)
	}
	// Higher score should appear first.
	idx1 := strings.Index(sysContent, "chunk two text")
	idx2 := strings.Index(sysContent, "chunk one text")
	if idx1 > idx2 {
		t.Errorf("higher-scoring chunk should appear first")
	}
}

func TestCompose_ExistingSystemMessage(t *testing.T) {
	c := New(4000)
	req := makeRequest(t,
		map[string]string{"role": "system", "content": "You are a helpful coding assistant."},
		map[string]string{"role": "user", "content": "help me"},
	)

	out, err := c.Compose(req, nil, nil, "User: engineer.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (merged system + user), got %d", len(msgs))
	}
	sysContent := getContent(msgs[0])
	if !strings.Contains(sysContent, "User: engineer.") {
		t.Errorf("missing profile in merged system message: %s", sysContent)
	}
	if !strings.Contains(sysContent, "helpful coding assistant") {
		t.Errorf("original system message lost after merge: %s", sysContent)
	}
	if getContent(msgs[1]) != "help me" {
		t.Errorf("user message changed: %q", getContent(msgs[1]))
	}
}

func TestCompose_TokenBudget(t *testing.T) {
	c := New(50) // very tight budget ~200 chars
	req := makeRequest(t, map[string]string{"role": "user", "content": "q"})

	chunks := make([]retrieval.ContextChunk, 20)
	for i := range chunks {
		chunks[i] = retrieval.ContextChunk{
			ID:         "id",
			SourceID:   "src",
			SourceType: "manual",
			Text:       strings.Repeat("x", 100), // each chunk ~100+ chars
			Score:      float32(20-i) / 20.0,
		}
	}

	out, err := c.Compose(req, chunks, nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])
	tokens := EstimateTokens(sysContent)
	if tokens > 50 {
		t.Errorf("system message exceeds token budget: %d tokens", tokens)
	}
}

func TestCompose_LowestScoringChunkDropped(t *testing.T) {
	// Budget allows profile + one chunk but not two.
	c := New(60) // ~240 chars
	req := makeRequest(t, map[string]string{"role": "user", "content": "q"})

	chunks := []retrieval.ContextChunk{
		{ID: "a", SourceID: "a", SourceType: "m", Text: strings.Repeat("A", 80), Score: 0.9},
		{ID: "b", SourceID: "b", SourceType: "m", Text: strings.Repeat("B", 80), Score: 0.5},
	}

	out, err := c.Compose(req, chunks, nil, "short")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])
	hasA := strings.Contains(sysContent, strings.Repeat("A", 80))
	hasB := strings.Contains(sysContent, strings.Repeat("B", 80))
	if !hasA {
		t.Error("expected high-scoring chunk A to be kept")
	}
	if hasB {
		t.Error("expected low-scoring chunk B to be dropped")
	}
}

func TestCompose_UserMessagesUnchanged(t *testing.T) {
	c := New(4000)

	original := []map[string]string{
		{"role": "user", "content": "first message"},
		{"role": "assistant", "content": "response"},
		{"role": "user", "content": "second message"},
	}
	req := makeRequest(t, original[0], original[1], original[2])

	chunks := []retrieval.ContextChunk{
		{ID: "1", SourceID: "s1", SourceType: "m", Text: "context", Score: 0.8},
	}

	out, err := c.Compose(req, chunks, nil, "profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	// System prepended → 4 messages total.
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	for i, orig := range original {
		got := getContent(msgs[i+1])
		if got != orig["content"] {
			t.Errorf("message %d changed: want %q, got %q", i, orig["content"], got)
		}
	}
}

func TestCompose_PreservesUnknownFields(t *testing.T) {
	c := New(4000)
	raw := `[{"role":"user","content":"hi","name":"alice","tool_call_id":"tc_123"}]`
	req := proxy.ChatRequest{
		Model:    "m",
		Messages: json.RawMessage(raw),
	}

	out, err := c.Compose(req, nil, nil, "profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	// System + original user.
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	userMsg := msgs[1]
	if v, ok := userMsg["name"]; !ok {
		t.Error("name field lost")
	} else {
		var name string
		json.Unmarshal(v, &name)
		if name != "alice" {
			t.Errorf("name changed: %q", name)
		}
	}
	if _, ok := userMsg["tool_call_id"]; !ok {
		t.Error("tool_call_id field lost")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		// "hello world" is 11 characters, so (11+3)/4 = 3 tokens.
		{"hello world", 3},
		{"", 0},
		{"abcd", 1},
		{"abcde", 2},
	}

	for _, tt := range tests {
		got := EstimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestCompose_ExplicitPreferencesCapEnforced(t *testing.T) {
	// Very tight token budget — only enough for a few chunks.
	c := New(80)
	req := makeRequest(t, map[string]string{"role": "user", "content": "q"})

	// 30 uniquely-named explicit preferences, each long enough to trigger the
	// 200-token cap. Each ~40 chars → ~10 tokens, so 30 × 10 = ~300 > 200.
	prefs := make([]string, 30)
	for i := range prefs {
		prefs[i] = fmt.Sprintf("preference number %02d with extended detail", i)
	}

	// 20 bulky chunks that will compete for budget.
	chunks := make([]retrieval.ContextChunk, 20)
	for i := range chunks {
		chunks[i] = retrieval.ContextChunk{
			ID:         "id",
			SourceID:   "src",
			SourceType: "manual",
			Text:       strings.Repeat("x", 100),
			Score:      float32(20-i) / 20.0,
		}
	}

	out, err := c.Compose(req, chunks, prefs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])

	if !strings.Contains(sysContent, "[Explicit Preferences]") {
		t.Error("system message missing [Explicit Preferences] section")
	}

	// The 200-token cap means not all 30 fit. Verify:
	// 1. First items are present (highest priority preserved).
	if !strings.Contains(sysContent, "preference number 00") {
		t.Error("first preference (highest priority) missing")
	}
	if !strings.Contains(sysContent, "preference number 01") {
		t.Error("second preference missing")
	}

	// 2. Some later items are dropped (cap enforced).
	included := 0
	for i := range prefs {
		if strings.Contains(sysContent, fmt.Sprintf("preference number %02d", i)) {
			included++
		}
	}
	if included == 30 {
		t.Error("expected 200-token cap to truncate some preferences, but all 30 were included")
	}
	if included == 0 {
		t.Error("no preferences included at all")
	}
	t.Logf("included %d of 30 preferences (200-token cap)", included)

	// 3. Chunks are truncated, not preferences — no chunk content should appear
	//    given the tight budget after preferences.
	if strings.Contains(sysContent, "[Retrieved Context]") {
		t.Error("expected [Retrieved Context] section to be absent when budget is exceeded by fixed content")
	}
}

func TestCompose_ExplicitSectionBeforeContext(t *testing.T) {
	c := New(4000)
	req := makeRequest(t, map[string]string{"role": "user", "content": "q"})

	prefs := []string{"prefer concise answers"}
	chunks := []retrieval.ContextChunk{
		{ID: "1", SourceID: "doc1", SourceType: "manual", Text: "retrieved context text", Score: 0.9},
	}

	out, err := c.Compose(req, chunks, prefs, "User: engineer.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])

	prefIdx := strings.Index(sysContent, "[Explicit Preferences]")
	ctxIdx := strings.Index(sysContent, "[Retrieved Context]")
	if prefIdx == -1 {
		t.Fatal("system message missing [Explicit Preferences] section")
	}
	if ctxIdx == -1 {
		t.Fatal("system message missing [Retrieved Context] section")
	}
	if prefIdx > ctxIdx {
		t.Error("[Explicit Preferences] should appear before [Retrieved Context]")
	}
}

func TestCompose_ChunksTruncatedWhenBudgetExceeded(t *testing.T) {
	// Budget large enough for explicit prefs and profile, but tight for chunks.
	c := New(100)
	req := makeRequest(t, map[string]string{"role": "user", "content": "q"})

	// A few explicit preferences — must always be present.
	explicitPrefs := []string{"explicit pref one", "explicit pref two"}

	// Many large chunks that should be truncated by the budget.
	chunks := make([]retrieval.ContextChunk, 10)
	for i := range chunks {
		chunks[i] = retrieval.ContextChunk{
			ID:         "id",
			SourceID:   "src",
			SourceType: "manual",
			Text:       strings.Repeat("z", 200),
			Score:      float32(10-i) / 10.0,
		}
	}

	out, err := c.Compose(req, chunks, explicitPrefs, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs := decodeMessages(t, out)
	sysContent := getContent(msgs[0])

	// Explicit prefs must be present.
	for _, pref := range explicitPrefs {
		if !strings.Contains(sysContent, pref) {
			t.Errorf("explicit pref %q was truncated (must never be)", pref)
		}
	}

	// At least some chunks should have been truncated (200 z's × 10 = way over budget).
	// Count occurrences of the chunk text.
	chunkCount := strings.Count(sysContent, strings.Repeat("z", 200))
	if chunkCount == 10 {
		t.Error("expected some chunks to be truncated when budget is tight, but all 10 are present")
	}
}
