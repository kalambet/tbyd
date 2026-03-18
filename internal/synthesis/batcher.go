package synthesis

import (
	"strings"

	"github.com/kalambet/tbyd/internal/storage"
)

// DefaultContextWindowTokens is the assumed context window size for the deep model.
const DefaultContextWindowTokens = 8192

// safetyMarginPct is the percentage of context window reserved as a safety margin.
// Set to 20% to account for system prompt overhead (~300 tokens) and the
// tendency of word-count-based estimation to undercount code and CJK text.
const safetyMarginPct = 0.20

// Batcher packs documents into batches that fit within a token budget.
type Batcher struct {
	maxTokens int // effective limit after safety margin
}

// NewBatcher creates a Batcher. contextWindowTokens is the raw context window
// size; the batcher reserves a 20% safety margin internally.
func NewBatcher(contextWindowTokens int) *Batcher {
	effective := int(float64(contextWindowTokens) * (1.0 - safetyMarginPct))
	if effective <= 0 {
		effective = 1
	}
	return &Batcher{maxTokens: effective}
}

// estimateTokensForText estimates the token count for a single text string
// using word count * 1.5. Intentionally rough — accurate tokenization
// requires the model's vocabulary, which is unavailable client-side.
func estimateTokensForText(text string) int {
	words := len(strings.Fields(text))
	return int(float64(words) * 1.5)
}

// perDocTemplateOverhead accounts for the fixed template text per document
// in the deep enrichment prompt (headers, labels, delimiters).
const perDocTemplateOverhead = 30

// EstimateTokens estimates the total token contribution of a text string.
// Exported for use in tests.
func EstimateTokens(text string) int {
	return estimateTokensForText(text)
}

// estimateDocTokens estimates the total token contribution of a ContextDoc
// including content, title, source, tags, and per-doc template overhead.
func estimateDocTokens(doc storage.ContextDoc) int {
	tokens := estimateTokensForText(doc.Content)
	tokens += estimateTokensForText(doc.Title)
	tokens += estimateTokensForText(doc.Source)
	tokens += estimateTokensForText(doc.Tags)
	tokens += perDocTemplateOverhead
	return tokens
}

// BatchDocuments packs docs into batches by estimated token count.
// A single document that exceeds the context window is placed alone in its
// own batch. Returns [][]storage.ContextDoc.
func (b *Batcher) BatchDocuments(docs []storage.ContextDoc) [][]storage.ContextDoc {
	if len(docs) == 0 {
		return nil
	}

	var batches [][]storage.ContextDoc
	var current []storage.ContextDoc
	currentTokens := 0

	for _, doc := range docs {
		tokens := estimateDocTokens(doc)

		// A single doc that overflows the window gets its own batch.
		if tokens >= b.maxTokens {
			if len(current) > 0 {
				batches = append(batches, current)
				current = nil
				currentTokens = 0
			}
			batches = append(batches, []storage.ContextDoc{doc})
			continue
		}

		// Adding this doc would overflow the current batch — flush first.
		if currentTokens+tokens > b.maxTokens && len(current) > 0 {
			batches = append(batches, current)
			current = nil
			currentTokens = 0
		}

		current = append(current, doc)
		currentTokens += tokens
	}

	if len(current) > 0 {
		batches = append(batches, current)
	}

	return batches
}
