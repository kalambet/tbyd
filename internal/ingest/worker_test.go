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
	if err := store.EnqueueJob(job); err != nil {
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
				if err := store.EnqueueJob(job); err != nil {
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
