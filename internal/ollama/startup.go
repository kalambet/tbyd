package ollama

import (
	"context"
	"fmt"
	"io"
	"time"
)

// EnsureReady checks that Ollama is running and required models are available.
// It pulls missing models automatically with progress output written to w.
// After all models are available, it warms up the fast model so subsequent
// chat calls don't pay the cold-load penalty.
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

	// Warm up the fast model by sending a trivial chat request so it stays
	// loaded in memory for low-latency intent extraction.
	fmt.Fprintf(w, "model %s: warming up...\n", fastModel)
	warmCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := c.Chat(warmCtx, fastModel, []Message{
		{Role: "user", Content: "ping"},
	}, nil)
	if err != nil {
		fmt.Fprintf(w, "model %s: warm-up failed (non-fatal): %v\n", fastModel, err)
	} else {
		fmt.Fprintf(w, "model %s: warm\n", fastModel)
	}

	return nil
}
