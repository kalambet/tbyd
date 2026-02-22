package profile

import (
	"sync"
	"testing"
	"time"
)

// --- Mock store ---

type mockStore struct {
	mu   sync.Mutex
	data map[string]string

	getAllCalls int
}

func newMockStore() *mockStore {
	return &mockStore{data: make(map[string]string)}
}

func (m *mockStore) SetProfileKey(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *mockStore) GetProfileKey(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

func (m *mockStore) GetAllProfileKeys() (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getAllCalls++
	cp := make(map[string]string, len(m.data))
	for k, v := range m.data {
		cp[k] = v
	}
	return cp, nil
}

// --- Mock clock ---

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// --- Tests ---

func TestGetProfile_Empty(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Identity.Role != "" {
		t.Errorf("expected empty role, got %q", p.Identity.Role)
	}
	if len(p.Interests) != 0 {
		t.Errorf("expected no interests, got %v", p.Interests)
	}
}

func TestSetAndGetField(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	if err := mgr.SetField("communication.tone", "direct"); err != nil {
		t.Fatalf("SetField error: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile error: %v", err)
	}
	if p.Communication.Tone != "direct" {
		t.Errorf("expected tone %q, got %q", "direct", p.Communication.Tone)
	}
}

func TestGetSummary_Empty(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	summary := mgr.GetSummary()
	if summary == "" {
		t.Error("expected non-empty summary for empty profile")
	}
}

func TestGetSummary_Full(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	mgr.SetField("identity.role", "software engineer")
	mgr.SetField("communication.tone", "direct")
	mgr.SetField("interests", []string{"privacy tech", "AI infra", "distributed systems"})
	mgr.SetField("expertise", map[string]string{"go": "expert", "distributed_systems": "expert"})

	summary := mgr.GetSummary()

	checks := []string{"software engineer", "direct", "privacy tech"}
	for _, want := range checks {
		if !contains(summary, want) {
			t.Errorf("summary missing %q: %s", want, summary)
		}
	}
}

func TestGetSummary_TokenBudget(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	prefs := make([]string, 50)
	for i := range prefs {
		prefs[i] = "Always do something very specific and detailed for testing the token budget constraint"
	}
	mgr.SetField("preferences", prefs)

	summary := mgr.GetSummary()
	tokens := len(summary) / 4
	if tokens >= 500 {
		t.Errorf("summary too long: %d estimated tokens (len=%d)", tokens, len(summary))
	}
}

func TestCacheTTL(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	mgr := NewManagerWithClock(store, clock, 60*time.Second)

	mgr.SetField("identity.role", "engineer")

	mgr.GetProfile()
	mgr.GetProfile()

	store.mu.Lock()
	calls := store.getAllCalls
	store.mu.Unlock()

	if calls != 1 {
		t.Errorf("expected 1 store call (cache hit on second), got %d", calls)
	}
}

func TestCacheInvalidation(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	ttl := 60 * time.Second
	mgr := NewManagerWithClock(store, clock, ttl)

	mgr.SetField("identity.role", "engineer")

	mgr.GetProfile()

	// Advance past TTL
	clock.Advance(ttl + time.Second)

	mgr.GetProfile()

	store.mu.Lock()
	calls := store.getAllCalls
	store.mu.Unlock()

	if calls != 2 {
		t.Errorf("expected 2 store calls (cache expired), got %d", calls)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
