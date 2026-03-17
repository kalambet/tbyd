package synthesis

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/storage"
)

// deepMockChatter is a test double for OllamaChatter used in deep enrichment tests.
type deepMockChatter struct {
	response string
	err      error
	calls    atomic.Int64
}

func (m *deepMockChatter) Chat(_ context.Context, _ string, _ []ollama.Message, _ *ollama.Schema) (string, error) {
	m.calls.Add(1)
	if m.err != nil {
		return "", m.err
	}
	return m.response, nil
}

func newDeepEnricher(chatter OllamaChatter) *DeepEnricher {
	return NewDeepEnricher(chatter, "test-model")
}

func TestEnrichBatch_ParsesResponse(t *testing.T) {
	docs := []storage.ContextDoc{
		{ID: "doc-1", Title: "Go concurrency", Content: "Goroutines and channels"},
		{ID: "doc-2", Title: "Kubernetes basics", Content: "Pods and services"},
		{ID: "doc-3", Title: "Privacy law", Content: "GDPR overview"},
	}

	mockResp := `{
		"enrichments": [
			{
				"doc_id": "doc-1",
				"enriched_entities": ["Go", "goroutines"],
				"enriched_topics": ["concurrency", "programming"],
				"deep_key_points": ["Go uses goroutines for concurrency"],
				"cross_references": ["doc-2"],
				"domain_classification": "engineering",
				"relationship_notes": "Related to doc-2 as both are technical"
			},
			{
				"doc_id": "doc-2",
				"enriched_entities": ["Kubernetes"],
				"enriched_topics": ["orchestration", "devops"],
				"deep_key_points": ["Kubernetes manages containers"],
				"cross_references": [],
				"domain_classification": "engineering",
				"relationship_notes": ""
			},
			{
				"doc_id": "doc-3",
				"enriched_entities": ["GDPR"],
				"enriched_topics": ["privacy", "law"],
				"deep_key_points": ["GDPR governs data processing in the EU"],
				"cross_references": [],
				"domain_classification": "law",
				"relationship_notes": ""
			}
		]
	}`

	chatter := &deepMockChatter{response: mockResp}
	enricher := newDeepEnricher(chatter)

	results, err := enricher.EnrichBatch(context.Background(), docs)
	if err != nil {
		t.Fatalf("EnrichBatch() error = %v, want nil", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	if chatter.calls.Load() != 1 {
		t.Errorf("LLM called %d times, want 1", chatter.calls.Load())
	}

	// Find doc-1 result.
	byID := make(map[string]DeepEnrichment)
	for _, r := range results {
		byID[r.DocID] = r
	}

	r1, ok := byID["doc-1"]
	if !ok {
		t.Fatal("result for doc-1 not found")
	}
	if r1.DomainClassification != "engineering" {
		t.Errorf("doc-1 domain = %q, want %q", r1.DomainClassification, "engineering")
	}
	if len(r1.EnrichedEntities) == 0 {
		t.Error("doc-1 enriched_entities should not be empty")
	}
}

func TestEnrichBatch_CrossReferences(t *testing.T) {
	docs := []storage.ContextDoc{
		{ID: "doc-a", Content: "Machine learning fundamentals"},
		{ID: "doc-b", Content: "Deep learning neural networks"},
	}

	mockResp := `{
		"enrichments": [
			{
				"doc_id": "doc-a",
				"enriched_entities": [],
				"enriched_topics": ["ML"],
				"deep_key_points": [],
				"cross_references": ["doc-b"],
				"domain_classification": "science",
				"relationship_notes": "doc-b is a specialisation of doc-a"
			},
			{
				"doc_id": "doc-b",
				"enriched_entities": [],
				"enriched_topics": ["deep learning"],
				"deep_key_points": [],
				"cross_references": ["doc-a"],
				"domain_classification": "science",
				"relationship_notes": "extends concepts from doc-a"
			}
		]
	}`

	chatter := &deepMockChatter{response: mockResp}
	enricher := newDeepEnricher(chatter)

	results, err := enricher.EnrichBatch(context.Background(), docs)
	if err != nil {
		t.Fatalf("EnrichBatch() error = %v, want nil", err)
	}

	byID := make(map[string]DeepEnrichment)
	for _, r := range results {
		byID[r.DocID] = r
	}

	if len(byID["doc-a"].CrossReferences) == 0 {
		t.Error("doc-a should have cross-references to doc-b")
	}
	if byID["doc-a"].CrossReferences[0] != "doc-b" {
		t.Errorf("doc-a cross-reference = %q, want %q", byID["doc-a"].CrossReferences[0], "doc-b")
	}
}

func TestEnrichBatch_LLMFails(t *testing.T) {
	docs := []storage.ContextDoc{
		{ID: "doc-1", Content: "Some content"},
	}

	sentinel := errors.New("LLM unavailable")
	chatter := &deepMockChatter{err: sentinel}
	enricher := newDeepEnricher(chatter)

	results, err := enricher.EnrichBatch(context.Background(), docs)
	if err == nil {
		t.Fatal("EnrichBatch() expected error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want wrapping %v", err, sentinel)
	}
	if results != nil {
		t.Errorf("results = %v, want nil on error", results)
	}
}

func TestEnrichBatch_MalformedJSON(t *testing.T) {
	docs := []storage.ContextDoc{
		{ID: "doc-1", Content: "Some content"},
	}

	chatter := &deepMockChatter{response: `not valid json {{`}
	enricher := newDeepEnricher(chatter)

	_, err := enricher.EnrichBatch(context.Background(), docs)
	if err == nil {
		t.Fatal("EnrichBatch() expected error for malformed JSON, got nil")
	}
}

func TestEnrichBatch_EmptyDocs(t *testing.T) {
	chatter := &deepMockChatter{}
	enricher := newDeepEnricher(chatter)

	results, err := enricher.EnrichBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("EnrichBatch(nil) error = %v, want nil", err)
	}
	if results != nil {
		t.Errorf("EnrichBatch(nil) = %v, want nil", results)
	}
	if chatter.calls.Load() != 0 {
		t.Errorf("LLM called %d times for empty input, want 0", chatter.calls.Load())
	}
}
