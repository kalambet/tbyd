package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrFieldNotFound is returned when a profile field path does not exist.
var ErrFieldNotFound = errors.New("profile field not found")

// isNotFound reports whether err represents a not-found condition from the
// storage layer. The profile package cannot import storage (that would create
// an import cycle), so we match against the well-known error message that both
// storage.ErrNotFound and test mocks use.
//
// strings.Contains is used rather than exact equality so that wrapped errors
// (e.g. fmt.Errorf("deleting key: %w", storage.ErrNotFound)) are also detected.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// ProfileStore defines the storage operations the Manager needs.
// Implemented by storage.Store.
type ProfileStore interface {
	SetProfileKey(key, value string) error
	GetProfileKey(key string) (string, error)
	GetAllProfileKeys() (map[string]string, error)
	DeleteProfileKey(key string) error
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

	mu             sync.RWMutex
	cached         *Profile
	cachedAt       time.Time
	onInvalidate   func()
	profileVersion int64 // monotonically increasing; bumped on each SetField
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

// OnInvalidate registers a callback that fires whenever the profile cache is
// invalidated (e.g., on SetField). Used to cascade invalidation to the query cache.
// Only one callback is supported; panics if called twice to prevent silent drops.
func (m *Manager) OnInvalidate(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.onInvalidate != nil {
		panic("profile.Manager.OnInvalidate: callback already registered; only one is supported")
	}
	m.onInvalidate = fn
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
	m.profileVersion++

	if m.onInvalidate != nil {
		m.onInvalidate()
	}
	return nil
}

// DeleteField removes a profile field identified by a dot-notation path and
// invalidates the cache. Returns ErrFieldNotFound if the path does not exist.
//
// Supported path forms:
//   - Scalar fields:         "communication.tone", "identity.role"
//   - Map entries:           "expertise.go", "identity.expertise.go"
//   - Array items by value:  "interests.primary[go]", "interests.primary[0]"
//   - Top-level JSON arrays: "interests.primary" (removes entire key)
//
// The write lock is held for the full duration of the storage read-modify-write
// and cache invalidation to prevent TOCTOU races with concurrent SetField or
// DeleteField calls.
func (m *Manager) DeleteField(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	keys, err := m.store.GetAllProfileKeys()
	if err != nil {
		return fmt.Errorf("loading profile keys: %w", err)
	}

	storageKey, subPath, err := resolveDeletePath(path, keys)
	if err != nil {
		return err
	}

	if subPath == "" {
		// Delete the entire storage key. The store returns a not-found error if
		// the key was removed by a concurrent writer between our GetAllProfileKeys
		// call and this delete; surface that as ErrFieldNotFound to the caller.
		if err := m.store.DeleteProfileKey(storageKey); err != nil {
			if isNotFound(err) {
				return ErrFieldNotFound
			}
			return fmt.Errorf("deleting profile key %q: %w", storageKey, err)
		}
	} else {
		// Mutate the JSON value at storageKey by removing the sub-path entry.
		raw, ok := keys[storageKey]
		if !ok {
			return ErrFieldNotFound
		}
		updated, err := deleteFromJSON(raw, subPath)
		if err != nil {
			return fmt.Errorf("deleting sub-path %q from key %q: %w", subPath, storageKey, err)
		}
		if err := m.store.SetProfileKey(storageKey, updated); err != nil {
			return fmt.Errorf("updating profile key %q: %w", storageKey, err)
		}
	}

	m.cached = nil
	m.profileVersion++
	if m.onInvalidate != nil {
		m.onInvalidate()
	}
	return nil
}

// knownStorageKeys lists all valid profile storage keys. resolveDeletePath uses
// this to match paths generically, so adding a new field to the profile schema
// only requires adding its storage key here (and the corresponding buildProfile
// logic) — resolveDeletePath does not need manual updates for simple scalar or
// array keys.
var knownStorageKeys = []string{
	"identity.role",
	"identity.expertise",
	"identity.working_context",
	"communication.tone",
	"communication.detail_level",
	"communication.format",
	"interests.primary",
	"interests.emerging",
	"opinions",
	"preferences",
	"language",
	"cloud_model_preference",
}

// mapStorageKeys are keys whose JSON values are objects (maps), supporting
// sub-key deletion like "identity.expertise.go".
var mapStorageKeys = map[string]bool{
	"identity.expertise": true,
}

// legacyKeyAliases maps old storage key names to their new equivalents.
var legacyKeyAliases = map[string]string{
	"interests": "interests.primary",
}

// resolveDeletePath maps a user-facing dot-notation path to a storage key and
// an optional sub-path within the JSON value stored at that key.
// Returns ErrFieldNotFound if nothing matches.
//
// The resolution strategy is data-driven via knownStorageKeys, mapStorageKeys,
// and legacyKeyAliases. Adding a new profile field requires only adding its
// storage key to the appropriate list — no case-by-case code changes needed
// for scalar or array keys.
func resolveDeletePath(path string, keys map[string]string) (storageKey, subPath string, err error) {
	// 1. Direct match — the path is a storage key itself.
	if _, ok := keys[path]; ok {
		return path, "", nil
	}

	// 2. Check if the path targets a sub-entry of a map-type key.
	// e.g., "identity.expertise.go" → key="identity.expertise", sub="map:go"
	// Also supports the shorthand "expertise.go".
	for mapKey := range mapStorageKeys {
		// Full prefix: "identity.expertise.X"
		prefix := mapKey + "."
		if strings.HasPrefix(path, prefix) {
			sub := path[len(prefix):]
			if raw, ok := keys[mapKey]; ok {
				var m map[string]string
				if json.Unmarshal([]byte(raw), &m) == nil {
					if _, exists := m[sub]; exists {
						return mapKey, "map:" + sub, nil
					}
				}
			}
			return "", "", ErrFieldNotFound
		}
		// Shorthand prefix: strip the leading namespace.
		// "identity.expertise" → shorthand "expertise.X"
		if dotIdx := strings.LastIndex(mapKey, "."); dotIdx >= 0 {
			shortPrefix := mapKey[dotIdx+1:] + "."
			if strings.HasPrefix(path, shortPrefix) {
				sub := path[len(shortPrefix):]
				if raw, ok := keys[mapKey]; ok {
					var m map[string]string
					if json.Unmarshal([]byte(raw), &m) == nil {
						if _, exists := m[sub]; exists {
							return mapKey, "map:" + sub, nil
						}
					}
				}
				return "", "", ErrFieldNotFound
			}
		}
	}

	// 3. Check if the path targets an array item via bracket syntax.
	// e.g., "interests.primary[Distributed Systems]" → key="interests.primary", sub="array:Distributed Systems"
	if bracketIdx := strings.Index(path, "["); bracketIdx > 0 && strings.HasSuffix(path, "]") {
		arrayKey := path[:bracketIdx]
		itemValue := path[bracketIdx+1 : len(path)-1]
		if _, ok := keys[arrayKey]; ok {
			return arrayKey, "array:" + itemValue, nil
		}
		// Check legacy aliases.
		if newKey, ok := legacyKeyAliases[arrayKey]; ok {
			if _, ok := keys[arrayKey]; ok {
				return arrayKey, "array:" + itemValue, nil
			}
			// Also try the canonical key.
			_ = newKey
		}
		// Check if a known key matches via legacy alias for the base path.
		for oldKey := range legacyKeyAliases {
			if arrayKey == oldKey {
				if _, ok := keys[oldKey]; ok {
					return oldKey, "array:" + itemValue, nil
				}
			}
		}
		return "", "", ErrFieldNotFound
	}

	// 4. Check legacy aliases for whole-key deletion.
	// e.g., the old "interests" key when "interests.primary" is requested.
	for oldKey, newKey := range legacyKeyAliases {
		if path == newKey {
			if _, ok := keys[oldKey]; ok {
				return oldKey, "", nil
			}
		}
	}

	// 5. Check if the path is a known key that simply doesn't exist in storage yet.
	for _, known := range knownStorageKeys {
		if path == known {
			return "", "", ErrFieldNotFound
		}
	}

	return "", "", ErrFieldNotFound
}

// deleteFromJSON mutates a JSON-encoded value by removing the element
// described by selector. selector formats:
//   - "map:KEY"   — remove key KEY from a JSON object
//   - "array:VAL" — remove element by value or by index from a JSON array
func deleteFromJSON(raw, selector string) (string, error) {
	switch {
	case strings.HasPrefix(selector, "map:"):
		mapKey := strings.TrimPrefix(selector, "map:")
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return "", fmt.Errorf("unmarshal map: %w", err)
		}
		if _, ok := m[mapKey]; !ok {
			return "", ErrFieldNotFound
		}
		delete(m, mapKey)
		b, err := json.Marshal(m)
		if err != nil {
			return "", fmt.Errorf("marshal map: %w", err)
		}
		return string(b), nil

	case strings.HasPrefix(selector, "array:"):
		target := strings.TrimPrefix(selector, "array:")
		var arr []interface{}
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return "", fmt.Errorf("unmarshal array: %w", err)
		}

		// Try numeric index first.
		if idx, err := strconv.Atoi(target); err == nil {
			if idx < 0 || idx >= len(arr) {
				return "", ErrFieldNotFound
			}
			arr = append(arr[:idx], arr[idx+1:]...)
		} else {
			// Remove by string value. JSON arrays of profile data always contain
			// strings; use a type assertion rather than fmt.Sprintf to avoid false
			// matches on non-string elements (e.g., a number whose %v representation
			// happens to equal the target string).
			found := false
			for i, v := range arr {
				if s, ok := v.(string); ok && s == target {
					arr = append(arr[:i], arr[i+1:]...)
					found = true
					break
				}
			}
			if !found {
				return "", ErrFieldNotFound
			}
		}

		b, err := json.Marshal(arr)
		if err != nil {
			return "", fmt.Errorf("marshal array: %w", err)
		}
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown selector format: %q", selector)
	}
}

// ProfileVersion returns the current profile version. It is a monotonically
// increasing counter bumped on each SetField call. Used by the enrichment
// pipeline to detect stale cache entries produced with an outdated profile.
func (m *Manager) ProfileVersion() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.profileVersion
}

// GetSummary returns a compact string representation of the profile suitable
// for injection into a system prompt. Targets < 500 tokens (~2000 chars).
// Explicit preferences (set directly by user) appear before inferred ones.
func (m *Manager) GetSummary() (string, error) {
	p, err := m.GetProfile()
	if err != nil {
		return "", fmt.Errorf("getting profile for summary: %w", err)
	}
	return summarize(p), nil
}

// maxSummaryChars caps the summary to stay under ~500 tokens.
// The 4 bytes/token heuristic holds for typical English/ASCII text; non-Latin
// scripts or code-heavy profiles may tokenize differently and could exceed the
// 500-token target at this byte limit.
const maxSummaryChars = 2000

func summarize(p Profile) string {
	var parts []string

	// Identity
	if p.Identity.Role != "" {
		parts = append(parts, fmt.Sprintf("User: %s.", p.Identity.Role))
	}

	// Expertise (sorted for deterministic output); now lives under Identity.
	if len(p.Identity.Expertise) > 0 {
		domains := make([]string, 0, len(p.Identity.Expertise))
		for domain := range p.Identity.Expertise {
			domains = append(domains, domain)
		}
		sort.Strings(domains)
		var exps []string
		for _, domain := range domains {
			exps = append(exps, fmt.Sprintf("%s (%s)", domain, p.Identity.Expertise[domain]))
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

	// Explicit preferences first (positional priority per issue 3.4 spec).
	for _, pref := range p.Preferences {
		parts = append(parts, pref)
	}

	// Interests — primary first, then emerging. Allocate a fresh slice so that
	// append never writes into the backing array of p.Interests.Primary, which
	// would silently corrupt the caller's copy when capacity allows it.
	allInterests := make([]string, 0, len(p.Interests.Primary)+len(p.Interests.Emerging))
	allInterests = append(allInterests, p.Interests.Primary...)
	allInterests = append(allInterests, p.Interests.Emerging...)
	if len(allInterests) > 0 {
		parts = append(parts, fmt.Sprintf("Interests: %s.", strings.Join(allInterests, ", ")))
	}

	// Opinions
	for _, o := range p.Opinions {
		parts = append(parts, o)
	}

	// Language / model preference
	if p.Language != "" {
		parts = append(parts, fmt.Sprintf("Language: %s.", p.Language))
	}
	if p.CloudModelPreference != "" {
		parts = append(parts, fmt.Sprintf("Preferred model: %s.", p.CloudModelPreference))
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

	// Interests
	if p.Interests.Primary != nil {
		cp.Interests.Primary = make([]string, len(p.Interests.Primary))
		copy(cp.Interests.Primary, p.Interests.Primary)
	}
	if p.Interests.Emerging != nil {
		cp.Interests.Emerging = make([]string, len(p.Interests.Emerging))
		copy(cp.Interests.Emerging, p.Interests.Emerging)
	}

	// Expertise lives inside Identity now.
	if p.Identity.Expertise != nil {
		cp.Identity.Expertise = make(map[string]string, len(p.Identity.Expertise))
		for k, v := range p.Identity.Expertise {
			cp.Identity.Expertise[k] = v
		}
	}

	// WorkingContext
	if p.Identity.WorkingContext != nil {
		wc := *p.Identity.WorkingContext
		if p.Identity.WorkingContext.CurrentProjects != nil {
			wc.CurrentProjects = make([]string, len(p.Identity.WorkingContext.CurrentProjects))
			copy(wc.CurrentProjects, p.Identity.WorkingContext.CurrentProjects)
		}
		if p.Identity.WorkingContext.TechStack != nil {
			wc.TechStack = make([]string, len(p.Identity.WorkingContext.TechStack))
			copy(wc.TechStack, p.Identity.WorkingContext.TechStack)
		}
		cp.Identity.WorkingContext = &wc
	}

	if p.Opinions != nil {
		cp.Opinions = make([]string, len(p.Opinions))
		copy(cp.Opinions, p.Opinions)
	}
	if p.Preferences != nil {
		cp.Preferences = make([]string, len(p.Preferences))
		copy(cp.Preferences, p.Preferences)
	}
	return cp
}

// buildProfile assembles a Profile from flat key-value pairs.
//
// Storage key layout:
//
//	identity.role              → string
//	identity.expertise         → JSON object {"go": "expert"}
//	identity.working_context   → JSON object (WorkingContext)
//	communication.tone         → string
//	communication.format       → string
//	communication.detail_level → string
//	interests.primary          → JSON array
//	interests.emerging         → JSON array
//	interests                  → JSON array (legacy; loaded into Primary)
//	opinions                   → JSON array
//	preferences                → JSON array
//	language                   → string
//	cloud_model_preference     → string
func buildProfile(keys map[string]string) Profile {
	var p Profile

	// Identity
	if v, ok := keys["identity.role"]; ok {
		p.Identity.Role = v
	}
	unmarshalProfileKey(keys, "identity.expertise", &p.Identity.Expertise)

	// WorkingContext — support both new key and legacy map.
	var wc WorkingContext
	if unmarshalProfileKeyBool(keys, "identity.working_context", &wc) {
		p.Identity.WorkingContext = &wc
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

	// Interests — prefer explicit primary/emerging keys; fall back to legacy "interests" array.
	unmarshalProfileKey(keys, "interests.primary", &p.Interests.Primary)
	unmarshalProfileKey(keys, "interests.emerging", &p.Interests.Emerging)
	if p.Interests.Primary == nil {
		// Legacy key: plain array stored under "interests" maps to Primary.
		unmarshalProfileKey(keys, "interests", &p.Interests.Primary)
	}

	// Top-level fields
	unmarshalProfileKey(keys, "opinions", &p.Opinions)
	unmarshalProfileKey(keys, "preferences", &p.Preferences)

	if v, ok := keys["language"]; ok {
		p.Language = v
	}
	if v, ok := keys["cloud_model_preference"]; ok {
		p.CloudModelPreference = v
	}

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

// unmarshalProfileKeyBool is like unmarshalProfileKey but reports whether the
// key was present and successfully decoded.
func unmarshalProfileKeyBool(keys map[string]string, key string, target interface{}) bool {
	v, ok := keys[key]
	if !ok {
		return false
	}
	if err := json.Unmarshal([]byte(v), target); err != nil {
		slog.Warn("malformed profile key, skipping", "key", key, "error", err)
		return false
	}
	return true
}
