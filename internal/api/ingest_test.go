package api

import (
	"context"
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

// testPDFB64 is a minimal valid PDF containing the text "Hello PDF".
var testPDFB64 = "JVBERi0xLjQKMSAwIG9iago8PC9UeXBlIC9DYXRhbG9nIC9QYWdlcyAyIDAgUj4+CmVuZG9iagoyIDAgb2JqCjw8L1R5cGUgL1BhZ2VzIC9LaWRzIFszIDAgUl0gL0NvdW50IDE+PgplbmRvYmoKMyAwIG9iago8PC9UeXBlIC9QYWdlIC9QYXJlbnQgMiAwIFIgL01lZGlhQm94IFswIDAgNjEyIDc5Ml0gL0NvbnRlbnRzIDQgMCBSIC9SZXNvdXJjZXMgPDwvRm9udCA8PC9GMSA1IDAgUj4+Pj4+PgplbmRvYmoKNCAwIG9iago8PC9MZW5ndGggNDE+PgpzdHJlYW0KQlQgL0YxIDEyIFRmIDEwMCA3MDAgVGQgKEhlbGxvIFBERikgVGogRVQKZW5kc3RyZWFtCmVuZG9iago1IDAgb2JqCjw8L1R5cGUgL0ZvbnQgL1N1YnR5cGUgL1R5cGUxIC9CYXNlRm9udCAvSGVsdmV0aWNhPj4KZW5kb2JqCnhyZWYKMCA2CjAwMDAwMDAwMDAgNjU1MzUgZiAKMDAwMDAwMDAwOSAwMDAwMCBuIAowMDAwMDAwMDU2IDAwMDAwIG4gCjAwMDAwMDAxMTEgMDAwMDAgbiAKMDAwMDAwMDIzMSAwMDAwMCBuIAowMDAwMDAwMzIwIDAwMDAwIG4gCnRyYWlsZXIKPDwvU2l6ZSA2IC9Sb290IDEgMCBSPj4Kc3RhcnR4cmVmCjM4OAolJUVPRgo="

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
	if err := store.SaveInteraction(context.Background(), interaction); err != nil {
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

// TestExtractTextFromPDF validates PDF text extraction at the function level.
func TestExtractTextFromPDF(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString(testPDFB64)
	if err != nil {
		t.Fatalf("decode test PDF: %v", err)
	}

	text, err := extractTextFromPDF(data)
	if err != nil {
		t.Fatalf("extractTextFromPDF: %v", err)
	}
	if !strings.Contains(text, "Hello PDF") {
		t.Errorf("extracted text %q does not contain %q", text, "Hello PDF")
	}
}

func TestIngest_FilePDF(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	body := fmt.Sprintf(`{"source":"cli","type":"file","content":"%s","metadata":{"filename":"doc.pdf"}}`, testPDFB64)
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
		t.Fatalf("GetContextDoc: %v", err)
	}

	// Content should be extracted text, not the raw PDF bytes.
	if !strings.Contains(doc.Content, "Hello PDF") {
		t.Errorf("doc.Content = %q, want it to contain %q", doc.Content, "Hello PDF")
	}
	if strings.HasPrefix(doc.Content, "%PDF") {
		t.Errorf("doc.Content appears to be raw PDF bytes, not extracted text")
	}
	var meta map[string]string
	if err := json.Unmarshal([]byte(doc.Metadata), &meta); err != nil {
		t.Fatalf("failed to unmarshal doc metadata: %v", err)
	}
	if meta["mime_type"] != "application/pdf" {
		t.Errorf("doc.Metadata[\"mime_type\"] = %q, want %q", meta["mime_type"], "application/pdf")
	}
}

func TestIngest_FileMarkdown(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	mdContent := "# Hello\n\nThis is **markdown** content."
	encoded := base64.StdEncoding.EncodeToString([]byte(mdContent))
	body := fmt.Sprintf(`{"source":"cli","type":"file","content":"%s","metadata":{"filename":"notes.md"}}`, encoded)
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
		t.Fatalf("GetContextDoc: %v", err)
	}
	if doc.Content != mdContent {
		t.Errorf("doc.Content = %q, want %q", doc.Content, mdContent)
	}
}

func TestIngest_FileUnsupportedMIME(t *testing.T) {
	h, _ := setupAppHandler(t, testToken)

	// Minimal 1x1 PNG.
	const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"

	body := fmt.Sprintf(`{"source":"cli","type":"file","content":"%s","metadata":{"filename":"image.png"}}`, pngB64)
	rr := httptest.NewRecorder()
	req := authReq(http.MethodPost, "/ingest", body, testToken)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}

	var errResp map[string]interface{}
	json.NewDecoder(rr.Body).Decode(&errResp)
	errObj, _ := errResp["error"].(map[string]interface{})
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "unsupported file type") {
		t.Errorf("error message = %q, want it to contain %q", msg, "unsupported file type")
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

func TestIngest_FileHTML(t *testing.T) {
	h, store := setupAppHandler(t, testToken)

	htmlContent := `<html><body><p>visible</p><script>hidden js</script><style>.hidden{}</style></body></html>`
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlContent))
	body := fmt.Sprintf(`{"source":"cli","type":"file","content":"%s","metadata":{"filename":"page.html"}}`, encoded)
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
		t.Fatalf("GetContextDoc: %v", err)
	}
	if !strings.Contains(doc.Content, "visible") {
		t.Errorf("doc.Content = %q, want it to contain %q", doc.Content, "visible")
	}
	if strings.Contains(doc.Content, "hidden js") {
		t.Errorf("doc.Content = %q, must NOT contain script content %q", doc.Content, "hidden js")
	}
	if strings.Contains(doc.Content, ".hidden") {
		t.Errorf("doc.Content = %q, must NOT contain style content %q", doc.Content, ".hidden")
	}
}
