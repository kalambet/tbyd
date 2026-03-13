package profile

import (
	"errors"
	"strings"
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

func (m *mockStore) DeleteProfileKey(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[key]; !ok {
		return errNotFound
	}
	delete(m.data, key)
	return nil
}

// errNotFound mirrors storage.ErrNotFound for the mock.
var errNotFound = errors.New("not found")

func (m *mockStore) GetAllProfileKeysCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getAllCalls
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
	if len(p.Interests.Primary) != 0 || len(p.Interests.Emerging) != 0 {
		t.Errorf("expected no interests, got primary=%v emerging=%v", p.Interests.Primary, p.Interests.Emerging)
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

	summary, err := mgr.GetSummary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary for empty profile")
	}
}

func TestGetSummary_Full(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	if err := mgr.SetField("identity.role", "software engineer"); err != nil {
		t.Fatalf("SetField(identity.role) error: %v", err)
	}
	if err := mgr.SetField("communication.tone", "direct"); err != nil {
		t.Fatalf("SetField(communication.tone) error: %v", err)
	}
	if err := mgr.SetField("interests", []string{"privacy tech", "AI infra", "distributed systems"}); err != nil {
		t.Fatalf("SetField(interests) error: %v", err)
	}
	if err := mgr.SetField("identity.expertise", map[string]string{"go": "expert", "distributed_systems": "expert"}); err != nil {
		t.Fatalf("SetField(identity.expertise) error: %v", err)
	}

	summary, err := mgr.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary error: %v", err)
	}

	checks := []string{"software engineer", "direct", "privacy tech"}
	for _, want := range checks {
		if !strings.Contains(summary, want) {
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
	if err := mgr.SetField("preferences", prefs); err != nil {
		t.Fatalf("SetField(preferences) error: %v", err)
	}

	summary, err := mgr.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary error: %v", err)
	}
	tokens := len(summary) / 4
	if tokens >= 500 {
		t.Errorf("summary too long: %d estimated tokens (len=%d)", tokens, len(summary))
	}
}

func TestCacheTTL(t *testing.T) {
	store := newMockStore()
	store.SetProfileKey("identity.role", "engineer")

	clock := &mockClock{now: time.Now()}
	mgr := NewManagerWithClock(store, clock, 60*time.Second)

	mgr.GetProfile()
	mgr.GetProfile()

	if calls := store.GetAllProfileKeysCalls(); calls != 1 {
		t.Errorf("expected 1 store call (cache hit on second), got %d", calls)
	}
}

func TestCacheInvalidation_TTLExpiry(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	ttl := 60 * time.Second
	mgr := NewManagerWithClock(store, clock, ttl)

	if err := mgr.SetField("identity.role", "engineer"); err != nil {
		t.Fatalf("SetField error: %v", err)
	}

	mgr.GetProfile()

	// Advance past TTL
	clock.Advance(ttl + time.Second)

	mgr.GetProfile()

	if calls := store.GetAllProfileKeysCalls(); calls != 2 {
		t.Errorf("expected 2 store calls (cache expired), got %d", calls)
	}
}

func TestCacheInvalidation_SetField(t *testing.T) {
	store := newMockStore()
	clock := &mockClock{now: time.Now()}
	mgr := NewManagerWithClock(store, clock, 60*time.Second)

	// Populate cache.
	mgr.GetProfile()
	if calls := store.GetAllProfileKeysCalls(); calls != 1 {
		t.Fatalf("expected 1 store call after first GetProfile, got %d", calls)
	}

	// SetField should invalidate cache.
	if err := mgr.SetField("identity.role", "engineer"); err != nil {
		t.Fatalf("SetField error: %v", err)
	}

	// Next GetProfile should re-query the store.
	mgr.GetProfile()
	if calls := store.GetAllProfileKeysCalls(); calls != 2 {
		t.Errorf("expected 2 store calls after SetField + GetProfile, got %d", calls)
	}
}

func TestPatchProfile_MergesFields(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	// Set initial state: tone "direct", role "engineer".
	if err := mgr.SetField("communication.tone", "direct"); err != nil {
		t.Fatalf("SetField(communication.tone): %v", err)
	}
	if err := mgr.SetField("identity.role", "engineer"); err != nil {
		t.Fatalf("SetField(identity.role): %v", err)
	}

	// PATCH only tone → should not affect role.
	if err := mgr.SetField("communication.tone", "formal"); err != nil {
		t.Fatalf("SetField(communication.tone patch): %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Communication.Tone != "formal" {
		t.Errorf("tone = %q, want %q", p.Communication.Tone, "formal")
	}
	if p.Identity.Role != "engineer" {
		t.Errorf("role = %q, want %q (should be unchanged)", p.Identity.Role, "engineer")
	}
}

func TestPatchProfile_AppendsToArrays(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	// Set initial interests (2 items) using legacy key to test backward compat.
	if err := mgr.SetField("interests", []string{"go", "privacy"}); err != nil {
		t.Fatalf("SetField(interests): %v", err)
	}

	// Add one more via the primary key.
	if err := mgr.SetField("interests.primary", []string{"go", "privacy", "distributed-systems"}); err != nil {
		t.Fatalf("SetField(interests.primary): %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	// interests.primary wins over legacy interests key.
	if len(p.Interests.Primary) != 3 {
		t.Errorf("interests.primary len = %d, want 3; got %v", len(p.Interests.Primary), p.Interests.Primary)
	}
}

func TestDeleteProfileField_Scalar(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	if err := mgr.SetField("communication.tone", "direct"); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	if err := mgr.DeleteField("communication.tone"); err != nil {
		t.Fatalf("DeleteField: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if p.Communication.Tone != "" {
		t.Errorf("communication.tone = %q, want empty after delete", p.Communication.Tone)
	}
}

func TestDeleteProfileField_ArrayItem(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	if err := mgr.SetField("interests.primary", []string{"go", "privacy"}); err != nil {
		t.Fatalf("SetField: %v", err)
	}

	if err := mgr.DeleteField("interests.primary[go]"); err != nil {
		t.Fatalf("DeleteField: %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if len(p.Interests.Primary) != 1 || p.Interests.Primary[0] != "privacy" {
		t.Errorf("interests.primary = %v, want [privacy]", p.Interests.Primary)
	}
}

func TestDeleteProfileField_NotFound(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	err := mgr.DeleteField("communication.tone")
	if !errors.Is(err, ErrFieldNotFound) {
		t.Errorf("expected ErrFieldNotFound, got %v", err)
	}
}

func TestGetSummary_ExplicitPreferencesFirst(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	// Preferences appear before interests in the summary.
	if err := mgr.SetField("preferences", []string{"always show code examples"}); err != nil {
		t.Fatalf("SetField(preferences): %v", err)
	}
	if err := mgr.SetField("interests.primary", []string{"distributed systems"}); err != nil {
		t.Fatalf("SetField(interests.primary): %v", err)
	}

	summary, err := mgr.GetSummary()
	if err != nil {
		t.Fatalf("GetSummary: %v", err)
	}

	prefIdx := strings.Index(summary, "always show code examples")
	interestIdx := strings.Index(summary, "distributed systems")

	if prefIdx == -1 {
		t.Fatal("summary missing preference text")
	}
	if interestIdx == -1 {
		t.Fatal("summary missing interest text")
	}
	if prefIdx > interestIdx {
		t.Errorf("preference (%d) appears after interest (%d); explicit preferences should appear first", prefIdx, interestIdx)
	}
}

func TestProfileRoundTrip(t *testing.T) {
	store := newMockStore()
	mgr := NewManager(store)

	// Build a complex nested profile.
	if err := mgr.SetField("identity.role", "senior engineer"); err != nil {
		t.Fatalf("SetField(identity.role): %v", err)
	}
	if err := mgr.SetField("identity.expertise", map[string]string{"go": "expert", "rust": "intermediate"}); err != nil {
		t.Fatalf("SetField(identity.expertise): %v", err)
	}
	if err := mgr.SetField("communication.tone", "direct"); err != nil {
		t.Fatalf("SetField(communication.tone): %v", err)
	}
	if err := mgr.SetField("communication.format", "markdown"); err != nil {
		t.Fatalf("SetField(communication.format): %v", err)
	}
	if err := mgr.SetField("communication.detail_level", "balanced"); err != nil {
		t.Fatalf("SetField(communication.detail_level): %v", err)
	}
	if err := mgr.SetField("interests.primary", []string{"privacy", "ai-infra"}); err != nil {
		t.Fatalf("SetField(interests.primary): %v", err)
	}
	if err := mgr.SetField("interests.emerging", []string{"wasm"}); err != nil {
		t.Fatalf("SetField(interests.emerging): %v", err)
	}
	if err := mgr.SetField("opinions", []string{"privacy over convenience"}); err != nil {
		t.Fatalf("SetField(opinions): %v", err)
	}
	if err := mgr.SetField("preferences", []string{"show code examples", "skip boilerplate"}); err != nil {
		t.Fatalf("SetField(preferences): %v", err)
	}
	if err := mgr.SetField("language", "English"); err != nil {
		t.Fatalf("SetField(language): %v", err)
	}
	if err := mgr.SetField("cloud_model_preference", "claude-3-7-sonnet"); err != nil {
		t.Fatalf("SetField(cloud_model_preference): %v", err)
	}

	p, err := mgr.GetProfile()
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}

	// Verify deep equality on all fields.
	if p.Identity.Role != "senior engineer" {
		t.Errorf("role = %q", p.Identity.Role)
	}
	if p.Identity.Expertise["go"] != "expert" || p.Identity.Expertise["rust"] != "intermediate" {
		t.Errorf("expertise = %v", p.Identity.Expertise)
	}
	if p.Communication.Tone != "direct" {
		t.Errorf("tone = %q", p.Communication.Tone)
	}
	if p.Communication.Format != "markdown" {
		t.Errorf("format = %q", p.Communication.Format)
	}
	if p.Communication.DetailLevel != "balanced" {
		t.Errorf("detail_level = %q", p.Communication.DetailLevel)
	}
	if len(p.Interests.Primary) != 2 || p.Interests.Primary[0] != "privacy" || p.Interests.Primary[1] != "ai-infra" {
		t.Errorf("interests.primary = %v", p.Interests.Primary)
	}
	if len(p.Interests.Emerging) != 1 || p.Interests.Emerging[0] != "wasm" {
		t.Errorf("interests.emerging = %v", p.Interests.Emerging)
	}
	if len(p.Opinions) != 1 || p.Opinions[0] != "privacy over convenience" {
		t.Errorf("opinions = %v", p.Opinions)
	}
	if len(p.Preferences) != 2 {
		t.Errorf("preferences = %v", p.Preferences)
	}
	if p.Language != "English" {
		t.Errorf("language = %q", p.Language)
	}
	if p.CloudModelPreference != "claude-3-7-sonnet" {
		t.Errorf("cloud_model_preference = %q", p.CloudModelPreference)
	}
}
