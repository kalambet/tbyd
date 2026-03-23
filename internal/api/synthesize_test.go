package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTriggerSynthesis_EnqueuesJob(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/synthesize", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["status"] != "queued" {
		t.Errorf("status = %q, want %q", resp["status"], "queued")
	}

	// Verify the job was actually written to the store.
	job, err := store.ClaimNextJob([]string{"nightly_synthesis"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if job == nil {
		t.Fatal("expected a nightly_synthesis job in the queue, got nil")
	}
	if job.Type != "nightly_synthesis" {
		t.Errorf("job.Type = %q, want %q", job.Type, "nightly_synthesis")
	}
}

func TestTriggerSynthesis_AlreadyQueued(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// First call enqueues.
	rr1 := httptest.NewRecorder()
	req1 := authReq(http.MethodPost, "/profile/synthesize", "", testToken)
	h.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first call: status = %d, want %d", rr1.Code, http.StatusOK)
	}

	// Second call should return already_queued.
	rr2 := httptest.NewRecorder()
	req2 := authReq(http.MethodPost, "/profile/synthesize", "", testToken)
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second call: status = %d, want %d", rr2.Code, http.StatusOK)
	}

	var resp map[string]string
	if err := json.NewDecoder(rr2.Body).Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp["status"] != "already_queued" {
		t.Errorf("status = %q, want %q", resp["status"], "already_queued")
	}
}

func TestTriggerSynthesis_Unauthorized(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/profile/synthesize", "", "wrong-token")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}
