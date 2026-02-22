package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

type Config struct {
	Server  ServerConfig
	Ollama  OllamaConfig
	Storage StorageConfig
	Proxy   ProxyConfig
	Log     LogConfig
}

type LogConfig struct {
	Level string // default "info"
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
		Log: LogConfig{
			Level: "info",
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
	return loadWith(newPlatformBackend(), keychainClient{})
}

// NewKeychain returns the platform keychain client for use outside config loading.
func NewKeychain() keychain {
	return keychainClient{}
}

// Keychain re-exports the keychain interface for use by callers of GetAPIToken.
type Keychain = keychain

// keychain abstracts Keychain access for testing.
type keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
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

// keychainClient reads from and writes to the platform secret store.
type keychainClient struct{}

func (keychainClient) Get(service, account string) (string, error) {
	out, err := keychainGet(service, account)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (keychainClient) Set(service, account, value string) error {
	return keychainSet(service, account, value)
}

const (
	apiTokenService = "tbyd"
	apiTokenAccount = "tbyd-api-token"
)

// GetAPIToken reads the API bearer token from the secret store. If none
// exists, a random 256-bit hex-encoded token is generated and stored.
func GetAPIToken(kc keychain) (string, error) {
	tok, err := kc.Get(apiTokenService, apiTokenAccount)
	if err == nil && tok != "" {
		return tok, nil
	}

	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating API token: %w", err)
	}
	tok = hex.EncodeToString(b)

	if err := kc.Set(apiTokenService, apiTokenAccount, tok); err != nil {
		return "", fmt.Errorf("storing API token: %w", err)
	}
	return tok, nil
}
