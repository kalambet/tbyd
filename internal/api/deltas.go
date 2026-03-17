package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

func handleGetPendingDeltas(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		deltas, err := deps.Store.ListPendingDeltas()
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to list pending deltas: %v", err)
			return
		}
		if deltas == nil {
			deltas = []storage.PendingProfileDelta{}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(deltas)
	}
}

func handleAcceptDelta(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		// Atomically mark as accepted first to prevent TOCTOU races.
		if err := deps.Store.ReviewDelta(id, true); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "pending delta not found")
				return
			}
			if errors.Is(err, storage.ErrAlreadyReviewed) {
				httpError(w, http.StatusConflict, "conflict", "delta has already been reviewed")
				return
			}
			httpError(w, http.StatusInternalServerError, "api_error", "failed to mark delta as accepted: %v", err)
			return
		}

		// Now fetch the delta to apply it.
		delta, err := deps.Store.GetPendingDelta(id)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to get pending delta: %v", err)
			return
		}

		var profileDelta profile.ProfileDelta
		if err := json.Unmarshal([]byte(delta.DeltaJSON), &profileDelta); err != nil {
			httpError(w, http.StatusInternalServerError, "api_error", "failed to parse delta JSON: %v", err)
			return
		}

		if err := deps.Profile.ApplyDelta(profileDelta); err != nil {
			// Roll back the review so the user can retry.
			if rbErr := deps.Store.UnreviewDelta(id); rbErr != nil {
				slog.Error("failed to roll back delta review after apply failure",
					"delta_id", id, "apply_error", err, "rollback_error", rbErr)
			}
			httpError(w, http.StatusInternalServerError, "api_error", "failed to apply delta: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "accepted"})
	}
}

func handleRejectDelta(deps AppDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")

		if err := deps.Store.ReviewDelta(id, false); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				httpError(w, http.StatusNotFound, "not_found", "pending delta not found")
				return
			}
			if errors.Is(err, storage.ErrAlreadyReviewed) {
				httpError(w, http.StatusConflict, "conflict", "delta has already been reviewed")
				return
			}
			httpError(w, http.StatusInternalServerError, "api_error", "failed to mark delta as rejected: %v", err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "rejected"})
	}
}
