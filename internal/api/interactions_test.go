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
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, true, nil)

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
	h, _ := NewOpenAIHandler(context.Background(), c, nil, saver, false, false, nil) // disabled

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

	h, _ := NewOpenAIHandler(context.Background(), c, nil, nil, true, false, nil) // enabled but no saver

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
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, true, nil)

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
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, true, nil)

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
	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, true, nil)

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

// TestSaveInteraction_ChannelFull verifies that when the interaction save channel
// is full the handler still returns a successful response (non-blocking) and the
// droppedInteractions counter is incremented.
func TestSaveInteraction_ChannelFull(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Use a saver whose SaveInteraction blocks until we unblock it, so the
	// single consumer goroutine is occupied and the channel fills up.
	blocked := make(chan struct{})
	saver := &mockInteractionSaver{
		saveDone: make(chan struct{}, 1),
		saveFn: func(_ storage.Interaction) error {
			<-blocked
			return nil
		},
	}

	h, _ := NewOpenAIHandler(ctx, c, nil, saver, true, false, nil)

	reqBody := `{"model":"test","messages":[{"role":"user","content":"hi"}]}`

	// Send enough requests to fill the channel (capacity 64) plus one more.
	// We only need 1 to be in-flight (consumer blocked) and then send enough
	// to fill the buffer before adding one that must drop.
	//
	// Simpler approach: use a zero-capacity channel by directly testing the
	// drop path via the health endpoint after a single blocked request
	// consumes the goroutine and all 64 buffer slots are claimed.
	//
	// Even simpler: just send 66 requests (1 in consumer + 64 buffered + 1 drop).
	const total = 66
	for i := 0; i < total; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want %d", i+1, rr.Code, http.StatusOK)
		}
	}

	// Verify at least one interaction was dropped.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("health: status = %d, want %d", rr.Code, http.StatusOK)
	}
	var body map[string]any
	json.NewDecoder(rr.Body).Decode(&body)
	dropped, _ := body["dropped_interactions"].(float64)
	if dropped == 0 {
		t.Error("dropped_interactions = 0, want > 0 after channel-full drop")
	}

	// Unblock the saver to allow the save loop to drain and exit cleanly.
	close(blocked)
}

// mockOnboardingConfig implements OnboardingConfig for tests.
type mockOnboardingConfig struct {
	saveInteractionsSet bool
	onboardingShown     bool
	markShownCalled     int
}

func (m *mockOnboardingConfig) SaveInteractionsExplicitlySet() (bool, error) {
	return m.saveInteractionsSet, nil
}

func (m *mockOnboardingConfig) OnboardingShown() bool {
	return m.onboardingShown
}

func (m *mockOnboardingConfig) MarkOnboardingShown() error {
	m.markShownCalled++
	return nil
}

// TestOnboardingPrompt_ShownOnce verifies that when onboarding_shown=false and
// save_interactions has never been explicitly set, the prompt is printed exactly
// once even across multiple requests.
func TestOnboardingPrompt_ShownOnce(t *testing.T) {
	respJSON := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}]}`

	_, c := mockUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respJSON)
	})

	cfg := &mockOnboardingConfig{
		saveInteractionsSet: false,
		onboardingShown:     false,
	}
	notifier := NewOnboardingNotifier(cfg)

	var buf strings.Builder
	// Override Notify target by wrapping: the notifier uses os.Stderr normally,
	// but in tests we call Notify directly to capture output.
	//
	// We test via the notifier directly (not through the handler) to avoid
	// needing os.Stderr capture, which is fragile. The handler wires os.Stderr;
	// the unit under test is OnboardingNotifier.Notify.
	notifier.Notify(&buf)
	notifier.Notify(&buf) // second call — must be a no-op

	got := buf.String()
	if got != onboardingMessage {
		t.Errorf("output = %q, want %q", got, onboardingMessage)
	}

	if cfg.markShownCalled != 1 {
		t.Errorf("MarkOnboardingShown called %d times, want 1", cfg.markShownCalled)
	}

	// Also verify that NewOpenAIHandler itself calls Notify exactly once at
	// construction time (not per-request). Use a fresh notifier so the
	// sync.Once has not been consumed by the direct Notify calls above.
	freshCfg := &mockOnboardingConfig{
		saveInteractionsSet: false,
		onboardingShown:     false,
	}
	freshNotifier := NewOnboardingNotifier(freshCfg)
	_, _ = NewOpenAIHandler(context.Background(), c, nil, nil, false, false, freshNotifier)

	if freshCfg.markShownCalled != 1 {
		t.Errorf("after NewOpenAIHandler: MarkOnboardingShown called %d times, want 1", freshCfg.markShownCalled)
	}
}

// TestOnboardingPrompt_SuppressedAfterRestart verifies that a fresh
// OnboardingNotifier (simulating a server restart) does not print the prompt
// when onboarding_shown was already persisted as true in a prior run.
func TestOnboardingPrompt_SuppressedAfterRestart(t *testing.T) {
	cfg := &mockOnboardingConfig{
		saveInteractionsSet: false,
		onboardingShown:     true, // persisted from a prior run
	}
	notifier := NewOnboardingNotifier(cfg)

	var buf strings.Builder
	notifier.Notify(&buf)

	if buf.Len() != 0 {
		t.Errorf("output = %q, want empty (onboarding already shown in prior run)", buf.String())
	}
	if cfg.markShownCalled != 0 {
		t.Errorf("MarkOnboardingShown called %d times, want 0", cfg.markShownCalled)
	}
}

// TestOnboardingPrompt_NotShownWhenConfigured verifies that when
// save_interactions has been explicitly set, the onboarding prompt is never
// printed.
func TestOnboardingPrompt_NotShownWhenConfigured(t *testing.T) {
	cfg := &mockOnboardingConfig{
		saveInteractionsSet: true, // explicitly configured
		onboardingShown:     false,
	}
	notifier := NewOnboardingNotifier(cfg)

	var buf strings.Builder
	notifier.Notify(&buf)

	if buf.Len() != 0 {
		t.Errorf("output = %q, want empty (save_interactions is configured)", buf.String())
	}

	if cfg.markShownCalled != 0 {
		t.Errorf("MarkOnboardingShown called %d times, want 0", cfg.markShownCalled)
	}
}
