package retrieval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/engine"
)

// mockEngine implements engine.Engine for testing, with only Embed wired up.
type mockEngine struct {
	engine.Engine
	embedFn func(ctx context.Context, model string, text string) ([]float32, error)
}

func (m *mockEngine) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	return m.embedFn(ctx, model, text)
}

func makeVector(dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = float32(i) * 0.001
	}
	return v
}

func TestEmbed_ReturnsDimension(t *testing.T) {
	mock := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return makeVector(384), nil
		},
	}
	e := NewEmbedder(mock, "nomic-embed-text")

	vec, err := e.Embed(context.Background(), "hello world")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vec) != 384 {
		t.Errorf("got %d dimensions, want 384", len(vec))
	}
}

func TestEmbed_OllamaError(t *testing.T) {
	mock := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return nil, errors.New("connection refused")
		},
	}
	e := NewEmbedder(mock, "nomic-embed-text")

	_, err := e.Embed(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestEmbedBatch_CountMatches(t *testing.T) {
	mock := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return makeVector(384), nil
		},
	}
	e := NewEmbedder(mock, "nomic-embed-text")

	vecs, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vecs) != 3 {
		t.Errorf("got %d vectors, want 3", len(vecs))
	}
}

func TestEmbedBatch_OllamaError(t *testing.T) {
	mock := &mockEngine{
		embedFn: func(_ context.Context, _ string, text string) ([]float32, error) {
			if text == "b" {
				return nil, errors.New("embedding failed")
			}
			return makeVector(384), nil
		},
	}
	e := NewEmbedder(mock, "nomic-embed-text")

	_, err := e.EmbedBatch(context.Background(), []string{"a", "b", "c"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "embedding failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestEmbedBatch_EmptyInput(t *testing.T) {
	mock := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			t.Fatal("should not be called for empty input")
			return nil, nil
		},
	}
	e := NewEmbedder(mock, "nomic-embed-text")

	vecs, err := e.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if vecs != nil {
		t.Errorf("got %v, want nil", vecs)
	}
}
