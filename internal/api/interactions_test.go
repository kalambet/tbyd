package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/storage"
)

// mockInteractionSaver records calls to SaveInteraction and EnqueueJob.
type mockInteractionSaver struct {
	mu           sync.Mutex
	interactions []storage.Interaction
	jobs         []storage.Job
	saveFn       func(storage.Interaction) error
	saveDone     chan struct{} // signalled after each successful save
}

func (m *mockInteractionSaver) SaveInteraction(_ context.Context, i storage.Interaction) error {
	if m.saveFn != nil {
		return m.saveFn(i)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.interactions = append(m.interactions, i)
	if m.saveDone != nil {
		m.saveDone <- struct{}{}
	}
	return nil
}

func (m *mockInteractionSaver) EnqueueJob(_ context.Context, j storage.Job) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, j)
	return nil
}

func (m *mockInteractionSaver) getInteractions() []storage.Interaction {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]storage.Interaction, len(m.interactions))
	copy(cp, m.interactions)
	return cp
}

func (m *mockInteractionSaver) getJobs() []storage.Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]storage.Job, len(m.jobs))
	copy(cp, m.jobs)
	return cp
}

func TestSaveInteraction_OptInEnabled(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h := NewOpenAIHandler(ctx, c, nil, saver, true, true)

	body := `{"model":"test","messages":[{"role":"user","content":"hi there"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Wait for the async save goroutine to complete.
	select {
	case <-saver.saveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction save")
	}

	interactions := saver.getInteractions()
	if len(interactions) != 1 {
		t.Fatalf("saved %d interactions, want 1", len(interactions))
	}

	ix := interactions[0]
	if ix.UserQuery != "hi there" {
		t.Errorf("UserQuery = %q, want %q", ix.UserQuery, "hi there")
	}
	if ix.CloudModel != "test" {
		t.Errorf("CloudModel = %q, want %q", ix.CloudModel, "test")
	}
	if ix.CloudResponse != respJSON {
		t.Errorf("CloudResponse = %q, want %q", ix.CloudResponse, respJSON)
	}
	if ix.Status != "completed" {
		t.Errorf("Status = %q, want %q", ix.Status, "completed")
	}
	if ix.ID == "" {
		t.Error("ID is empty")
	}

	// Verify summarization job was enqueued.
	jobs := saver.getJobs()
	if len(jobs) != 1 {
		t.Fatalf("enqueued %d jobs, want 1", len(jobs))
	}
	if jobs[0].Type != "interaction_summarize" {
		t.Errorf("job.Type = %q, want %q", jobs[0].Type, "interaction_summarize")
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(jobs[0].PayloadJSON), &payload); err != nil {
		t.Fatalf("parsing job payload: %v", err)
	}
	if payload["interaction_id"] != ix.ID {
		t.Errorf("job interaction_id = %q, want %q", payload["interaction_id"], ix.ID)
	}
}

func TestSaveInteraction_OptInDisabled(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h := NewOpenAIHandler(context.Background(), c, nil, saver, false, false) // disabled

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Confirm no save occurred — brief wait is acceptable for negative assertion.
	select {
	case <-saver.saveDone:
		t.Fatal("interaction was saved despite being disabled")
	case <-time.After(50 * time.Millisecond):
		// Expected: no save.
	}

	interactions := saver.getInteractions()
	if len(interactions) != 0 {
		t.Errorf("saved %d interactions, want 0 (disabled)", len(interactions))
	}
}

func TestSaveInteraction_NilSaver(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	h := NewOpenAIHandler(context.Background(), c, nil, nil, true, false) // enabled but no saver

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	// No panic means success — nil saver is safely handled.
}

func TestSaveInteraction_Streaming(t *testing.T) {
	// Multi-chunk streaming with model field — verifies delta reassembly.
	sseData := "data: {\"id\":\"gen-1\",\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"id\":\"gen-1\",\"model\":\"test-model\",\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: [DONE]\n\n"

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseData)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{saveDone: make(chan struct{}, 1)}
	h := NewOpenAIHandler(ctx, c, nil, saver, true, true)

	body := `{"model":"test","messages":[{"role":"user","content":"stream me"}],"stream":true}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Wait for the async save goroutine to complete.
	select {
	case <-saver.saveDone:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for interaction save")
	}

	interactions := saver.getInteractions()
	if len(interactions) != 1 {
		t.Fatalf("saved %d interactions, want 1", len(interactions))
	}

	ix := interactions[0]
	if ix.UserQuery != "stream me" {
		t.Errorf("UserQuery = %q, want %q", ix.UserQuery, "stream me")
	}

	// CloudResponse should be valid JSON (not raw SSE), with reassembled content.
	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal([]byte(ix.CloudResponse), &resp); err != nil {
		t.Fatalf("CloudResponse is not valid JSON: %v\nraw: %q", err, ix.CloudResponse)
	}
	if len(resp.Choices) == 0 {
		t.Fatal("CloudResponse has no choices")
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("reassembled content = %q, want %q", resp.Choices[0].Message.Content, "Hello world")
	}
}

func TestSaveInteraction_ErrorDoesNotBlockResponse(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{
		saveFn: func(_ storage.Interaction) error {
			return fmt.Errorf("database error")
		},
	}
	h := NewOpenAIHandler(ctx, c, nil, saver, true, true)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	h.ServeHTTP(rr, req)

	// Response should still succeed even if save fails.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	if rr.Body.String() != respJSON {
		t.Errorf("body = %q, want %q", rr.Body.String(), respJSON)
	}
}

func TestExtractLastUserMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages string
		want     string
	}{
		{
			name:     "single user message",
			messages: `[{"role":"user","content":"hello"}]`,
			want:     "hello",
		},
		{
			name:     "user then assistant then user",
			messages: `[{"role":"user","content":"first"},{"role":"assistant","content":"reply"},{"role":"user","content":"second"}]`,
			want:     "second",
		},
		{
			name:     "no user messages",
			messages: `[{"role":"system","content":"you are helpful"}]`,
			want:     "",
		},
		{
			name:     "invalid json",
			messages: `invalid`,
			want:     "",
		},
		{
			name:     "unparseable content falls through to earlier user message",
			messages: `[{"role":"user","content":"first"},{"role":"user","content":42}]`,
			want:     "first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractLastUserMessage(json.RawMessage(tt.messages))
			if got != tt.want {
				t.Errorf("extractLastUserMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveInteraction_QueuedImmediately(t *testing.T) {
	// Verify response arrives before save completes (non-blocking).
	saveCh := make(chan struct{})
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	saver := &mockInteractionSaver{
		saveFn: func(i storage.Interaction) error {
			// Block until test signals.
			<-saveCh
			return nil
		},
	}
	h := NewOpenAIHandler(ctx, c, nil, saver, true, true)

	body := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))

	// Handler should return immediately even though save blocks.
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
		// Good — response returned before save completed.
	case <-time.After(2 * time.Second):
		t.Fatal("handler blocked waiting for save to complete")
	}

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	// Unblock the saver.
	close(saveCh)
}
