package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kalambet/tbyd/internal/composer"
	"github.com/kalambet/tbyd/internal/intent"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/retrieval"
)

// EnrichmentMetadata captures diagnostic information about the enrichment process.
type EnrichmentMetadata struct {
	IntentExtracted      bool
	ChunksUsed           []string
	EnrichmentDurationMs int64
}

// Enricher orchestrates the enrichment pipeline: intent extraction, context
// retrieval, profile loading, and prompt composition.
type Enricher struct {
	extractor *intent.Extractor
	retriever *retrieval.Retriever
	profile   *profile.Manager
	composer  *composer.Composer
	topK      int
}

// NewEnricher creates an Enricher wired to all pipeline components.
// topK controls how many context chunks are retrieved (default 5 if <= 0).
func NewEnricher(
	extractor *intent.Extractor,
	retriever *retrieval.Retriever,
	profileMgr *profile.Manager,
	comp *composer.Composer,
	topK int,
) *Enricher {
	if topK <= 0 {
		topK = 5
	}
	return &Enricher{
		extractor: extractor,
		retriever: retriever,
		profile:   profileMgr,
		composer:  comp,
		topK:      topK,
	}
}

// Enrich runs the full enrichment pipeline on the incoming request:
//  1. Extract intent from the last user message (3s timeout, fallback on failure)
//  2. Retrieve context chunks using the intent
//  3. Load user profile summary
//  4. Compose the enriched request
//
// On failure at any step, the pipeline degrades gracefully — the original
// request is enriched with whatever context is available.
func (e *Enricher) Enrich(ctx context.Context, req proxy.ChatRequest) (out proxy.ChatRequest, meta EnrichmentMetadata) {
	start := time.Now()
	defer func() {
		meta.EnrichmentDurationMs = time.Since(start).Milliseconds()
	}()

	// 1. Extract intent from last user message.
	lastUserMsg := extractLastUserMessage(req.Messages)
	extracted := e.extractor.Extract(ctx, lastUserMsg, nil, "")
	if extracted.IntentType != "" {
		meta.IntentExtracted = true
	}

	// 2. Retrieve context chunks.
	chunks := e.retriever.RetrieveForIntent(ctx, lastUserMsg, extracted, e.topK)
	for _, ch := range chunks {
		meta.ChunksUsed = append(meta.ChunksUsed, ch.ID)
	}

	// 3. Load profile summary.
	profileSummary, err := e.profile.GetSummary()
	if err != nil {
		slog.Warn("enrichment: failed to load profile summary", "error", err)
		profileSummary = ""
	}

	// 4. Compose enriched request.
	enriched, err := e.composer.Compose(req, chunks, profileSummary)
	if err != nil {
		slog.Warn("enrichment: composition failed, forwarding original request", "error", err)
		out = req
		return
	}

	slog.Debug("enrichment complete",
		"intent_extracted", meta.IntentExtracted,
		"chunks_used", len(meta.ChunksUsed),
	)

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
