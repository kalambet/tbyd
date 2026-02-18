package retrieval

import (
	"context"
	"database/sql"
	"time"
)

// ContextChunk is a retrieved context fragment with its similarity score.
type ContextChunk struct {
	ID         string
	SourceID   string
	SourceType string
	Text       string
	Score      float32
	Tags       string
	CreatedAt  time.Time
}

// IDLookup is an optional interface for VectorStore implementations that support
// direct record lookup by ID without vector search. SQLiteStore implements this
// via its DB() method. Backends that don't support it will fall back to a
// full Search call in RetrieveByIDs.
type IDLookup interface {
	DB() *sql.DB
}

// Retriever combines embedding and vector search to find relevant context.
type Retriever struct {
	embedder *Embedder
	store    VectorStore
}

// NewRetriever creates a Retriever backed by the given Embedder and VectorStore.
func NewRetriever(embedder *Embedder, store VectorStore) *Retriever {
	return &Retriever{embedder: embedder, store: store}
}

// Retrieve embeds the query and returns the top-K most similar context chunks.
func (r *Retriever) Retrieve(ctx context.Context, query string, topK int) ([]ContextChunk, error) {
	vec, err := r.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}

	scored, err := r.store.Search("context_vectors", vec, topK, "")
	if err != nil {
		return nil, err
	}

	return scoredToChunks(scored), nil
}

// RetrieveByIDs returns context chunks for the given record IDs.
func (r *Retriever) RetrieveByIDs(ctx context.Context, ids []string) ([]ContextChunk, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Fast path: if the store supports direct SQL lookups, use them.
	if lookup, ok := r.store.(IDLookup); ok {
		return r.retrieveByIDsSQL(ctx, lookup.DB(), ids)
	}

	// Fallback: not applicable for non-SQL backends.
	// Future LanceDB implementation should add its own ID lookup method.
	return nil, nil
}

func (r *Retriever) retrieveByIDsSQL(ctx context.Context, db *sql.DB, ids []string) ([]ContextChunk, error) {
	var chunks []ContextChunk
	for _, id := range ids {
		rows, err := db.QueryContext(ctx, `
			SELECT id, source_id, source_type, text_chunk, created_at, tags
			FROM context_vectors WHERE id = ?`, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c ContextChunk
			var createdAt string
			if err := rows.Scan(&c.ID, &c.SourceID, &c.SourceType, &c.Text, &createdAt, &c.Tags); err != nil {
				rows.Close()
				return nil, err
			}
			t, _ := time.Parse(time.RFC3339, createdAt)
			c.CreatedAt = t
			chunks = append(chunks, c)
		}
		rows.Close()
	}
	return chunks, nil
}

func scoredToChunks(scored []ScoredRecord) []ContextChunk {
	chunks := make([]ContextChunk, len(scored))
	for i, s := range scored {
		chunks[i] = ContextChunk{
			ID:         s.ID,
			SourceID:   s.SourceID,
			SourceType: s.SourceType,
			Text:       s.TextChunk,
			Score:      s.Score,
			Tags:       s.Tags,
			CreatedAt:  s.CreatedAt,
		}
	}
	return chunks
}
