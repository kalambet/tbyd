package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

const maxFeedbackNotesLength = 2000

type feedbackRequest struct {
	Score int    `json:"score"`
	Notes string `json:"notes"`
}

func handleFeedback(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodySize)
		defer r.Body.Close()

		var req feedbackRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "invalid request body: %v", err)
			return
		}

		if req.Score != 1 && req.Score != -1 {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "score must be 1 or -1")
			return
		}

		if err := saveFeedback(r.Context(), deps.Store, id, req.Score, req.Notes); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "interaction not found")
				return
			}
			if errors.Is(err, errNotesTooLong) {
				httpError(w, http.StatusBadRequest, "invalid_request_error", "notes must be %d characters or fewer", maxFeedbackNotesLength)
				return
			}
			httpError(w, http.StatusInternalServerError, "api_error", "failed to update feedback: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
}

var errNotesTooLong = fmt.Errorf("notes exceed maximum length")

// saveFeedback validates notes length, persists the feedback, enqueues
// a feedback_extract job, and adjusts quality scores for the retrieved chunks.
// Shared by the HTTP handler and MCP tool.
//
// Note: feedback persistence, job enqueue, and quality adjustment are not
// transactional. A crash between steps may leave quality scores slightly off.
// This is acceptable for a best-effort quality signal.
func saveFeedback(ctx context.Context, store *storage.Store, id string, score int, notes string) error {
	if len(notes) > maxFeedbackNotesLength {
		return errNotesTooLong
	}

	if err := store.UpdateFeedback(id, score, notes); err != nil {
		return err
	}

	if err := enqueueFeedbackExtract(ctx, store, id); err != nil {
		slog.Error("feedback saved but failed to enqueue feedback_extract job",
			"interaction_id", id, "error", err)
	}

	// Adjust quality scores for any vector chunks used in this interaction.
	interaction, err := store.GetInteraction(id)
	if err != nil {
		slog.Warn("feedback: could not load interaction for quality adjustment",
			"interaction_id", id, "error", err)
		return nil
	}

	var vectorIDs []string
	if interaction.VectorIDs != "" && interaction.VectorIDs != "[]" {
		if err := json.Unmarshal([]byte(interaction.VectorIDs), &vectorIDs); err != nil {
			slog.Debug("feedback: could not parse vector_ids JSON",
				"interaction_id", id, "error", err)
			return nil
		}
	}

	if len(vectorIDs) > 0 {
		positive := score == 1
		if err := retrieval.AdjustQualityScores(store.DB(), vectorIDs, positive); err != nil {
			slog.Error("feedback: failed to adjust quality scores",
				"interaction_id", id, "vector_count", len(vectorIDs), "error", err)
		} else {
			slog.Debug("feedback: quality scores adjusted",
				"interaction_id", id, "vector_ids", vectorIDs, "positive", positive)
		}
	}

	return nil
}

// enqueueFeedbackExtract enqueues a feedback_extract job for the given interaction ID.
// Used by the MCP layer to avoid duplicating job-building logic.
func enqueueFeedbackExtract(ctx context.Context, store *storage.Store, id string) error {
	payload, err := json.Marshal(map[string]string{"interaction_id": id})
	if err != nil {
		return fmt.Errorf("failed to marshal feedback job payload: %w", err)
	}
	job := storage.Job{
		ID:          uuid.New().String(),
		Type:        "feedback_extract",
		PayloadJSON: string(payload),
	}
	return store.EnqueueJob(ctx, job)
}
