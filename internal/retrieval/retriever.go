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

	scored, err := r.store.Search(expectedTable, vec, topK, "")
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

	records, err := r.store.GetByIDs(ctx, expectedTable, ids)
	if err != nil {
		return nil, err
	}

	return recordsToChunks(records), nil
}

// defaultHybridRatio is the default vector weight when the intent doesn't specify one.
const defaultHybridRatio = 0.7

// RetrieveForIntent uses the extracted intent to perform richer context retrieval.
// It selects between vector-only, hybrid, or keyword-heavy search based on
// the intent's SearchStrategy. For hybrid/keyword_heavy strategies, it uses
// SearchHybrid which combines BM25 keyword search with vector similarity.
// On embedding failure, it returns an empty slice (graceful degradation).
func (r *Retriever) RetrieveForIntent(ctx context.Context, query string, extracted intent.Intent, topK int) []ContextChunk {
	if topK <= 0 {
		return nil
	}

	// Use intent's suggested topK if non-zero.
	if extracted.SuggestedTopK > 0 {
		topK = extracted.SuggestedTopK
	}

	// Build a best-effort filter from intent topics.
	// The SQLite backend currently ignores this; future backends (LanceDB)
	// will use it for metadata filtering.
	var filter string
	if len(extracted.Topics) > 0 {
		filter = "topics:" + strings.Join(extracted.Topics, ",")
	}

	// Determine search strategy and hybrid ratio.
	strategy := extracted.SearchStrategy
	if strategy == "" {
		strategy = "hybrid"
	}
	hybridRatio := extracted.HybridRatio
	if hybridRatio == 0 && strategy != "keyword_heavy" {
		hybridRatio = defaultHybridRatio
	}
	if strategy == "keyword_heavy" && hybridRatio == 0 {
		hybridRatio = 0.3 // keyword-heavy: 30% vector, 70% keyword
	}

	// For vector_only strategy, use the original multi-embedding approach.
	if strategy == "vector_only" {
		return r.retrieveVectorOnly(ctx, query, extracted, topK, filter)
	}

	// For hybrid and keyword_heavy: use SearchHybrid with entity expansion.
	return r.retrieveHybrid(ctx, query, extracted, topK, float32(hybridRatio), filter)
}

// retrieveVectorOnly performs vector-only retrieval with entity expansion.
func (r *Retriever) retrieveVectorOnly(ctx context.Context, query string, extracted intent.Intent, topK int, filter string) []ContextChunk {
	perSearchK := topK
	if len(extracted.Entities) > 0 {
		perSearchK = topK * 2
	}

	textsToSearch := make([]string, 0, 1+len(extracted.Entities))
	textsToSearch = append(textsToSearch, query)
	textsToSearch = append(textsToSearch, extracted.Entities...)

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

			results, err := r.store.Search(expectedTable, vec, perSearchK, filter)
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

	if err := g.Wait(); err != nil {
		slog.Warn("retrieval group wait returned an error", "error", err)
	}

	return deduplicateAndTrim(allScored, topK)
}

// retrieveHybrid performs hybrid (vector + BM25) retrieval with entity expansion.
func (r *Retriever) retrieveHybrid(ctx context.Context, query string, extracted intent.Intent, topK int, vectorWeight float32, filter string) []ContextChunk {
	// Retrieve more candidates for merging/deduplication.
	perSearchK := topK * 4
	if len(extracted.Entities) > 0 {
		perSearchK = topK * 4
	}

	// Build search texts: original query + entities.
	textsToSearch := make([]string, 0, 1+len(extracted.Entities))
	textsToSearch = append(textsToSearch, query)
	textsToSearch = append(textsToSearch, extracted.Entities...)

	var allScored []ScoredRecord
	var mu sync.Mutex

	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(4)

	for _, text := range textsToSearch {
		g.Go(func() error {
			vec, err := r.embedder.Embed(gCtx, text)
			if err != nil {
				slog.Warn("hybrid retrieval embed failed, skipping", "text", text, "error", err)
				return nil
			}

			results, err := r.store.SearchHybrid(expectedTable, vec, text, perSearchK, vectorWeight, filter)
			if err != nil {
				slog.Warn("hybrid retrieval search failed, skipping", "text", text, "error", err)
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

	if err := g.Wait(); err != nil {
		slog.Warn("hybrid retrieval group wait returned an error", "error", err)
	}

	return deduplicateAndTrim(allScored, topK)
}

// deduplicateAndTrim deduplicates ScoredRecords by SourceID (keeping highest score),
// sorts by score descending, and trims to topK.
func deduplicateAndTrim(allScored []ScoredRecord, topK int) []ContextChunk {
	if len(allScored) == 0 {
		return nil
	}

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

	sort.Slice(deduped, func(i, j int) bool {
		return deduped[i].Score > deduped[j].Score
	})

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
