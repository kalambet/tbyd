package intent

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
)

const extractionTimeout = 3 * time.Second

// OllamaChatter is the interface for chat completion via Ollama.
type OllamaChatter interface {
	Chat(ctx context.Context, model string, messages []ollama.Message, jsonSchema *ollama.Schema) (string, error)
}

// Intent holds the structured extraction result from a user query.
type Intent struct {
	IntentType   string   `json:"intent_type"`
	Entities     []string `json:"entities"`
	Topics       []string `json:"topics"`
	ContextNeeds []string `json:"context_needs"`
	IsPrivate    bool     `json:"is_private"`
}

// Extractor uses a fast local LLM to extract structured intent from user queries.
type Extractor struct {
	client OllamaChatter
	model  string
}

// NewExtractor creates an Extractor using the given Ollama client and model name.
func NewExtractor(client OllamaChatter, model string) *Extractor {
	return &Extractor{client: client, model: model}
}

// Extract analyses the query and recent history, returning a structured Intent.
// On any failure (timeout, malformed JSON, Ollama error) it returns a zero-value
// Intent — the enrichment pipeline must not block on extraction failures.
func (e *Extractor) Extract(ctx context.Context, query string, recentHistory []ollama.Message, profileSummary string) Intent {
	if query == "" {
		return Intent{}
	}

	ctx, cancel := context.WithTimeout(ctx, extractionTimeout)
	defer cancel()

	messages := BuildPrompt(query, recentHistory, profileSummary)

	raw, err := e.client.Chat(ctx, e.model, messages, intentSchema())
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

// intentSchema returns the Ollama JSON schema for structured intent output.
func intentSchema() *ollama.Schema {
	return &ollama.Schema{
		Type: "object",
		Properties: map[string]ollama.SchemaProperty{
			"intent_type":   {Type: "string", Description: "One of: recall, task, question, preference_update"},
			"entities":      {Type: "array", Description: "Named entities mentioned in the query"},
			"topics":        {Type: "array", Description: "Semantic topic tags"},
			"context_needs": {Type: "array", Description: "What kind of context would help answer this query"},
			"is_private":    {Type: "boolean", Description: "Whether the user flagged this as sensitive"},
		},
		Required: []string{"intent_type", "entities", "topics", "context_needs", "is_private"},
	}
}
