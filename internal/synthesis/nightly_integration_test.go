//go:build integration

package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

// TestSynthesisEndToEnd inserts 12 feedback-labeled interactions with consistent
// negative feedback on verbosity, runs Run(), and verifies the pending delta
// reflects a preference for concise responses.
func TestSynthesisEndToEnd(t *testing.T) {
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	defer store.Close()

	ctx := context.Background()

	// Insert 12 interactions with consistent negative feedback on verbosity.
	for i := 0; i < 12; i++ {
		ix := storage.Interaction{
			ID:            fmt.Sprintf("ix-e2e-%d", i),
			CreatedAt:     time.Now().UTC(),
			UserQuery:     "Explain the CAP theorem",
			CloudResponse: "The CAP theorem states... [very long verbose response that goes on and on]",
			FeedbackScore: -1,
			FeedbackNotes: "too verbose, please be more concise",
			Status:        "completed",
			VectorIDs:     "[]",
		}
		if err := store.SaveInteraction(ctx, ix); err != nil {
			t.Fatalf("SaveInteraction(%d): %v", i, err)
		}
	}

	// Use a stub chatter that simulates a real LLM response indicating conciseness preference.
	chatter := &e2eVerbosityChatter{}

	synth := NewNightlySynthesizer(store, chatter, "test-deep-model")
	if err := synth.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	deltas, err := store.ListPendingDeltas()
	if err != nil {
		t.Fatalf("ListPendingDeltas: %v", err)
	}
	if len(deltas) != 1 {
		t.Fatalf("got %d pending deltas, want 1", len(deltas))
	}

	var pd profile.ProfileDelta
	if err := json.Unmarshal([]byte(deltas[0].DeltaJSON), &pd); err != nil {
		t.Fatalf("unmarshal delta JSON: %v", err)
	}

	// Verify the delta includes a "concise" preference.
	foundConcise := false
	for _, p := range pd.AddPreferences {
		if strings.Contains(strings.ToLower(p), "concise") {
			foundConcise = true
			break
		}
	}
	if !foundConcise {
		t.Errorf("expected a 'concise' preference in AddPreferences, got: %v", pd.AddPreferences)
	}
}

// e2eVerbosityChatter is a stub OllamaChatter that returns a fixed synthesis response.
type e2eVerbosityChatter struct{}

func (v *e2eVerbosityChatter) Chat(_ context.Context, _ string, _ []ollama.Message, _ *ollama.Schema) (string, error) {
	return `{
		"add_preferences": ["concise responses"],
		"remove_preferences": ["verbose explanations"],
		"update_fields": {},
		"description": "User consistently negatively rated verbose responses; prefer concise answers"
	}`, nil
}
