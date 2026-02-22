//go:build integration

package intent

import (
	"context"
	"testing"
	"time"

	"github.com/kalambet/tbyd/internal/ollama"
)

func TestExtract_RealOllama(t *testing.T) {
	client := ollama.New("http://localhost:11434")
	if !client.IsRunning(context.Background()) {
		t.Skip("Ollama is not running, skipping integration test")
	}
	if !client.HasModel(context.Background(), "phi3.5") {
		t.Skip("phi3.5 model not available, skipping integration test")
	}

	e := NewExtractor(client, "phi3.5")

	start := time.Now()
	intent := e.Extract(context.Background(), "what did I decide about the database schema last week", nil, "")
	elapsed := time.Since(start)

	if elapsed > 3*time.Second {
		t.Errorf("extraction took %v, want < 3s", elapsed)
	}

	if intent.IntentType == "" {
		t.Error("IntentType is empty, expected a non-empty value")
	}

	t.Logf("intent: %+v (took %v)", intent, elapsed)
}
