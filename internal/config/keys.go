package config

import (
	"fmt"
	"os"
	"strconv"
)

type keyType int

const (
	kString keyType = iota
	kInt
	kBool
	kFloat
)

type keySpec struct {
	key     string
	typ     keyType
	env     string
	secret  bool
	apply   func(cfg *Config, v any)
	extract func(cfg Config) any
}

var specs = []keySpec{
	{
		key: "server.port", typ: kInt, env: "TBYD_SERVER_PORT",
		apply:   func(cfg *Config, v any) { cfg.Server.Port = v.(int) },
		extract: func(cfg Config) any { return cfg.Server.Port },
	},
	{
		key: "server.mcp_port", typ: kInt, env: "TBYD_SERVER_MCP_PORT",
		apply:   func(cfg *Config, v any) { cfg.Server.MCPPort = v.(int) },
		extract: func(cfg Config) any { return cfg.Server.MCPPort },
	},
	{
		key: "ollama.base_url", typ: kString, env: "TBYD_OLLAMA_BASE_URL",
		apply:   func(cfg *Config, v any) { cfg.Ollama.BaseURL = v.(string) },
		extract: func(cfg Config) any { return cfg.Ollama.BaseURL },
	},
	{
		key: "ollama.fast_model", typ: kString, env: "TBYD_OLLAMA_FAST_MODEL",
		apply:   func(cfg *Config, v any) { cfg.Ollama.FastModel = v.(string) },
		extract: func(cfg Config) any { return cfg.Ollama.FastModel },
	},
	{
		key: "ollama.deep_model", typ: kString, env: "TBYD_OLLAMA_DEEP_MODEL",
		apply:   func(cfg *Config, v any) { cfg.Ollama.DeepModel = v.(string) },
		extract: func(cfg Config) any { return cfg.Ollama.DeepModel },
	},
	{
		key: "ollama.embed_model", typ: kString, env: "TBYD_OLLAMA_EMBED_MODEL",
		apply:   func(cfg *Config, v any) { cfg.Ollama.EmbedModel = v.(string) },
		extract: func(cfg Config) any { return cfg.Ollama.EmbedModel },
	},
	{
		key: "storage.data_dir", typ: kString, env: "TBYD_STORAGE_DATA_DIR",
		apply:   func(cfg *Config, v any) { cfg.Storage.DataDir = v.(string) },
		extract: func(cfg Config) any { return cfg.Storage.DataDir },
	},
	{
		key: "proxy.openrouter_api_key", typ: kString, env: "TBYD_OPENROUTER_API_KEY",
		secret: true,
		apply:   func(cfg *Config, v any) { cfg.Proxy.OpenRouterAPIKey = v.(string) },
		extract: func(cfg Config) any { return cfg.Proxy.OpenRouterAPIKey },
	},
	{
		key: "proxy.default_model", typ: kString, env: "TBYD_PROXY_DEFAULT_MODEL",
		apply:   func(cfg *Config, v any) { cfg.Proxy.DefaultModel = v.(string) },
		extract: func(cfg Config) any { return cfg.Proxy.DefaultModel },
	},
	{
		key: "log.level", typ: kString, env: "TBYD_LOG_LEVEL",
		apply:   func(cfg *Config, v any) { cfg.Log.Level = v.(string) },
		extract: func(cfg Config) any { return cfg.Log.Level },
	},
	{
		key: "retrieval.top_k", typ: kInt, env: "TBYD_RETRIEVAL_TOP_K",
		apply:   func(cfg *Config, v any) { cfg.Retrieval.TopK = v.(int) },
		extract: func(cfg Config) any { return cfg.Retrieval.TopK },
	},
	{
		key: "enrichment.reranking_enabled", typ: kBool, env: "TBYD_ENRICHMENT_RERANKING_ENABLED",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.RerankingEnabled = v.(bool) },
		extract: func(cfg Config) any { return cfg.Enrichment.RerankingEnabled },
	},
	{
		key: "enrichment.reranking_timeout", typ: kString, env: "TBYD_ENRICHMENT_RERANKING_TIMEOUT",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.RerankingTimeout = v.(string) },
		extract: func(cfg Config) any { return cfg.Enrichment.RerankingTimeout },
	},
	{
		key: "enrichment.reranking_threshold", typ: kFloat, env: "TBYD_ENRICHMENT_RERANKING_THRESHOLD",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.RerankingThreshold = v.(float64) },
		extract: func(cfg Config) any { return cfg.Enrichment.RerankingThreshold },
	},
}

func applyBackend(cfg *Config, b ConfigBackend) error {
	for _, s := range specs {
		if s.secret {
			continue
		}
		switch s.typ {
		case kString:
			v, ok, err := b.GetString(s.key)
			if err != nil {
				return fmt.Errorf("reading %s: %w", s.key, err)
			}
			if ok {
				s.apply(cfg, v)
			}
		case kInt:
			v, ok, err := b.GetInt(s.key)
			if err != nil {
				return fmt.Errorf("reading %s: %w", s.key, err)
			}
			if ok {
				s.apply(cfg, v)
			}
		case kBool:
			v, ok, err := b.GetString(s.key)
			if err != nil {
				return fmt.Errorf("reading %s: %w", s.key, err)
			}
			if ok && v != "" {
				if bv, err := strconv.ParseBool(v); err == nil {
					s.apply(cfg, bv)
				} else {
					fmt.Fprintf(os.Stderr, "[WARN] could not parse bool from config key %s=%q: %v. Using default value.\n", s.key, v, err)
				}
			}
		case kFloat:
			v, ok, err := b.GetString(s.key)
			if err != nil {
				return fmt.Errorf("reading %s: %w", s.key, err)
			}
			if ok && v != "" {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					s.apply(cfg, f)
				} else {
					fmt.Fprintf(os.Stderr, "[WARN] could not parse float from config key %s=%q: %v. Using default value.\n", s.key, v, err)
				}
			}
		}
	}
	return nil
}

func applyEnvOverrides(cfg *Config) {
	for _, s := range specs {
		if s.env == "" {
			continue
		}
		raw := os.Getenv(s.env)
		if raw == "" {
			continue
		}
		switch s.typ {
		case kString:
			s.apply(cfg, raw)
		case kInt:
			if i, err := strconv.Atoi(raw); err == nil {
				s.apply(cfg, i)
			} else {
				fmt.Fprintf(os.Stderr, "[WARN] could not parse integer from env var %s=%q: %v. Using default value.\n", s.env, raw, err)
			}
		case kBool:
			if b, err := strconv.ParseBool(raw); err == nil {
				s.apply(cfg, b)
			} else {
				fmt.Fprintf(os.Stderr, "[WARN] could not parse bool from env var %s=%q: %v. Using default value.\n", s.env, raw, err)
			}
		case kFloat:
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				s.apply(cfg, f)
			} else {
				fmt.Fprintf(os.Stderr, "[WARN] could not parse float from env var %s=%q: %v. Using default value.\n", s.env, raw, err)
			}
		}
	}
}
