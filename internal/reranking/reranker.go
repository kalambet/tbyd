package reranking

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/retrieval"
)

const defaultConcurrency = 3

// Reranker re-scores retrieved context chunks by query relevance.
type Reranker interface {
	Rerank(ctx context.Context, query string, chunks []retrieval.ContextChunk) ([]retrieval.ContextChunk, error)
}

// NewReranker returns an LLMReranker if enabled, NoOpReranker otherwise.
//
// topK controls the early-return threshold: once topK chunks have been scored,
// the reranker returns that subset immediately without waiting for remaining
// chunks. Set topK to 0 (or >= len(chunks)) to disable early return.
func NewReranker(eng engine.Engine, model string, enabled bool, timeout time.Duration, threshold float64, topK int) Reranker {
	if !enabled {
		return &NoOpReranker{}
	}
	return &LLMReranker{
		engine:    eng,
		model:     model,
		timeout:   timeout,
		threshold: threshold,
		topK:      topK,
	}
}

// LLMReranker uses a local LLM to score (query, chunk) relevance pairs.
// Scoring runs concurrently (bounded to defaultConcurrency goroutines).
// Results are filtered by threshold and sorted by score descending.
type LLMReranker struct {
	engine    engine.Engine
	model     string
	timeout   time.Duration
	threshold float64
	topK      int // early-return threshold; 0 = score all
}

// Rerank scores each chunk against the query and returns a filtered, sorted
// result set. If the timeout fires before scoring completes, the original
// chunk order is returned unchanged (graceful degradation).
func (r *LLMReranker) Rerank(ctx context.Context, query string, chunks []retrieval.ContextChunk) ([]retrieval.ContextChunk, error) {
	if len(chunks) == 0 {
		return chunks, nil
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// Early return fires when topK > 0 and topK < len(chunks).
	earlyReturnAt := r.topK
	if earlyReturnAt <= 0 || earlyReturnAt >= len(chunks) {
		earlyReturnAt = 0
	}

	// Buffered channel prevents goroutines from blocking on send after we stop reading.
	results := make(chan retrieval.ContextChunk, len(chunks))
	sem := make(chan struct{}, defaultConcurrency)

	var wg sync.WaitGroup
	for _, ch := range chunks {
		wg.Add(1)
		go func(chunk retrieval.ContextChunk) {
			defer wg.Done()
			// Acquire concurrency slot or bail on cancellation.
			select {
			case sem <- struct{}{}:
			case <-timeoutCtx.Done():
				return
			}
			defer func() { <-sem }()

			score, err := r.scoreChunk(timeoutCtx, query, chunk)
			if err != nil {
				if timeoutCtx.Err() != nil {
					return // context cancelled — don't send partial result
				}
				slog.Debug("reranker: score failed, retaining original", "error", err)
				results <- chunk // original score preserved
				return
			}
			chunk.Score = float32(score)
			results <- chunk
		}(ch)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	scored := make([]retrieval.ContextChunk, 0, len(chunks))
collect:
	for {
		select {
		case ch, ok := <-results:
			if !ok {
				break collect // all goroutines finished
			}
			scored = append(scored, ch)
			if earlyReturnAt > 0 && len(scored) >= earlyReturnAt {
				cancel() // stop remaining goroutines
				break collect
			}
		case <-timeoutCtx.Done():
			// Hard timeout hit before enough chunks were scored: graceful degradation.
			return chunks, nil
		}
	}

	if len(scored) == 0 {
		return chunks, nil
	}

	// Filter chunks below the relevance threshold.
	filtered := make([]retrieval.ContextChunk, 0, len(scored))
	for _, ch := range scored {
		if float64(ch.Score) >= r.threshold {
			filtered = append(filtered, ch)
		}
	}

	// Sort by score descending.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	return filtered, nil
}

func (r *LLMReranker) scoreChunk(ctx context.Context, query string, chunk retrieval.ContextChunk) (float64, error) {
	prompt := "Rate the relevance of the following text to the query on a scale of 0.0 to 1.0.\n" +
		"Query: " + query + "\n" +
		"Text: " + chunk.Text + "\n" +
		`Respond with only a JSON object: {"score": <float>}`

	schema := &engine.Schema{
		Type: "object",
		Properties: map[string]engine.SchemaProperty{
			"score": {Type: "number", Description: "Relevance score 0.0–1.0"},
		},
		Required: []string{"score"},
	}

	resp, err := r.engine.Chat(ctx, r.model, []engine.Message{
		{Role: "user", Content: prompt},
	}, schema)
	if err != nil {
		return float64(chunk.Score), err
	}

	score, parseErr := parseScore(resp, chunk.Score)
	if parseErr != nil {
		slog.Debug("reranker: parse failed, using original score", "resp", resp, "error", parseErr)
		return float64(chunk.Score), nil
	}
	return score, nil
}

// parseScore robustly extracts a relevance score float from an LLM response.
// Small local models (phi3.5) frequently wrap JSON in markdown code fences or
// prepend conversational filler. The parser:
//  1. Strips markdown code fences if present (```json ... ```)
//  2. Finds the first { and last } to extract the JSON object
//  3. Attempts json.Unmarshal on the extracted substring
//  4. On failure: returns originalScore so the chunk is not penalised
func parseScore(resp string, originalScore float32) (float64, error) {
	s := strings.TrimSpace(resp)

	// Strip markdown code fences.
	if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx+3:]
		if strings.HasPrefix(s, "json") {
			s = s[4:]
		}
		if end := strings.Index(s, "```"); end != -1 {
			s = s[:end]
		}
	}

	// Extract JSON object by brace position.
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end <= start {
		return float64(originalScore), fmt.Errorf("no JSON object in response")
	}

	var obj struct {
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &obj); err != nil {
		return float64(originalScore), fmt.Errorf("unmarshal score: %w", err)
	}
	return obj.Score, nil
}

// NoOpReranker passes chunks through unchanged. Used when reranking is disabled.
type NoOpReranker struct{}

func (n *NoOpReranker) Rerank(_ context.Context, _ string, chunks []retrieval.ContextChunk) ([]retrieval.ContextChunk, error) {
	return chunks, nil
}
