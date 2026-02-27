package reranking

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/retrieval"
)

// --- mock engine ---

type mockEngine struct {
	chatFn func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error)
}

func (m *mockEngine) Chat(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
	if m.chatFn != nil {
		return m.chatFn(ctx, model, msgs, schema)
	}
	return `{"score": 0.5}`, nil
}

func (m *mockEngine) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockEngine) IsRunning(ctx context.Context) bool                                    { return true }
func (m *mockEngine) ListModels(ctx context.Context) ([]string, error)                      { return nil, nil }
func (m *mockEngine) HasModel(ctx context.Context, name string) bool                        { return true }
func (m *mockEngine) PullModel(ctx context.Context, name string, fn func(engine.PullProgress)) error {
	return nil
}

// --- helpers ---

func makeChunks(n int, score float32) []retrieval.ContextChunk {
	chunks := make([]retrieval.ContextChunk, n)
	for i := range chunks {
		chunks[i] = retrieval.ContextChunk{
			ID:    fmt.Sprintf("chunk-%d", i),
			Text:  fmt.Sprintf("text %d", i),
			Score: score,
		}
	}
	return chunks
}

func newLLMReranker(eng engine.Engine, threshold float64, timeout time.Duration, topK int) *LLMReranker {
	return &LLMReranker{
		engine:    eng,
		model:     "phi3.5",
		timeout:   timeout,
		threshold: threshold,
		topK:      topK,
	}
}

// --- tests ---

func TestLLMReranker_ReordersChunks(t *testing.T) {
	scores := []float64{0.9, 0.3, 0.7}
	var callIdx atomic.Int32
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			i := int(callIdx.Add(1)) - 1
			return fmt.Sprintf(`{"score": %g}`, scores[i]), nil
		},
	}

	chunks := makeChunks(3, 0.5)
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 3 {
		t.Fatalf("got %d chunks, want 3", len(result))
	}
	wantOrder := []float32{0.9, 0.7, 0.3}
	for i, ch := range result {
		if ch.Score != wantOrder[i] {
			t.Errorf("result[%d].Score = %g, want %g", i, ch.Score, wantOrder[i])
		}
	}
}

func TestLLMReranker_DropsLowScore(t *testing.T) {
	// One chunk scores 0.1 (below threshold 0.3), two score above.
	scores := []float64{0.8, 0.1, 0.7}
	var callIdx atomic.Int32
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			i := int(callIdx.Add(1)) - 1
			return fmt.Sprintf(`{"score": %g}`, scores[i]), nil
		},
	}

	chunks := makeChunks(3, 0.5)
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("got %d chunks, want 2 (low-score chunk should be dropped)", len(result))
	}
	for _, ch := range result {
		if float64(ch.Score) < 0.3 {
			t.Errorf("chunk with score %g below threshold was not dropped", ch.Score)
		}
	}
}

func TestLLMReranker_AllBelowThreshold(t *testing.T) {
	// All chunks score below threshold — should return empty slice, not original.
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			return `{"score": 0.1}`, nil // all below threshold 0.3
		},
	}

	chunks := makeChunks(3, 0.9)
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d chunks, want 0 — all scored below threshold, original must not be returned", len(result))
	}
}

func TestLLMReranker_Timeout(t *testing.T) {
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			// Hang until context is cancelled.
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	chunks := makeChunks(3, 0.8)
	r := newLLMReranker(eng, 0.3, 200*time.Millisecond, 0)

	start := time.Now()
	result, err := r.Rerank(context.Background(), "query", chunks)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Rerank took %v, want < 500ms (2.5x timeout)", elapsed)
	}
	if result != nil {
		t.Errorf("expected nil result on timeout, got %d chunks", len(result))
	}
}

func TestLLMReranker_MarkdownCodeFence(t *testing.T) {
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			return "```json\n{\"score\": 0.8}\n```", nil
		},
	}

	chunks := makeChunks(1, 0.5)
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d chunks, want 1", len(result))
	}
	if result[0].Score != 0.8 {
		t.Errorf("score = %g, want 0.8 (parsed from markdown-fenced JSON)", result[0].Score)
	}
}

func TestLLMReranker_ConversationalFiller(t *testing.T) {
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			return `The relevance score is: {"score": 0.6}`, nil
		},
	}

	chunks := makeChunks(1, 0.5)
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d chunks, want 1", len(result))
	}
	if result[0].Score != 0.6 {
		t.Errorf("score = %g, want 0.6 (extracted from conversational filler)", result[0].Score)
	}
}

func TestLLMReranker_MalformedJSON(t *testing.T) {
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			return "completely unparseable garbage blah blah", nil
		},
	}

	originalScore := float32(0.9)
	chunks := []retrieval.ContextChunk{
		{ID: "c1", Text: "text", Score: originalScore},
	}
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d chunks, want 1 (chunk should not be dropped on parse failure)", len(result))
	}
	if result[0].Score != originalScore {
		t.Errorf("score = %g, want original %g (should not be penalised)", result[0].Score, originalScore)
	}
}

func TestLLMReranker_EarlyReturn(t *testing.T) {
	const total = 10
	const quickCount = 5

	var callCount atomic.Int32
	eng := &mockEngine{
		chatFn: func(ctx context.Context, model string, msgs []engine.Message, schema *engine.Schema) (string, error) {
			n := int(callCount.Add(1))
			if n <= quickCount {
				return `{"score": 0.8}`, nil // score quickly
			}
			// Hang until context is cancelled.
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
	}

	chunks := makeChunks(total, 0.5)
	// topK=5, total=10: early return fires once 5 chunks are scored.
	r := newLLMReranker(eng, 0.3, 10*time.Second, quickCount)

	done := make(chan []retrieval.ContextChunk, 1)
	go func() {
		result, _ := r.Rerank(context.Background(), "query", chunks)
		done <- result
	}()

	select {
	case result := <-done:
		if len(result) != quickCount {
			t.Errorf("got %d chunks, want %d (early return set)", len(result), quickCount)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Rerank did not return early — waited for timeout instead of using early-return")
	}
}

func TestLLMReranker_EmptyChunks(t *testing.T) {
	eng := &mockEngine{}
	r := newLLMReranker(eng, 0.3, 5*time.Second, 0)
	result, err := r.Rerank(context.Background(), "query", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d chunks, want 0 for empty input", len(result))
	}
}

func TestNoOpReranker(t *testing.T) {
	chunks := makeChunks(3, 0.5)
	// Shuffle scores to verify order is unchanged.
	chunks[0].Score = 0.3
	chunks[1].Score = 0.9
	chunks[2].Score = 0.1

	r := &NoOpReranker{}
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("got %d chunks, want 3", len(result))
	}
	for i, ch := range result {
		if ch.Score != chunks[i].Score {
			t.Errorf("result[%d].Score = %g, want %g (order must be unchanged)", i, ch.Score, chunks[i].Score)
		}
	}
}

func TestNewReranker_Enabled(t *testing.T) {
	eng := &mockEngine{}
	r := NewReranker(eng, "phi3.5", true, 5*time.Second, 0.3, 5)
	if _, ok := r.(*LLMReranker); !ok {
		t.Errorf("NewReranker(enabled=true) returned %T, want *LLMReranker", r)
	}
}

func TestNewReranker_Disabled(t *testing.T) {
	r := NewReranker(nil, "", false, 0, 0, 0)
	if _, ok := r.(*NoOpReranker); !ok {
		t.Errorf("NewReranker(enabled=false) returned %T, want *NoOpReranker", r)
	}
}

func TestNewReranker_NilEngine(t *testing.T) {
	// Enabled but nil engine must fall back to NoOpReranker rather than panic later.
	r := NewReranker(nil, "phi3.5", true, 5*time.Second, 0.3, 5)
	if _, ok := r.(*NoOpReranker); !ok {
		t.Errorf("NewReranker(enabled=true, eng=nil) returned %T, want *NoOpReranker", r)
	}
	// Verify it works without panicking.
	chunks := makeChunks(2, 0.8)
	result, err := r.Rerank(context.Background(), "query", chunks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d chunks, want 2", len(result))
	}
}
