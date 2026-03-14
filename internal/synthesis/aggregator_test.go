package synthesis

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/profile"
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
// ApplyDelta tests — inline mock store (mirrors profile/manager_test.go)
// ============================================================

// synthMockStore is a minimal ProfileStore for use in ApplyDelta tests.
type synthMockStore struct {
	mu   sync.Mutex
	data map[string]string
}

func newSynthMockStore() *synthMockStore {
	return &synthMockStore{data: make(map[string]string)}
}

func (m *synthMockStore) SetProfileKey(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *synthMockStore) GetProfileKey(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func (m *synthMockStore) GetAllProfileKeys() (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make(map[string]string, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, nil
}

func (m *synthMockStore) DeleteProfileKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return errors.New("not found")
	}
	delete(m.data, key)
	return nil
}

// newTestManager creates a profile.Manager backed by synthMockStore.
func newTestManager() (*profile.Manager, *synthMockStore) {
	store := newSynthMockStore()
	mgr := profile.NewManagerWithClock(store, &fixedClock{t: time.Now()}, time.Hour)
	return mgr, store
}

// fixedClock returns a constant time (cache never expires in tests).
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func TestApplyDelta_AddsPreferences(t *testing.T) {
	mgr, _ := newTestManager()

	delta := profile.ProfileDelta{
		AddPreferences: []string{"concise responses", "use examples"},
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if len(p.Preferences) != 2 {
		t.Fatalf("expected 2 preferences, got %d: %v", len(p.Preferences), p.Preferences)
	}
}

func TestApplyDelta_RemovesPreferences(t *testing.T) {
	mgr, _ := newTestManager()

	// Seed with two preferences.
	if err := mgr.SetField("preferences", []string{"concise responses", "use examples"}); err != nil {
		t.Fatalf("SetField failed: %v", err)
	}

	delta := profile.ProfileDelta{
		RemovePreferences: []string{"concise responses"},
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if len(p.Preferences) != 1 {
		t.Fatalf("expected 1 preference, got %d: %v", len(p.Preferences), p.Preferences)
	}
	if p.Preferences[0] != "use examples" {
		t.Errorf("unexpected remaining preference: %q", p.Preferences[0])
	}
}

func TestApplyDelta_Idempotent(t *testing.T) {
	mgr, _ := newTestManager()

	delta := profile.ProfileDelta{
		AddPreferences: []string{"concise responses"},
	}

	// Apply the same delta twice.
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("first ApplyDelta failed: %v", err)
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("second ApplyDelta failed: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile failed: %v", err)
	}
	if len(p.Preferences) != 1 {
		t.Fatalf("expected 1 preference after idempotent apply, got %d: %v", len(p.Preferences), p.Preferences)
	}
}

func TestApplyDelta_InvalidatesCache(t *testing.T) {
	mgr, _ := newTestManager()

	invalidated := false
	mgr.OnInvalidate(func() { invalidated = true })

	delta := profile.ProfileDelta{
		AddPreferences: []string{"some preference"},
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	if !invalidated {
		t.Error("expected onInvalidate callback to be called after ApplyDelta")
	}
}
