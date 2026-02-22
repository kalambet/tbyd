package engine

import "context"

// Engine abstracts a local inference backend (Ollama, MLX, or any
// OpenAI-compatible server). Consumers such as intent extraction and
// embedding use this interface instead of depending on a concrete client.
type Engine interface {
	// Chat sends messages to the given model and returns the assistant's response.
	// When jsonSchema is non-nil, structured JSON output is requested.
	Chat(ctx context.Context, model string, messages []Message, jsonSchema *Schema) (string, error)

	// Embed returns the embedding vector for the given text using the specified model.
	Embed(ctx context.Context, model string, text string) ([]float32, error)

	// IsRunning reports whether the inference backend is reachable.
	IsRunning(ctx context.Context) bool

	// ListModels returns the names of all locally available models.
	ListModels(ctx context.Context) ([]string, error)

	// HasModel reports whether the given model name is available locally.
	HasModel(ctx context.Context, name string) bool

	// PullModel downloads a model. The optional callback receives progress updates.
	PullModel(ctx context.Context, name string, onProgress func(PullProgress)) error
}
