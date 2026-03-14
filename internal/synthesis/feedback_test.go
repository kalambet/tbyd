package synthesis

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/storage"
)

// --- Mock OllamaChatter ---

type mockChatter struct {
	response string
	err      error
	// capturedMessages records the messages sent on the last call.
	capturedMessages []ollama.Message
}

func (m *mockChatter) Chat(_ context.Context, _ string, messages []ollama.Message, _ *ollama.Schema) (string, error) {
	m.capturedMessages = messages
	return m.response, m.err
}

// --- Helpers ---

func newTestInteraction() storage.Interaction {
	return storage.Interaction{
		ID:             "test-id",
		UserQuery:      "How do I write a Go interface?",
		EnrichedPrompt: "ENRICHED: some sensitive internal context",
		CloudResponse:  `{"choices":[{"message":{"content":"Use the interface keyword."}}]}`,
		FeedbackScore:  1,
		FeedbackNotes:  "Very helpful",
	}
}

// --- Tests ---

func TestExtractFromFeedback_PositiveScore(t *testing.T) {
	chatter := &mockChatter{
		response: `{"signals":[{"type":"positive","pattern":"user prefers concise responses"}]}`,
	}
	extractor := NewPreferenceExtractor(chatter, "test-model")

	signals, err := extractor.ExtractFromFeedback(context.Background(), newTestInteraction(), 1, "Good")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].Type != "positive" {
		t.Errorf("expected type=positive, got %q", signals[0].Type)
	}
	if signals[0].Pattern != "user prefers concise responses" {
		t.Errorf("unexpected pattern: %q", signals[0].Pattern)
	}
}

func TestExtractFromFeedback_NegativeScore(t *testing.T) {
	chatter := &mockChatter{
		response: `{"signals":[{"type":"negative","pattern":"user dislikes verbose explanations"}]}`,
	}
	extractor := NewPreferenceExtractor(chatter, "test-model")

	signals, err := extractor.ExtractFromFeedback(context.Background(), newTestInteraction(), -1, "Too long")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].Type != "negative" {
		t.Errorf("expected type=negative, got %q", signals[0].Type)
	}
}

func TestExtractFromFeedback_UsesOriginalQueryOnly(t *testing.T) {
	chatter := &mockChatter{
		response: `{"signals":[]}`,
	}
	extractor := NewPreferenceExtractor(chatter, "test-model")

	interaction := newTestInteraction()
	_, _ = extractor.ExtractFromFeedback(context.Background(), interaction, 1, "")

	// Verify EnrichedPrompt does NOT appear in any message sent to the LLM.
	for _, msg := range chatter.capturedMessages {
		if strings.Contains(msg.Content, "ENRICHED") {
			t.Errorf("LLM prompt contained EnrichedPrompt content; messages: %+v", chatter.capturedMessages)
		}
	}

	// Verify UserQuery IS present.
	found := false
	for _, msg := range chatter.capturedMessages {
		if strings.Contains(msg.Content, interaction.UserQuery) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LLM prompt did not contain UserQuery; messages: %+v", chatter.capturedMessages)
	}
}

func TestExtractFromFeedback_LLMFails(t *testing.T) {
	chatter := &mockChatter{
		err: errors.New("connection refused"),
	}
	extractor := NewPreferenceExtractor(chatter, "test-model")

	signals, err := extractor.ExtractFromFeedback(context.Background(), newTestInteraction(), 1, "")

	if err == nil {
		t.Fatal("expected error on LLM failure, got nil")
	}
	if signals != nil {
		t.Errorf("expected nil signals on LLM failure, got %v", signals)
	}
}

func TestExtractFromFeedback_MalformedLLMResponse(t *testing.T) {
	chatter := &mockChatter{
		response: `not valid json`,
	}
	extractor := NewPreferenceExtractor(chatter, "test-model")

	signals, err := extractor.ExtractFromFeedback(context.Background(), newTestInteraction(), 1, "")

	if err == nil {
		t.Fatal("expected error on malformed response, got nil")
	}
	if signals != nil {
		t.Errorf("expected nil signals on malformed response, got %v", signals)
	}
}
