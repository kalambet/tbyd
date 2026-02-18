package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server  ServerConfig  `toml:"server"`
	Ollama  OllamaConfig  `toml:"ollama"`
	Storage StorageConfig `toml:"storage"`
	Proxy   ProxyConfig   `toml:"proxy"`
}

type ServerConfig struct {
	Port    int `toml:"port"`
	MCPPort int `toml:"mcp_port"`
}

type OllamaConfig struct {
	BaseURL    string `toml:"base_url"`
	FastModel  string `toml:"fast_model"`
	DeepModel  string `toml:"deep_model"`
	EmbedModel string `toml:"embed_model"`
}

type StorageConfig struct {
	DataDir string `toml:"data_dir"`
}

type ProxyConfig struct {
	OpenRouterAPIKey string `toml:"openrouter_api_key"`
	DefaultModel     string `toml:"default_model"`
}

func defaults() Config {
	homeDir, _ := os.UserHomeDir()
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
			DataDir: filepath.Join(homeDir, "Library", "Application Support", "tbyd"),
		},
		Proxy: ProxyConfig{
			DefaultModel: "anthropic/claude-opus-4",
		},
	}
}

// Load reads configuration from TOML files, environment variables, and macOS Keychain.
// Search order for config files: ~/.config/tbyd/config.toml, then ./tbyd.toml.
// Environment variables override file values. Keychain is checked for the API key.
func Load() (Config, error) {
	return load(keychainReader{})
}

// keychain abstracts Keychain access for testing.
type keychain interface {
	Get(service, account string) (string, error)
}

func load(kc keychain) (Config, error) {
	cfg := defaults()

	// Try config file paths in order
	for _, p := range configPaths() {
		if _, err := os.Stat(p); err == nil {
			if _, err := toml.DecodeFile(p, &cfg); err != nil {
				return Config{}, fmt.Errorf("parsing config file %s: %w", p, err)
			}
			break
		}
	}

	return finalize(cfg, kc)
}

// loadFromPath loads config from a specific file path. Used by tests.
func loadFromPath(path string, kc keychain) (Config, error) {
	cfg := defaults()

	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			return Config{}, fmt.Errorf("parsing config file %s: %w", path, err)
		}
	}

	return finalize(cfg, kc)
}

func finalize(cfg Config, kc keychain) (Config, error) {
	// Apply environment variable overrides
	applyEnvOverrides(&cfg)

	// Try macOS Keychain for API key if still empty
	if cfg.Proxy.OpenRouterAPIKey == "" {
		if key, err := kc.Get("tbyd", "openrouter_api_key"); err == nil && key != "" {
			cfg.Proxy.OpenRouterAPIKey = key
		}
	}

	// Validate required fields
	if cfg.Proxy.OpenRouterAPIKey == "" {
		return Config{}, fmt.Errorf(
			"missing required config: OpenRouter API key. " +
				"Set it in config.toml (proxy.openrouter_api_key), " +
				"environment variable TBYD_OPENROUTER_API_KEY, " +
				"or macOS Keychain (service: tbyd, account: openrouter_api_key)",
		)
	}

	return cfg, nil
}

func configPaths() []string {
	var paths []string

	// XDG: ~/.config/tbyd/config.toml
	if homeDir, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(homeDir, ".config", "tbyd", "config.toml"))
	}

	// Local fallback: ./tbyd.toml
	paths = append(paths, "tbyd.toml")

	return paths
}

func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("TBYD_SERVER_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("TBYD_SERVER_MCP_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.MCPPort = port
		}
	}
	if v := os.Getenv("TBYD_OLLAMA_BASE_URL"); v != "" {
		cfg.Ollama.BaseURL = v
	}
	if v := os.Getenv("TBYD_OLLAMA_FAST_MODEL"); v != "" {
		cfg.Ollama.FastModel = v
	}
	if v := os.Getenv("TBYD_OLLAMA_DEEP_MODEL"); v != "" {
		cfg.Ollama.DeepModel = v
	}
	if v := os.Getenv("TBYD_OLLAMA_EMBED_MODEL"); v != "" {
		cfg.Ollama.EmbedModel = v
	}
	if v := os.Getenv("TBYD_STORAGE_DATA_DIR"); v != "" {
		cfg.Storage.DataDir = v
	}
	if v := os.Getenv("TBYD_OPENROUTER_API_KEY"); v != "" {
		cfg.Proxy.OpenRouterAPIKey = v
	}
	if v := os.Getenv("TBYD_PROXY_DEFAULT_MODEL"); v != "" {
		cfg.Proxy.DefaultModel = v
	}
}

// keychainReader reads from macOS Keychain via the security CLI.
type keychainReader struct{}

func (keychainReader) Get(service, account string) (string, error) {
	// #nosec G204 — service and account are internal constants, not user input
	out, err := keychainExec(service, account)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
