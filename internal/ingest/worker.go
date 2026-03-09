package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

// JobStore abstracts the job queue operations.
type JobStore interface {
	ClaimNextJob(types []string) (*storage.Job, error)
	CompleteJob(id string) error
	FailJob(id string, errMsg string) error
	GetContextDoc(id string) (storage.ContextDoc, error)
	UpdateContextDocVectorID(id, vectorID string) error
	GetInteraction(id string) (storage.Interaction, error)
	UpdateInteractionVectorIDs(id, vectorIDsJSON string) error
}

// ContentEmbedder generates embeddings for text.
type ContentEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorInserter inserts records into the vector store.
type VectorInserter interface {
	Insert(table string, records []retrieval.Record) error
}

// Summarizer generates a short summary of an interaction for embedding.
type Summarizer interface {
	Summarize(ctx context.Context, userQuery, cloudResponse string) (string, error)
}

// interactionIDNamespacePrefix is the namespace prefix used to derive
// deterministic vector IDs from interaction IDs via uuid.NewSHA1.
const interactionIDNamespacePrefix = "interaction:"

// Worker processes ingest_enrich and interaction_summarize jobs from the SQLite job queue.
type Worker struct {
	store      JobStore
	embedder   ContentEmbedder
	vectors    VectorInserter
	summarizer Summarizer
	poll       time.Duration
	logger     *slog.Logger
}

// NewWorker creates a Worker with the given dependencies.
// If pollInterval is <= 0, it defaults to 500ms.
func NewWorker(store JobStore, embedder ContentEmbedder, vectors VectorInserter, pollInterval time.Duration) *Worker {
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	return &Worker{
		store:    store,
		embedder: embedder,
		vectors:  vectors,
		poll:     pollInterval,
		logger:   slog.Default(),
	}
}

// SetSummarizer configures the summarizer used for interaction_summarize jobs.
func (w *Worker) SetSummarizer(s Summarizer) {
	w.summarizer = s
}

// Run polls for jobs until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}

		done, err := w.RunOnce(ctx)
		if err != nil {
			w.logger.Error("worker iteration failed", "error", err)
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

// RunOnce claims and processes a single job.
// Returns true if a job was processed (regardless of success/failure).
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.store.ClaimNextJob([]string{"ingest_enrich", "interaction_summarize"})
	if err != nil {
		return false, fmt.Errorf("claiming job: %w", err)
	}
	if job == nil {
		return false, nil
	}

	var processErr error
	switch job.Type {
	case "ingest_enrich":
		processErr = w.processEnrichJob(ctx, job)
	case "interaction_summarize":
		processErr = w.processSummarizeJob(ctx, job)
	default:
		processErr = fmt.Errorf("unknown job type: %s", job.Type)
	}

	if processErr != nil {
		w.logger.Warn("job failed", "job_id", job.ID, "type", job.Type, "error", processErr)
		if failErr := w.store.FailJob(job.ID, processErr.Error()); failErr != nil {
			w.logger.Error("failed to mark job as failed", "job_id", job.ID, "error", failErr)
		}
		return true, nil
	}

	if err := w.store.CompleteJob(job.ID); err != nil {
		return true, fmt.Errorf("completing job %s: %w", job.ID, err)
	}
	return true, nil
}

type enrichPayload struct {
	ContextDocID string `json:"context_doc_id"`
}

func (w *Worker) processEnrichJob(ctx context.Context, job *storage.Job) error {
	var payload enrichPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parsing payload: %w", err)
	}

	doc, err := w.store.GetContextDoc(payload.ContextDocID)
	if err != nil {
		return fmt.Errorf("loading context doc %s: %w", payload.ContextDocID, err)
	}

	vec, err := w.embedder.Embed(ctx, doc.Content)
	if err != nil {
		return fmt.Errorf("embedding content: %w", err)
	}

	rec := retrieval.Record{
		ID:         uuid.New().String(),
		SourceID:   doc.ID,
		SourceType: "context_doc",
		TextChunk:  doc.Content,
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       doc.Tags,
	}

	if err := w.vectors.Insert(retrieval.VectorTable, []retrieval.Record{rec}); err != nil {
		return fmt.Errorf("inserting vector: %w", err)
	}

	if err := w.store.UpdateContextDocVectorID(doc.ID, rec.ID); err != nil {
		return fmt.Errorf("updating vector_id: %w", err)
	}

	return nil
}

type summarizePayload struct {
	InteractionID string `json:"interaction_id"`
}

func (w *Worker) processSummarizeJob(ctx context.Context, job *storage.Job) error {
	if w.summarizer == nil {
		// Return an error so the job is retried/failed rather than silently
		// completed. This preserves the ability to reprocess interactions
		// once a summarizer model is configured.
		return fmt.Errorf("no summarizer configured; job will be retried when a model is available")
	}

	var payload summarizePayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("parsing payload: %w", err)
	}

	interaction, err := w.store.GetInteraction(payload.InteractionID)
	if err != nil {
		return fmt.Errorf("loading interaction %s: %w", payload.InteractionID, err)
	}

	// Extract assistant response text from cloud response JSON.
	responseText := extractAssistantContent(interaction.CloudResponse)

	summary, err := w.summarizer.Summarize(ctx, interaction.UserQuery, responseText)
	if err != nil {
		return fmt.Errorf("generating summary: %w", err)
	}

	// Validate summary before embedding — empty or whitespace-only summaries
	// would produce near-zero vectors that cause false-positive retrieval matches.
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("summarizer returned empty result for interaction %s", payload.InteractionID)
	}

	vec, err := w.embedder.Embed(ctx, summary)
	if err != nil {
		return fmt.Errorf("embedding summary: %w", err)
	}

	// Use a deterministic vector ID derived from the interaction ID so that
	// retries are idempotent. If a previous attempt inserted the vector but
	// failed to update interaction.vector_ids, the next attempt overwrites
	// the same record instead of creating an orphan.
	vectorID := uuid.NewSHA1(uuid.NameSpaceOID, []byte(interactionIDNamespacePrefix+interaction.ID)).String()

	rec := retrieval.Record{
		ID:         vectorID,
		SourceID:   interaction.ID,
		SourceType: "interaction",
		TextChunk:  summary,
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       "[]",
	}

	if err := w.vectors.Insert(retrieval.VectorTable, []retrieval.Record{rec}); err != nil {
		return fmt.Errorf("inserting vector: %w", err)
	}

	vectorIDsJSON, err := json.Marshal([]string{vectorID})
	if err != nil {
		return fmt.Errorf("marshaling vector IDs: %w", err)
	}

	// Invariant: exactly one vector per interaction. This overwrites any
	// previous value, which is safe because the vector ID is deterministic.
	if err := w.store.UpdateInteractionVectorIDs(interaction.ID, string(vectorIDsJSON)); err != nil {
		return fmt.Errorf("updating interaction vector_ids: %w", err)
	}

	return nil
}

// extractAssistantContent extracts the assistant message content from an OpenAI-format
// chat completion response JSON. Falls back to the raw response on parse failure.
func extractAssistantContent(responseJSON string) string {
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(responseJSON), &resp); err == nil && len(resp.Choices) > 0 {
		return resp.Choices[0].Message.Content
	}
	return responseJSON
}
