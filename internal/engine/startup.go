package engine

import (
	"context"
	"fmt"
	"io"
)

// EnsureReady checks that the Engine is reachable and required models are
// available. Missing models are pulled automatically with progress output
// written to w.
func EnsureReady(ctx context.Context, e Engine, fastModel, embedModel string, w io.Writer) error {
	if !e.IsRunning(ctx) {
		return fmt.Errorf("local inference engine is not running; please ensure the backend is started")
	}

	for _, model := range []string{fastModel, embedModel} {
		if e.HasModel(ctx, model) {
			fmt.Fprintf(w, "model %s: ready\n", model)
			continue
		}

		fmt.Fprintf(w, "model %s: pulling...\n", model)
		err := e.PullModel(ctx, model, func(p PullProgress) {
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
