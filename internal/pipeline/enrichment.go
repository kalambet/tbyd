package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/kalambet/tbyd/internal/cache"
	"github.com/kalambet/tbyd/internal/composer"
	"github.com/kalambet/tbyd/internal/intent"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/reranking"
	"github.com/kalambet/tbyd/internal/retrieval"
)

// EnrichmentMetadata captures diagnostic information about the enrichment process.
type EnrichmentMetadata struct {
	IntentExtracted      bool
	ChunksUsed           []string
	EnrichmentDurationMs int64
	RerankingDurationMs  int64
	CacheHit             bool
	CacheLevel           string // "exact" or "semantic"
}

// Enricher orchestrates the enrichment pipeline: intent extraction, context
// retrieval, reranking, profile loading, and prompt composition.
type Enricher struct {
	extractor *intent.Extractor
	retriever *retrieval.Retriever
	reranker  reranking.Reranker
	profile   *profile.Manager
	composer  *composer.Composer
	cache     *cache.QueryCache
	topK      int
}

// NewEnricher creates an Enricher wired to all pipeline components.
// topK controls how many context chunks are retrieved (default 5 if <= 0).
// If reranker is nil, a NoOpReranker is used.
// queryCache may be nil (caching disabled).
func NewEnricher(
	extractor *intent.Extractor,
	retriever *retrieval.Retriever,
	profileMgr *profile.Manager,
	comp *composer.Composer,
	rr reranking.Reranker,
	topK int,
	queryCache *cache.QueryCache,
) *Enricher {
	if topK <= 0 {
		topK = 5
	}
	if rr == nil {
		rr = &reranking.NoOpReranker{}
	}
	return &Enricher{
		extractor: extractor,
		retriever: retriever,
		reranker:  rr,
		profile:   profileMgr,
		composer:  comp,
		cache:     queryCache,
		topK:      topK,
	}
}

// candidateMultiplier controls how many extra candidates are fetched for
// reranking. Retrieval fetches topK*candidateMultiplier chunks; after reranking
// the result is trimmed back to topK. This lets the reranker surface relevant
// documents that vector search placed below position topK.
const candidateMultiplier = 4

// Enrich runs the full enrichment pipeline on the incoming request:
//  0. Check query cache (exact then semantic) — return early on hit
//  1. Extract intent from the last user message (3s timeout, fallback on failure)
//  2. Retrieve a larger candidate pool (topK×4) for reranking
//  3. Rerank candidates by query relevance and trim to topK
//  4. Load user profile summary
//  5. Compose the enriched request
//  6. Store result in cache
//
// On failure at any step, the pipeline degrades gracefully — the original
// request is enriched with whatever context is available.
func (e *Enricher) Enrich(ctx context.Context, req proxy.ChatRequest) (out proxy.ChatRequest, meta EnrichmentMetadata) {
	start := time.Now()
	defer func() {
		meta.EnrichmentDurationMs = time.Since(start).Milliseconds()
	}()

	lastUserMsg := extractLastUserMessage(req.Messages)

	// 0. Check cache.
	var queryEmbedding []float32
	if e.cache != nil {
		cr := e.cache.Get(ctx, lastUserMsg)
		if cr.Hit {
			if m, ok := cr.Entry.Metadata.(EnrichmentMetadata); ok {
				meta = m
			} else {
				slog.Warn("cache: metadata type mismatch, dropping cached metrics",
					"type", fmt.Sprintf("%T", cr.Entry.Metadata))
			}
			meta.CacheHit = true
			meta.CacheLevel = cr.CacheLevel
			// EnrichmentDurationMs is set by the defer on return.
			return cr.Entry.EnrichedRequest, meta
		}
		queryEmbedding = cr.Embedding // reuse embedding from L2 lookup
	}

	// Capture profile version before pipeline work begins. If the profile
	// changes mid-pipeline, the stored cache entry will carry an older version
	// and be rejected on subsequent Get calls (see QueryCache.minProfileVersion).
	profileVersion := e.profile.ProfileVersion()

	// 1. Load profile (single fetch, reused for extraction and composition).
	var p profile.Profile
	var profileLoaded bool
	if loaded, profErr := e.profile.GetProfile(); profErr == nil {
		p = loaded
		profileLoaded = true
	} else {
		slog.Warn("enrichment: failed to load profile", "error", profErr)
	}

	// Compute profile summary once — reused for both extraction and composition.
	var profileSummary string
	if profileLoaded {
		profileSummary = e.profile.SummarizeProfile(p)
	}

	// Derive calibration from the already-loaded profile to avoid a redundant
	// storage round-trip through the CalibrationProvider.
	var calibration profile.CalibrationContext
	if profileLoaded {
		calibration = e.profile.BuildCalibration(p)
	}

	// Extract intent — pass profile summary and calibration for domain-aware extraction.
	extracted := e.extractor.Extract(ctx, lastUserMsg, nil, profileSummary, calibration)
	if extracted.IntentType != "" {
		meta.IntentExtracted = true
	}

	// 2. Retrieve a larger candidate pool for reranking.
	candidates := e.retriever.RetrieveForIntent(ctx, lastUserMsg, extracted, e.topK*candidateMultiplier)

	// 3. Rerank candidates and trim to topK.
	rerankStart := time.Now()
	chunks, err := e.reranker.Rerank(ctx, lastUserMsg, candidates)
	meta.RerankingDurationMs = time.Since(rerankStart).Milliseconds()
	if err != nil {
		slog.Warn("enrichment: reranking failed, using original order", "error", err)
		chunks = candidates
	}
	if len(chunks) > e.topK {
		chunks = chunks[:e.topK]
	}

	for _, ch := range chunks {
		meta.ChunksUsed = append(meta.ChunksUsed, ch.ID)
	}

	// 4. Build explicit preferences from the already-loaded profile.
	// Allocate a fresh slice to avoid appending into p.Preferences' backing array.
	var explicitPrefs []string
	if profileLoaded {
		explicitPrefs = make([]string, 0, len(p.Preferences)+len(p.Opinions))
		explicitPrefs = append(explicitPrefs, p.Preferences...)
		explicitPrefs = append(explicitPrefs, p.Opinions...)
	}

	// 5. Compose enriched request.
	enriched, err := e.composer.Compose(req, chunks, explicitPrefs, profileSummary)
	if err != nil {
		slog.Warn("enrichment: composition failed, forwarding original request", "error", err)
		out = req
		return
	}

	slog.Debug("enrichment complete",
		"intent_extracted", meta.IntentExtracted,
		"chunks_used", len(meta.ChunksUsed),
	)

	// 6. Store result in cache.
	// Snapshot the duration now — the defer updates meta.EnrichmentDurationMs
	// after return, so the cached copy would otherwise record 0.
	if e.cache != nil {
		metaForCache := meta
		metaForCache.EnrichmentDurationMs = time.Since(start).Milliseconds()
		e.cache.Set(ctx, lastUserMsg, queryEmbedding, cache.CachedEnrichment{
			EnrichedRequest: enriched,
			Metadata:        metaForCache,
			Topics:          extracted.Topics,
			ProfileVersion:  profileVersion,
		})
	}

	out = enriched
	return
}

// extractLastUserMessage finds the last message with role "user" in the
// raw JSON messages array and returns its content string. Returns "" if
// no user message is found or parsing fails.
func extractLastUserMessage(raw json.RawMessage) string {
	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return ""
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}
