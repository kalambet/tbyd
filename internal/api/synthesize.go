package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/kalambet/tbyd/internal/storage"
)

func handleTriggerSynthesis(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Deduplicate: if a nightly_synthesis job is already pending or running,
		// return a 200 without enqueuing another one.
		exists, err := deps.Store.HasPendingJobOfType(r.Context(), "nightly_synthesis")
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to check for existing synthesis job: %v", err)
			return
		}
		if exists {
			writeJSON(w, map[string]string{"status": "already_queued"})
			return
		}

		job := storage.Job{
			ID:          uuid.New().String(),
			Type:        "nightly_synthesis",
			PayloadJSON: "{}",
		}
		if err := deps.Store.EnqueueJob(r.Context(), job); err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to enqueue synthesis job: %v", err)
			return
		}

		writeJSON(w, map[string]string{"status": "queued"})
	}
}
