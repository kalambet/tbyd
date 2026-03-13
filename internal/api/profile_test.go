package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/profile"
)

func TestGetProfile_ReturnsJSON(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

func TestPatchProfile_PartialUpdate(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// First set two independent fields.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile",
		`{"communication.tone":"direct","identity.role":"engineer"}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial PATCH status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// PATCH only tone — role should remain unchanged.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodPatch, "/profile", `{"communication.tone":"formal"}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("partial PATCH status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// GET and verify both fields.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rr.Code, http.StatusOK)
	}

	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if p.Communication.Tone != "formal" {
		t.Errorf("tone = %q, want %q", p.Communication.Tone, "formal")
	}
	if p.Identity.Role != "engineer" {
		t.Errorf("role = %q, want %q (should be unchanged)", p.Identity.Role, "engineer")
	}
}

func TestDeleteProfileField(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Set a field first.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile", `{"communication.tone":"direct"}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// DELETE the field.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodDelete, "/profile/communication.tone", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var delResp map[string]string
	json.NewDecoder(rr.Body).Decode(&delResp)
	if delResp["status"] != "deleted" {
		t.Errorf("delete response status = %q, want %q", delResp["status"], "deleted")
	}

	// GET and verify field is gone.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}

	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if p.Communication.Tone != "" {
		t.Errorf("communication.tone = %q, want empty after delete", p.Communication.Tone)
	}
}

func TestDeleteProfileField_NotFound(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodDelete, "/profile/communication.tone", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusNotFound, rr.Body.String())
	}
}

// TestDeleteProfileField_ArrayItem verifies that DELETE /profile/interests.primary[go]
// removes a single value from the array while leaving the rest intact.
func TestDeleteProfileField_ArrayItem(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Seed interests.primary with two values.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile",
		`{"interests.primary":["go","privacy"]}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// DELETE the "go" item by value.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodDelete, "/profile/interests.primary[go]", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// GET and verify only "privacy" remains.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}

	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if len(p.Interests.Primary) != 1 || p.Interests.Primary[0] != "privacy" {
		t.Errorf("interests.primary = %v, want [privacy]", p.Interests.Primary)
	}
}

// TestDeleteProfileField_MapEntry verifies that DELETE /profile/identity.expertise.go
// removes a single key from the expertise map while leaving others intact.
func TestDeleteProfileField_MapEntry(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Seed expertise with two entries.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile",
		`{"identity.expertise":{"go":"expert","rust":"intermediate"}}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// DELETE the "go" expertise entry.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodDelete, "/profile/identity.expertise.go", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// GET and verify only "rust" remains.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}

	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if _, hasGo := p.Identity.Expertise["go"]; hasGo {
		t.Errorf("expertise still contains 'go' after delete")
	}
	if p.Identity.Expertise["rust"] != "intermediate" {
		t.Errorf("expertise['rust'] = %q, want 'intermediate'", p.Identity.Expertise["rust"])
	}
}

// TestDeleteProfileField_WholeSubArray verifies that DELETE /profile/interests.primary
// removes the entire primary interests array.
func TestDeleteProfileField_WholeSubArray(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Seed both primary and emerging interests.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile",
		`{"interests.primary":["go","privacy"],"interests.emerging":["wasm"]}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// DELETE the entire primary array.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodDelete, "/profile/interests.primary", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// GET and verify primary is gone but emerging is untouched.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}

	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if len(p.Interests.Primary) != 0 {
		t.Errorf("interests.primary = %v, want empty after delete", p.Interests.Primary)
	}
	if len(p.Interests.Emerging) != 1 || p.Interests.Emerging[0] != "wasm" {
		t.Errorf("interests.emerging = %v, want [wasm] (should be unchanged)", p.Interests.Emerging)
	}
}

// TestPatchProfile_UnknownKeyRejected verifies that PATCH with an unrecognised key
// returns 400 and does not write any data.
func TestPatchProfile_UnknownKeyRejected(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile", `{"__proto__":"injected"}`, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	// Verify the error message mentions the bad key.
	body := rr.Body.String()
	if !strings.Contains(body, "__proto__") {
		t.Errorf("error body %q does not mention rejected key", body)
	}
}

// TestDeleteProfileField_ArrayItemWithSpaces verifies that array items with spaces
// (e.g., "Distributed Systems") can be deleted via bracket syntax.
func TestDeleteProfileField_ArrayItemWithSpaces(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Seed interests with a multi-word value.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile",
		`{"interests.primary":["Distributed Systems","Privacy"]}`, testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", rr.Code, rr.Body.String())
	}

	// DELETE the multi-word item. URL-encode the space for httptest.NewRequest.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodDelete, "/profile/interests.primary[Distributed%20Systems]", "", testToken)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	// GET and verify only "Privacy" remains.
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)
	var p profile.Profile
	if err := json.NewDecoder(rr.Body).Decode(&p); err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if len(p.Interests.Primary) != 1 || p.Interests.Primary[0] != "Privacy" {
		t.Errorf("interests.primary = %v, want [Privacy]", p.Interests.Primary)
	}
}

// TestDeleteProfileField_InvalidChars verifies that a DELETE path containing
// disallowed characters is rejected with 400 before touching storage.
// Paths are chosen to be syntactically valid HTTP URLs but contain characters
// outside the allowlist (alphanumeric, '.', '[', ']', '_', '-').
func TestDeleteProfileField_InvalidChars(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	for _, badPath := range []string{
		"communication.tone!important",  // '!' is not in allowlist
		"identity.role$admin",           // '$' is not in allowlist
		"interests.primary+extra",       // '+' is not in allowlist
	} {
		rr := httptest.NewRecorder()
		req := authReq(http.MethodDelete, "/profile/"+badPath, "", testToken)
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("path %q: status = %d, want %d; body = %s",
				badPath, rr.Code, http.StatusBadRequest, rr.Body.String())
		}
	}
}
