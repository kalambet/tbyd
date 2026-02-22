package profile

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ProfileStore defines the storage operations the Manager needs.
// Implemented by storage.Store.
type ProfileStore interface {
	SetProfileKey(key, value string) error
	GetProfileKey(key string) (string, error)
	GetAllProfileKeys() (map[string]string, error)
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// Manager provides cached, structured access to the user profile stored in SQLite.
type Manager struct {
	store ProfileStore
	clock Clock
	ttl   time.Duration

	mu        sync.Mutex
	cached    *Profile
	cachedAt  time.Time
}

// NewManager creates a Manager with a 60-second cache TTL.
func NewManager(store ProfileStore) *Manager {
	return &Manager{
		store: store,
		clock: realClock{},
		ttl:   60 * time.Second,
	}
}

// NewManagerWithClock creates a Manager with a custom clock (for testing).
func NewManagerWithClock(store ProfileStore, clock Clock, ttl time.Duration) *Manager {
	return &Manager{
		store: store,
		clock: clock,
		ttl:   ttl,
	}
}

// GetProfile reads all profile keys from storage (or cache) and assembles
// a structured Profile. Returns a zero-value Profile on empty store.
func (m *Manager) GetProfile() (Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cached != nil && m.clock.Now().Before(m.cachedAt.Add(m.ttl)) {
		return *m.cached, nil
	}

	keys, err := m.store.GetAllProfileKeys()
	if err != nil {
		return Profile{}, fmt.Errorf("loading profile keys: %w", err)
	}

	p := buildProfile(keys)
	m.cached = &p
	m.cachedAt = m.clock.Now()
	return p, nil
}

// SetField persists a profile key and invalidates the cache.
func (m *Manager) SetField(key string, value interface{}) error {
	var str string
	switch v := value.(type) {
	case string:
		str = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshalling value for key %q: %w", key, err)
		}
		str = string(b)
	}

	if err := m.store.SetProfileKey(key, str); err != nil {
		return fmt.Errorf("setting profile key %q: %w", key, err)
	}

	m.mu.Lock()
	m.cached = nil
	m.mu.Unlock()
	return nil
}

// GetSummary returns a compact string representation of the profile suitable
// for injection into a system prompt. Targets < 500 tokens (~2000 chars).
func (m *Manager) GetSummary() string {
	p, _ := m.GetProfile()
	return summarize(p)
}

// maxSummaryChars caps the summary to stay under ~500 tokens (4 chars/token).
const maxSummaryChars = 2000

func summarize(p Profile) string {
	var parts []string

	// Identity
	if p.Identity.Role != "" {
		parts = append(parts, fmt.Sprintf("User: %s.", p.Identity.Role))
	}

	// Expertise
	if len(p.Expertise) > 0 {
		var exps []string
		for domain, level := range p.Expertise {
			exps = append(exps, fmt.Sprintf("%s (%s)", domain, level))
		}
		parts = append(parts, fmt.Sprintf("Expert in: %s.", strings.Join(exps, ", ")))
	}

	// Communication
	var commParts []string
	if p.Communication.Tone != "" {
		commParts = append(commParts, p.Communication.Tone+" tone")
	}
	if p.Communication.Format != "" {
		commParts = append(commParts, p.Communication.Format)
	}
	if p.Communication.DetailLevel != "" {
		commParts = append(commParts, p.Communication.DetailLevel)
	}
	if len(commParts) > 0 {
		parts = append(parts, fmt.Sprintf("Prefers: %s.", strings.Join(commParts, ", ")))
	}

	// Interests
	if len(p.Interests) > 0 {
		parts = append(parts, fmt.Sprintf("Interests: %s.", strings.Join(p.Interests, ", ")))
	}

	// Opinions
	for _, o := range p.Opinions {
		parts = append(parts, o)
	}

	// Preferences
	for _, pref := range p.Preferences {
		parts = append(parts, pref)
	}

	if len(parts) == 0 {
		return "User profile: not yet configured."
	}

	summary := strings.Join(parts, " ")
	if len(summary) >= maxSummaryChars {
		summary = summary[:maxSummaryChars-1]
	}
	return summary
}

// buildProfile assembles a Profile from flat key-value pairs.
// Keys use dot-notation: "identity.role", "communication.tone",
// "interests", "expertise", "opinions", "preferences".
// List/map values are stored as JSON arrays/objects.
func buildProfile(keys map[string]string) Profile {
	var p Profile

	// Identity
	if v, ok := keys["identity.role"]; ok {
		p.Identity.Role = v
	}
	if v, ok := keys["identity.working_context"]; ok {
		var wc map[string]string
		if json.Unmarshal([]byte(v), &wc) == nil {
			p.Identity.WorkingContext = wc
		}
	}

	// Communication
	if v, ok := keys["communication.tone"]; ok {
		p.Communication.Tone = v
	}
	if v, ok := keys["communication.format"]; ok {
		p.Communication.Format = v
	}
	if v, ok := keys["communication.detail_level"]; ok {
		p.Communication.DetailLevel = v
	}

	// Interests
	if v, ok := keys["interests"]; ok {
		var list []string
		if json.Unmarshal([]byte(v), &list) == nil {
			p.Interests = list
		}
	}

	// Expertise
	if v, ok := keys["expertise"]; ok {
		var m map[string]string
		if json.Unmarshal([]byte(v), &m) == nil {
			p.Expertise = m
		}
	}

	// Opinions
	if v, ok := keys["opinions"]; ok {
		var list []string
		if json.Unmarshal([]byte(v), &list) == nil {
			p.Opinions = list
		}
	}

	// Preferences
	if v, ok := keys["preferences"]; ok {
		var list []string
		if json.Unmarshal([]byte(v), &list) == nil {
			p.Preferences = list
		}
	}

	return p
}
