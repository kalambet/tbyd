package retrieval

import (
	"context"
	"fmt"

	"github.com/kalambet/tbyd/internal/engine"
	"golang.org/x/sync/errgroup"
)

// Embedder wraps an Engine to generate text embeddings.
type Embedder struct {
	engine engine.Engine
	model  string
}

// NewEmbedder creates an Embedder using the given Engine and model name.
func NewEmbedder(e engine.Engine, model string) *Embedder {
	return &Embedder{engine: e, model: model}
}

// Embed returns the embedding vector for a single text.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec, err := e.engine.Embed(ctx, e.model, text)
	if err != nil {
		return nil, fmt.Errorf("embedding text: %w", err)
	}
	return vec, nil
}

// EmbedBatch returns embedding vectors for multiple texts concurrently.
// Returns nil (not error) for empty/nil input.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	results := make([][]float32, len(texts))
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(4) // Bound concurrency to avoid overwhelming the engine.

	for i, text := range texts {
		i, text := i, text
		g.Go(func() error {
			vec, err := e.engine.Embed(gCtx, e.model, text)
			if err != nil {
				return fmt.Errorf("embedding text %d: %w", i, err)
			}
			results[i] = vec
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}
	return results, nil
}
