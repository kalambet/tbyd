package synthesis

import (
	"errors"
	"testing"

	"github.com/kalambet/tbyd/internal/storage"
)

// ============================================================
// Aggregator tests (pure logic, no mocks needed)
// ============================================================

func TestAggregate_BelowCount(t *testing.T) {
	// 1 positive signal — below all thresholds (count < 3 AND net < 2)
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers bullet points"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 0 {
		t.Errorf("expected no additions, got %v", delta.AddPreferences)
	}
	if len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no removals, got %v", delta.RemovePreferences)
	}
}

func TestAggregate_CountRuleApplies(t *testing.T) {
	// 3 positive signals — count rule activates
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers bullet points"},
		{Type: "positive", Pattern: "user prefers bullet points"},
		{Type: "positive", Pattern: "user prefers bullet points"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition, got %v", delta.AddPreferences)
	}
	if delta.AddPreferences[0] != "user prefers bullet points" {
		t.Errorf("unexpected addition: %q", delta.AddPreferences[0])
	}
}

func TestAggregate_NetScoreAdds(t *testing.T) {
	// 4 positive + 1 negative → net = 3 >= 2, net score rule adds
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers examples"},
		{Type: "positive", Pattern: "user prefers examples"},
		{Type: "positive", Pattern: "user prefers examples"},
		{Type: "positive", Pattern: "user prefers examples"},
		{Type: "negative", Pattern: "user prefers examples"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition, got %v", delta.AddPreferences)
	}
}

func TestAggregate_NetScoreRemoves(t *testing.T) {
	// 1 positive + 3 negative → net = -2 <= -2, net score rule removes
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers long answers"},
		{Type: "negative", Pattern: "user prefers long answers"},
		{Type: "negative", Pattern: "user prefers long answers"},
		{Type: "negative", Pattern: "user prefers long answers"},
	}
	delta := Aggregate(signals)
	if len(delta.RemovePreferences) != 1 {
		t.Fatalf("expected 1 removal, got %v", delta.RemovePreferences)
	}
}

func TestAggregate_TrueConflict(t *testing.T) {
	// 2 positive + 2 negative → net = 0, count < 3 for either side → no changes
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers markdown"},
		{Type: "positive", Pattern: "user prefers markdown"},
		{Type: "negative", Pattern: "user prefers markdown"},
		{Type: "negative", Pattern: "user prefers markdown"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 0 || len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no changes on true conflict, got add=%v remove=%v", delta.AddPreferences, delta.RemovePreferences)
	}
}

func TestAggregate_NetScoreAddsBoundary(t *testing.T) {
	// 2 positive + 0 negative → net = 2, count < 3 → net score rule adds
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers short answers"},
		{Type: "positive", Pattern: "user prefers short answers"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition for net=2, got %v", delta.AddPreferences)
	}
}

func TestAggregate_CountRuleWithoutNet(t *testing.T) {
	// 3 positive + 2 negative → count rule fires (pos=3), net=1 (no net activation)
	// Count rule alone should add.
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "user prefers tables"},
		{Type: "positive", Pattern: "user prefers tables"},
		{Type: "positive", Pattern: "user prefers tables"},
		{Type: "negative", Pattern: "user prefers tables"},
		{Type: "negative", Pattern: "user prefers tables"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition from count rule, got %v", delta.AddPreferences)
	}
}

func TestAggregate_BothCountsConflict(t *testing.T) {
	// 5 positive + 5 negative → both count rules fire → conflict, no changes
	signals := make([]PreferenceSignal, 0, 10)
	for i := 0; i < 5; i++ {
		signals = append(signals, PreferenceSignal{Type: "positive", Pattern: "user prefers dark mode"})
		signals = append(signals, PreferenceSignal{Type: "negative", Pattern: "user prefers dark mode"})
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 0 || len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no changes on true conflict (both count rules), got add=%v remove=%v", delta.AddPreferences, delta.RemovePreferences)
	}
}

func TestAggregate_EmptyInput(t *testing.T) {
	delta := Aggregate(nil)
	if len(delta.AddPreferences) != 0 || len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no changes on empty input, got add=%v remove=%v", delta.AddPreferences, delta.RemovePreferences)
	}
}

func TestAggregate_CaseNormalization(t *testing.T) {
	// "User Prefers X" and "user prefers x" should aggregate together
	signals := []PreferenceSignal{
		{Type: "positive", Pattern: "User Prefers Markdown"},
		{Type: "positive", Pattern: "user prefers markdown"},
		{Type: "positive", Pattern: "USER PREFERS MARKDOWN"},
	}
	delta := Aggregate(signals)
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition after case normalization, got %v", delta.AddPreferences)
	}
	// Original casing of first signal should be preserved.
	if delta.AddPreferences[0] != "User Prefers Markdown" {
		t.Errorf("expected first-seen casing preserved, got %q", delta.AddPreferences[0])
	}
}

func TestAggregate_RemovesNegated(t *testing.T) {
	// 3 negative signals → count rule removes
	signals := []PreferenceSignal{
		{Type: "negative", Pattern: "user prefers code only"},
		{Type: "negative", Pattern: "user prefers code only"},
		{Type: "negative", Pattern: "user prefers code only"},
	}
	delta := Aggregate(signals)
	if len(delta.RemovePreferences) != 1 {
		t.Fatalf("expected 1 removal, got %v", delta.RemovePreferences)
	}
	if delta.RemovePreferences[0] != "user prefers code only" {
		t.Errorf("unexpected removal: %q", delta.RemovePreferences[0])
	}
}

// ============================================================
// AggregateFromCounts tests (production aggregation path)
// ============================================================

// mockSignalCountReader implements SignalCountReader for testing.
type mockSignalCountReader struct {
	counts []storage.SignalCount
	err    error
}

func (m *mockSignalCountReader) GetSignalCounts() ([]storage.SignalCount, error) {
	return m.counts, m.err
}

func TestAggregateFromCounts_Adds(t *testing.T) {
	reader := &mockSignalCountReader{
		counts: []storage.SignalCount{
			{PatternKey: "concise", PatternDisplay: "user prefers concise responses", PositiveCount: 3, NegativeCount: 0},
		},
	}
	delta, err := AggregateFromCounts(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(delta.AddPreferences) != 1 {
		t.Fatalf("expected 1 addition, got %v", delta.AddPreferences)
	}
	if delta.AddPreferences[0] != "user prefers concise responses" {
		t.Errorf("unexpected addition: %q", delta.AddPreferences[0])
	}
}

func TestAggregateFromCounts_Removes(t *testing.T) {
	reader := &mockSignalCountReader{
		counts: []storage.SignalCount{
			{PatternKey: "verbose", PatternDisplay: "user prefers verbose answers", PositiveCount: 0, NegativeCount: 3},
		},
	}
	delta, err := AggregateFromCounts(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(delta.RemovePreferences) != 1 {
		t.Fatalf("expected 1 removal, got %v", delta.RemovePreferences)
	}
}

func TestAggregateFromCounts_Conflict(t *testing.T) {
	reader := &mockSignalCountReader{
		counts: []storage.SignalCount{
			{PatternKey: "markdown", PatternDisplay: "user prefers markdown", PositiveCount: 5, NegativeCount: 5},
		},
	}
	delta, err := AggregateFromCounts(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(delta.AddPreferences) != 0 || len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no changes on conflict, got add=%v remove=%v", delta.AddPreferences, delta.RemovePreferences)
	}
}

func TestAggregateFromCounts_BelowThreshold(t *testing.T) {
	reader := &mockSignalCountReader{
		counts: []storage.SignalCount{
			{PatternKey: "bullets", PatternDisplay: "user prefers bullet points", PositiveCount: 1, NegativeCount: 0},
		},
	}
	delta, err := AggregateFromCounts(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(delta.AddPreferences) != 0 || len(delta.RemovePreferences) != 0 {
		t.Errorf("expected no changes below threshold, got add=%v remove=%v", delta.AddPreferences, delta.RemovePreferences)
	}
}

func TestAggregateFromCounts_StorageError(t *testing.T) {
	reader := &mockSignalCountReader{
		err: errors.New("database locked"),
	}
	_, err := AggregateFromCounts(reader)
	if err == nil {
		t.Fatal("expected error from storage, got nil")
	}
}
