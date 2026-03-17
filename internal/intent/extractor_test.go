package intent

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/profile"
)

// mockChatter implements OllamaChatter for testing.
type mockChatter struct {
	response string
	err      error
	delay    time.Duration
}

func (m *mockChatter) Chat(ctx context.Context, model string, messages []ollama.Message, jsonSchema *ollama.Schema) (string, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return m.response, m.err
}

func TestExtract_RecallIntent(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"recall","entities":["database schema"],"topics":["architecture","decisions"],"context_needs":["past_decisions"],"is_private":false}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	got := e.Extract(context.Background(), "what did I decide about the database schema last week", nil, "", profile.CalibrationContext{})

	want := Intent{
		IntentType:   "recall",
		Entities:     []string{"database schema"},
		Topics:       []string{"architecture", "decisions"},
		ContextNeeds: []string{"past_decisions"},
		IsPrivate:    false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Extract() = %+v, want %+v", got, want)
	}
}

func TestExtract_TaskIntent(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"task","entities":["CI pipeline"],"topics":["devops","automation"],"context_needs":["project_config"],"is_private":false}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	got := e.Extract(context.Background(), "set up CI for the project", nil, "", profile.CalibrationContext{})

	want := Intent{
		IntentType:   "task",
		Entities:     []string{"CI pipeline"},
		Topics:       []string{"devops", "automation"},
		ContextNeeds: []string{"project_config"},
		IsPrivate:    false,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Extract() = %+v, want %+v", got, want)
	}
}

func TestExtract_MalformedJSON(t *testing.T) {
	mock := &mockChatter{
		response: `not valid json {{{`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	intent := e.Extract(context.Background(), "some query", nil, "", profile.CalibrationContext{})

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value", intent.IntentType)
	}
}

func TestExtract_Timeout(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"recall"}`,
		delay:    5 * time.Second,
	}
	e := NewExtractor(mock, "phi3.5", nil)

	start := time.Now()
	intent := e.Extract(context.Background(), "query", nil, "", profile.CalibrationContext{})
	elapsed := time.Since(start)

	if elapsed > 3500*time.Millisecond {
		t.Errorf("Extract took %v, want < 3.5s", elapsed)
	}
	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value on timeout", intent.IntentType)
	}
}

func TestExtract_OllamaDown(t *testing.T) {
	mock := &mockChatter{
		err: fmt.Errorf("connection refused"),
	}
	e := NewExtractor(mock, "phi3.5", nil)
	intent := e.Extract(context.Background(), "hello", nil, "", profile.CalibrationContext{})

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value on error", intent.IntentType)
	}
}

func TestExtract_PrivateFlag(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":true}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	intent := e.Extract(context.Background(), "what is my SSN", nil, "", profile.CalibrationContext{})

	if !intent.IsPrivate {
		t.Error("IsPrivate = false, want true")
	}
}

func TestExtract_EmptyQuery(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"question"}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	intent := e.Extract(context.Background(), "", nil, "", profile.CalibrationContext{})

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value for empty query", intent.IntentType)
	}
}

func TestExtract_SearchStrategy(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"recall","entities":["Kubernetes"],"topics":["devops"],"context_needs":["docs"],"is_private":false,"search_strategy":"hybrid","hybrid_ratio":0.6,"suggested_top_k":10}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	got := e.Extract(context.Background(), "find docs about Kubernetes", nil, "", profile.CalibrationContext{})

	if got.SearchStrategy != "hybrid" {
		t.Errorf("SearchStrategy = %q, want %q", got.SearchStrategy, "hybrid")
	}
	if got.HybridRatio == nil || *got.HybridRatio != 0.6 {
		t.Errorf("HybridRatio = %v, want 0.6", got.HybridRatio)
	}
	if got.SuggestedTopK != 10 {
		t.Errorf("SuggestedTopK = %d, want 10", got.SuggestedTopK)
	}
}

func TestExtract_DefaultStrategy(t *testing.T) {
	// When the LLM doesn't return search fields, they should be zero values.
	mock := &mockChatter{
		response: `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":false}`,
	}
	e := NewExtractor(mock, "phi3.5", nil)
	got := e.Extract(context.Background(), "how does Go handle concurrency", nil, "", profile.CalibrationContext{})

	if got.SearchStrategy != "" {
		t.Errorf("SearchStrategy = %q, want empty (default)", got.SearchStrategy)
	}
	if got.HybridRatio != nil {
		t.Errorf("HybridRatio = %v, want nil (default)", got.HybridRatio)
	}
	if got.SuggestedTopK != 0 {
		t.Errorf("SuggestedTopK = %d, want 0 (default)", got.SuggestedTopK)
	}
}

func TestExtract_WithCalibration(t *testing.T) {
	var capturedMessages []ollama.Message
	mock := &mockChatter{
		response: `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":false}`,
	}
	// Wrap the mock to capture messages sent to Chat.
	capturingMock := &capturingChatter{
		inner:    mock,
		captured: &capturedMessages,
	}

	calibration := profile.CalibrationContext{Hints: "User is an expert Go developer."}
	e := NewExtractor(capturingMock, "phi3.5", func() profile.CalibrationContext { return calibration })
	e.Extract(context.Background(), "how do goroutines work", nil, "", profile.CalibrationContext{})

	if len(capturedMessages) == 0 {
		t.Fatal("no messages captured")
	}
	systemContent := capturedMessages[0].Content
	if !strings.Contains(systemContent, "User is an expert Go developer.") {
		t.Errorf("system prompt missing calibration text: %q", systemContent)
	}
	if !strings.Contains(systemContent, "[Calibration]") {
		t.Errorf("system prompt missing [Calibration] section header: %q", systemContent)
	}
}

// capturingChatter records the messages passed to Chat for assertion in tests.
type capturingChatter struct {
	inner    OllamaChatter
	captured *[]ollama.Message
}

func (c *capturingChatter) Chat(ctx context.Context, model string, messages []ollama.Message, jsonSchema *ollama.Schema) (string, error) {
	*c.captured = append(*c.captured, messages...)
	return c.inner.Chat(ctx, model, messages, jsonSchema)
}
