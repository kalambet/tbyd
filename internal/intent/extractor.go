package intent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/profile"
)

const extractionTimeout = 3 * time.Second

// OllamaChatter is the interface for chat completion via Ollama.
type OllamaChatter interface {
	Chat(ctx context.Context, model string, messages []ollama.Message, jsonSchema *ollama.Schema) (string, error)
}

// Intent holds the structured extraction result from a user query.
type Intent struct {
	IntentType     string   `json:"intent_type"`
	Entities       []string `json:"entities"`
	Topics         []string `json:"topics"`
	ContextNeeds   []string `json:"context_needs"`
	IsPrivate      bool     `json:"is_private"`
	SearchStrategy string   `json:"search_strategy"`  // "vector_only", "hybrid", "keyword_heavy"; empty treated as "hybrid"
	HybridRatio    *float64 `json:"hybrid_ratio,omitempty"` // nil = use default; 0.0 = all keyword, 1.0 = all vector
	SuggestedTopK  int      `json:"suggested_top_k"`   // 0 = use default
}

// CalibrationProvider returns a fresh CalibrationContext on each call so the
// intent extractor always uses the latest profile expertise. Implementations
// must be safe for concurrent use.
type CalibrationProvider func() profile.CalibrationContext

// Extractor uses a fast local LLM to extract structured intent from user queries.
type Extractor struct {
	client              OllamaChatter
	model               string
	calibrationProvider CalibrationProvider
}

// NewExtractor creates an Extractor using the given Ollama client, model name,
// and calibration provider. The provider is called on every Extract invocation
// so that profile expertise changes are reflected without restarting the server.
func NewExtractor(client OllamaChatter, model string, calibrationProvider CalibrationProvider) *Extractor {
	return &Extractor{client: client, model: model, calibrationProvider: calibrationProvider}
}

// Extract analyses the query and recent history, returning a structured Intent.
// On any failure (timeout, malformed JSON, Ollama error) it returns a zero-value
// Intent — the enrichment pipeline must not block on extraction failures.
//
// calibration is optional: if non-zero it is used directly; otherwise the
// CalibrationProvider (if set) is called. This lets callers that already hold
// the profile avoid a redundant storage round-trip.
func (e *Extractor) Extract(ctx context.Context, query string, recentHistory []ollama.Message, profileSummary string, calibration profile.CalibrationContext) Intent {
	if query == "" {
		return Intent{}
	}

	ctx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()

	if calibration.Hints == "" && e.calibrationProvider != nil {
		calibration = e.calibrationProvider()
	}
	messages := BuildPrompt(query, recentHistory, profileSummary, calibration)

	raw, err := e.client.Chat(ctx, e.model, messages, &intentSchema)
	if err != nil {
		slog.Warn("intent extraction chat failed", "error", err)
		return Intent{}
	}

	var result Intent
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		slog.Warn("failed to unmarshal intent from LLM response", "error", err, "response", raw)
		return Intent{}
	}
	return result
}

// intentSchema is the static Ollama JSON schema for structured intent output.
var intentSchema = ollama.Schema{
	Type: "object",
	Properties: map[string]ollama.SchemaProperty{
		"intent_type":     {Type: "string", Description: "One of: recall, task, question, preference_update"},
		"entities":        {Type: "array", Description: "Named entities mentioned in the query"},
		"topics":          {Type: "array", Description: "Semantic topic tags"},
		"context_needs":   {Type: "array", Description: "What kind of context would help answer this query"},
		"is_private":      {Type: "boolean", Description: "Whether the user flagged this as sensitive"},
		"search_strategy": {Type: "string", Description: "One of: vector_only, hybrid, keyword_heavy"},
		"hybrid_ratio":    {Type: "number", Description: "0.0 = all keyword, 1.0 = all vector; default 0.7"},
		"suggested_top_k": {Type: "integer", Description: "Suggested number of results; 0 = use default"},
	},
	Required: []string{"intent_type", "entities", "topics", "context_needs", "is_private"},
}
