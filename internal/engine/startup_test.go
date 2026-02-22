package engine

import (
	"context"
	"fmt"
	"io"
	"testing"
)

type mockEngine struct {
	isRunning bool
	models    map[string]bool
	pulled    []string
}

func (m *mockEngine) Chat(_ context.Context, _ string, _ []Message, _ *Schema) (string, error) {
	return "", nil
}
func (m *mockEngine) Embed(_ context.Context, _ string, _ string) ([]float32, error) {
	return nil, nil
}
func (m *mockEngine) IsRunning(_ context.Context) bool { return m.isRunning }
func (m *mockEngine) ListModels(_ context.Context) ([]string, error) {
	var names []string
	for n := range m.models {
		names = append(names, n)
	}
	return names, nil
}
func (m *mockEngine) HasModel(_ context.Context, name string) bool { return m.models[name] }
func (m *mockEngine) PullModel(_ context.Context, name string, cb func(PullProgress)) error {
	m.pulled = append(m.pulled, name)
	if cb != nil {
		cb(PullProgress{Status: "success"})
	}
	return nil
}

func TestEnsureReady_AllModelsPresent(t *testing.T) {
	m := &mockEngine{
		isRunning: true,
		models:    map[string]bool{"phi3.5": true, "nomic-embed-text": true},
	}
	err := EnsureReady(context.Background(), m, "phi3.5", "nomic-embed-text", io.Discard)
	if err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	if len(m.pulled) != 0 {
		t.Errorf("expected no pulls, got %v", m.pulled)
	}
}

func TestEnsureReady_PullsMissing(t *testing.T) {
	m := &mockEngine{
		isRunning: true,
		models:    map[string]bool{"phi3.5": true},
	}
	err := EnsureReady(context.Background(), m, "phi3.5", "nomic-embed-text", io.Discard)
	if err != nil {
		t.Fatalf("EnsureReady: %v", err)
	}
	if len(m.pulled) != 1 || m.pulled[0] != "nomic-embed-text" {
		t.Errorf("expected pull of nomic-embed-text, got %v", m.pulled)
	}
}

func TestEnsureReady_EngineDown(t *testing.T) {
	m := &mockEngine{isRunning: false, models: map[string]bool{}}
	err := EnsureReady(context.Background(), m, "phi3.5", "nomic-embed-text", io.Discard)
	if err == nil {
		t.Fatal("expected error when engine is down")
	}
	fmt.Println(err)
}
