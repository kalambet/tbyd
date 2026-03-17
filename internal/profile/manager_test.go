package profile

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// --- Mock ProfileStore ---

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
		return "", errors.New("not found")
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

func (m *mockProfileStore) DeleteProfileKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return errors.New("not found")
	}
	delete(m.data, key)
	return nil
}

// fixedClock returns a constant time (cache never expires in tests).
type fixedClock struct{ t time.Time }

func (c *fixedClock) Now() time.Time { return c.t }

func newTestManager() (*Manager, *mockProfileStore) {
	store := newMockProfileStore()
	mgr := NewManagerWithClock(store, &fixedClock{t: time.Now()}, time.Hour)
	return mgr, store
}

// --- ApplyDelta tests ---

func TestApplyDelta_AddsPreferences(t *testing.T) {
	mgr, _ := newTestManager()

	delta := ProfileDelta{
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

	delta := ProfileDelta{
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

	delta := ProfileDelta{
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

	delta := ProfileDelta{
		AddPreferences: []string{"some preference"},
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	if !invalidated {
		t.Error("expected onInvalidate callback to be called after ApplyDelta")
	}
}

func TestApplyDelta_InvalidatesExactlyOnce(t *testing.T) {
	mgr, _ := newTestManager()

	callCount := 0
	mgr.OnInvalidate(func() { callCount++ })

	// Delta that exercises both phases: preferences + field updates.
	delta := ProfileDelta{
		AddPreferences: []string{"be concise"},
		UpdateFields: map[string]string{
			"communication.tone":         "technical",
			"communication.detail_level": "high",
		},
	}
	if err := mgr.ApplyDelta(delta); err != nil {
		t.Fatalf("ApplyDelta failed: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected onInvalidate to be called exactly once, got %d", callCount)
	}
}
