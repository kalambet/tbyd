package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

type mockEmbedder struct {
	embedFn func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	return m.embedFn(ctx, text)
}

type mockVectorInserter struct {
	mu       sync.Mutex
	inserted []retrieval.Record
	insertFn func(table string, records []retrieval.Record) error
}

func (m *mockVectorInserter) Insert(table string, records []retrieval.Record) error {
	if m.insertFn != nil {
		return m.insertFn(table, records)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inserted = append(m.inserted, records...)
	return nil
}

func openTestStore(t *testing.T) *storage.Store {
	t.Helper()
	s, err := storage.Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func enqueueTestJob(t *testing.T, store *storage.Store, docID, content string) {
	t.Helper()
	doc := storage.ContextDoc{
		ID:        docID,
		Title:     "Test Doc",
		Content:   content,
		Source:    "test",
		Tags:      `["test"]`,
		CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveContextDoc(doc); err != nil {
		t.Fatalf("SaveContextDoc: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"context_doc_id": docID})
	job := storage.Job{
		ID:          "job-" + docID,
		Type:        "ingest_enrich",
		PayloadJSON: string(payload),
	}
	if err := store.EnqueueJob(context.Background(),job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
}

// resetRunAfter sets run_after to now so the job is immediately claimable after FailJob backoff.
func resetRunAfter(t *testing.T, store *storage.Store, jobID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := store.DB().Exec(`UPDATE jobs SET run_after = ? WHERE id = ?`, now, jobID)
	if err != nil {
		t.Fatalf("resetRunAfter: %v", err)
	}
}

func TestWorker_ProcessesJob(t *testing.T) {
	store := openTestStore(t)
	enqueueTestJob(t, store, "doc-1", "Hello world")

	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}, inserter, 0)

	ctx := context.Background()
	didWork, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce returned false, expected true")
	}

	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	if len(inserter.inserted) != 1 {
		t.Fatalf("inserted %d records, want 1", len(inserter.inserted))
	}
	rec := inserter.inserted[0]
	if rec.SourceID != "doc-1" {
		t.Errorf("SourceID = %q, want %q", rec.SourceID, "doc-1")
	}
	if rec.SourceType != "context_doc" {
		t.Errorf("SourceType = %q, want %q", rec.SourceType, "context_doc")
	}

	doc, err := store.GetContextDoc("doc-1")
	if err != nil {
		t.Fatalf("GetContextDoc: %v", err)
	}
	if doc.VectorID == "" {
		t.Error("VectorID is empty after processing")
	}
}

func TestWorker_RetryOnFailure(t *testing.T) {
	store := openTestStore(t)
	enqueueTestJob(t, store, "doc-r", "retry content")

	var calls atomic.Int32
	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			n := calls.Add(1)
			if n <= 2 {
				return nil, fmt.Errorf("transient error %d", n)
			}
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}, inserter, 0)

	ctx := context.Background()

	// 1st attempt — fails
	didWork, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 1 error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce 1 returned false")
	}

	// Verify attempts=1, status=pending (retryable)
	var status1 string
	var attempts1 int
	if err := store.DB().QueryRow(`SELECT status, attempts FROM jobs WHERE id = 'job-doc-r'`).Scan(&status1, &attempts1); err != nil {
		t.Fatalf("query after 1st fail: %v", err)
	}
	if status1 != "pending" || attempts1 != 1 {
		t.Errorf("after 1st fail: status=%q attempts=%d, want pending/1", status1, attempts1)
	}

	// Reset backoff so job is claimable
	resetRunAfter(t, store, "job-doc-r")

	// 2nd attempt — fails
	didWork, err = w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 2 error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce 2 returned false")
	}

	var attempts2 int
	if err := store.DB().QueryRow(`SELECT attempts FROM jobs WHERE id = 'job-doc-r'`).Scan(&attempts2); err != nil {
		t.Fatalf("query after 2nd fail: %v", err)
	}
	if attempts2 != 2 {
		t.Errorf("after 2nd fail: attempts=%d, want 2", attempts2)
	}

	resetRunAfter(t, store, "job-doc-r")

	// 3rd attempt — succeeds
	didWork, err = w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 3 error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce 3 returned false")
	}

	var status3 string
	if err := store.DB().QueryRow(`SELECT status FROM jobs WHERE id = 'job-doc-r'`).Scan(&status3); err != nil {
		t.Fatalf("query after 3rd attempt: %v", err)
	}
	if status3 != "completed" {
		t.Errorf("after 3rd attempt: status=%q, want completed", status3)
	}
}

func TestWorker_MaxRetriesExceeded(t *testing.T) {
	store := openTestStore(t)
	enqueueTestJob(t, store, "doc-m", "max retry content")

	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return nil, fmt.Errorf("permanent error")
		},
	}, inserter, 0)

	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		didWork, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce %d error: %v", i, err)
		}
		if !didWork {
			t.Fatalf("RunOnce %d returned false", i)
		}
		if i < 3 {
			resetRunAfter(t, store, "job-doc-m")
		}
	}

	var status string
	if err := store.DB().QueryRow(`SELECT status FROM jobs WHERE id = 'job-doc-m'`).Scan(&status); err != nil {
		t.Fatalf("query final status: %v", err)
	}
	if status != "failed" {
		t.Errorf("final status = %q, want %q", status, "failed")
	}
}

func TestWorker_ConcurrentEnqueue(t *testing.T) {
	store := openTestStore(t)

	const goroutines = 5
	const jobsPerGoroutine = 10
	const total = goroutines * jobsPerGoroutine

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < jobsPerGoroutine; j++ {
				docID := fmt.Sprintf("doc-%d-%d", g, j)
				doc := storage.ContextDoc{
					ID:        docID,
					Title:     "Test Doc",
					Content:   fmt.Sprintf("content %d-%d", g, j),
					Source:    "test",
					Tags:      `["test"]`,
					CreatedAt: time.Now().UTC(),
				}
				if err := store.SaveContextDoc(doc); err != nil {
					t.Errorf("SaveContextDoc %s: %v", docID, err)
					return
				}
				payload, _ := json.Marshal(map[string]string{"context_doc_id": docID})
				job := storage.Job{
					ID:          "job-" + docID,
					Type:        "ingest_enrich",
					PayloadJSON: string(payload),
				}
				if err := store.EnqueueJob(context.Background(),job); err != nil {
					t.Errorf("EnqueueJob %s: %v", docID, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1, 0.2, 0.3}, nil
		},
	}, inserter, 0)

	ctx := context.Background()
	deadline := time.After(5 * time.Second)
	processed := 0
	for processed < total {
		select {
		case <-deadline:
			t.Fatalf("timed out after processing %d/%d jobs", processed, total)
		default:
		}
		didWork, err := w.RunOnce(ctx)
		if err != nil {
			t.Fatalf("RunOnce error at job %d: %v", processed, err)
		}
		if didWork {
			processed++
		}
	}

	if processed != total {
		t.Errorf("processed %d jobs, want %d", processed, total)
	}

	for g := 0; g < goroutines; g++ {
		for j := 0; j < jobsPerGoroutine; j++ {
			docID := fmt.Sprintf("doc-%d-%d", g, j)
			doc, err := store.GetContextDoc(docID)
			if err != nil {
				t.Errorf("GetContextDoc %s: %v", docID, err)
				continue
			}
			if doc.VectorID == "" {
				t.Errorf("doc %s has empty VectorID", docID)
			}
		}
	}
}

// --- interaction_summarize tests ---

type mockSummarizer struct {
	summarizeFn func(ctx context.Context, userQuery, cloudResponse string) (string, error)
}

func (m *mockSummarizer) Summarize(ctx context.Context, userQuery, cloudResponse string) (string, error) {
	return m.summarizeFn(ctx, userQuery, cloudResponse)
}

func enqueueSummarizeJob(t *testing.T, store *storage.Store, interactionID, userQuery, cloudResponse string) {
	t.Helper()
	interaction := storage.Interaction{
		ID:            interactionID,
		CreatedAt:     time.Now().UTC(),
		UserQuery:     userQuery,
		CloudModel:    "test-model",
		CloudResponse: cloudResponse,
		Status:        "completed",
		VectorIDs:     "[]",
	}
	if err := store.SaveInteraction(context.Background(),interaction); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}
	payload, _ := json.Marshal(map[string]string{"interaction_id": interactionID})
	job := storage.Job{
		ID:          "job-" + interactionID,
		Type:        "interaction_summarize",
		PayloadJSON: string(payload),
	}
	if err := store.EnqueueJob(context.Background(),job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
}

func TestWorker_SummarizeJob(t *testing.T) {
	store := openTestStore(t)
	cloudResp := `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Go is great for backends."}}]}`
	enqueueSummarizeJob(t, store, "ix-1", "What language for backends?", cloudResp)

	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, text string) ([]float32, error) {
			return []float32{0.4, 0.5, 0.6}, nil
		},
	}, inserter, 0)

	w.SetSummarizer(&mockSummarizer{
		summarizeFn: func(_ context.Context, query, resp string) (string, error) {
			if query != "What language for backends?" {
				t.Errorf("summarizer got query = %q", query)
			}
			if resp != "Go is great for backends." {
				t.Errorf("summarizer got response = %q", resp)
			}
			return "[2026-03-09] User asked about backend languages. Response: Go recommended.", nil
		},
	})

	ctx := context.Background()
	didWork, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce returned false, expected true")
	}

	// Verify vector was inserted with source_type="interaction".
	inserter.mu.Lock()
	defer inserter.mu.Unlock()
	if len(inserter.inserted) != 1 {
		t.Fatalf("inserted %d records, want 1", len(inserter.inserted))
	}
	rec := inserter.inserted[0]
	if rec.SourceID != "ix-1" {
		t.Errorf("SourceID = %q, want %q", rec.SourceID, "ix-1")
	}
	if rec.SourceType != "interaction" {
		t.Errorf("SourceType = %q, want %q", rec.SourceType, "interaction")
	}
	if rec.TextChunk == "" {
		t.Error("TextChunk is empty")
	}

	// Verify interaction vector_ids were updated.
	ix, err := store.GetInteraction("ix-1")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}
	var vectorIDs []string
	if err := json.Unmarshal([]byte(ix.VectorIDs), &vectorIDs); err != nil {
		t.Fatalf("parsing vector_ids: %v", err)
	}
	if len(vectorIDs) != 1 {
		t.Errorf("vector_ids has %d entries, want 1", len(vectorIDs))
	}
	if len(vectorIDs) > 0 && vectorIDs[0] != rec.ID {
		t.Errorf("vector_ids[0] = %q, want %q", vectorIDs[0], rec.ID)
	}
}

func TestWorker_SummarizeJob_NoSummarizer(t *testing.T) {
	store := openTestStore(t)
	enqueueSummarizeJob(t, store, "ix-2", "hello", `{"choices":[{"message":{"content":"hi"}}]}`)

	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1}, nil
		},
	}, inserter, 0)
	// No summarizer set.

	ctx := context.Background()
	didWork, err := w.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce error: %v", err)
	}
	if !didWork {
		t.Fatal("RunOnce returned false")
	}

	// Job should be pending (failed and queued for retry) rather than
	// silently completed, so it can be reprocessed once a summarizer is
	// configured.
	var status string
	if err := store.DB().QueryRow(`SELECT status FROM jobs WHERE id = 'job-ix-2'`).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "pending" {
		t.Errorf("status = %q, want pending (retry when summarizer available)", status)
	}
}

func TestWorker_SummarizeJob_RetryOnFailure(t *testing.T) {
	store := openTestStore(t)
	enqueueSummarizeJob(t, store, "ix-3", "hello", `{"choices":[{"message":{"content":"world"}}]}`)

	var calls atomic.Int32
	inserter := &mockVectorInserter{}
	w := NewWorker(store, &mockEmbedder{
		embedFn: func(_ context.Context, _ string) ([]float32, error) {
			return []float32{0.1, 0.2}, nil
		},
	}, inserter, 0)

	w.SetSummarizer(&mockSummarizer{
		summarizeFn: func(_ context.Context, _, _ string) (string, error) {
			n := calls.Add(1)
			if n == 1 {
				return "", fmt.Errorf("model timeout")
			}
			return "summary text", nil
		},
	})

	ctx := context.Background()

	// 1st attempt — fails.
	didWork, err := w.RunOnce(ctx)
	if err != nil || !didWork {
		t.Fatalf("RunOnce 1: didWork=%v, err=%v", didWork, err)
	}

	resetRunAfter(t, store, "job-ix-3")

	// 2nd attempt — succeeds.
	didWork, err = w.RunOnce(ctx)
	if err != nil || !didWork {
		t.Fatalf("RunOnce 2: didWork=%v, err=%v", didWork, err)
	}

	var status string
	if err := store.DB().QueryRow(`SELECT status FROM jobs WHERE id = 'job-ix-3'`).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
}

func TestExtractAssistantContent(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "valid openai response",
			input: `{"id":"gen-1","choices":[{"message":{"role":"assistant","content":"Hello world"}}]}`,
			want:  "Hello world",
		},
		{
			name:  "empty choices",
			input: `{"id":"gen-1","choices":[]}`,
			want:  `{"id":"gen-1","choices":[]}`,
		},
		{
			name:  "invalid json",
			input: `not json`,
			want:  `not json`,
		},
		{
			name:  "streaming sse data",
			input: "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
			want:  "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractAssistantContent(tt.input)
			if got != tt.want {
				t.Errorf("extractAssistantContent() = %q, want %q", got, tt.want)
			}
		})
	}
}
