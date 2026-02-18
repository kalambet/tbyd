package ollama

import (
	"context"
	"fmt"
	"io"
)

// EnsureReady checks that Ollama is running and required models are available.
// It pulls missing models automatically with progress output written to w.
// Returns a non-nil error if Ollama is unreachable.
func EnsureReady(ctx context.Context, c *Client, fastModel, embedModel string, w io.Writer) error {
	if !c.IsRunning(ctx) {
		return fmt.Errorf("Ollama is not running. Start it with: ollama serve")
	}

	for _, model := range []string{fastModel, embedModel} {
		if c.HasModel(ctx, model) {
			fmt.Fprintf(w, "model %s: ready\n", model)
			continue
		}

		fmt.Fprintf(w, "model %s: pulling...\n", model)
		err := c.PullModel(ctx, model, func(p pullProgress) {
			if p.Total > 0 {
				pct := float64(p.Completed) / float64(p.Total) * 100
				fmt.Fprintf(w, "  %s %.0f%%\n", p.Status, pct)
			} else {
				fmt.Fprintf(w, "  %s\n", p.Status)
			}
		})
		if err != nil {
			return fmt.Errorf("pulling model %s: %w", model, err)
		}
		fmt.Fprintf(w, "model %s: ready\n", model)
	}

	return nil
}
