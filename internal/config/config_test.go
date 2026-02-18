package config

import (
	"os"
	"path/filepath"
	"testing"
)

// mockKeychain is a test double for the keychain interface.
type mockKeychain struct {
	value string
	err   error
}

func (m mockKeychain) Get(service, account string) (string, error) {
	return m.value, m.err
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDefaults verifies all default values are applied when loading an empty config file.
func TestDefaults(t *testing.T) {
	path := writeTempConfig(t, `[proxy]
openrouter_api_key = "test-key"
`)
	// Point the loader at our temp file by overriding the search paths.
	orig := os.Getenv("TBYD_OPENROUTER_API_KEY")
	os.Setenv("TBYD_OPENROUTER_API_KEY", "")
	defer os.Setenv("TBYD_OPENROUTER_API_KEY", orig)

	cfg, err := loadFromPath(path, mockKeychain{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 4000 {
		t.Errorf("Server.Port = %d, want 4000", cfg.Server.Port)
	}
	if cfg.Server.MCPPort != 4001 {
		t.Errorf("Server.MCPPort = %d, want 4001", cfg.Server.MCPPort)
	}
	if cfg.Ollama.BaseURL != "http://localhost:11434" {
		t.Errorf("Ollama.BaseURL = %q, want %q", cfg.Ollama.BaseURL, "http://localhost:11434")
	}
	if cfg.Ollama.FastModel != "phi3.5" {
		t.Errorf("Ollama.FastModel = %q, want %q", cfg.Ollama.FastModel, "phi3.5")
	}
	if cfg.Ollama.DeepModel != "mistral-nemo" {
		t.Errorf("Ollama.DeepModel = %q, want %q", cfg.Ollama.DeepModel, "mistral-nemo")
	}
	if cfg.Ollama.EmbedModel != "nomic-embed-text" {
		t.Errorf("Ollama.EmbedModel = %q, want %q", cfg.Ollama.EmbedModel, "nomic-embed-text")
	}
	if cfg.Proxy.DefaultModel != "anthropic/claude-opus-4" {
		t.Errorf("Proxy.DefaultModel = %q, want %q", cfg.Proxy.DefaultModel, "anthropic/claude-opus-4")
	}
}

// TestEnvOverride verifies that environment variables override config file values.
func TestEnvOverride(t *testing.T) {
	path := writeTempConfig(t, `[proxy]
openrouter_api_key = "file-key"
`)

	t.Setenv("TBYD_OPENROUTER_API_KEY", "env-key")

	cfg, err := loadFromPath(path, mockKeychain{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Proxy.OpenRouterAPIKey != "env-key" {
		t.Errorf("OpenRouterAPIKey = %q, want %q", cfg.Proxy.OpenRouterAPIKey, "env-key")
	}
}

// TestMissingRequiredField verifies a clear error when the API key is missing everywhere.
func TestMissingRequiredField(t *testing.T) {
	path := writeTempConfig(t, `# empty config`)

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	_, err := loadFromPath(path, mockKeychain{})
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}

	want := "missing required config"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error = %q, want it to contain %q", got, want)
	}
}

// TestTOMLParsing verifies that all fields are correctly read from a TOML file.
func TestTOMLParsing(t *testing.T) {
	content := `
[server]
port = 5000
mcp_port = 5001

[ollama]
base_url = "http://custom:11434"
fast_model = "custom-fast"
deep_model = "custom-deep"
embed_model = "custom-embed"

[storage]
data_dir = "/tmp/tbyd-test"

[proxy]
openrouter_api_key = "toml-key-123"
default_model = "openai/gpt-4o"
`
	path := writeTempConfig(t, content)

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	cfg, err := loadFromPath(path, mockKeychain{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 5000 {
		t.Errorf("Server.Port = %d, want 5000", cfg.Server.Port)
	}
	if cfg.Server.MCPPort != 5001 {
		t.Errorf("Server.MCPPort = %d, want 5001", cfg.Server.MCPPort)
	}
	if cfg.Ollama.BaseURL != "http://custom:11434" {
		t.Errorf("Ollama.BaseURL = %q", cfg.Ollama.BaseURL)
	}
	if cfg.Ollama.FastModel != "custom-fast" {
		t.Errorf("Ollama.FastModel = %q", cfg.Ollama.FastModel)
	}
	if cfg.Ollama.DeepModel != "custom-deep" {
		t.Errorf("Ollama.DeepModel = %q", cfg.Ollama.DeepModel)
	}
	if cfg.Ollama.EmbedModel != "custom-embed" {
		t.Errorf("Ollama.EmbedModel = %q", cfg.Ollama.EmbedModel)
	}
	if cfg.Storage.DataDir != "/tmp/tbyd-test" {
		t.Errorf("Storage.DataDir = %q", cfg.Storage.DataDir)
	}
	if cfg.Proxy.OpenRouterAPIKey != "toml-key-123" {
		t.Errorf("Proxy.OpenRouterAPIKey = %q", cfg.Proxy.OpenRouterAPIKey)
	}
	if cfg.Proxy.DefaultModel != "openai/gpt-4o" {
		t.Errorf("Proxy.DefaultModel = %q", cfg.Proxy.DefaultModel)
	}
}

// TestKeychainFallback verifies the Keychain is consulted when no API key is in file or env.
func TestKeychainFallback(t *testing.T) {
	path := writeTempConfig(t, `# no api key in file`)

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	kc := mockKeychain{value: "keychain-secret"}
	cfg, err := loadFromPath(path, kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Proxy.OpenRouterAPIKey != "keychain-secret" {
		t.Errorf("OpenRouterAPIKey = %q, want %q", cfg.Proxy.OpenRouterAPIKey, "keychain-secret")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
