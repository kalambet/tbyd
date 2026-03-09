//go:build generate_fixtures

package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/kalambet/tbyd/internal/ollama"
)

// TestGenerateThresholdFixtures generates testdata/threshold_embeddings.json
// by calling the real nomic-embed-text model via Ollama.
//
// Run with: go test -tags generate_fixtures -run TestGenerateThresholdFixtures ./internal/cache/
func TestGenerateThresholdFixtures(t *testing.T) {
	client := ollama.New("http://localhost:11434")
	ctx := context.Background()

	// Verify Ollama is reachable.
	if !client.IsRunning(ctx) {
		t.Skip("Ollama not running; cannot generate fixtures")
	}

	const model = "nomic-embed-text"

	// Query pairs designed to test semantic cache threshold behavior.
	//
	// Near-duplicate pairs (should match at high threshold):
	//   Close rephrasings, added filler words, synonym substitutions.
	//   These represent queries a user might repeat slightly differently.
	//
	// Related-but-different pairs (should NOT match):
	//   Same topic but different intent or scope.
	//
	// Unrelated pairs (should definitely NOT match):
	//   Completely different topics.
	queries := []string{
		// Near-duplicate pairs (indices 0-1, 2-3, 4-5, 6-7, 8-9):
		"How do I sort a slice in Go?",                    // 0
		"What's the way to sort slices in Golang?",        // 1
		"What are Go interfaces?",                         // 2
		"What is a Go interface?",                         // 3
		"How to handle errors in Go",                      // 4
		"How do you handle errors in Golang?",             // 5
		"What is the difference between concurrency and parallelism?",  // 6
		"What's the difference between concurrency and parallelism?",   // 7
		"Explain how channels work in Go",                 // 8
		"How do Go channels work?",                        // 9

		// Related but different (same domain, different question):
		"How do I install Go?",                            // 10
		"What is the latest version of Go?",               // 11
		"How do I write tests in Go?",                     // 12
		"How do I benchmark Go code?",                     // 13

		// Unrelated queries:
		"What is the capital of France?",                  // 14
		"How do I make chocolate chip cookies?",           // 15
		"What are the health benefits of meditation?",     // 16
		"Explain quantum computing basics",                // 17
	}

	type fixture struct {
		Queries    []string    `json:"queries"`
		Embeddings [][]float32 `json:"embeddings"`
	}

	var f fixture
	f.Queries = queries
	f.Embeddings = make([][]float32, len(queries))

	for i, q := range queries {
		emb, err := client.Embed(ctx, model, q)
		if err != nil {
			t.Fatalf("embedding query %d (%q): %v", i, q, err)
		}
		f.Embeddings[i] = emb
		fmt.Printf("embedded %d/%d: %q (dim=%d)\n", i+1, len(queries), q, len(emb))
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("marshalling fixtures: %v", err)
	}

	if err := os.WriteFile("testdata/threshold_embeddings.json", data, 0644); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	t.Logf("wrote testdata/threshold_embeddings.json (%d queries, %d bytes)", len(queries), len(data))
}
