package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/proxy"
)

// --- mock embedder ---

type mockEmbedder struct {
	embedFn func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFn != nil {
		return m.embedFn(ctx, text)
	}
	return make([]float32, 768), nil
}

// --- mock clock ---

type mockClock struct {
	now time.Time
}

func (c *mockClock) Now() time.Time { return c.now }

func (c *mockClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// --- helpers ---

func makeEntry(content string, topics []string) CachedEnrichment {
	return CachedEnrichment{
		EnrichedRequest: proxy.ChatRequest{Model: "test"},
		Metadata:        content,
		Topics:          topics,
	}
}

// fixedEmbedding returns an embedder that always returns the same vector.
func fixedEmbedding(vec []float32) *mockEmbedder {
	return &mockEmbedder{
		embedFn: func(ctx context.Context, text string) ([]float32, error) {
			return vec, nil
		},
	}
}

// --- tests ---

func TestExactCacheHit(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("cached result", []string{"go"})
	qc.Set(context.Background(), "hello world", nil, entry)

	result := qc.Get(context.Background(), "hello world")
	if !result.Hit {
		t.Fatal("expected cache hit")
	}
	if result.CacheLevel != "exact" {
		t.Errorf("CacheLevel = %q, want \"exact\"", result.CacheLevel)
	}
}

func TestExactCacheMiss(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("cached result", nil)
	qc.Set(context.Background(), "hello world", nil, entry)

	result := qc.Get(context.Background(), "goodbye world")
	if result.Hit {
		t.Fatal("expected cache miss for different query")
	}
}

func TestExactCacheTTL(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("cached result", nil)
	qc.Set(context.Background(), "hello world", nil, entry)

	// Advance past exact TTL.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "hello world")
	if result.Hit {
		t.Fatal("expected cache miss after TTL expiry")
	}
}

func TestSemanticCacheHit(t *testing.T) {
	// Use identical embeddings for high cosine similarity.
	vec := make([]float32, 768)
	vec[0] = 1.0
	vec[1] = 0.5

	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(vec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("semantic result", []string{"go"})
	qc.Set(context.Background(), "original query", vec, entry)

	// Advance past exact TTL so L1 misses, but within semantic TTL.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "similar query")
	if !result.Hit {
		t.Fatal("expected semantic cache hit")
	}
	if result.CacheLevel != "semantic" {
		t.Errorf("CacheLevel = %q, want \"semantic\"", result.CacheLevel)
	}
}

func TestSemanticCacheMiss(t *testing.T) {
	// Dissimilar embeddings.
	storedVec := make([]float32, 768)
	storedVec[0] = 1.0

	queryVec := make([]float32, 768)
	queryVec[1] = 1.0 // orthogonal

	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(queryVec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("semantic result", nil)
	qc.Set(context.Background(), "original query", storedVec, entry)

	// Advance past exact TTL.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "very different query")
	if result.Hit {
		t.Fatal("expected semantic cache miss for dissimilar embeddings")
	}
}

func TestSemanticCacheTTL(t *testing.T) {
	vec := make([]float32, 768)
	vec[0] = 1.0

	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(vec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	entry := makeEntry("semantic result", nil)
	qc.Set(context.Background(), "original query", vec, entry)

	// Advance past semantic TTL.
	clock.Advance(31 * time.Minute)

	result := qc.Get(context.Background(), "similar query")
	if result.Hit {
		t.Fatal("expected cache miss after semantic TTL expiry")
	}
}

func TestInvalidate(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	vec := make([]float32, 768)
	vec[0] = 1.0
	emb := fixedEmbedding(vec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "query1", vec, makeEntry("r1", nil))
	qc.Set(context.Background(), "query2", vec, makeEntry("r2", nil))

	qc.Invalidate()

	r1 := qc.Get(context.Background(), "query1")
	r2 := qc.Get(context.Background(), "query2")
	if r1.Hit || r2.Hit {
		t.Fatal("expected all misses after invalidation")
	}
}

func TestCacheDisabled(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, false, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "hello world", nil, makeEntry("result", nil))

	result := qc.Get(context.Background(), "hello world")
	if result.Hit {
		t.Fatal("expected always miss when cache disabled")
	}
}

func TestNormalization(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "Hello World", nil, makeEntry("result", nil))

	// Should match with different case/whitespace.
	result := qc.Get(context.Background(), "  hello  world  ")
	if !result.Hit {
		t.Fatal("expected cache hit for normalized equivalent query")
	}
}

func TestInvalidateByTopics_SelectiveEviction(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "go query", nil, makeEntry("go result", []string{"go", "programming"}))
	qc.Set(context.Background(), "rust query", nil, makeEntry("rust result", []string{"rust", "systems"}))

	qc.InvalidateByTopics([]string{"go"})

	r1 := qc.Get(context.Background(), "go query")
	r2 := qc.Get(context.Background(), "rust query")

	if r1.Hit {
		t.Error("expected 'go' entry to be evicted")
	}
	if !r2.Hit {
		t.Error("expected 'rust' entry to be preserved")
	}
}

func TestInvalidateByTopics_NoMetadataEvicted(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	// Entry with no topics should be evicted when any topic invalidation occurs.
	qc.Set(context.Background(), "no topic query", nil, makeEntry("no topic result", nil))

	qc.InvalidateByTopics([]string{"anything"})

	result := qc.Get(context.Background(), "no topic query")
	if result.Hit {
		t.Error("expected entry with no topics to be evicted")
	}
}

func TestInvalidateByTopics_PreservesKnownSafe(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "matching query", nil, makeEntry("match", []string{"database"}))
	qc.Set(context.Background(), "safe query", nil, makeEntry("safe", []string{"networking"}))
	qc.Set(context.Background(), "no meta query", nil, makeEntry("no meta", nil))

	qc.InvalidateByTopics([]string{"database"})

	rMatch := qc.Get(context.Background(), "matching query")
	rSafe := qc.Get(context.Background(), "safe query")
	rNoMeta := qc.Get(context.Background(), "no meta query")

	if rMatch.Hit {
		t.Error("matching topic entry should be evicted")
	}
	if !rSafe.Hit {
		t.Error("non-matching topic entry should be preserved")
	}
	if rNoMeta.Hit {
		t.Error("no-metadata entry should be evicted")
	}
}

func TestEmbedFailureDoesNotPanic(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := &mockEmbedder{
		embedFn: func(ctx context.Context, text string) ([]float32, error) {
			return nil, errors.New("embed failed")
		},
	}
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	// Set an entry with exact match only (no embedding).
	qc.Set(context.Background(), "test query", nil, makeEntry("result", nil))

	// Advance past exact TTL so it tries L2 (semantic) which will fail.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "test query")
	if result.Hit {
		t.Error("expected miss when embed fails and exact TTL expired")
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float64
		tol  float64
	}{
		{
			name: "identical",
			a:    []float32{1, 2, 3},
			b:    []float32{1, 2, 3},
			want: 1.0,
			tol:  0.001,
		},
		{
			name: "orthogonal",
			a:    []float32{1, 0, 0},
			b:    []float32{0, 1, 0},
			want: 0.0,
			tol:  0.001,
		},
		{
			name: "opposite",
			a:    []float32{1, 0},
			b:    []float32{-1, 0},
			want: -1.0,
			tol:  0.001,
		},
		{
			name: "empty",
			a:    []float32{},
			b:    []float32{},
			want: 0.0,
			tol:  0.001,
		},
		{
			name: "different lengths",
			a:    []float32{1, 2},
			b:    []float32{1, 2, 3},
			want: 0.0,
			tol:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cosineSimilarity(tt.a, tt.b)
			if got < tt.want-tt.tol || got > tt.want+tt.tol {
				t.Errorf("cosineSimilarity = %f, want %f (tol %f)", got, tt.want, tt.tol)
			}
		})
	}
}

func TestNormalizeFunction(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello world"},
		{"  hello  world  ", "hello world"},
		{"UPPER", "upper"},
		{"", ""},
		{"  ", ""},
	}

	for _, tt := range tests {
		got := normalize(tt.input)
		if got != tt.want {
			t.Errorf("normalize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSemanticCacheBestMatch(t *testing.T) {
	// Three stored entries with varying similarity to the query embedding.
	// The cache should return the best match (highest similarity), not the first.

	// Query embedding: unit vector along dim 0.
	queryVec := make([]float32, 768)
	queryVec[0] = 1.0

	// Entry A: stored first, similarity ~0.93 (mostly dim 0, small dim 1 component).
	vecA := make([]float32, 768)
	vecA[0] = 0.93
	vecA[1] = 0.37 // cos(queryVec, vecA) ≈ 0.93

	// Entry B: stored second, similarity ~0.99 (almost identical to query).
	vecB := make([]float32, 768)
	vecB[0] = 0.99
	vecB[1] = 0.10 // cos(queryVec, vecB) ≈ 0.995

	// Entry C: stored third, similarity ~0.95.
	vecC := make([]float32, 768)
	vecC[0] = 0.95
	vecC[1] = 0.30 // cos(queryVec, vecC) ≈ 0.95

	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(queryVec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	// Insert entries in order A, B, C (B is best match but not first).
	qc.Set(context.Background(), "query A", vecA, makeEntry("result A", []string{"a"}))
	qc.Set(context.Background(), "query B", vecB, makeEntry("result B", []string{"b"}))
	qc.Set(context.Background(), "query C", vecC, makeEntry("result C", []string{"c"}))

	// Advance past exact TTL so only L2 is checked.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "new query")
	if !result.Hit {
		t.Fatal("expected semantic cache hit")
	}
	// Should return entry B (best match), not entry A (first match).
	if result.Entry.Metadata != "result B" {
		t.Errorf("expected best match 'result B', got %q", result.Entry.Metadata)
	}
}

func TestSemanticCacheMaxSize(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)
	qc.maxSemanticSize = 3 // small cap for testing

	for i := 0; i < 5; i++ {
		vec := make([]float32, 768)
		vec[0] = float32(i)
		qc.Set(context.Background(), fmt.Sprintf("query %d", i), vec, makeEntry(fmt.Sprintf("r%d", i), nil))
	}

	qc.mu.RLock()
	size := qc.semanticLen
	qc.mu.RUnlock()

	if size != 3 {
		t.Errorf("semantic cache size = %d, want 3", size)
	}
}

func TestProfileVersionRejectsStaleExactEntries(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(make([]float32, 768))
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	// Store entry with ProfileVersion=0 (pipeline started before profile update).
	entry := makeEntry("stale result", []string{"go"})
	entry.ProfileVersion = 0
	qc.Set(context.Background(), "hello world", nil, entry)

	// Verify hit before invalidation.
	result := qc.Get(context.Background(), "hello world")
	if !result.Hit {
		t.Fatal("expected cache hit before invalidation")
	}

	// Invalidate bumps minProfileVersion to 1.
	qc.Invalidate()

	// Re-store the stale entry (simulates concurrent pipeline finishing after invalidation).
	qc.Set(context.Background(), "hello world", nil, entry)

	// Get should reject: ProfileVersion=0 < minProfileVersion=1.
	result = qc.Get(context.Background(), "hello world")
	if result.Hit {
		t.Error("expected cache miss for stale profile version entry")
	}
}

func TestProfileVersionRejectsStaleSemanticEntries(t *testing.T) {
	vec := make([]float32, 768)
	vec[0] = 1.0

	clock := &mockClock{now: time.Now()}
	emb := fixedEmbedding(vec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	// Store semantic entry with ProfileVersion=0.
	entry := makeEntry("stale semantic", []string{"go"})
	entry.ProfileVersion = 0
	qc.Set(context.Background(), "original query", vec, entry)

	// Invalidate bumps minProfileVersion.
	qc.Invalidate()

	// Re-store stale entry (concurrent pipeline).
	qc.Set(context.Background(), "original query", vec, entry)

	// Advance past exact TTL so only L2 is checked.
	clock.Advance(6 * time.Minute)

	result := qc.Get(context.Background(), "similar query")
	if result.Hit {
		t.Error("expected semantic cache miss for stale profile version entry")
	}
}

func TestEvictExpired(t *testing.T) {
	clock := &mockClock{now: time.Now()}
	vec := make([]float32, 768)
	vec[0] = 1.0
	emb := fixedEmbedding(vec)
	qc := NewQueryCacheWithClock(emb, true, 0.92, 5*time.Minute, 30*time.Minute, clock)

	qc.Set(context.Background(), "query1", vec, makeEntry("r1", nil))

	// Advance past all TTLs.
	clock.Advance(31 * time.Minute)

	qc.evictExpired()

	// Both caches should be empty.
	qc.mu.RLock()
	exactLen := len(qc.exactCache)
	semLen := qc.semanticLen
	qc.mu.RUnlock()

	if exactLen != 0 {
		t.Errorf("exact cache has %d entries, want 0", exactLen)
	}
	if semLen != 0 {
		t.Errorf("semantic cache has %d entries, want 0", semLen)
	}
}
