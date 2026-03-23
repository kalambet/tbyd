package api

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/kalambet/tbyd/internal/storage"
)

func handleTriggerSynthesis(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		job := storage.Job{
			ID:          uuid.New().String(),
			Type:        "nightly_synthesis",
			PayloadJSON: "{}",
		}

		inserted, err := deps.Store.EnqueueJobIfNotExists(r.Context(), job)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to enqueue synthesis job: %v", err)
			return
		}

		if inserted {
			writeJSON(w, map[string]string{"status": "queued"})
		} else {
			writeJSON(w, map[string]string{"status": "already_queued"})
		}
	}
}
