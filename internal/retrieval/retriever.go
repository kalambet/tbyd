package retrieval

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kalambet/tbyd/internal/intent"
	"golang.org/x/sync/errgroup"
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

	records, err := r.store.GetByIDs(ctx, "context_vectors", ids)
	if err != nil {
		return nil, err
	}

	return recordsToChunks(records), nil
}

// RetrieveForIntent uses the extracted intent to perform richer context retrieval.
// It embeds the original query and each entity separately, merges results,
// deduplicates by SourceID, and returns the top-K chunks by score.
// On embedding failure, it returns an empty slice (graceful degradation).
func (r *Retriever) RetrieveForIntent(ctx context.Context, query string, intent intent.Intent, topK int) []ContextChunk {
	if topK <= 0 {
		return nil
	}

	// Build a best-effort filter from intent topics.
	// The SQLite backend currently ignores this; future backends (LanceDB)
	// will use it for metadata filtering.
	var filter string
	if len(intent.Topics) > 0 {
		filter = "topics:" + strings.Join(intent.Topics, ",")
	}

	// Search with original query vector. Use a larger topK per search to
	// have enough candidates for deduplication and merging.
	perSearchK := topK
	if len(intent.Entities) > 0 {
		perSearchK = topK * 2
	}

	// Embed and search query + each entity concurrently.
	textsToSearch := make([]string, 0, 1+len(intent.Entities))
	textsToSearch = append(textsToSearch, query)
	textsToSearch = append(textsToSearch, intent.Entities...)

	var allScored []ScoredRecord
	var mu sync.Mutex

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(4)

	for _, text := range textsToSearch {
		g.Go(func() error {
			vec, err := r.embedder.Embed(gCtx, text)
			if err != nil {
				slog.Warn("retrieval embed failed, skipping", "text", text, "error", err)
				return nil
			}

			results, err := r.store.Search("context_vectors", vec, perSearchK, filter)
			if err != nil {
				slog.Warn("retrieval search failed, skipping", "text", text, "error", err)
				return nil
			}

			if len(results) > 0 {
				mu.Lock()
				allScored = append(allScored, results...)
				mu.Unlock()
			}
			return nil
		})
	}

	_ = g.Wait()

	if len(allScored) == 0 {
		return nil
	}

	// Deduplicate by SourceID, keeping the highest score per source.
	seen := make(map[string]ScoredRecord)
	for _, sr := range allScored {
		if existing, ok := seen[sr.SourceID]; !ok || sr.Score > existing.Score {
			seen[sr.SourceID] = sr
		}
	}

	deduped := make([]ScoredRecord, 0, len(seen))
	for _, sr := range seen {
		deduped = append(deduped, sr)
	}

	// Sort by score descending.
	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

	// Trim to topK.
	if len(deduped) > topK {
		deduped = deduped[:topK]
	}

	return scoredToChunks(deduped)
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

func recordsToChunks(records []Record) []ContextChunk {
	chunks := make([]ContextChunk, len(records))
	for i, r := range records {
		chunks[i] = ContextChunk{
			ID:         r.ID,
			SourceID:   r.SourceID,
			SourceType: r.SourceType,
			Text:       r.TextChunk,
			Tags:       r.Tags,
			CreatedAt:  r.CreatedAt,
		}
	}
	return chunks
}
