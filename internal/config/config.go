package config

import (
	"fmt"
	"strings"
)

type Config struct {
	Server  ServerConfig
	Ollama  OllamaConfig
	Storage StorageConfig
	Proxy   ProxyConfig
}

type ServerConfig struct {
	Port    int
	MCPPort int
}

type OllamaConfig struct {
	BaseURL    string
	FastModel  string
	DeepModel  string
	EmbedModel string
}

type StorageConfig struct {
	DataDir string
}

type ProxyConfig struct {
	OpenRouterAPIKey string
	DefaultModel     string
}

func defaults() Config {
	dataDir := defaultDataDir()
	return Config{
		Server: ServerConfig{
			Port:    4000,
			MCPPort: 4001,
		},
		Ollama: OllamaConfig{
			BaseURL:    "http://localhost:11434",
			FastModel:  "phi3.5",
			DeepModel:  "mistral-nemo",
			EmbedModel: "nomic-embed-text",
		},
		Storage: StorageConfig{
			DataDir: dataDir,
		},
		Proxy: ProxyConfig{
			DefaultModel: "anthropic/claude-opus-4",
		},
	}
}

// Load reads configuration from the platform-native backend, environment
// variables, and platform secret store.
//
// On macOS the backend is UserDefaults (domain: com.tbyd.app) and secrets
// fall back to macOS Keychain.
// On Linux the backend is a JSON file at $XDG_CONFIG_HOME/tbyd/config.json
// and secrets must be provided via environment variables.
//
// Environment variables (TBYD_*) override backend values on all platforms.
func Load() (Config, error) {
	return loadWith(newPlatformBackend(), keychainReader{})
}

// keychain abstracts Keychain access for testing.
type keychain interface {
	Get(service, account string) (string, error)
}

func loadWith(b ConfigBackend, kc keychain) (Config, error) {
	cfg := defaults()

	if err := applyBackend(&cfg, b); err != nil {
		return Config{}, err
	}

	applyEnvOverrides(&cfg)

	// Try platform keychain for API key if still empty.
	if cfg.Proxy.OpenRouterAPIKey == "" {
		if key, err := kc.Get("tbyd", "openrouter_api_key"); err == nil && key != "" {
			cfg.Proxy.OpenRouterAPIKey = key
		}
	}

	if cfg.Proxy.OpenRouterAPIKey == "" {
		msg := "missing required config: OpenRouter API key. " +
			"Set it via environment variable TBYD_OPENROUTER_API_KEY" +
			apiKeyHint()
		return Config{}, fmt.Errorf("%s", msg)
	}

	return cfg, nil
}

// keychainReader reads from macOS Keychain via the security CLI.
type keychainReader struct{}

func (keychainReader) Get(service, account string) (string, error) {
	out, err := keychainExec(service, account)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
