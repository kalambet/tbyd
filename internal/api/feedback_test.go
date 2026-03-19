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
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
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
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
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

// seedInteractionWithVectors saves an interaction with a specific vector_ids JSON array.
func seedInteractionWithVectors(t *testing.T, store *storage.Store, id string, vectorIDs []string) {
	t.Helper()
	vectorIDsJSON := "[]"
	if len(vectorIDs) > 0 {
		b, err := json.Marshal(vectorIDs)
		if err != nil {
			t.Fatalf("marshaling vectorIDs: %v", err)
		}
		vectorIDsJSON = string(b)
	}
	err := store.SaveInteraction(context.Background(), storage.Interaction{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		UserQuery: "test query",
		Status:    "completed",
		VectorIDs: vectorIDsJSON,
	})
	if err != nil {
		t.Fatalf("seedInteractionWithVectors(%q): %v", id, err)
	}
}

// seedVector inserts a minimal vector into context_vectors with a default quality_score.
func seedVector(t *testing.T, store *storage.Store, id string) {
	t.Helper()
	_, err := store.DB().Exec(
		`INSERT INTO context_vectors (id, source_id, source_type, text_chunk, embedding, created_at, quality_score)
		 VALUES (?, 'src', 'doc', 'text', X'00000000', datetime('now'), 1.0)`,
		id,
	)
	if err != nil {
		t.Fatalf("seedVector(%q): %v", id, err)
	}
}

func TestFeedback_Negative_AdjustsQualityScores(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	seedInteractionWithVectors(t, store, "ix-adj-neg", []string{"vec-a", "vec-b"})
	seedVector(t, store, "vec-a")
	seedVector(t, store, "vec-b")

	body := `{"score":-1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-adj-neg/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	for _, id := range []string{"vec-a", "vec-b"} {
		var qs float64
		err := store.DB().QueryRow(`SELECT quality_score FROM context_vectors WHERE id = ?`, id).Scan(&qs)
		if err != nil {
			t.Fatalf("querying quality_score for %s: %v", id, err)
		}
		want := 0.9
		if qs < want-0.001 || qs > want+0.001 {
			t.Errorf("vector %s: quality_score = %f, want %f after negative feedback", id, qs, want)
		}
	}
}

func TestFeedback_Positive_AdjustsQualityScores(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	seedInteractionWithVectors(t, store, "ix-adj-pos", []string{"vec-c"})
	seedVector(t, store, "vec-c")

	body := `{"score":1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-adj-pos/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var qs float64
	err := store.DB().QueryRow(`SELECT quality_score FROM context_vectors WHERE id = 'vec-c'`).Scan(&qs)
	if err != nil {
		t.Fatalf("querying quality_score: %v", err)
	}
	want := 1.05
	if qs < want-0.001 || qs > want+0.001 {
		t.Errorf("quality_score = %f, want %f after positive feedback", qs, want)
	}
}

func TestFeedback_NoVectorIDs_SkipsAdjustment(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	// Seed an interaction with empty vector_ids.
	seedInteraction(t, store, "ix-no-vecs")

	// Seed a vector that should NOT be touched.
	seedVector(t, store, "untouched-vec")

	body := `{"score":-1}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/interactions/ix-no-vecs/feedback", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// The unrelated vector's quality_score must be untouched.
	var qs float64
	err := store.DB().QueryRow(`SELECT quality_score FROM context_vectors WHERE id = 'untouched-vec'`).Scan(&qs)
	if err != nil {
		t.Fatalf("querying quality_score: %v", err)
	}
	if qs != 1.0 {
		t.Errorf("quality_score = %f, want 1.0 (should be unaffected when vector_ids is empty)", qs)
	}
}
