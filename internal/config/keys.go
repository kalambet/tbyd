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
		key: "storage.save_interactions", typ: kBool, env: "TBYD_STORAGE_SAVE_INTERACTIONS",
		apply:   func(cfg *Config, v any) { cfg.Storage.SaveInteractions = v.(bool) },
		extract: func(cfg Config) any { return cfg.Storage.SaveInteractions },
	},
	{
		key: "storage.onboarding_shown", typ: kBool, env: "TBYD_STORAGE_ONBOARDING_SHOWN",
		apply:   func(cfg *Config, v any) { cfg.Storage.OnboardingShown = v.(bool) },
		extract: func(cfg Config) any { return cfg.Storage.OnboardingShown },
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
	{
		key: "enrichment.cache_enabled", typ: kBool, env: "TBYD_ENRICHMENT_CACHE_ENABLED",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.CacheEnabled = v.(bool) },
		extract: func(cfg Config) any { return cfg.Enrichment.CacheEnabled },
	},
	{
		key: "enrichment.cache_semantic_threshold", typ: kFloat, env: "TBYD_ENRICHMENT_CACHE_SEMANTIC_THRESHOLD",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.CacheSemanticThreshold = v.(float64) },
		extract: func(cfg Config) any { return cfg.Enrichment.CacheSemanticThreshold },
	},
	{
		key: "enrichment.cache_exact_ttl", typ: kString, env: "TBYD_ENRICHMENT_CACHE_EXACT_TTL",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.CacheExactTTL = v.(string) },
		extract: func(cfg Config) any { return cfg.Enrichment.CacheExactTTL },
	},
	{
		key: "enrichment.cache_semantic_ttl", typ: kString, env: "TBYD_ENRICHMENT_CACHE_SEMANTIC_TTL",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.CacheSemanticTTL = v.(string) },
		extract: func(cfg Config) any { return cfg.Enrichment.CacheSemanticTTL },
	},
	{
		key: "enrichment.deep_enabled", typ: kBool, env: "TBYD_ENRICHMENT_DEEP_ENABLED",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.DeepEnabled = v.(bool) },
		extract: func(cfg Config) any { return cfg.Enrichment.DeepEnabled },
	},
	{
		key: "enrichment.deep_schedule", typ: kString, env: "TBYD_ENRICHMENT_DEEP_SCHEDULE",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.DeepSchedule = v.(string) },
		extract: func(cfg Config) any { return cfg.Enrichment.DeepSchedule },
	},
	{
		key: "enrichment.deep_idle_cpu_max_percent", typ: kInt, env: "TBYD_ENRICHMENT_DEEP_IDLE_CPU_MAX_PERCENT",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.DeepIdleCPUMaxPct = v.(int) },
		extract: func(cfg Config) any { return cfg.Enrichment.DeepIdleCPUMaxPct },
	},
	{
		key: "enrichment.deep_idle_mem_min_gb", typ: kInt, env: "TBYD_ENRICHMENT_DEEP_IDLE_MEM_MIN_GB",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.DeepIdleMemMinGB = v.(int) },
		extract: func(cfg Config) any { return cfg.Enrichment.DeepIdleMemMinGB },
	},
	{
		key: "enrichment.deep_batch_claim_limit", typ: kInt, env: "TBYD_ENRICHMENT_DEEP_BATCH_CLAIM_LIMIT",
		apply:   func(cfg *Config, v any) { cfg.Enrichment.DeepBatchClaimLimit = v.(int) },
		extract: func(cfg Config) any { return cfg.Enrichment.DeepBatchClaimLimit },
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
