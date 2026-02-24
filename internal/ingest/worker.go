package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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
}

// ContentEmbedder generates embeddings for text.
type ContentEmbedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// VectorInserter inserts records into the vector store.
type VectorInserter interface {
	Insert(table string, records []retrieval.Record) error
}

// Worker processes ingest_enrich jobs from the SQLite job queue.
type Worker struct {
	store    JobStore
	embedder ContentEmbedder
	vectors  VectorInserter
	poll     time.Duration
	logger   *slog.Logger
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

// RunOnce claims and processes a single ingest_enrich job.
// Returns true if a job was processed (regardless of success/failure).
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	job, err := w.store.ClaimNextJob([]string{"ingest_enrich"})
	if err != nil {
		return false, fmt.Errorf("claiming job: %w", err)
	}
	if job == nil {
		return false, nil
	}

	if err := w.processJob(ctx, job); err != nil {
		w.logger.Warn("job failed", "job_id", job.ID, "error", err)
		if failErr := w.store.FailJob(job.ID, err.Error()); failErr != nil {
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

func (w *Worker) processJob(ctx context.Context, job *storage.Job) error {
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

	if err := w.vectors.Insert("context_vectors", []retrieval.Record{rec}); err != nil {
		return fmt.Errorf("inserting vector: %w", err)
	}

	if err := w.store.UpdateContextDocVectorID(doc.ID, rec.ID); err != nil {
		return fmt.Errorf("updating vector_id: %w", err)
	}

	return nil
}
