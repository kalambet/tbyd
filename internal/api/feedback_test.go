package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/storage"
)

// seedInteraction saves a minimal interaction into the store for feedback tests.
func seedInteraction(t *testing.T, store *storage.Store, id string) {
	t.Helper()
	err := store.SaveInteraction(context.Background(), storage.Interaction{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		UserQuery: "test query",
		Status:    "completed",
		VectorIDs: "[]",
	})
	if err != nil {
		t.Fatalf("seedInteraction(%q): %v", id, err)
	}
}

func TestFeedback_Positive(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	seedInteraction(t, store, "ix-pos-1")

	body := `{"score":1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-pos-1/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}

	// Verify feedback was persisted.
	ix, err := store.GetInteraction("ix-pos-1")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}
	if ix.FeedbackScore != 1 {
		t.Errorf("FeedbackScore = %d, want 1", ix.FeedbackScore)
	}
}

func TestFeedback_Negative(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	seedInteraction(t, store, "ix-neg-1")

	body := `{"score":-1,"notes":"too verbose"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-neg-1/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "ok" {
		t.Errorf("status = %q, want %q", resp["status"], "ok")
	}

	// Verify score and notes were persisted.
	ix, err := store.GetInteraction("ix-neg-1")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}
	if ix.FeedbackScore != -1 {
		t.Errorf("FeedbackScore = %d, want -1", ix.FeedbackScore)
	}
	if ix.FeedbackNotes != "too verbose" {
		t.Errorf("FeedbackNotes = %q, want %q", ix.FeedbackNotes, "too verbose")
	}
}

func TestFeedback_InvalidScore(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	seedInteraction(t, store, "ix-bad-score")

	for _, body := range []string{`{"score":0}`, `{"score":2}`, `{"score":-2}`, `{}`} {
		rr := httptest.NewRecorder()
		req := authReq(http.MethodPost, "/interactions/ix-bad-score/feedback", body, testToken)
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("body=%s: status = %d, want %d", body, rr.Code, http.StatusBadRequest)
		}
	}
}

func TestFeedback_InteractionNotFound(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"score":1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/nonexistent-id/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

func TestFeedback_QueuesPreferenceJob(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	seedInteraction(t, store, "ix-queue-1")

	body := `{"score":1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-queue-1/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Claim the next job and verify it is a feedback_extract job for the right interaction.
	job, err := store.ClaimNextJob([]string{"feedback_extract"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected a feedback_extract job to be enqueued, got nil")
	}
	if job.Type != "feedback_extract" {
		t.Errorf("job.Type = %q, want %q", job.Type, "feedback_extract")
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		t.Fatalf("parsing job payload: %v", err)
	}
	if payload["interaction_id"] != "ix-queue-1" {
		t.Errorf("payload interaction_id = %q, want %q", payload["interaction_id"], "ix-queue-1")
	}
}

func TestFeedback_NoAuth(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	seedInteraction(t, store, "ix-noauth-1")

	body := `{"score":1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-noauth-1/feedback", body, "")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestFeedback_IndexExists(t *testing.T) {
	// Note: idx_interactions_feedback is created in 001_initial.sql and predates this PR.
	_, store := setupAppHandler(t, testToken)

	var count int
	err := store.DB().QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_interactions_feedback'",
	).Scan(&count)
	if err != nil {
		t.Fatalf("querying sqlite_master: %v", err)
	}
	if count != 1 {
		t.Error("index idx_interactions_feedback not found in sqlite_master")
	}
}
