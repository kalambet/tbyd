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
	UpdateExtractedSignals(id string, signalsJSON string) error
	IncrementSignalCount(patternKey, patternDisplay string, pos, neg int) error
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

	// Load only the triggering interaction (1 LLM call per job).
	interaction, err := w.store.GetInteraction(payload.InteractionID)
	if err != nil {
		return fmt.Errorf("loading interaction %s: %w", payload.InteractionID, err)
	}

	// Extract signals from this single interaction.
	signals := w.extractor.ExtractFromFeedback(ctx, interaction, interaction.FeedbackScore, interaction.FeedbackNotes)

	// Persist extracted signals on the interaction row (for auditability) and
	// update the summary signal_counts table (for O(1) aggregation).
	if len(signals) > 0 {
		signalsJSON, err := json.Marshal(signals)
		if err != nil {
			return fmt.Errorf("marshalling signals: %w", err)
		}
		if err := w.store.UpdateExtractedSignals(interaction.ID, string(signalsJSON)); err != nil {
			return fmt.Errorf("storing extracted signals: %w", err)
		}

		// Increment per-pattern counts in the summary table.
		if err := w.persistSignalCounts(signals); err != nil {
			return fmt.Errorf("persisting signal counts: %w", err)
		}
	}

	// Aggregate from the summary counts table — O(distinct patterns), not
	// O(total interactions). Typically < 100 rows for a single user.
	delta, err := w.aggregateFromCounts()
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

// persistSignalCounts increments per-pattern counters in the signal_counts
// summary table. Each signal contributes +1 to the positive or negative counter
// for its normalized pattern key.
func (w *FeedbackWorker) persistSignalCounts(signals []PreferenceSignal) error {
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
		}
		if err := w.store.IncrementSignalCount(key, s.Pattern, pos, neg); err != nil {
			return err
		}
	}
	return nil
}

// aggregateFromCounts reads the signal_counts summary table and applies the
// shared activation rules via ShouldActivate. This avoids loading all
// historical signal JSON into memory.
func (w *FeedbackWorker) aggregateFromCounts() (profile.ProfileDelta, error) {
	counts, err := w.store.GetSignalCounts()
	if err != nil {
		return profile.ProfileDelta{}, err
	}

	var delta profile.ProfileDelta
	for _, c := range counts {
		shouldAdd, shouldRemove := ShouldActivate(c.PositiveCount, c.NegativeCount)

		if shouldAdd && shouldRemove {
			continue // true conflict
		}
		if shouldAdd {
			delta.AddPreferences = append(delta.AddPreferences, c.PatternDisplay)
		} else if shouldRemove {
			delta.RemovePreferences = append(delta.RemovePreferences, c.PatternDisplay)
		}
	}
	return delta, nil
}
