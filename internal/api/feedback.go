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

	"github.com/kalambet/tbyd/internal/storage"
)

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

		if len(req.Notes) > 2000 {
			httpError(w, http.StatusBadRequest, "invalid_request_error", "notes must be 2000 characters or fewer")
			return
		}

		if err := deps.Store.UpdateFeedback(id, req.Score, req.Notes); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "interaction not found")
				return
			}
			httpError(w, http.StatusInternalServerError, "api_error", "failed to update feedback: %v", err)
			return
		}

		if err := enqueueFeedbackExtract(r.Context(), deps.Store, id); err != nil {
			slog.Error("feedback saved but failed to enqueue feedback_extract job",
				"interaction_id", id, "error", err)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	}
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
