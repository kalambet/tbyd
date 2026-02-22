package engine

import (
	"context"
	"fmt"
)

// MLXEngine is a placeholder for future MLX-based local inference.
//
// MLX integration path:
//   - Implement Engine methods by calling an OpenAI-compatible HTTP server
//     (e.g., mlx-lm or oMLX running on a configurable port).
//   - Update Detect() to probe for the MLX server and return MLXEngine when available.
type MLXEngine struct {
	baseURL string
}

func (e *MLXEngine) Chat(_ context.Context, _ string, _ []Message, _ *Schema) (string, error) {
	return "", fmt.Errorf("mlx engine not yet implemented")
}

func (e *MLXEngine) Embed(_ context.Context, _ string, _ string) ([]float32, error) {
	return nil, fmt.Errorf("mlx engine not yet implemented")
}

func (e *MLXEngine) IsRunning(_ context.Context) bool {
	return false
}

func (e *MLXEngine) ListModels(_ context.Context) ([]string, error) {
	return nil, fmt.Errorf("mlx engine not yet implemented")
}

func (e *MLXEngine) HasModel(_ context.Context, _ string) bool {
	return false
}

func (e *MLXEngine) PullModel(_ context.Context, _ string, _ func(PullProgress)) error {
	return fmt.Errorf("mlx engine not yet implemented")
}
