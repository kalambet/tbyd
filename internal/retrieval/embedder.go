package retrieval

import (
	"context"
	"fmt"
)

// OllamaEmbedder is the interface for generating embeddings via Ollama.
type OllamaEmbedder interface {
	Embed(ctx context.Context, model string, text string) ([]float32, error)
}

// Embedder wraps an Ollama client to generate text embeddings.
type Embedder struct {
	client OllamaEmbedder
	model  string
}

// NewEmbedder creates an Embedder using the given Ollama client and model name.
func NewEmbedder(client OllamaEmbedder, model string) *Embedder {
	return &Embedder{client: client, model: model}
}

// Embed returns the embedding vector for a single text.
func (e *Embedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec, err := e.client.Embed(ctx, e.model, text)
	if err != nil {
		return nil, fmt.Errorf("embedding text: %w", err)
	}
	return vec, nil
}

// EmbedBatch returns embedding vectors for multiple texts.
// Returns nil (not error) for empty/nil input.
func (e *Embedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	results := make([][]float32, len(texts))
	for i, text := range texts {
		vec, err := e.client.Embed(ctx, e.model, text)
		if err != nil {
			return nil, fmt.Errorf("embedding text %d: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}
