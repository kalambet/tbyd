package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

// FeedbackJobStore abstracts the storage operations the FeedbackWorker needs.
type FeedbackJobStore interface {
	ClaimNextJob(types []string) (*storage.Job, error)
	CompleteJob(id string) error
	FailJob(id string, errMsg string) error
	GetInteraction(id string) (storage.Interaction, error)
	HasExtractedSignals(id string) (bool, error)
	UpdateExtractedSignals(id string, signalsJSON string) error
	PersistSignalsAtomically(interactionID string, signalsJSON string, counts []storage.SignalCountDelta) error
	GetSignalCounts() ([]storage.SignalCount, error)
}

// ProfileApplier applies a ProfileDelta to the user profile.
type ProfileApplier interface {
	ApplyDelta(delta profile.ProfileDelta) error
}

// FeedbackWorker polls the job queue for "feedback_extract" jobs and uses the
// PreferenceExtractor + Aggregate pipeline to update the user profile.
type FeedbackWorker struct {
	store     FeedbackJobStore
	extractor *PreferenceExtractor
	applier   ProfileApplier
	poll      time.Duration
	logger    *slog.Logger
}

// NewFeedbackWorker creates a FeedbackWorker. If pollInterval is <= 0 it
// defaults to 500ms.
func NewFeedbackWorker(store FeedbackJobStore, extractor *PreferenceExtractor, applier ProfileApplier, poll time.Duration) *FeedbackWorker {
	if poll <= 0 {
		poll = 500 * time.Millisecond
	}
	return &FeedbackWorker{
		store:     store,
		extractor: extractor,
		applier:   applier,
		poll:      poll,
		logger:    slog.Default(),
	}
}

// Run polls for feedback_extract jobs until ctx is cancelled.
func (w *FeedbackWorker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		done, err := w.RunOnce(ctx)
		if err != nil {
			w.logger.Error("feedback worker iteration failed", "error", err)
		}
		if done {
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(w.poll):
		}
	}
}

// feedbackPayload is the expected structure of a feedback_extract job payload.
type feedbackPayload struct {
	InteractionID string `json:"interaction_id"`
}

// RunOnce claims and processes a single feedback_extract job.
// Returns true if a job was found (regardless of success/failure).
func (w *FeedbackWorker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.store.ClaimNextJob([]string{"feedback_extract"})
	if err != nil {
		return false, fmt.Errorf("claiming job: %w", err)
	}
	if job == nil {
		return false, nil
	}

	processErr := w.processJob(ctx, job)

	if processErr != nil {
		w.logger.Warn("feedback job failed", "job_id", job.ID, "error", processErr)
		if failErr := w.store.FailJob(job.ID, processErr.Error()); failErr != nil {
			w.logger.Error("failed to mark feedback job as failed", "job_id", job.ID, "error", failErr)
		}
		return true, nil
	}

	if err := w.store.CompleteJob(job.ID); err != nil {
		return true, fmt.Errorf("completing feedback job %s: %w", job.ID, err)
	}
	return true, nil
}

func (w *FeedbackWorker) processJob(ctx context.Context, job *storage.Job) error {
	// If no model is configured, return an error so the job is retried once a
	// model becomes available — matching the ingest worker pattern.
	if !w.extractor.IsConfigured() {
		return fmt.Errorf("no extraction model configured; job will be retried when a model is available")
	}

	var payload feedbackPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parsing feedback job payload: %w", err)
	}

	// Idempotency check: if a previous attempt already extracted and persisted
	// signals for this interaction, skip the LLM call and counting to avoid
	// double-counting. Go straight to aggregation.
	alreadyProcessed, err := w.store.HasExtractedSignals(payload.InteractionID)
	if err != nil {
		return fmt.Errorf("checking existing signals for %s: %w", payload.InteractionID, err)
	}

	if !alreadyProcessed {
		// Load only the triggering interaction (1 LLM call per job).
		interaction, err := w.store.GetInteraction(payload.InteractionID)
		if err != nil {
			return fmt.Errorf("loading interaction %s: %w", payload.InteractionID, err)
		}

		// Extract signals — errors are propagated so the job is retried.
		signals, err := w.extractor.ExtractFromFeedback(ctx, interaction, interaction.FeedbackScore, interaction.FeedbackNotes)
		if err != nil {
			return fmt.Errorf("extracting signals: %w", err)
		}

		// Persist extracted signals on the interaction row (for auditability
		// and idempotency) and update the summary signal_counts table.
		if len(signals) > 0 {
			signalsJSON, err := json.Marshal(signals)
			if err != nil {
				return fmt.Errorf("marshalling signals: %w", err)
			}
			// Build deltas for the atomic persist.
			counts := buildSignalCountDeltas(signals)
			// Atomically increment signal counts AND mark interaction as
			// processed in a single transaction, preventing double-counting
			// if the process crashes between the two steps.
			if err := w.store.PersistSignalsAtomically(interaction.ID, string(signalsJSON), counts); err != nil {
				return fmt.Errorf("persisting signals atomically: %w", err)
			}
		} else {
			// No signals extracted — mark as processed with empty array so
			// retries don't re-run the LLM call.
			if err := w.store.UpdateExtractedSignals(interaction.ID, "[]"); err != nil {
				return fmt.Errorf("marking interaction as processed: %w", err)
			}
		}
	}

	// Aggregate from the summary counts table — O(distinct patterns), not
	// O(total interactions). Typically < 100 rows for a single user.
	delta, err := AggregateFromCounts(w.store)
	if err != nil {
		return fmt.Errorf("aggregating signal counts: %w", err)
	}

	// Apply the delta when there are changes.
	if len(delta.AddPreferences) > 0 || len(delta.RemovePreferences) > 0 || len(delta.UpdateFields) > 0 {
		if err := w.applier.ApplyDelta(delta); err != nil {
			return fmt.Errorf("applying profile delta: %w", err)
		}
		w.logger.Info("feedback_extract: profile updated",
			"added", len(delta.AddPreferences),
			"removed", len(delta.RemovePreferences),
			"job_id", job.ID,
		)
	}

	return nil
}

// buildSignalCountDeltas converts extracted signals into storage deltas for
// the atomic persist call.
func buildSignalCountDeltas(signals []PreferenceSignal) []storage.SignalCountDelta {
	var deltas []storage.SignalCountDelta
	for _, s := range signals {
		key := strings.ToLower(strings.TrimSpace(s.Pattern))
		if key == "" {
			continue
		}
		pos, neg := 0, 0
		switch s.Type {
		case "positive":
			pos = 1
		case "negative":
			neg = 1
		default:
			continue
		}
		deltas = append(deltas, storage.SignalCountDelta{
			PatternKey:     key,
			PatternDisplay: s.Pattern,
			Positive:       pos,
			Negative:       neg,
		})
	}
	return deltas
}

