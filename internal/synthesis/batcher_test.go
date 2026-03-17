package synthesis

import (
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/storage"
)

// makeDoc creates a ContextDoc with the given ID and content for testing.
func makeDoc(id, content string) storage.ContextDoc {
	return storage.ContextDoc{ID: id, Content: content}
}

// makeDocWords creates a ContextDoc whose content is approximately wordCount words.
func makeDocWords(id string, wordCount int) storage.ContextDoc {
	word := "word"
	words := make([]string, wordCount)
	for i := range words {
		words[i] = word
	}
	return makeDoc(id, strings.Join(words, " "))
}

func TestBatchDocuments_EmptyInput(t *testing.T) {
	b := NewBatcher(DefaultContextWindowTokens)
	batches := b.BatchDocuments(nil)
	if len(batches) != 0 {
		t.Errorf("empty input: got %d batches, want 0", len(batches))
	}

	batches = b.BatchDocuments([]storage.ContextDoc{})
	if len(batches) != 0 {
		t.Errorf("empty slice: got %d batches, want 0", len(batches))
	}
}

func TestBatchDocuments_FitsInOneBatch(t *testing.T) {
	// 5 docs of ~50 words each → ~75 tokens each → 375 total, well within effective limit
	docs := make([]storage.ContextDoc, 5)
	for i := range docs {
		docs[i] = makeDocWords(string(rune('a'+i)), 50)
	}

	b := NewBatcher(DefaultContextWindowTokens)
	batches := b.BatchDocuments(docs)

	if len(batches) != 1 {
		t.Errorf("got %d batches, want 1", len(batches))
	}
	if len(batches[0]) != 5 {
		t.Errorf("batch 0 has %d docs, want 5", len(batches[0]))
	}
}

func TestBatchDocuments_SplitsIntoBatches(t *testing.T) {
	// Use a tiny context window so batches are forced to split.
	// Each doc is ~100 words → ~150 tokens. Window of 500 → effective ~450.
	// Each batch fits 3 docs max (3*150=450). 20 docs → at least 7 batches.
	docs := make([]storage.ContextDoc, 20)
	for i := range docs {
		docs[i] = makeDocWords(string(rune('a'+i%26))+string(rune('0'+i%10)), 100)
	}

	b := NewBatcher(500)
	batches := b.BatchDocuments(docs)

	if len(batches) < 2 {
		t.Errorf("expected multiple batches, got %d", len(batches))
	}

	// Each batch must be within the effective token limit.
	effective := int(500 * (1.0 - safetyMarginPct))
	for i, batch := range batches {
		total := 0
		for _, doc := range batch {
			total += estimateDocTokens(doc)
		}
		if total > effective && len(batch) > 1 {
			t.Errorf("batch %d token count %d exceeds effective limit %d (multi-doc batch)", i, total, effective)
		}
	}

	// All docs must appear exactly once.
	seen := make(map[string]int)
	for _, batch := range batches {
		for _, doc := range batch {
			seen[doc.ID]++
		}
	}
	for _, doc := range docs {
		if seen[doc.ID] != 1 {
			t.Errorf("doc %s appeared %d times across batches, want 1", doc.ID, seen[doc.ID])
		}
	}
}

func TestBatchDocuments_LargeDocAlone(t *testing.T) {
	// One huge doc (5000 words → 7500 tokens) exceeds the effective limit of any
	// reasonable window — it should appear alone in its own batch.
	hugeDoc := makeDocWords("huge", 5000)
	normalDoc := makeDocWords("normal", 10)

	b := NewBatcher(DefaultContextWindowTokens)
	batches := b.BatchDocuments([]storage.ContextDoc{hugeDoc, normalDoc})

	// hugeDoc alone in batch 0; normalDoc in batch 1.
	if len(batches) != 2 {
		t.Fatalf("got %d batches, want 2", len(batches))
	}

	found := false
	for _, batch := range batches {
		for _, doc := range batch {
			if doc.ID == "huge" && len(batch) == 1 {
				found = true
			}
		}
	}
	if !found {
		t.Error("huge doc should be alone in its own batch")
	}
}

func TestBatchDocuments_TokenEstimation(t *testing.T) {
	tests := []struct {
		text      string
		wantWords int
	}{
		{"", 0},
		{"hello world", 2},
		{"one two three four five", 5},
	}

	for _, tt := range tests {
		got := EstimateTokens(tt.text)
		want := int(float64(tt.wantWords) * 1.5)
		if got != want {
			t.Errorf("EstimateTokens(%q) = %d, want %d", tt.text, got, want)
		}
	}
}
