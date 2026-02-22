package intent

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
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
	e := NewExtractor(mock, "phi3.5")
	got := e.Extract(context.Background(), "what did I decide about the database schema last week", nil, "")

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
	e := NewExtractor(mock, "phi3.5")
	got := e.Extract(context.Background(), "set up CI for the project", nil, "")

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
	e := NewExtractor(mock, "phi3.5")
	intent := e.Extract(context.Background(), "some query", nil, "")

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value", intent.IntentType)
	}
}

func TestExtract_Timeout(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"recall"}`,
		delay:    5 * time.Second,
	}
	e := NewExtractor(mock, "phi3.5")

	start := time.Now()
	intent := e.Extract(context.Background(), "query", nil, "")
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
	e := NewExtractor(mock, "phi3.5")
	intent := e.Extract(context.Background(), "hello", nil, "")

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value on error", intent.IntentType)
	}
}

func TestExtract_PrivateFlag(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"question","entities":[],"topics":[],"context_needs":[],"is_private":true}`,
	}
	e := NewExtractor(mock, "phi3.5")
	intent := e.Extract(context.Background(), "what is my SSN", nil, "")

	if !intent.IsPrivate {
		t.Error("IsPrivate = false, want true")
	}
}

func TestExtract_EmptyQuery(t *testing.T) {
	mock := &mockChatter{
		response: `{"intent_type":"question"}`,
	}
	e := NewExtractor(mock, "phi3.5")
	intent := e.Extract(context.Background(), "", nil, "")

	if intent.IntentType != "" {
		t.Errorf("IntentType = %q, want zero value for empty query", intent.IntentType)
	}
}
