# tbyd — Quick Context for Implementation

> **Read this first** when starting any issue. It replaces the need to explore the codebase from scratch.

## What is tbyd

A **local-first data sovereignty layer** (Go binary) that sits between the user and cloud LLMs. It intercepts chat requests on `localhost:4000`, enriches them with personal context via a local LLM (Ollama), and proxies to cloud LLMs via OpenRouter. All data stays on-device in SQLite.

## Build & Run

```bash
make build        # → bin/tbyd
make test         # go test ./...
make run          # build + run

# Requires:
# - Ollama running at localhost:11434 with phi3.5 + nomic-embed-text
# - TBYD_OPENROUTER_API_KEY env var (or macOS Keychain entry)
```

## Package Map

| Package | Purpose | Key Types |
|---------|---------|-----------|
| `cmd/tbyd` | Entrypoint, DI wiring | `main.go` — builds all deps and starts servers |
| `internal/api` | HTTP handlers | `OpenAIHandler` (proxy), `AppHandler` (mgmt), `MCPServer` (stdio) |
| `internal/config` | Config loading | `Config`, `Keychain`, `ConfigBackend` |
| `internal/engine` | Local LLM abstraction | `Engine` interface (Chat, Embed, PullModel) |
| `internal/intent` | Intent extraction | `Extractor` — calls local LLM for structured JSON intent |
| `internal/retrieval` | Vector search | `VectorStore` interface, `Retriever`, `Embedder`, `SQLiteStore` |
| `internal/composer` | Prompt composition | `Composer` — merges context + profile into enriched prompt |
| `internal/pipeline` | Enrichment orchestrator | `Enricher` — chains intent→retrieval→profile→compose |
| `internal/proxy` | Cloud LLM client | `Client` — HTTP client for OpenRouter |
| `internal/profile` | User profile CRUD | `Manager` — reads/writes `user_profile` table |
| `internal/storage` | SQLite persistence | `Store` — interactions, context_docs, jobs, profile |
| `internal/ingest` | Background doc processing | `Worker` — polls job queue, embeds context docs |
| `internal/ollama` | Ollama-specific client | `Client` — raw Ollama API (used by engine) |

## Key Interfaces

```go
// engine.Engine — abstracts Ollama/MLX, used everywhere for inference
Chat(ctx, model, []Message, *Schema) (string, error)
Embed(ctx, model, text) ([]float32, error)
IsRunning(ctx) bool
ListModels(ctx) ([]string, error)
HasModel(ctx, name) bool
PullModel(ctx, name, onProgress) error

// retrieval.VectorStore — brute-force cosine similarity over SQLite
Insert(table, []Record) error
Search(table, vector, topK, filter) ([]ScoredRecord, error)
GetByIDs(ctx, table, []string) ([]Record, error)
Delete(table, id) error

// config.Keychain — platform secret store
Get(service, account) (string, error)
Set(service, account, value) error
```

## HTTP API Routes

**OpenAI-compat** (no auth):
- `POST /v1/chat/completions` — enrich → proxy → cloud LLM
- `GET /v1/models` — list available models from OpenRouter

**Management** (bearer token auth):
- `POST /ingest` — add content to knowledge base (queues background embedding)
- `GET/PATCH /profile` — user profile CRUD
- `GET /interactions`, `GET/DELETE /interactions/{id}` — interaction history
- `GET /context-docs`, `DELETE /context-docs/{id}` — knowledge base docs

**MCP** (stdio transport, port 4001):
- Tools: `add_context`, `recall`, `set_preference`, `summarize_session`
- Resources: `user://profile`, `user://context`

## Dependency Wiring (main.go)

```
config.Load() → Config
engine.Detect() → Engine (auto-detects Ollama)
engine.EnsureReady() → pulls phi3.5 + nomic-embed-text if missing
storage.Open() → Store (SQLite, auto-migrates)
intent.NewExtractor(engine, fastModel)
retrieval.NewEmbedder(engine, embedModel)
retrieval.NewSQLiteStore(store.DB())
retrieval.NewRetriever(embedder, vectorStore)
profile.NewManager(store)
composer.New(maxTokens)
pipeline.NewEnricher(extractor, retriever, profile, composer, topK)
proxy.NewClient(apiKey)
api.NewOpenAIHandler(proxy, enricher)
api.NewAppHandler(store, profile, token, httpClient, vectorStore)
api.NewMCPServer(store, profile, retriever, engine, deepModel)
ingest.NewWorker(store, embedder, vectorStore, pollInterval)
```

## Data Models (storage/models.go)

- **Interaction**: id, user_query, enriched_prompt, cloud_model, cloud_response, status, feedback_score, vector_ids
- **ContextDoc**: id, title, content, source, tags (JSON), vector_id
- **Job**: id, type, payload_json, status (pending/running/completed/failed), attempts, max_attempts, run_after

## Config Keys (all have TBYD_ env overrides)

| Key | Env Var | Default |
|-----|---------|---------|
| `server.port` | `TBYD_SERVER_PORT` | `4000` |
| `server.mcp_port` | `TBYD_SERVER_MCP_PORT` | `4001` |
| `ollama.base_url` | `TBYD_OLLAMA_BASE_URL` | `http://localhost:11434` |
| `ollama.fast_model` | `TBYD_OLLAMA_FAST_MODEL` | `phi3.5` |
| `ollama.deep_model` | `TBYD_OLLAMA_DEEP_MODEL` | `mistral-nemo` |
| `ollama.embed_model` | `TBYD_OLLAMA_EMBED_MODEL` | `nomic-embed-text` |
| `storage.data_dir` | `TBYD_STORAGE_DATA_DIR` | `~/Library/Application Support/tbyd` |
| `proxy.openrouter_api_key` | `TBYD_OPENROUTER_API_KEY` | (required, Keychain) |
| `proxy.default_model` | `TBYD_PROXY_DEFAULT_MODEL` | `anthropic/claude-opus-4` |
| `retrieval.top_k` | `TBYD_RETRIEVAL_TOP_K` | `5` |

## Conventions

- **Logging**: `log/slog` (text handler to stderr). Debug/Info levels via `TBYD_LOG_LEVEL`.
- **HTTP framework**: `go-chi/chi/v5`. Handlers are closures returning `http.HandlerFunc`.
- **Errors**: Return `fmt.Errorf("context: %w", err)`. API errors via `httpError(w, code, type, msg)`.
- **SQLite**: Pure Go via `modernc.org/sqlite` (no CGO). Single connection, WAL mode, embedded migrations.
- **Config**: Platform backend (UserDefaults on macOS, XDG JSON on Linux) → env overrides → Keychain for secrets.
- **Testing**: Table-driven tests with `t.Run()`. In-memory SQLite via `storage.Open(":memory:")`. No external test deps.
- **UUIDs**: `github.com/google/uuid` for all IDs.
- **JSON arrays in SQLite**: Stored as TEXT, marshalled/unmarshalled manually.

## Implementation Status

**Done** (Phase 0 + 00 + 1): Foundation, config, storage, Ollama integration, engine abstraction, intent extraction, vector search, context retrieval, prompt composition, enrichment pipeline, MCP server, ingestion API.

**In Progress** (Phase 2): User surfaces — CLI interface, interaction storage opt-in, macOS app.

**Not Started**: Phase 3 (personalization/feedback), Phase 4 (browser extension, Feedly, fine-tuning), Phase 5 (polish/distribution).

See `docs/phase-*.md` for detailed issue breakdowns per phase.

## File Structure Cheatsheet

```
cmd/tbyd/main.go              ← start here for dependency wiring
internal/api/openai.go         ← chat completions proxy handler
internal/api/ingest.go         ← management API (ingest, profile, interactions)
internal/api/mcp.go            ← MCP server tools and resources
internal/api/auth.go           ← bearer token middleware
internal/pipeline/enrichment.go ← enrichment orchestrator
internal/engine/engine.go      ← Engine interface definition
internal/engine/ollama.go      ← Ollama Engine implementation
internal/engine/detect.go      ← auto-detect available engine
internal/storage/sqlite.go     ← all SQLite operations
internal/storage/models.go     ← Interaction, Job, ContextDoc structs
internal/storage/migrations/   ← embedded SQL migration files
internal/retrieval/vectorstore.go ← VectorStore interface
internal/retrieval/store.go    ← SQLite cosine-similarity implementation
internal/retrieval/retriever.go ← semantic search orchestration
internal/retrieval/embedder.go ← Ollama embedding client
internal/intent/extractor.go   ← local LLM intent extraction
internal/intent/prompt.go      ← intent extraction prompt template
internal/composer/prompt.go    ← prompt composition logic
internal/proxy/openrouter.go   ← OpenRouter HTTP client
internal/proxy/types.go        ← ChatRequest/ChatResponse types
internal/profile/manager.go    ← user profile CRUD
internal/config/config.go      ← Config struct + Load()
internal/config/keys.go        ← config key specs + env overrides
internal/ingest/worker.go      ← background job worker
```
