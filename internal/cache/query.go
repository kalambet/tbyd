package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/kalambet/tbyd/internal/proxy"
)

// Embedder is the subset of retrieval.Embedder used for semantic cache lookups.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// CachedEnrichment stores a cached enrichment result.
type CachedEnrichment struct {
	EnrichedRequest proxy.ChatRequest
	Metadata        any       // pipeline.EnrichmentMetadata stored opaquely to avoid import cycle
	CachedAt        time.Time
	Topics          []string // intent topics for selective invalidation
	ProfileVersion  int64    // profile version at enrichment time; entries below cache min are stale
}

// SemanticEntry pairs a pre-normalized query embedding with its cached result.
// Embeddings are unit-normalized at insertion time so that the L2 scan uses
// a simple dot product instead of full cosine similarity.
type SemanticEntry struct {
	QueryHash string
	Embedding []float32
	Result    CachedEnrichment
	CachedAt  time.Time
}

// Clock abstracts time for testability.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// defaultMaxSemanticSize caps the semantic cache slice to prevent unbounded growth.
// At 768-dim float32 (~3KB per entry), 5000 entries ≈ 15MB — acceptable for a
// single-user local tool. The cap also bounds L2 scan latency.
const defaultMaxSemanticSize = 5000

// defaultMaxExactSize caps the exact cache map to prevent unbounded memory growth.
// Each entry holds a CachedEnrichment (full ChatRequest + metadata). 10 000 unique
// queries within a 5-minute TTL window is well beyond realistic single-user traffic.
const defaultMaxExactSize = 10000

// QueryCache provides two-level caching: exact match (L1) and semantic (L2).
type QueryCache struct {
	mu               sync.RWMutex
	exactCache       map[string]CachedEnrichment
	semanticCache    []SemanticEntry // pre-allocated ring buffer; only [0..semanticLen) are valid
	semanticLen      int             // number of valid entries in the ring buffer
	semanticWriteIdx int             // next write position (wraps at maxSemanticSize)
	embedder         Embedder
	clock            Clock
	enabled          bool
	semThreshold     float64
	exactTTL         time.Duration
	semanticTTL      time.Duration
	maxSemanticSize   int
	maxExactSize      int
	stopEviction      chan struct{}
	minProfileVersion int64 // bumped on Invalidate(); rejects entries with older ProfileVersion
}

// NewQueryCache creates a QueryCache. If enabled is false, Get always misses.
// The embedder must produce vectors from the same model used for retrieval
// to ensure cosine similarity scores are meaningful.
func NewQueryCache(embedder Embedder, enabled bool, semThreshold float64, exactTTL, semanticTTL time.Duration) *QueryCache {
	qc := &QueryCache{
		exactCache:      make(map[string]CachedEnrichment),
		semanticCache:   make([]SemanticEntry, defaultMaxSemanticSize),
		embedder:        embedder,
		clock:           realClock{},
		enabled:         enabled,
		semThreshold:    semThreshold,
		exactTTL:        exactTTL,
		semanticTTL:     semanticTTL,
		maxSemanticSize: defaultMaxSemanticSize,
		maxExactSize:    defaultMaxExactSize,
		stopEviction:    make(chan struct{}),
	}

	if enabled {
		go qc.evictionLoop()
	}

	return qc
}

// NewQueryCacheWithClock creates a QueryCache with a custom clock (for testing).
// Does not start the eviction goroutine.
func NewQueryCacheWithClock(embedder Embedder, enabled bool, semThreshold float64, exactTTL, semanticTTL time.Duration, clock Clock) *QueryCache {
	return &QueryCache{
		exactCache:      make(map[string]CachedEnrichment),
		semanticCache:   make([]SemanticEntry, defaultMaxSemanticSize),
		embedder:        embedder,
		clock:           clock,
		enabled:         enabled,
		semThreshold:    semThreshold,
		exactTTL:        exactTTL,
		semanticTTL:     semanticTTL,
		maxSemanticSize: defaultMaxSemanticSize,
		maxExactSize:    defaultMaxExactSize,
		stopEviction:    make(chan struct{}),
	}
}

// Stop terminates the background eviction goroutine.
func (qc *QueryCache) Stop() {
	select {
	case <-qc.stopEviction:
	default:
		close(qc.stopEviction)
	}
}

// CacheResult holds the result of a cache lookup.
type CacheResult struct {
	Entry      CachedEnrichment
	Embedding  []float32 // query embedding computed for L2 (reusable on miss)
	Hit        bool
	CacheLevel string // "exact" or "semantic"
}

// Get checks the cache for a matching entry. Returns a CacheResult with the
// cached entry, the query embedding (computed for L2 lookup, reusable by caller
// on miss), hit status, and cache level.
func (qc *QueryCache) Get(ctx context.Context, query string) CacheResult {
	if !qc.enabled {
		return CacheResult{}
	}

	normalized := normalize(query)
	hash := hashQuery(normalized)
	now := qc.clock.Now()

	// L1: exact match.
	qc.mu.RLock()
	if entry, ok := qc.exactCache[hash]; ok && now.Before(entry.CachedAt.Add(qc.exactTTL)) && entry.ProfileVersion >= qc.minProfileVersion {
		qc.mu.RUnlock()
		slog.Debug("cache: L1 exact hit", "query_hash", hash[:12])
		return CacheResult{Entry: entry, Hit: true, CacheLevel: "exact"}
	}
	qc.mu.RUnlock()

	// L2: semantic match — embed the query once.
	//
	// Note: there is a TOCTOU gap between the L1 RUnlock above and the L2
	// RLock below. A concurrent Invalidate() or Set() between these points
	// can change cache state. This is benign: Invalidate clears entries
	// (L2 scan finds nothing — correct), and a concurrent Set might be
	// missed (the caller runs the full pipeline — suboptimal but safe).
	embedding, err := qc.embedder.Embed(ctx, query)
	if err != nil {
		slog.Warn("cache: embedding for semantic lookup failed", "error", err)
		return CacheResult{}
	}

	// Normalize query vector once for dot-product scan against pre-normalized
	// cache entries. Return the raw embedding in CacheResult.Embedding so the
	// caller (pipeline) can reuse it for vector-DB storage.
	normQuery := unitNormalize(embedding)
	if normQuery == nil {
		slog.Warn("cache: zero-norm query embedding, skipping L2")
		return CacheResult{Embedding: embedding}
	}

	qc.mu.RLock()
	defer qc.mu.RUnlock()

	// Scan all non-expired entries and return the best (highest similarity) match.
	var bestSim float64
	var bestEntry *SemanticEntry
	for i := 0; i < qc.semanticLen; i++ {
		entry := &qc.semanticCache[i]
		if now.After(entry.CachedAt.Add(qc.semanticTTL)) {
			continue
		}
		if entry.Result.ProfileVersion < qc.minProfileVersion {
			continue
		}
		sim := dotProduct(normQuery, entry.Embedding)
		if sim >= qc.semThreshold && sim > bestSim {
			bestSim = sim
			bestEntry = entry
		}
	}

	if bestEntry != nil {
		slog.Debug("cache: L2 semantic hit", "similarity", bestSim, "query_hash", bestEntry.QueryHash[:12])
		return CacheResult{Entry: bestEntry.Result, Embedding: embedding, Hit: true, CacheLevel: "semantic"}
	}

	slog.Debug("cache: miss", "query_hash", hash[:12])
	return CacheResult{Embedding: embedding}
}

// Set stores a result in both exact and semantic caches.
func (qc *QueryCache) Set(_ context.Context, query string, queryEmbedding []float32, result CachedEnrichment) {
	if !qc.enabled {
		return
	}

	normalized := normalize(query)
	hash := hashQuery(normalized)
	now := qc.clock.Now()

	result.CachedAt = now

	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Evict oldest exact-cache entry if at capacity.
	if len(qc.exactCache) >= qc.maxExactSize {
		qc.evictOldestExact()
	}
	qc.exactCache[hash] = result

	if len(queryEmbedding) > 0 {
		norm := unitNormalize(queryEmbedding)
		if norm == nil {
			return // zero-norm vector; skip semantic cache
		}
		// Ring buffer: overwrite at cursor, advance with wrap.
		qc.semanticCache[qc.semanticWriteIdx] = SemanticEntry{
			QueryHash: hash,
			Embedding: norm,
			Result:    result,
			CachedAt:  now,
		}
		qc.semanticWriteIdx = (qc.semanticWriteIdx + 1) % qc.maxSemanticSize
		if qc.semanticLen < qc.maxSemanticSize {
			qc.semanticLen++
		}
	}
}

// Invalidate clears both caches entirely.
func (qc *QueryCache) Invalidate() {
	qc.mu.Lock()
	defer qc.mu.Unlock()

	qc.exactCache = make(map[string]CachedEnrichment)
	// Zero ring buffer entries for GC; keep pre-allocated backing array.
	for i := 0; i < qc.semanticLen; i++ {
		qc.semanticCache[i] = SemanticEntry{}
	}
	qc.semanticLen = 0
	qc.semanticWriteIdx = 0
	qc.minProfileVersion++
}

// InvalidateByTopics selectively evicts entries whose intent topics overlap
// with the given topics, plus entries with no topic metadata (can't prove safe).
//
// NOTE: currently unused in production; profile changes use full Invalidate()
// because profile fields affect all enrichments. Reserved for future use when
// context-doc updates need topic-scoped cache eviction.
func (qc *QueryCache) InvalidateByTopics(topics []string) {
	if len(topics) == 0 {
		qc.Invalidate()
		return
	}

	topicSet := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		topicSet[strings.ToLower(t)] = struct{}{}
	}

	qc.mu.Lock()
	defer qc.mu.Unlock()

	// Evict from exact cache.
	for hash, entry := range qc.exactCache {
		if shouldEvict(entry.Topics, topicSet) {
			delete(qc.exactCache, hash)
		}
	}

	// Compact ring buffer in-place, removing evicted entries.
	writePos := 0
	for i := 0; i < qc.semanticLen; i++ {
		if !shouldEvict(qc.semanticCache[i].Result.Topics, topicSet) {
			if writePos != i {
				qc.semanticCache[writePos] = qc.semanticCache[i]
			}
			writePos++
		}
	}
	for i := writePos; i < qc.semanticLen; i++ {
		qc.semanticCache[i] = SemanticEntry{}
	}
	qc.semanticLen = writePos
	qc.semanticWriteIdx = writePos % qc.maxSemanticSize
}

// shouldEvict returns true if the entry should be evicted: either has no topics
// (can't prove safe) or has overlapping topics.
func shouldEvict(entryTopics []string, invalidTopics map[string]struct{}) bool {
	if len(entryTopics) == 0 {
		return true // no metadata — can't determine if affected
	}
	for _, t := range entryTopics {
		if _, ok := invalidTopics[strings.ToLower(t)]; ok {
			return true
		}
	}
	return false
}

// evictionLoop periodically removes expired entries.
func (qc *QueryCache) evictionLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-qc.stopEviction:
			return
		case <-ticker.C:
			qc.evictExpired()
		}
	}
}

func (qc *QueryCache) evictExpired() {
	now := qc.clock.Now()

	qc.mu.Lock()
	defer qc.mu.Unlock()

	for hash, entry := range qc.exactCache {
		if now.After(entry.CachedAt.Add(qc.exactTTL)) {
			delete(qc.exactCache, hash)
		}
	}

	// Compact ring buffer in-place, removing expired entries.
	writePos := 0
	for i := 0; i < qc.semanticLen; i++ {
		if now.Before(qc.semanticCache[i].CachedAt.Add(qc.semanticTTL)) {
			if writePos != i {
				qc.semanticCache[writePos] = qc.semanticCache[i]
			}
			writePos++
		}
	}
	for i := writePos; i < qc.semanticLen; i++ {
		qc.semanticCache[i] = SemanticEntry{}
	}
	qc.semanticLen = writePos
	qc.semanticWriteIdx = writePos % qc.maxSemanticSize
}

// evictOldestExact removes the entry with the earliest CachedAt from the exact
// cache. Called under qc.mu write lock.
func (qc *QueryCache) evictOldestExact() {
	var oldestHash string
	var oldestTime time.Time
	first := true
	for hash, entry := range qc.exactCache {
		if first || entry.CachedAt.Before(oldestTime) {
			oldestHash = hash
			oldestTime = entry.CachedAt
			first = false
		}
	}
	if !first {
		delete(qc.exactCache, oldestHash)
	}
}

// normalize lowercases and collapses whitespace.
func normalize(query string) string {
	lower := strings.ToLower(query)
	fields := strings.Fields(lower)
	return strings.Join(fields, " ")
}

// hashQuery returns SHA256 hex digest of the normalized query.
func hashQuery(normalized string) string {
	h := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(h[:])
}

// unitNormalize returns a copy of v scaled to unit L2 norm.
// Returns nil if v is empty or has zero norm.
func unitNormalize(v []float32) []float32 {
	if len(v) == 0 {
		return nil
	}

	var sumSq float64
	for _, x := range v {
		sumSq += float64(x) * float64(x)
	}
	norm := math.Sqrt(sumSq)
	if norm == 0 {
		return nil
	}

	out := make([]float32, len(v))
	invNorm := float32(1.0 / norm)
	for i, x := range v {
		out[i] = x * invNorm
	}
	return out
}

// dotProduct computes the dot product of two vectors.
// For unit-normalized vectors, this equals cosine similarity.
func dotProduct(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}

// cosineSimilarity computes cosine similarity between two vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}

	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}

	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}
