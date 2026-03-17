package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/storage"
)

// savePendingDelta is a test helper that inserts a pending delta and fails on error.
func savePendingDelta(t *testing.T, store *storage.Store, id, source string) storage.PendingProfileDelta {
	t.Helper()
	delta := storage.PendingProfileDelta{
		ID:          id,
		DeltaJSON:   `{"AddPreferences":["concise responses"],"RemovePreferences":[],"UpdateFields":null}`,
		Description: "Test delta " + id,
		Source:      source,
		CreatedAt:   time.Now().UTC(),
	}
	if err := store.SavePendingDelta(delta); err != nil {
		t.Fatalf("SavePendingDelta(%q): %v", id, err)
	}
	return delta
}

func TestGetPendingDeltas_Empty(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/profile/pending-deltas", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	body := rr.Body.String()
	// Expect empty JSON array.
	var result []interface{}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatalf("response is not valid JSON array: %v; body = %s", err, body)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d items", len(result))
	}
}

func TestGetPendingDeltas_ReturnsList(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	savePendingDelta(t, store, "delta-1", "nightly_synthesis")
	savePendingDelta(t, store, "delta-2", "nightly_synthesis")

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/profile/pending-deltas", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result []storage.PendingProfileDelta
	if err := json.NewDecoder(rr.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d deltas, want 2", len(result))
	}
}

func TestAcceptDelta(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	delta := savePendingDelta(t, store, "delta-accept-1", "nightly_synthesis")

	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/accept", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "accepted" {
		t.Errorf("status = %q, want %q", resp["status"], "accepted")
	}

	// Verify the delta was marked accepted in storage.
	stored, err := store.GetPendingDelta(delta.ID)
	if err != nil {
		t.Fatalf("GetPendingDelta: %v", err)
	}
	if stored.Accepted == nil || !*stored.Accepted {
		t.Errorf("stored.Accepted = %v, want true", stored.Accepted)
	}
	if stored.ReviewedAt == nil {
		t.Error("stored.ReviewedAt is nil, want non-nil")
	}
}

func TestRejectDelta(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	delta := savePendingDelta(t, store, "delta-reject-1", "nightly_synthesis")

	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/reject", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "rejected" {
		t.Errorf("status = %q, want %q", resp["status"], "rejected")
	}

	// Verify the delta is stored as rejected.
	stored, err := store.GetPendingDelta(delta.ID)
	if err != nil {
		t.Fatalf("GetPendingDelta: %v", err)
	}
	if stored.Accepted == nil || *stored.Accepted {
		t.Errorf("stored.Accepted = %v, want false", stored.Accepted)
	}
	if stored.ReviewedAt == nil {
		t.Error("stored.ReviewedAt is nil, want non-nil")
	}
}

func TestAcceptDelta_AlreadyReviewed(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	delta := savePendingDelta(t, store, "delta-dup-1", "nightly_synthesis")

	// Accept once.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/accept", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first accept status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// Accept again — should return 409.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/accept", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("second accept status = %d, want %d; body = %s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestRejectedDeltaNotReapplied(t *testing.T) {
	h, store := setupAppHandler(t, testToken)
	delta := savePendingDelta(t, store, "delta-norepply-1", "nightly_synthesis")

	// Reject the delta.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/reject", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("reject status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// The rejected delta no longer appears in the pending list.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile/pending-deltas", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var result []storage.PendingProfileDelta
	json.NewDecoder(rr.Body).Decode(&result)
	for _, d := range result {
		if d.ID == delta.ID {
			t.Errorf("rejected delta %q still appears in pending list", delta.ID)
		}
	}

	// Attempting to accept a rejected delta returns 409 (already reviewed).
	rr = httptest.NewRecorder()
	req = authReq(http.MethodPost, "/profile/pending-deltas/"+delta.ID+"/accept", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Errorf("accept-after-reject status = %d, want %d (conflict)", rr.Code, http.StatusConflict)
	}
}
