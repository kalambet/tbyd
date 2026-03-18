package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/kalambet/tbyd/internal/storage"
)

// deepEnrichJobType is the job type string for deep enrichment jobs.
const deepEnrichJobType = "ingest_deep_enrich"

// defaultStaleJobTimeout is how long a job must be stuck in "running" before
// it is reset back to "pending" by ResetStaleJobs.
const defaultStaleJobTimeout = 30 * time.Minute

// DeepEnrichStore abstracts storage for the deep enrichment worker.
type DeepEnrichStore interface {
	ClaimJobs(types []string, limit int) ([]storage.Job, error)
	CompleteJob(id string) error
	FailJob(id string, errMsg string) error
	GetContextDoc(id string) (storage.ContextDoc, error)
	UpdateContextDocTagsAndDeepMetadata(id, tags, deepMetadataJSON string) error
	ResetStaleJobs(jobTypes []string, timeout time.Duration) (int, error)
	EnqueueJob(ctx context.Context, job storage.Job) error
}

// deepEnrichPayload is the expected JSON structure of an ingest_deep_enrich job.
type deepEnrichPayload struct {
	ContextDocID string `json:"context_doc_id"`
}

// DeepEnrichmentWorker claims ingest_deep_enrich jobs, groups and batches them,
// and processes each batch with the deep model.
type DeepEnrichmentWorker struct {
	store      DeepEnrichStore
	enricher   *DeepEnricher
	batcher    *Batcher
	idle       *IdleDetector
	claimLimit int
	logger     *slog.Logger
}

// NewDeepEnrichmentWorker creates a DeepEnrichmentWorker.
func NewDeepEnrichmentWorker(
	store DeepEnrichStore,
	enricher *DeepEnricher,
	batcher *Batcher,
	idle *IdleDetector,
	claimLimit int,
) *DeepEnrichmentWorker {
	return &DeepEnrichmentWorker{
		store:      store,
		enricher:   enricher,
		batcher:    batcher,
		idle:       idle,
		claimLimit: claimLimit,
		logger:     slog.Default(),
	}
}

// Run performs a single deep enrichment pass:
//  1. Reset stale jobs (running > 30 min → pending/failed)
//  2. Claim up to claimLimit pending ingest_deep_enrich jobs
//  3. If empty, return nil (nothing to do)
//  4. Load context docs for claimed job payloads
//  5. Group by topics (GroupByTopics)
//  6. Batch each group (Batcher.BatchDocuments)
//  7. For each batch, call enricher.EnrichBatch
//  8. Apply results: merge deep_metadata, update tags additively
//  9. Complete/fail each job accordingly
func (w *DeepEnrichmentWorker) Run(ctx context.Context) error {
	// Step 1: Reset stale jobs.
	reset, err := w.store.ResetStaleJobs([]string{deepEnrichJobType}, defaultStaleJobTimeout)
	if err != nil {
		w.logger.Warn("deep_enrich: failed to reset stale jobs", "error", err)
		// Non-fatal: continue with the pass.
	} else if reset > 0 {
		w.logger.Info("deep_enrich: reset stale jobs", "count", reset)
	}

	// Step 2: Claim pending jobs.
	jobs, err := w.store.ClaimJobs([]string{deepEnrichJobType}, w.claimLimit)
	if err != nil {
		return fmt.Errorf("claiming deep enrich jobs: %w", err)
	}

	// Step 3: Nothing to do.
	if len(jobs) == 0 {
		return nil
	}

	w.logger.Info("deep_enrich: claimed jobs", "count", len(jobs))

	// Step 4: Load context docs.
	// Build a map from doc ID to job ID for completion tracking.
	type jobRef struct {
		jobID string
		doc   storage.ContextDoc
	}
	docRefs := make([]jobRef, 0, len(jobs))
	processedJobIDs := make(map[string]struct{}, len(jobs))

	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}

		var payload deepEnrichPayload
		if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
			w.logger.Warn("deep_enrich: malformed job payload", "job_id", job.ID, "error", err)
			_ = w.store.FailJob(job.ID, fmt.Sprintf("malformed payload: %v", err))
			processedJobIDs[job.ID] = struct{}{}
			continue
		}

		doc, err := w.store.GetContextDoc(payload.ContextDocID)
		if err != nil {
			w.logger.Warn("deep_enrich: context doc not found", "job_id", job.ID, "doc_id", payload.ContextDocID, "error", err)
			_ = w.store.FailJob(job.ID, fmt.Sprintf("context doc not found: %v", err))
			processedJobIDs[job.ID] = struct{}{}
			continue
		}

		docRefs = append(docRefs, jobRef{jobID: job.ID, doc: doc})
	}

	// If context was cancelled during doc loading, fail any jobs that were
	// neither loaded into docRefs nor already individually failed above.
	if ctx.Err() != nil {
		loadedJobIDs := make(map[string]struct{}, len(docRefs))
		for _, ref := range docRefs {
			loadedJobIDs[ref.jobID] = struct{}{}
		}
		for _, j := range jobs {
			if _, done := processedJobIDs[j.ID]; done {
				continue
			}
			if _, loaded := loadedJobIDs[j.ID]; loaded {
				continue
			}
			_ = w.store.FailJob(j.ID, "context cancelled before processing")
		}
		return ctx.Err()
	}

	if len(docRefs) == 0 {
		return nil
	}

	// Extract the docs for grouping.
	docs := make([]storage.ContextDoc, len(docRefs))
	for i, ref := range docRefs {
		docs[i] = ref.doc
	}

	// Step 5: Group by topics.
	groups := GroupByTopics(docs)

	// Build a lookup from doc ID → jobRef for applying results.
	refByDocID := make(map[string]jobRef, len(docRefs))
	for _, ref := range docRefs {
		refByDocID[ref.doc.ID] = ref
	}

	// Step 6 & 7: Batch each group and process.
	for _, group := range groups {
		if ctx.Err() != nil {
			break
		}

		batches := w.batcher.BatchDocuments(group)

		for _, batch := range batches {
			if ctx.Err() != nil {
				break
			}

			enrichments, err := w.enricher.EnrichBatch(ctx, batch)
			if err != nil {
				w.logger.Warn("deep_enrich: batch enrichment failed", "batch_size", len(batch), "error", err)
				for _, doc := range batch {
					if ref, ok := refByDocID[doc.ID]; ok {
						_ = w.store.FailJob(ref.jobID, fmt.Sprintf("enrichment failed: %v", err))
						delete(refByDocID, doc.ID)
					}
				}
				continue
			}

			// Step 8: Apply results.
			// Build a set of valid doc IDs in this batch for validation.
			batchDocIDs := make(map[string]struct{}, len(batch))
			for _, d := range batch {
				batchDocIDs[d.ID] = struct{}{}
			}

			enrichByDocID := make(map[string]DeepEnrichment, len(enrichments))
			for _, e := range enrichments {
				if _, valid := batchDocIDs[e.DocID]; !valid {
					w.logger.Warn("deep_enrich: LLM returned unknown doc_id, ignoring", "doc_id", e.DocID)
					continue
				}
				// Filter cross-references to only include doc IDs in this batch.
				validRefs := make([]string, 0, len(e.CrossReferences))
				for _, ref := range e.CrossReferences {
					if _, ok := batchDocIDs[ref]; ok {
						validRefs = append(validRefs, ref)
					}
				}
				e.CrossReferences = validRefs
				enrichByDocID[e.DocID] = e
			}

			for _, doc := range batch {
				ref, ok := refByDocID[doc.ID]
				if !ok {
					continue
				}

				enrich, hasResult := enrichByDocID[doc.ID]
				if !hasResult {
					// LLM returned no result for this doc — complete (non-fatal).
					w.logger.Warn("deep_enrich: no enrichment result for doc", "doc_id", doc.ID)
					_ = w.store.CompleteJob(ref.jobID)
					delete(refByDocID, doc.ID)
					continue
				}

				// Merge tags additively.
				mergedTags := mergeTagsWithEnrichment(doc.Tags, enrich.EnrichedTopics)

				// Serialize deep metadata.
				deepMetadataJSON, err := json.Marshal(enrich)
				if err != nil {
					w.logger.Warn("deep_enrich: failed to marshal enrichment", "doc_id", doc.ID, "error", err)
					_ = w.store.FailJob(ref.jobID, fmt.Sprintf("marshal enrichment: %v", err))
					delete(refByDocID, doc.ID)
					continue
				}

				// Step 9: Persist.
				if err := w.store.UpdateContextDocTagsAndDeepMetadata(doc.ID, mergedTags, string(deepMetadataJSON)); err != nil {
					w.logger.Warn("deep_enrich: failed to update doc", "doc_id", doc.ID, "error", err)
					_ = w.store.FailJob(ref.jobID, fmt.Sprintf("update doc: %v", err))
					delete(refByDocID, doc.ID)
					continue
				}

				_ = w.store.CompleteJob(ref.jobID)
				delete(refByDocID, doc.ID)
			}
		}
	}

	// Fail any jobs whose docs were not processed (context cancellation mid-loop).
	for docID, ref := range refByDocID {
		w.logger.Warn("deep_enrich: job not processed", "doc_id", docID, "job_id", ref.jobID)
		_ = w.store.FailJob(ref.jobID, "not processed: context cancelled or grouping error")
	}

	return nil
}

// Schedule polls the idle detector and triggers Run when the system is idle,
// and also fires at the scheduledHour (UTC) each day.
//
// idleCheckInterval controls how often idle state is checked.
// scheduledHour (0–23) is the hour in UTC when the worker fires unconditionally.
func (w *DeepEnrichmentWorker) Schedule(ctx context.Context, idleCheckInterval time.Duration, scheduledHour int) {
	if idleCheckInterval <= 0 {
		idleCheckInterval = 5 * time.Minute
	}

	ticker := time.NewTicker(idleCheckInterval)
	defer ticker.Stop()

	// Suppress re-fire only if the server starts during the scheduled hour itself.
	// If we start before or after the scheduled hour, lastScheduledDay = -1 ensures
	// the first scheduled-hour tick fires normally.
	lastScheduledDay := -1
	if time.Now().UTC().Hour() == scheduledHour {
		lastScheduledDay = time.Now().UTC().YearDay()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()

			// Fire at the scheduled hour (once per day).
			if now.Hour() == scheduledHour && now.YearDay() != lastScheduledDay {
				lastScheduledDay = now.YearDay()
				w.logger.Info("deep_enrich: scheduled run triggered", "hour", scheduledHour)
				if err := w.Run(ctx); err != nil && ctx.Err() == nil {
					w.logger.Error("deep_enrich: scheduled run failed", "error", err)
				}
				continue
			}

			// Also fire when idle.
			if w.idle.IsIdle() {
				w.logger.Info("deep_enrich: idle run triggered")
				if err := w.Run(ctx); err != nil && ctx.Err() == nil {
					w.logger.Error("deep_enrich: idle run failed", "error", err)
				}
			}
		}
	}
}

// mergeTagsWithEnrichment additively merges enriched topics into the existing
// tags JSON array. Pass-1 tags are preserved; duplicates are deduplicated.
func mergeTagsWithEnrichment(existingTagsJSON string, enrichedTopics []string) string {
	// Parse existing tags.
	var existing []string
	if existingTagsJSON != "" && existingTagsJSON != "[]" {
		if err := json.Unmarshal([]byte(existingTagsJSON), &existing); err != nil {
			slog.Warn("deep_enrich: malformed existing tags JSON, preserving original", "error", err)
			return existingTagsJSON
		}
	}

	seen := make(map[string]struct{}, len(existing)+len(enrichedTopics))
	merged := make([]string, 0, len(existing)+len(enrichedTopics))

	for _, t := range existing {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			merged = append(merged, t)
		}
	}
	for _, t := range enrichedTopics {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			merged = append(merged, t)
		}
	}

	b, err := json.Marshal(merged)
	if err != nil {
		return existingTagsJSON
	}
	return string(b)
}
