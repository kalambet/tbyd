package engine

import (
	"context"

	"github.com/kalambet/tbyd/internal/ollama"
)

// ollamaChatAdapter adapts an Engine to the intent.OllamaChatter interface,
// converting between engine.Message/Schema and ollama.Message/Schema types.
type ollamaChatAdapter struct {
	eng Engine
}

// ChatAdapter returns an adapter that implements the intent.OllamaChatter
// interface using the given Engine.
func ChatAdapter(eng Engine) *ollamaChatAdapter {
	return &ollamaChatAdapter{eng: eng}
}

func (a *ollamaChatAdapter) Chat(ctx context.Context, model string, messages []ollama.Message, jsonSchema *ollama.Schema) (string, error) {
	engineMsgs := make([]Message, len(messages))
	for i, m := range messages {
		engineMsgs[i] = Message{Role: m.Role, Content: m.Content}
	}

	var engineSchema *Schema
	if jsonSchema != nil {
		props := make(map[string]SchemaProperty, len(jsonSchema.Properties))
		for k, v := range jsonSchema.Properties {
			props[k] = SchemaProperty{Type: v.Type, Description: v.Description}
		}
		engineSchema = &Schema{
			Type:       jsonSchema.Type,
			Properties: props,
			Required:   jsonSchema.Required,
		}
	}

	return a.eng.Chat(ctx, model, engineMsgs, engineSchema)
}
