package profile

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

	mu        sync.RWMutex
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
	// Fast path: read lock for cache hit.
	m.mu.RLock()
	if m.cached != nil && m.clock.Now().Before(m.cachedAt.Add(m.ttl)) {
		p := deepCopyProfile(m.cached)
		m.mu.RUnlock()
		return p, nil
	}
	m.mu.RUnlock()

	// Slow path: write lock for cache miss.
	m.mu.Lock()
	defer m.mu.Unlock()

	// Double-check after acquiring write lock.
	if m.cached != nil && m.clock.Now().Before(m.cachedAt.Add(m.ttl)) {
		return deepCopyProfile(m.cached), nil
	}

	keys, err := m.store.GetAllProfileKeys()
	if err != nil {
		return Profile{}, fmt.Errorf("loading profile keys: %w", err)
	}

	p := buildProfile(keys)
	m.cached = &p
	m.cachedAt = m.clock.Now()
	return deepCopyProfile(&p), nil
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

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.store.SetProfileKey(key, str); err != nil {
		return fmt.Errorf("setting profile key %q: %w", key, err)
	}

	m.cached = nil
	return nil
}

// GetSummary returns a compact string representation of the profile suitable
// for injection into a system prompt. Targets < 500 tokens (~2000 chars).
func (m *Manager) GetSummary() (string, error) {
	p, err := m.GetProfile()
	if err != nil {
		return "", fmt.Errorf("getting profile for summary: %w", err)
	}
	return summarize(p), nil
}

// maxSummaryChars caps the summary to stay under ~500 tokens (4 chars/token).
const maxSummaryChars = 2000

func summarize(p Profile) string {
	var parts []string

	// Identity
	if p.Identity.Role != "" {
		parts = append(parts, fmt.Sprintf("User: %s.", p.Identity.Role))
	}

	// Expertise (sorted for deterministic output)
	if len(p.Expertise) > 0 {
		domains := make([]string, 0, len(p.Expertise))
		for domain := range p.Expertise {
			domains = append(domains, domain)
		}
		sort.Strings(domains)
		var exps []string
		for _, domain := range domains {
			exps = append(exps, fmt.Sprintf("%s (%s)", domain, p.Expertise[domain]))
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
	if len(summary) > maxSummaryChars {
		// Ensure we don't split a multi-byte UTF-8 character.
		end := maxSummaryChars
		for end > 0 && !utf8.RuneStart(summary[end]) {
			end--
		}
		if idx := strings.LastIndex(summary[:end], " "); idx > 0 {
			summary = summary[:idx]
		} else {
			summary = summary[:end]
		}
	}
	return summary
}

func deepCopyProfile(p *Profile) Profile {
	if p == nil {
		return Profile{}
	}
	cp := *p

	if p.Interests != nil {
		cp.Interests = make([]string, len(p.Interests))
		copy(cp.Interests, p.Interests)
	}
	if p.Expertise != nil {
		cp.Expertise = make(map[string]string, len(p.Expertise))
		for k, v := range p.Expertise {
			cp.Expertise[k] = v
		}
	}
	if p.Opinions != nil {
		cp.Opinions = make([]string, len(p.Opinions))
		copy(cp.Opinions, p.Opinions)
	}
	if p.Preferences != nil {
		cp.Preferences = make([]string, len(p.Preferences))
		copy(cp.Preferences, p.Preferences)
	}
	if p.Identity.WorkingContext != nil {
		cp.Identity.WorkingContext = make(map[string]string, len(p.Identity.WorkingContext))
		for k, v := range p.Identity.WorkingContext {
			cp.Identity.WorkingContext[k] = v
		}
	}
	return cp
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
	unmarshalProfileKey(keys, "identity.working_context", &p.Identity.WorkingContext)

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

	// JSON fields
	unmarshalProfileKey(keys, "interests", &p.Interests)
	unmarshalProfileKey(keys, "expertise", &p.Expertise)
	unmarshalProfileKey(keys, "opinions", &p.Opinions)
	unmarshalProfileKey(keys, "preferences", &p.Preferences)

	return p
}

// unmarshalProfileKey unmarshals a JSON value from keys into target, logging
// a warning if the value is present but malformed.
func unmarshalProfileKey(keys map[string]string, key string, target interface{}) {
	v, ok := keys[key]
	if !ok {
		return
	}
	if err := json.Unmarshal([]byte(v), target); err != nil {
		slog.Warn("malformed profile key, skipping", "key", key, "error", err)
	}
}
