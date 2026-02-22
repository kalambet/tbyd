package retrieval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/intent"
)

// mockVectorStore implements VectorStore for testing.
type mockVectorStore struct {
	searchFn    func(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error)
	insertFn    func(table string, records []Record) error
	getByIDsFn  func(ctx context.Context, table string, ids []string) ([]Record, error)
	deleteFn    func(table string, id string) error
	createFn    func(name string) error
	exportAllFn func(table string) ([]Record, error)
	countFn     func(table string) (int, error)
}

func (m *mockVectorStore) Search(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error) {
	return m.searchFn(table, vector, topK, filter)
}
func (m *mockVectorStore) Insert(table string, records []Record) error {
	if m.insertFn != nil {
		return m.insertFn(table, records)
	}
	return nil
}
func (m *mockVectorStore) GetByIDs(ctx context.Context, table string, ids []string) ([]Record, error) {
	if m.getByIDsFn != nil {
		return m.getByIDsFn(ctx, table, ids)
	}
	return nil, nil
}
func (m *mockVectorStore) Delete(table string, id string) error {
	if m.deleteFn != nil {
		return m.deleteFn(table, id)
	}
	return nil
}
func (m *mockVectorStore) CreateTable(name string) error {
	if m.createFn != nil {
		return m.createFn(name)
	}
	return nil
}
func (m *mockVectorStore) ExportAll(table string) ([]Record, error) {
	if m.exportAllFn != nil {
		return m.exportAllFn(table)
	}
	return nil, nil
}
func (m *mockVectorStore) Count(table string) (int, error) {
	if m.countFn != nil {
		return m.countFn(table)
	}
	return 0, nil
}

func TestRetrieveForIntent_NoEntities(t *testing.T) {
	embedCalls := 0
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			embedCalls++
			return makeVector(768), nil
		},
	}

	searchCalls := 0
	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			searchCalls++
			return []ScoredRecord{
				{Record: Record{ID: "r1", SourceID: "src1", SourceType: "doc", TextChunk: "some text", CreatedAt: time.Now().UTC(), Tags: `[]`}, Score: 0.9},
			}, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "test query", intent.Intent{
		IntentType: "question",
	}, 5)

	if embedCalls != 1 {
		t.Errorf("embed called %d times, want 1", embedCalls)
	}
	if searchCalls != 1 {
		t.Errorf("search called %d times, want 1", searchCalls)
	}
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].ID != "r1" {
		t.Errorf("chunk ID = %q, want %q", chunks[0].ID, "r1")
	}
}

func TestRetrieveForIntent_WithEntities(t *testing.T) {
	embedCalls := 0
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, text string) ([]float32, error) {
			embedCalls++
			return makeVector(768), nil
		},
	}

	searchCalls := 0
	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			searchCalls++
			return []ScoredRecord{
				{Record: Record{ID: "r1", SourceID: "src1", SourceType: "doc", TextChunk: "text1", CreatedAt: time.Now().UTC(), Tags: `[]`}, Score: 0.8},
			}, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "test query", intent.Intent{
		IntentType: "recall",
		Entities:   []string{"database schema", "migrations"},
	}, 5)

	// 1 query embed + 2 entity embeds = 3
	if embedCalls != 3 {
		t.Errorf("embed called %d times, want 3", embedCalls)
	}
	// 1 query search + 2 entity searches = 3
	if searchCalls != 3 {
		t.Errorf("search called %d times, want 3", searchCalls)
	}
	// All 3 searches return the same source_id, so deduplicated to 1 chunk.
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (same source_id deduped)", len(chunks))
	}
}

func TestRetrieveForIntent_Deduplication(t *testing.T) {
	callNum := 0
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return makeVector(768), nil
		},
	}

	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			callNum++
			if callNum == 1 {
				return []ScoredRecord{
					{Record: Record{ID: "r1", SourceID: "shared-src", SourceType: "doc", TextChunk: "text", CreatedAt: time.Now().UTC(), Tags: `[]`}, Score: 0.9},
					{Record: Record{ID: "r2", SourceID: "unique-src", SourceType: "doc", TextChunk: "text2", CreatedAt: time.Now().UTC(), Tags: `[]`}, Score: 0.7},
				}, nil
			}
			return []ScoredRecord{
				{Record: Record{ID: "r3", SourceID: "shared-src", SourceType: "doc", TextChunk: "text", CreatedAt: time.Now().UTC(), Tags: `[]`}, Score: 0.85},
			}, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "query", intent.Intent{
		IntentType: "recall",
		Entities:   []string{"entity1"},
	}, 5)

	// "shared-src" appears in both searches; should be deduplicated.
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2 (deduplicated)", len(chunks))
	}

	// The highest-scoring entry for "shared-src" (0.9) should be kept.
	for _, c := range chunks {
		if c.SourceID == "shared-src" && c.Score != 0.9 {
			t.Errorf("expected score 0.9 for shared-src, got %f", c.Score)
		}
	}
}

func TestRetrieveForIntent_TopKRespected(t *testing.T) {
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return makeVector(768), nil
		},
	}

	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			var results []ScoredRecord
			for i := 0; i < 10; i++ {
				results = append(results, ScoredRecord{
					Record: Record{ID: "r" + string(rune('a'+i)), SourceID: "src" + string(rune('a'+i)), SourceType: "doc", TextChunk: "text", CreatedAt: time.Now().UTC(), Tags: `[]`},
					Score:  float32(10-i) * 0.1,
				})
			}
			return results, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "query", intent.Intent{
		IntentType: "question",
	}, 3)

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
}

func TestRetrieveForIntent_EmptyKnowledgeBase(t *testing.T) {
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return makeVector(768), nil
		},
	}

	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			return nil, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "query", intent.Intent{
		IntentType: "question",
	}, 5)

	if len(chunks) != 0 {
		t.Errorf("got %d chunks, want 0", len(chunks))
	}
}

func TestRetrieveForIntent_EmbedFails(t *testing.T) {
	eng := &mockEngine{
		embedFn: func(_ context.Context, _ string, _ string) ([]float32, error) {
			return nil, errors.New("embed error")
		},
	}

	store := &mockVectorStore{
		searchFn: func(_ string, _ []float32, _ int, _ string) ([]ScoredRecord, error) {
			t.Fatal("search should not be called when embed fails")
			return nil, nil
		},
	}

	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	chunks := retriever.RetrieveForIntent(context.Background(), "query", intent.Intent{
		IntentType: "question",
	}, 5)

	if len(chunks) != 0 {
		t.Errorf("got %d chunks, want 0", len(chunks))
	}
}
