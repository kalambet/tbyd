package engine

import (
	"context"

	"github.com/kalambet/tbyd/internal/ollama"
)

// OllamaEngine adapts the internal/ollama.Client to the Engine interface.
type OllamaEngine struct {
	client *ollama.Client
}

// NewOllamaEngine creates an OllamaEngine backed by an Ollama server at baseURL.
func NewOllamaEngine(baseURL string) *OllamaEngine {
	return &OllamaEngine{client: ollama.New(baseURL)}
}

func (e *OllamaEngine) Chat(ctx context.Context, model string, messages []Message, jsonSchema *Schema) (string, error) {
	msgs := toOllamaMessages(messages)
	s := toOllamaSchema(jsonSchema)
	return e.client.Chat(ctx, model, msgs, s)
}

func (e *OllamaEngine) ChatWithOptions(ctx context.Context, model string, messages []Message, jsonSchema *Schema, opts ChatOptions) (string, error) {
	msgs := toOllamaMessages(messages)
	s := toOllamaSchema(jsonSchema)

	ollamaOpts := make(map[string]any)
	if opts.Temperature != nil {
		ollamaOpts["temperature"] = *opts.Temperature
	}

	return e.client.ChatWithOptions(ctx, model, msgs, s, ollamaOpts)
}

func toOllamaMessages(messages []Message) []ollama.Message {
	msgs := make([]ollama.Message, len(messages))
	for i, m := range messages {
		msgs[i] = ollama.Message{Role: m.Role, Content: m.Content}
	}
	return msgs
}

func toOllamaSchema(jsonSchema *Schema) *ollama.Schema {
	if jsonSchema == nil {
		return nil
	}
	s := &ollama.Schema{
		Type:     jsonSchema.Type,
		Required: jsonSchema.Required,
	}
	if jsonSchema.Properties != nil {
		s.Properties = make(map[string]ollama.SchemaProperty, len(jsonSchema.Properties))
		for k, v := range jsonSchema.Properties {
			s.Properties[k] = ollama.SchemaProperty{Type: v.Type, Description: v.Description}
		}
	}
	return s
}

func (e *OllamaEngine) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	return e.client.Embed(ctx, model, text)
}

func (e *OllamaEngine) IsRunning(ctx context.Context) bool {
	return e.client.IsRunning(ctx)
}

func (e *OllamaEngine) ListModels(ctx context.Context) ([]string, error) {
	return e.client.ListModels(ctx)
}

func (e *OllamaEngine) HasModel(ctx context.Context, name string) bool {
	return e.client.HasModel(ctx, name)
}

func (e *OllamaEngine) PullModel(ctx context.Context, name string, onProgress func(PullProgress)) error {
	var cb func(ollama.PullProgress)
	if onProgress != nil {
		cb = func(p ollama.PullProgress) {
			onProgress(PullProgress{
				Status:    p.Status,
				Total:     p.Total,
				Completed: p.Completed,
			})
		}
	}
	return e.client.PullModel(ctx, name, cb)
}
