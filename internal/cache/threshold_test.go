package cache

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/kalambet/tbyd/internal/config"
)

// thresholdFixture holds pre-computed embeddings from nomic-embed-text for
// validating the semantic similarity threshold.
//
// Regenerate with:
//
//	go test -tags generate_fixtures -run TestGenerateThresholdFixtures ./internal/cache/
type thresholdFixture struct {
	Queries    []string    `json:"queries"`
	Embeddings [][]float32 `json:"embeddings"`
}

// similarPair defines an index pair expected to be a semantic cache hit.
type similarPair struct {
	a, b int
	desc string
}

// Fixture query layout:
//
//	0-1:   "How do I sort a slice in Go?" / "What's the way to sort slices in Golang?"
//	2-3:   "What are Go interfaces?" / "What is a Go interface?"
//	4-5:   "How to handle errors in Go" / "How do you handle errors in Golang?"
//	6-7:   "What is the difference between concurrency and parallelism?" / "What's the difference..."
//	8-9:   "Explain how channels work in Go" / "How do Go channels work?"
//	10-13: related Go queries (different question, same domain)
//	14-17: unrelated queries (France, cookies, meditation, quantum)
var nearDuplicates = []similarPair{
	{0, 1, "sort slice rephrasings"},
	{2, 3, "interface singular/plural"},
	{4, 5, "error handling rephrasings"},
	{6, 7, "concurrency vs parallelism contraction"},
	{8, 9, "channels rephrasings"},
}

const fixtureSize = 18

// TestSemanticThresholdEvaluation validates the cosine similarity threshold
// against pre-computed embeddings from the nomic-embed-text model.
//
// The test verifies:
//   - Near-duplicate query pairs (minor rewordings) exceed the threshold
//   - Related-but-different queries and unrelated queries stay below it
//   - The false-positive rate across all non-match pairs is 0%
func TestSemanticThresholdEvaluation(t *testing.T) {
	data, err := os.ReadFile("testdata/threshold_embeddings.json")
	if err != nil {
		t.Skip("threshold fixtures not found; regenerate with: go test -tags generate_fixtures -run TestGenerateThresholdFixtures ./internal/cache/")
	}

	var f thresholdFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parsing fixture: %v", err)
	}

	if len(f.Queries) != fixtureSize || len(f.Embeddings) != fixtureSize {
		t.Fatalf("fixture has %d queries, %d embeddings; expected %d each", len(f.Queries), len(f.Embeddings), fixtureSize)
	}

	const threshold = config.DefaultSemanticThreshold

	// 1. Verify all near-duplicate pairs exceed the threshold.
	t.Log("=== Near-duplicate pairs ===")
	matchFailures := 0
	minMatchSim := math.MaxFloat64
	for _, p := range nearDuplicates {
		sim := cosineSimilarity(f.Embeddings[p.a], f.Embeddings[p.b])
		if sim < minMatchSim {
			minMatchSim = sim
		}
		status := "PASS"
		if sim < threshold {
			status = "FAIL"
			matchFailures++
		}
		t.Logf("  [%s] %.4f  %s", status, sim, p.desc)
	}

	// 2. Check all non-match pairs for false positives.
	falsePositives := 0
	totalCrossPairs := 0
	maxNonMatchSim := 0.0

	for i := 0; i < len(f.Embeddings); i++ {
		for j := i + 1; j < len(f.Embeddings); j++ {
			if isMatchPair(i, j) {
				continue
			}
			sim := cosineSimilarity(f.Embeddings[i], f.Embeddings[j])
			totalCrossPairs++
			if sim > maxNonMatchSim {
				maxNonMatchSim = sim
			}
			if sim >= threshold {
				falsePositives++
				t.Logf("  [FALSE POS] %.4f  %q vs %q", sim, f.Queries[i], f.Queries[j])
			}
		}
	}

	gap := minMatchSim - maxNonMatchSim

	t.Log("=== Summary ===")
	t.Logf("  Threshold:             %.2f", threshold)
	t.Logf("  Near-duplicates:       %d/%d passed", len(nearDuplicates)-matchFailures, len(nearDuplicates))
	t.Logf("  Non-match pairs:       %d checked, %d false positives (%.2f%%)",
		totalCrossPairs, falsePositives, float64(falsePositives)/float64(totalCrossPairs)*100)
	t.Logf("  Min match similarity:  %.4f", minMatchSim)
	t.Logf("  Max non-match similarity: %.4f", maxNonMatchSim)
	t.Logf("  Separation gap:        %.4f", gap)

	if matchFailures > 0 {
		t.Errorf("%d near-duplicate pairs fell below threshold %.2f", matchFailures, threshold)
	}
	if falsePositives > 0 {
		t.Errorf("%d false positives at threshold %.2f", falsePositives, threshold)
	}
	if gap <= 0 {
		t.Errorf("no separation gap: min match (%.4f) <= max non-match (%.4f)", minMatchSim, maxNonMatchSim)
	}
}

// isMatchPair returns true if (i, j) is one of the known near-duplicate pairs.
func isMatchPair(i, j int) bool {
	for _, p := range nearDuplicates {
		if (i == p.a && j == p.b) || (i == p.b && j == p.a) {
			return true
		}
	}
	return false
}
