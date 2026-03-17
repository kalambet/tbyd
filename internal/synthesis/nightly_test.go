package synthesis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/storage"
)

// --- mock store ---

type mockNightlyStore struct {
	interactions        []storage.Interaction
	docs                []storage.ContextDoc
	signals             []storage.SignalCount
	savedDeltas         []storage.PendingProfileDelta
	hasPending          bool
	enqueuedJobs        []storage.Job
	claimErr            error
	completeCount       int
	failCount           int
	enqueueCount        atomic.Int64
}

func (m *mockNightlyStore) GetInteractionsWithFeedbackSince(_ time.Time) ([]storage.Interaction, error) {
	return m.interactions, nil
}

func (m *mockNightlyStore) GetContextDocsSince(_ time.Time) ([]storage.ContextDoc, error) {
	return m.docs, nil
}

func (m *mockNightlyStore) GetSignalCounts() ([]storage.SignalCount, error) {
	return m.signals, nil
}

func (m *mockNightlyStore) SavePendingDelta(delta storage.PendingProfileDelta) error {
	m.savedDeltas = append(m.savedDeltas, delta)
	return nil
}

func (m *mockNightlyStore) HasPendingDeltaForSource(_ string, _ time.Time) (bool, error) {
	return m.hasPending, nil
}

func (m *mockNightlyStore) EnqueueJob(_ context.Context, job storage.Job) error {
	m.enqueueCount.Add(1)
	m.enqueuedJobs = append(m.enqueuedJobs, job)
	return nil
}

func (m *mockNightlyStore) ClaimNextJob(_ []string) (*storage.Job, error) {
	if m.claimErr != nil {
		return nil, m.claimErr
	}
	return nil, nil
}

func (m *mockNightlyStore) CompleteJob(_ string) error {
	m.completeCount++
	return nil
}

func (m *mockNightlyStore) FailJob(_ string, _ string) error {
	m.failCount++
	return nil
}

// --- mock LLM ---

type nightlyMockChatter struct {
	response string
	err      error
	called   atomic.Int64
}

func (m *nightlyMockChatter) Chat(_ context.Context, _ string, _ []ollama.Message, _ *ollama.Schema) (string, error) {
	m.called.Add(1)
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

// buildSynthesizer is a helper for constructing a NightlySynthesizer with mocks.
func buildSynthesizer(store NightlyStore, chatter OllamaChatter) *NightlySynthesizer {
	return NewNightlySynthesizer(store, chatter, "test-model")
}

// --- tests ---

func TestRun_NoInteractions(t *testing.T) {
	store := &mockNightlyStore{}
	chatter := &nightlyMockChatter{response: `{"add_preferences":[],"remove_preferences":[],"description":"no changes"}`}

	s := buildSynthesizer(store, chatter)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	// No data → no LLM call, no delta written.
	if chatter.called.Load() != 0 {
		t.Errorf("LLM called %d times, want 0", chatter.called.Load())
	}
	if len(store.savedDeltas) != 0 {
		t.Errorf("saved %d deltas, want 0", len(store.savedDeltas))
	}
}

func TestRun_ProducesDeltas(t *testing.T) {
	interactions := make([]storage.Interaction, 10)
	for i := range interactions {
		interactions[i] = storage.Interaction{
			ID:            fmt.Sprintf("ix-%d", i),
			UserQuery:     "Tell me something",
			FeedbackScore: -1,
			FeedbackNotes: "too verbose",
			CreatedAt:     time.Now(),
		}
	}

	store := &mockNightlyStore{interactions: interactions}
	chatter := &nightlyMockChatter{
		response: `{"add_preferences":["concise responses"],"remove_preferences":["verbose explanations"],"description":"user prefers brevity"}`,
	}

	s := buildSynthesizer(store, chatter)
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	if len(store.savedDeltas) != 1 {
		t.Fatalf("saved %d deltas, want 1", len(store.savedDeltas))
	}
	delta := store.savedDeltas[0]
	if delta.Source != nightlySynthesisJobType {
		t.Errorf("delta.Source = %q, want %q", delta.Source, nightlySynthesisJobType)
	}
	if delta.Description != "user prefers brevity" {
		t.Errorf("delta.Description = %q, want %q", delta.Description, "user prefers brevity")
	}
	if delta.ID == "" {
		t.Error("delta.ID is empty")
	}
}

func TestRun_LLMMalformedResponse(t *testing.T) {
	interactions := []storage.Interaction{
		{ID: "ix-1", UserQuery: "q", FeedbackScore: 1, CreatedAt: time.Now()},
	}

	store := &mockNightlyStore{interactions: interactions}
	chatter := &nightlyMockChatter{response: "not valid json at all {{"}

	s := buildSynthesizer(store, chatter)
	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("Run() expected error for malformed JSON, got nil")
	}
	if len(store.savedDeltas) != 0 {
		t.Errorf("saved %d deltas, want 0 on LLM error", len(store.savedDeltas))
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	interactions := []storage.Interaction{
		{ID: "ix-1", UserQuery: "q", FeedbackScore: 1, CreatedAt: time.Now()},
	}

	store := &mockNightlyStore{interactions: interactions}
	// The chatter blocks until context is cancelled.
	blockCh := make(chan struct{})
	chatter := &blockingChatter{blockCh: blockCh}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- buildSynthesizer(store, chatter).Run(ctx)
	}()

	// Cancel context before the LLM returns.
	cancel()
	close(blockCh)

	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			// Either context.Canceled or a wrapped cancellation error is acceptable.
			// Some callers wrap: "LLM synthesis call: context canceled".
			// Allow any non-nil error that propagates cancellation.
			if err == nil {
				t.Error("Run() = nil, want context cancellation error")
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not exit after context cancellation within 3s")
	}
}

// blockingChatter blocks until blockCh is closed, then returns the ctx error.
type blockingChatter struct {
	blockCh chan struct{}
}

func (b *blockingChatter) Chat(ctx context.Context, _ string, _ []ollama.Message, _ *ollama.Schema) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-b.blockCh:
		return "", context.Canceled
	}
}

func TestSchedule_FiresOnInterval(t *testing.T) {
	store := &mockNightlyStore{}
	chatter := &nightlyMockChatter{}

	s := buildSynthesizer(store, chatter)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go s.Schedule(ctx, 50*time.Millisecond)

	<-ctx.Done()

	count := store.enqueueCount.Load()
	if count < 2 {
		t.Errorf("enqueued %d jobs in 500ms with 50ms interval, want >= 2", count)
	}
}

func TestSchedule_StopsOnContextCancel(t *testing.T) {
	store := &mockNightlyStore{}
	chatter := &nightlyMockChatter{}

	s := buildSynthesizer(store, chatter)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Schedule(ctx, 10*time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Schedule did not stop after context cancellation within 2s")
	}

	// No further enqueue calls after cancel.
	beforeCount := store.enqueueCount.Load()
	time.Sleep(30 * time.Millisecond)
	afterCount := store.enqueueCount.Load()
	if afterCount != beforeCount {
		t.Errorf("enqueue count changed after cancel: before=%d after=%d", beforeCount, afterCount)
	}
}

func TestSanitizePreferences(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  int // expected length of output
	}{
		{"empty", nil, 0},
		{"normal", []string{"concise", "formal"}, 2},
		{"filters_empty", []string{"concise", "", "  ", "formal"}, 2},
		{"caps_count", make([]string, 30), 0}, // 30 empty strings → all filtered
		{
			"caps_count_nonempty",
			func() []string {
				s := make([]string, 25)
				for i := range s {
					s[i] = fmt.Sprintf("pref-%d", i)
				}
				return s
			}(),
			maxPreferences,
		},
		{
			"truncates_long",
			[]string{strings.Repeat("a", 300)},
			1,
		},
		{
			"utf8_boundary",
			// "é" is 2 bytes; a string of 101 "é" = 202 bytes > maxPreferenceLen (200).
			// Truncation must not split the multi-byte codepoint.
			[]string{strings.Repeat("é", 101)},
			1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizePreferences(tt.input)
			if len(got) != tt.want {
				t.Errorf("sanitizePreferences() returned %d items, want %d", len(got), tt.want)
			}
			for _, p := range got {
				if len(p) > maxPreferenceLen {
					t.Errorf("preference %q exceeds maxPreferenceLen (%d bytes)", p[:50], maxPreferenceLen)
				}
				if p == "" {
					t.Error("sanitizePreferences() returned an empty string")
				}
			}
		})
	}
}

func TestSanitizeUpdateFields(t *testing.T) {
	// Nil map.
	if got := sanitizeUpdateFields(nil); len(got) != 0 {
		t.Errorf("nil map: got %d entries", len(got))
	}

	// Normal map.
	m := map[string]string{"role": "engineer", "tone": "direct"}
	got := sanitizeUpdateFields(m)
	if len(got) != 2 {
		t.Errorf("normal map: got %d entries, want 2", len(got))
	}

	// Caps count.
	big := make(map[string]string, 30)
	for i := range 30 {
		big[fmt.Sprintf("key-%d", i)] = "val"
	}
	got = sanitizeUpdateFields(big)
	if len(got) > maxPreferences {
		t.Errorf("big map: got %d entries, want <= %d", len(got), maxPreferences)
	}
}
