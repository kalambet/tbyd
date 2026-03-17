package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/storage"
)

// mockDeepStore is a test double for DeepEnrichStore.
type mockDeepStore struct {
	mu            sync.Mutex
	pendingJobs   []storage.Job
	docs          map[string]storage.ContextDoc
	completedJobs []string
	failedJobs    []string
	updatedDocs   map[string]struct{ tags, deepMeta string }
	resetCount    int
	claimErr      error
}

func newMockDeepStore() *mockDeepStore {
	return &mockDeepStore{
		docs:        make(map[string]storage.ContextDoc),
		updatedDocs: make(map[string]struct{ tags, deepMeta string }),
	}
}

func (m *mockDeepStore) addJob(docID string) {
	payload, _ := json.Marshal(map[string]string{"context_doc_id": docID})
	m.pendingJobs = append(m.pendingJobs, storage.Job{
		ID:          "job-" + docID,
		Type:        deepEnrichJobType,
		PayloadJSON: string(payload),
		Status:      "running",
	})
}

func (m *mockDeepStore) addDoc(doc storage.ContextDoc) {
	m.docs[doc.ID] = doc
}

func (m *mockDeepStore) ClaimJobs(types []string, limit int) ([]storage.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.claimErr != nil {
		return nil, m.claimErr
	}
	if len(m.pendingJobs) == 0 {
		return nil, nil
	}
	n := limit
	if n > len(m.pendingJobs) {
		n = len(m.pendingJobs)
	}
	claimed := m.pendingJobs[:n]
	m.pendingJobs = m.pendingJobs[n:]
	return claimed, nil
}

func (m *mockDeepStore) CompleteJob(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.completedJobs = append(m.completedJobs, id)
	return nil
}

func (m *mockDeepStore) FailJob(id string, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failedJobs = append(m.failedJobs, id)
	return nil
}

func (m *mockDeepStore) GetContextDoc(id string) (storage.ContextDoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.docs[id]
	if !ok {
		return storage.ContextDoc{}, storage.ErrNotFound
	}
	return doc, nil
}

func (m *mockDeepStore) UpdateContextDocTagsAndDeepMetadata(id, tags, deepMetadataJSON string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updatedDocs[id] = struct{ tags, deepMeta string }{tags, deepMetadataJSON}
	return nil
}

func (m *mockDeepStore) ResetStaleJobs(_ []string, _ time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resetCount++
	return 0, nil
}

func (m *mockDeepStore) EnqueueJob(_ context.Context, _ storage.Job) error {
	return nil
}

// mockDeepEnricher builds a DeepEnricher backed by a fixed mock chatter.
func mockDeepEnricher(response string, err error) *DeepEnricher {
	return NewDeepEnricher(&deepMockChatter{response: response, err: err}, "test-model")
}

// successfulEnrichResponse returns a valid JSON enrichment response for the given doc IDs.
func successfulEnrichResponse(docIDs ...string) string {
	type enrichItem struct {
		DocID                string   `json:"doc_id"`
		EnrichedEntities     []string `json:"enriched_entities"`
		EnrichedTopics       []string `json:"enriched_topics"`
		DeepKeyPoints        []string `json:"deep_key_points"`
		CrossReferences      []string `json:"cross_references"`
		DomainClassification string   `json:"domain_classification"`
		RelationshipNotes    string   `json:"relationship_notes"`
	}
	items := make([]enrichItem, len(docIDs))
	for i, id := range docIDs {
		items[i] = enrichItem{
			DocID:                id,
			EnrichedTopics:       []string{"test-topic"},
			DomainClassification: "engineering",
			EnrichedEntities:     []string{},
			DeepKeyPoints:        []string{},
			CrossReferences:      []string{},
		}
	}
	type resp struct {
		Enrichments []enrichItem `json:"enrichments"`
	}
	b, _ := json.Marshal(resp{Enrichments: items})
	return string(b)
}

func buildWorker(store DeepEnrichStore, enricher *DeepEnricher) *DeepEnrichmentWorker {
	batcher := NewBatcher(DefaultContextWindowTokens)
	idle := NewIdleDetector(100, 0) // always idle in tests
	return NewDeepEnrichmentWorker(store, enricher, batcher, idle, 1000)
}

func TestRun_EmptyQueue(t *testing.T) {
	store := newMockDeepStore()
	enricher := mockDeepEnricher("", nil)
	worker := buildWorker(store, enricher)

	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() with empty queue error = %v, want nil", err)
	}
	if len(store.completedJobs) != 0 || len(store.failedJobs) != 0 {
		t.Errorf("expected no job completions/failures for empty queue, got %d/%d",
			len(store.completedJobs), len(store.failedJobs))
	}
}

func TestRun_ProcessesBatch(t *testing.T) {
	store := newMockDeepStore()
	docIDs := []string{"doc-1", "doc-2", "doc-3", "doc-4", "doc-5"}
	for _, id := range docIDs {
		store.addDoc(storage.ContextDoc{ID: id, Content: "Some content about " + id, Tags: "[]"})
		store.addJob(id)
	}

	enricher := mockDeepEnricher(successfulEnrichResponse(docIDs...), nil)
	worker := buildWorker(store, enricher)

	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	if len(store.completedJobs) != 5 {
		t.Errorf("completed %d jobs, want 5", len(store.completedJobs))
	}
	if len(store.failedJobs) != 0 {
		t.Errorf("failed %d jobs, want 0", len(store.failedJobs))
	}
	if len(store.updatedDocs) != 5 {
		t.Errorf("updated %d docs, want 5", len(store.updatedDocs))
	}
}

func TestRun_AdditiveUpdate(t *testing.T) {
	// Verify that pass-1 tags are preserved after deep enrichment.
	store := newMockDeepStore()
	store.addDoc(storage.ContextDoc{
		ID:      "doc-1",
		Content: "Content",
		Tags:    `["go","concurrency"]`,
	})
	store.addJob("doc-1")

	// Mock returns enriched topics including a new topic plus overlapping one.
	mockResp := `{
		"enrichments": [{
			"doc_id": "doc-1",
			"enriched_entities": [],
			"enriched_topics": ["concurrency", "goroutines"],
			"deep_key_points": [],
			"cross_references": [],
			"domain_classification": "engineering",
			"relationship_notes": ""
		}]
	}`
	enricher := mockDeepEnricher(mockResp, nil)
	worker := buildWorker(store, enricher)

	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v, want nil", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	updated, ok := store.updatedDocs["doc-1"]
	if !ok {
		t.Fatal("doc-1 was not updated")
	}

	var tags []string
	if err := json.Unmarshal([]byte(updated.tags), &tags); err != nil {
		t.Fatalf("failed to parse updated tags: %v", err)
	}

	tagSet := make(map[string]bool)
	for _, tag := range tags {
		tagSet[tag] = true
	}

	// Pass-1 tags must be preserved.
	for _, required := range []string{"go", "concurrency"} {
		if !tagSet[required] {
			t.Errorf("pass-1 tag %q missing from merged tags: %v", required, tags)
		}
	}
	// New enriched topic must be added.
	if !tagSet["goroutines"] {
		t.Errorf("enriched topic %q missing from merged tags: %v", "goroutines", tags)
	}
	// No duplicates.
	concurrencyCount := 0
	for _, tag := range tags {
		if tag == "concurrency" {
			concurrencyCount++
		}
	}
	if concurrencyCount != 1 {
		t.Errorf("tag %q appears %d times in merged tags, want 1", "concurrency", concurrencyCount)
	}
}

func TestDeepRun_ContextCancellation(t *testing.T) {
	store := newMockDeepStore()

	// Add several jobs.
	for i := 0; i < 3; i++ {
		id := string(rune('a' + i))
		store.addDoc(storage.ContextDoc{ID: id, Content: "content"})
		store.addJob(id)
	}

	// Chatter blocks until context is cancelled.
	blockCh := make(chan struct{})
	chatter := &blockingChatter{blockCh: blockCh}
	enricher := NewDeepEnricher(chatter, "test-model")
	worker := buildWorker(store, enricher)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- worker.Run(ctx)
	}()

	cancel()
	close(blockCh)

	select {
	case err := <-done:
		// Expect context.Canceled (or wrapping it), or nil if cancelled before LLM call.
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run() error = %v, want nil or context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run() did not exit after context cancellation within 3s")
	}
}
