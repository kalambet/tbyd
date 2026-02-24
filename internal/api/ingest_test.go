package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

const testToken = "test-token-12345"

func setupAppHandler(t *testing.T, token string) (http.Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	profileMgr := profile.NewManager(store)

	handler := NewAppHandler(AppDeps{
		Store:      store,
		Profile:    profileMgr,
		Token:      token,
		HTTPClient: http.DefaultClient,
	})
	return handler, store
}

func setupAppHandlerWithHTTPClient(t *testing.T, token string, httpClient *http.Client) (http.Handler, *storage.Store) {
	t.Helper()
	store, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	profileMgr := profile.NewManager(store)

	handler := NewAppHandler(AppDeps{
		Store:      store,
		Profile:    profileMgr,
		Token:      token,
		HTTPClient: httpClient,
	})
	return handler, store
}

func authReq(method, url, body, token string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, url, reader)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestIngest_TextContent(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	body := `{"source":"cli","type":"text","content":"I prefer Go over Python","tags":["preference"]}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "queued" {
		t.Errorf("status = %q, want %q", resp["status"], "queued")
	}
	if resp["id"] == "" {
		t.Fatal("response missing id")
	}

	doc, err := store.GetContextDoc(resp["id"])
	if err != nil {
		t.Fatalf("GetContextDoc(%q) failed: %v", resp["id"], err)
	}
	if doc.Content != "I prefer Go over Python" {
		t.Errorf("doc.Content = %q, want %q", doc.Content, "I prefer Go over Python")
	}
}

func TestIngest_MissingSource(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"type":"text","content":"hello"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestIngest_MissingContent(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"source":"cli"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestIngest_NoAuth(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"source":"cli","type":"text","content":"hello"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, "")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestIngest_ValidAuth(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"source":"cli","type":"text","content":"hello"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
}

func TestIngest_URLType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fetched content from URL")
	}))
	t.Cleanup(upstream.Close)

	h, store := setupAppHandlerWithHTTPClient(t, testToken, upstream.Client())

	body := fmt.Sprintf(`{"source":"cli","type":"url","url":"%s"}`, upstream.URL)
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)

	doc, err := store.GetContextDoc(resp["id"])
	if err != nil {
		t.Fatalf("GetContextDoc(%q) failed: %v", resp["id"], err)
	}
	if doc.Content != "fetched content from URL" {
		t.Errorf("doc.Content = %q, want %q", doc.Content, "fetched content from URL")
	}
}

func TestIngest_FileBase64(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	encoded := base64.StdEncoding.EncodeToString([]byte("Hello, World!"))
	body := fmt.Sprintf(`{"source":"cli","type":"file","content":"%s"}`, encoded)
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)

	doc, err := store.GetContextDoc(resp["id"])
	if err != nil {
		t.Fatalf("GetContextDoc(%q) failed: %v", resp["id"], err)
	}
	if doc.Content != "Hello, World!" {
		t.Errorf("doc.Content = %q, want %q", doc.Content, "Hello, World!")
	}
}

func TestIngest_QueuedImmediately(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	body := `{"source":"cli","type":"text","content":"quick test"}`
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["status"] != "queued" {
		t.Errorf("status = %q, want %q", resp["status"], "queued")
	}
}

func TestGetProfile(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	ct := rr.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var body map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestPatchProfile(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// PATCH
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPatch, "/profile", `{"communication.tone":"direct"}`, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, want %d", rr.Code, http.StatusOK)
	}

	var patchResp map[string]string
	json.NewDecoder(rr.Body).Decode(&patchResp)
	if patchResp["status"] != "updated" {
		t.Errorf("PATCH status = %q, want %q", patchResp["status"], "updated")
	}

	// GET and verify
	rr = httptest.NewRecorder()
	req = authReq(http.MethodGet, "/profile", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rr.Code, http.StatusOK)
	}

	var p profile.Profile
	json.NewDecoder(rr.Body).Decode(&p)
	if p.Communication.Tone != "direct" {
		t.Errorf("communication.tone = %q, want %q", p.Communication.Tone, "direct")
	}
}

func TestListInteractions_Empty(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/interactions", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := strings.TrimSpace(rr.Body.String())
	if body != "[]" {
		t.Errorf("body = %q, want %q", body, "[]")
	}
}

func TestGetInteraction(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	// Save an interaction directly.
	interaction := storage.Interaction{
		ID:        "int-get-1",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UserQuery: "What is Go?",
		Status:    "completed",
		VectorIDs: "[]",
	}
	if err := store.SaveInteraction(interaction); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}

	// GET existing interaction.
	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/interactions/int-get-1", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	var got storage.Interaction
	json.NewDecoder(rr.Body).Decode(&got)
	if got.ID != "int-get-1" {
		t.Errorf("ID = %q, want %q", got.ID, "int-get-1")
	}
	if got.UserQuery != "What is Go?" {
		t.Errorf("UserQuery = %q, want %q", got.UserQuery, "What is Go?")
	}
}

func TestGetInteraction_NotFound(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/interactions/nonexistent", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDeleteInteraction_NotFound(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodDelete, "/interactions/nonexistent", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestDeleteContextDoc_NotFound(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	rr := httptest.NewRecorder()
	req := authReq(http.MethodDelete, "/context-docs/nonexistent", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestListContextDocs_Paginated(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	for i := 0; i < 3; i++ {
		doc := storage.ContextDoc{
			ID:        fmt.Sprintf("doc-%d", i),
			Title:     fmt.Sprintf("Doc %d", i),
			Content:   "content",
			Source:    "test",
			Tags:      "[]",
			CreatedAt: time.Now().UTC(),
		}
		if err := store.SaveContextDoc(doc); err != nil {
			t.Fatalf("SaveContextDoc(%d) failed: %v", i, err)
		}
	}

	rr := httptest.NewRecorder()
	req := authReq(http.MethodGet, "/context-docs?limit=2", "", testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var docs []storage.ContextDoc
	json.NewDecoder(rr.Body).Decode(&docs)
	if len(docs) != 2 {
		t.Fatalf("got %d docs, want 2", len(docs))
	}
}
