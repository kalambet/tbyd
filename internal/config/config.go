package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
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

// Keychain abstracts platform secret store access.
type Keychain interface {
	Get(service, account string) (string, error)
	Set(service, account, value string) error
}

// ErrNotFound is returned by Keychain.Get when the requested secret does not exist.
var ErrNotFound = errors.New("secret not found")

// Load reads configuration from the platform-native backend, environment
// variables, and platform secret store.
//
// On macOS the backend is UserDefaults (domain: com.tbyd.app) and secrets
// fall back to macOS Keychain.
// On Linux the backend is a JSON file at $XDG_CONFIG_HOME/tbyd/config.json
// and secrets fall back to $XDG_DATA_HOME/tbyd/secrets.json.
//
// Environment variables (TBYD_*) override backend values on all platforms.
func Load() (Config, error) {
	return loadWith(newPlatformBackend(), keychainClient{})
}

// NewKeychain returns the platform keychain client for use outside config loading.
func NewKeychain() Keychain {
	return keychainClient{}
}

func loadWith(b ConfigBackend, kc Keychain) (Config, error) {
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
// Non-ErrNotFound errors from the keychain are propagated.
func GetAPIToken(kc Keychain) (string, error) {
	tok, err := kc.Get(apiTokenService, apiTokenAccount)
	if err == nil && tok != "" {
		return tok, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", fmt.Errorf("reading API token: %w", err)
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
