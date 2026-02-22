package engine

import "testing"

func TestDetect_ReturnsOllama(t *testing.T) {
	e, err := Detect(DetectConfig{OllamaBaseURL: "http://localhost:11434"})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if _, ok := e.(*OllamaEngine); !ok {
		t.Errorf("Detect returned %T, want *OllamaEngine", e)
	}
}
