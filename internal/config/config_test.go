package config

import (
	"testing"
)

// mockKeychain is a test double for the keychain interface.
type mockKeychain struct {
	store map[string]string // key = "service/account"
	value string            // legacy: returned by Get when store is nil
	err   error

	setCalled bool
}

func newMockKeychain() *mockKeychain {
	return &mockKeychain{store: make(map[string]string)}
}

func (m *mockKeychain) Get(service, account string) (string, error) {
	if m.store != nil {
		v, ok := m.store[service+"/"+account]
		if !ok {
			return "", m.err
		}
		return v, nil
	}
	return m.value, m.err
}

func (m *mockKeychain) Set(service, account, value string) error {
	m.setCalled = true
	if m.store != nil {
		m.store[service+"/"+account] = value
	}
	return nil
}

// mockBackend is an in-memory ConfigBackend for testing.
type mockBackend struct {
	strings map[string]string
	ints    map[string]int
}

func newMockBackend() *mockBackend {
	return &mockBackend{
		strings: make(map[string]string),
		ints:    make(map[string]int),
	}
}

func (m *mockBackend) GetString(key string) (string, bool, error) {
	v, ok := m.strings[key]
	return v, ok, nil
}

func (m *mockBackend) GetInt(key string) (int, bool, error) {
	v, ok := m.ints[key]
	return v, ok, nil
}

func (m *mockBackend) SetString(key, val string) error {
	m.strings[key] = val
	return nil
}

func (m *mockBackend) SetInt(key string, val int) error {
	m.ints[key] = val
	return nil
}

func (m *mockBackend) Delete(key string) error {
	delete(m.strings, key)
	delete(m.ints, key)
	return nil
}

// TestDefaults verifies all default values are applied when the backend is empty.
func TestDefaults(t *testing.T) {
	b := newMockBackend()
	kc := &mockKeychain{value: "test-key"}

	cfg, err := loadWith(b, kc)
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

// TestBackendOverride verifies that backend values override defaults.
func TestBackendOverride(t *testing.T) {
	b := newMockBackend()
	b.ints["server.port"] = 5000
	b.ints["server.mcp_port"] = 5001
	b.strings["ollama.base_url"] = "http://custom:11434"
	b.strings["ollama.fast_model"] = "custom-fast"
	b.strings["ollama.deep_model"] = "custom-deep"
	b.strings["ollama.embed_model"] = "custom-embed"
	b.strings["storage.data_dir"] = "/tmp/tbyd-test"
	b.strings["proxy.default_model"] = "openai/gpt-4o"

	kc := &mockKeychain{value: "backend-key"}
	cfg, err := loadWith(b, kc)
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
	if cfg.Proxy.DefaultModel != "openai/gpt-4o" {
		t.Errorf("Proxy.DefaultModel = %q", cfg.Proxy.DefaultModel)
	}
}

// TestEnvOverride verifies that environment variables override backend values.
func TestEnvOverride(t *testing.T) {
	b := newMockBackend()
	b.ints["server.port"] = 5000

	t.Setenv("TBYD_OPENROUTER_API_KEY", "env-key")
	t.Setenv("TBYD_SERVER_PORT", "6000")

	cfg, err := loadWith(b, &mockKeychain{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Proxy.OpenRouterAPIKey != "env-key" {
		t.Errorf("OpenRouterAPIKey = %q, want %q", cfg.Proxy.OpenRouterAPIKey, "env-key")
	}
	if cfg.Server.Port != 6000 {
		t.Errorf("Server.Port = %d, want 6000 (env should override backend)", cfg.Server.Port)
	}
}

// TestMissingRequiredField verifies a clear error when the API key is missing everywhere.
func TestMissingRequiredField(t *testing.T) {
	b := newMockBackend()

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	_, err := loadWith(b, &mockKeychain{})
	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}

	want := "missing required config"
	if got := err.Error(); !contains(got, want) {
		t.Errorf("error = %q, want it to contain %q", got, want)
	}
}

// TestKeychainFallback verifies the Keychain is consulted when no API key is in backend or env.
func TestKeychainFallback(t *testing.T) {
	b := newMockBackend()

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	kc := &mockKeychain{value: "keychain-secret"}
	cfg, err := loadWith(b, kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Proxy.OpenRouterAPIKey != "keychain-secret" {
		t.Errorf("OpenRouterAPIKey = %q, want %q", cfg.Proxy.OpenRouterAPIKey, "keychain-secret")
	}
}

// TestSecretNotReadFromBackend verifies that secret keys are never read from the backend.
func TestSecretNotReadFromBackend(t *testing.T) {
	b := newMockBackend()
	b.strings["proxy.openrouter_api_key"] = "should-be-ignored"

	t.Setenv("TBYD_OPENROUTER_API_KEY", "")

	_, err := loadWith(b, &mockKeychain{})
	if err == nil {
		t.Fatal("expected error: secret should not be read from backend, so API key should be missing")
	}
}

// TestDefaults_LogLevel verifies the default log level is "info".
func TestDefaults_LogLevel(t *testing.T) {
	b := newMockBackend()
	kc := &mockKeychain{value: "test-key"}

	cfg, err := loadWith(b, kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "info")
	}
}

// TestBackendOverride_LogLevel verifies backend can set log level.
func TestBackendOverride_LogLevel(t *testing.T) {
	b := newMockBackend()
	b.strings["log.level"] = "debug"
	kc := &mockKeychain{value: "test-key"}

	cfg, err := loadWith(b, kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "debug")
	}
}

// TestEnvOverride_LogLevel verifies TBYD_LOG_LEVEL overrides backend.
func TestEnvOverride_LogLevel(t *testing.T) {
	b := newMockBackend()
	kc := &mockKeychain{value: "test-key"}

	t.Setenv("TBYD_LOG_LEVEL", "debug")

	cfg, err := loadWith(b, kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Log.Level != "debug" {
		t.Errorf("Log.Level = %q, want %q", cfg.Log.Level, "debug")
	}
}

// TestGetAPIToken_GeneratesOnFirstCall verifies a new token is generated and stored.
func TestGetAPIToken_GeneratesOnFirstCall(t *testing.T) {
	kc := newMockKeychain()

	tok, err := GetAPIToken(kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(tok) != 64 {
		t.Errorf("token length = %d, want 64 hex chars", len(tok))
	}
	for _, c := range tok {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("token contains non-hex char: %q", c)
		}
	}
	if !kc.setCalled {
		t.Error("expected Set to be called to persist the token")
	}
}

// TestGetAPIToken_ReturnsExisting verifies an existing token is returned without generating a new one.
func TestGetAPIToken_ReturnsExisting(t *testing.T) {
	kc := newMockKeychain()
	kc.store["tbyd/tbyd-api-token"] = "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234"

	tok, err := GetAPIToken(kc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if tok != "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234" {
		t.Errorf("token = %q, want existing token", tok)
	}
	if kc.setCalled {
		t.Error("Set should NOT be called when token already exists")
	}
}

// TestGetAPIToken_Deterministic verifies calling twice with the same mock returns the same token.
func TestGetAPIToken_Deterministic(t *testing.T) {
	kc := newMockKeychain()

	tok1, err := GetAPIToken(kc)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	tok2, err := GetAPIToken(kc)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if tok1 != tok2 {
		t.Errorf("tokens differ: %q vs %q", tok1, tok2)
	}
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
