# Phase 0 — Foundation

> **Goal:** Establish the project skeleton, configuration, storage, and a working passthrough proxy before any enrichment logic. After this phase the binary compiles, starts cleanly, and transparently forwards OpenAI-format requests to OpenRouter.

---

## Issue 0.1 — Go module init and project layout

**Context:** Empty repo. Need a canonical Go module structure before anything else.

**Tasks:**
- Run `go mod init github.com/kalambet/tbyd`
- Create directory tree:
  ```
  cmd/tbyd/
  internal/api/
  internal/config/
  internal/storage/
  internal/ollama/
  internal/proxy/
  internal/pipeline/
  internal/intent/
  internal/retrieval/
  internal/composer/
  internal/ingest/
  internal/profile/
  internal/synthesis/
  internal/tuning/
  docs/
  scripts/
  macos/
  browser-extension/
  ```
- Add `cmd/tbyd/main.go` with a placeholder `main()` that prints version and exits
- Add `.gitignore` (Go standard + macOS + `.tbyd.toml` local override)
- Add `Makefile` with targets: `build`, `run`, `test`, `lint`

**Acceptance criteria:**
- `go build ./...` succeeds with no errors
- `make build` produces a binary at `./bin/tbyd`

---

## Issue 0.2 — Config loader (TOML)

**Context:** All runtime settings (ports, model names, data paths, API keys) must be read from a config file. API keys must never be hardcoded.

**Tasks:**
- Create `internal/config/config.go`
- Define `Config` struct with fields:
  ```go
  type Config struct {
    Server   ServerConfig
    Ollama   OllamaConfig
    Storage  StorageConfig
    Proxy    ProxyConfig
    Log      LogConfig
  }
  type ServerConfig struct {
    Port int    // default 4000
    MCPPort int // default 4001
  }
  type OllamaConfig struct {
    BaseURL    string // default "http://localhost:11434"
    FastModel  string // default "phi3.5"
    DeepModel  string // default "mistral-nemo"
    EmbedModel string // default "nomic-embed-text"
  }
  type StorageConfig struct {
    DataDir string // default "$HOME/Library/Application Support/tbyd"
  }
  type ProxyConfig struct {
    OpenRouterAPIKey  string
    DefaultModel      string // e.g. "anthropic/claude-opus-4"
  }
  ```
- Load from `~/.config/tbyd/config.toml` (XDG) with fallback to `./tbyd.toml`
- Override any field from environment variables (e.g. `TBYD_OPENROUTER_API_KEY`)
- Add `config.toml.example` to repo root with all fields documented
- Store `OpenRouterAPIKey` in macOS Keychain via `security` CLI on first run; read from Keychain at runtime (fallback to env var)
- On first run, generate a random 256-bit API token and store in Keychain under `tbyd-api-token`
- Add `GetAPIToken() (string, error)` to config that reads from Keychain
- Add `LogConfig` to `Config` struct:
  ```go
  type LogConfig struct {
      Level string // default "info"
  }
  ```

**Unit tests** (`internal/config/config_test.go`):
- `TestDefaults` — load with empty config file; verify all defaults are applied correctly
- `TestEnvOverride` — set `TBYD_OPENROUTER_API_KEY` env var; verify it overrides config file value
- `TestMissingRequiredField` — load with no API key anywhere; verify error message mentions the missing field
- `TestTOMLParsing` — load from a temp file with all fields set; verify each field is read correctly
- `TestKeychainFallback` — mock `security` CLI; verify Keychain read is attempted before env var
- `TestAPITokenGenerated` — first call generates token; second call returns same token

**Acceptance criteria:**
- Config loads without error from example file
- Missing required fields (API key) produce a clear error message pointing user to docs
- `go test ./internal/config/...` passes

---

## Issue 0.3 — SQLite storage: schema and migrations

**Context:** All interactions, user profile, and ingested documents are stored in SQLite. Must use `modernc.org/sqlite` (pure Go, no CGO).

**Tasks:**
- Add dependency: `go get modernc.org/sqlite`
- Create `internal/storage/sqlite.go` with:
  - `Open(dataDir string) (*Store, error)` — opens/creates the database file
     - Set pragmas on open: `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`
   - Migration runner: applies versioned SQL files in order
- Create `internal/storage/migrations/` with initial migration `001_initial.sql`:
  ```sql
  CREATE TABLE IF NOT EXISTS schema_version (
      version INTEGER PRIMARY KEY,
      applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );

  CREATE TABLE IF NOT EXISTS interactions (
      id TEXT PRIMARY KEY,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      user_query TEXT NOT NULL,
      enriched_prompt TEXT,
      cloud_model TEXT,
      cloud_response TEXT,
      status TEXT NOT NULL DEFAULT 'completed',
      feedback_score INTEGER DEFAULT 0,
      feedback_notes TEXT,
      vector_ids TEXT DEFAULT '[]'
  );

  CREATE TABLE IF NOT EXISTS user_profile (
      key TEXT PRIMARY KEY,
      value TEXT NOT NULL,
      updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );

  CREATE TABLE IF NOT EXISTS context_docs (
      id TEXT PRIMARY KEY,
      title TEXT,
      content TEXT NOT NULL,
      source TEXT NOT NULL,
      tags TEXT DEFAULT '[]',
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      vector_id TEXT
  );

  CREATE TABLE IF NOT EXISTS context_vectors (
      id TEXT PRIMARY KEY,
      source_id TEXT NOT NULL,
      source_type TEXT NOT NULL,
      text_chunk TEXT NOT NULL,
      embedding BLOB NOT NULL,
      created_at TEXT NOT NULL,
      tags TEXT NOT NULL DEFAULT '[]'
  );

  CREATE INDEX IF NOT EXISTS idx_context_vectors_source_id ON context_vectors(source_id);
  CREATE INDEX IF NOT EXISTS idx_context_vectors_source_type ON context_vectors(source_type);

  CREATE INDEX IF NOT EXISTS idx_interactions_feedback ON interactions(feedback_score);
  CREATE INDEX IF NOT EXISTS idx_interactions_created ON interactions(created_at);

  CREATE TABLE IF NOT EXISTS jobs (
      id TEXT PRIMARY KEY,
      type TEXT NOT NULL,
      payload_json TEXT NOT NULL,
      status TEXT NOT NULL DEFAULT 'pending',
      attempts INTEGER DEFAULT 0,
      max_attempts INTEGER DEFAULT 3,
      run_after DATETIME DEFAULT CURRENT_TIMESTAMP,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      last_error TEXT
  );

  CREATE INDEX IF NOT EXISTS idx_jobs_status_run_after ON jobs(status, run_after);
  ```
- Implement `Store` methods:
  - `SaveInteraction(i Interaction) error`
  - `GetInteraction(id string) (Interaction, error)`
  - `UpdateFeedback(id string, score int, notes string) error`
  - `GetRecentInteractions(limit int) ([]Interaction, error)`
  - `SetProfileKey(key, value string) error`
  - `GetProfileKey(key string) (string, error)`
  - `GetAllProfileKeys() (map[string]string, error)`
  - `SaveContextDoc(doc ContextDoc) error`
  - `GetContextDoc(id string) (ContextDoc, error)`
  - `ListContextDocs(limit int) ([]ContextDoc, error)`
   - `EnqueueJob(job Job) error`
   - `ClaimNextJob(types []string) (*Job, error)`
   - `CompleteJob(id string) error`
   - `FailJob(id string, err string) error`

**Unit tests** (`internal/storage/sqlite_test.go`) — use in-memory SQLite (`:memory:`):
- `TestMigrationsIdempotent` — run `Open()` twice on the same path; verify schema_version count stays correct
- `TestMigrationsOrdered` — verify migrations are applied in ascending numeric order
- `TestSaveAndGetInteraction` — save an interaction, retrieve by ID, assert all fields match
- `TestGetInteractionNotFound` — retrieve non-existent ID; verify sentinel error (not a panic)
- `TestUpdateFeedback` — save interaction, update feedback, retrieve and assert score + notes updated
- `TestGetRecentInteractions` — save 10 interactions; request limit 5; verify 5 returned in descending order
- `TestProfileKeyRoundTrip` — set a key, get it back, verify exact match
- `TestGetAllProfileKeys` — set 5 keys, call `GetAllProfileKeys`, verify map has all 5
- `TestSaveAndListContextDocs` — save 3 docs, call `ListContextDocs(2)`, verify 2 returned
- `TestEnqueueAndClaimJob` — enqueue a job, claim it, verify fields match
- `TestClaimJob_RespectRunAfter` — enqueue with future `run_after`; verify not claimed yet
- `TestFailJob_IncrementsAttempts` — fail a job; verify attempts incremented

**Acceptance criteria:**
- `go test ./internal/storage/...` passes (in-memory SQLite for tests)
- Migrations are idempotent (running twice is safe)
- Database file created on first `Open()` in the configured data directory

---

## Issue 0.4 — Ollama lifecycle manager

**Context:** The system depends on Ollama running locally. It should check Ollama's status on startup, and guide the user if it's missing or models are not yet downloaded.

**Tasks:**
- Create `internal/ollama/client.go` with:
  - `IsRunning() bool` — GET `/api/tags` and check for 200
  - `ListModels() ([]string, error)` — parse `/api/tags` response
  - `PullModel(name string) error` — POST `/api/pull` with streaming progress
  - `HasModel(name string) bool`
  - `Chat(model string, messages []Message, jsonSchema *Schema) (string, error)` — POST `/api/chat` with `format: "json"` when schema provided
  - `Embed(model string, text string) ([]float32, error)` — POST `/api/embed`
- On startup (called from `main.go`):
  1. Check if Ollama is running — if not, print instructions and exit with clear error
  2. Check if fast model (`phi3.5`) is present — if not, pull it (with progress output)
  3. Check if embed model (`nomic-embed-text`) is present — if not, pull it
  4. Deep model (`mistral-nemo`) pull is optional / deferred until first background task
- Use `context.Context` throughout for cancellation

**Unit tests** (`internal/ollama/client_test.go`) — use `httptest.NewServer` to mock the Ollama HTTP API:
- `TestIsRunning_Up` — mock server returns 200 on `/api/tags`; verify `IsRunning()` returns `true`
- `TestIsRunning_Down` — mock server returns connection refused; verify `IsRunning()` returns `false`
- `TestListModels` — mock `/api/tags` response with 3 models; verify slice matches
- `TestHasModel_Present` — mock response includes `phi3.5`; verify `HasModel("phi3.5")` is true
- `TestHasModel_Absent` — model not in response; verify false
- `TestChat_PlainText` — mock `/api/chat` returning a message; verify response string returned
- `TestChat_JSONSchema` — mock returns JSON; verify `format: "json"` was set in the request body
- `TestEmbed` — mock `/api/embed` returning `[0.1, 0.2, 0.3]`; verify float slice returned
- `TestPullModel_Progress` — mock streaming pull response; verify function completes without error

**Acceptance criteria:**
- If Ollama is not running, startup prints: `"Ollama is not running. Start it with: ollama serve"` and exits non-zero
- If model is missing, pull runs with a progress indicator
- `Chat()` returns structured JSON when schema is provided
- Unit tests mock the Ollama HTTP API

---

## Issue 0.5 — OpenRouter HTTP client (passthrough)

**Context:** Before enrichment exists, the proxy must be able to forward requests to OpenRouter unchanged and stream responses back to the caller.

**Tasks:**
- Add dependency: standard `net/http` (no extra packages needed)
- Create `internal/proxy/openrouter.go` with:
  - `Client` struct holding API key and base URL (`https://openrouter.ai/api/v1`)
  - `Chat(ctx context.Context, req ChatRequest) (io.ReadCloser, error)` — streams SSE response
  - `ListModels(ctx context.Context) ([]Model, error)`
  - Set required headers: `Authorization: Bearer <key>`, `HTTP-Referer`, `X-Title`
  - Handle rate limit errors (429) with exponential backoff, max 3 retries
  - Handle timeout (default 60s, streaming 300s)
- Define shared types in `internal/proxy/types.go`:
  - `ChatRequest`, `ChatMessage`, `Model` — OpenAI-compatible structs

**Unit tests** (`internal/proxy/openrouter_test.go`) — use `httptest.NewServer`:
- `TestChat_Streaming` — mock SSE response; verify `io.ReadCloser` returned; read and assert content
- `TestChat_NonStreaming` — `stream: false`; mock complete JSON response; verify body readable
- `TestChat_AuthHeader` — intercept request; assert `Authorization: Bearer test-key` header present
- `TestChat_RateLimit_Retry` — mock 429 then 200; verify request retried and success returned
- `TestChat_RateLimit_Exhausted` — mock 429 three times; verify error returned after max retries
- `TestChat_ContextCancellation` — cancel context mid-request; verify function returns promptly
- `TestListModels` — mock `/v1/models`; verify model slice parsed correctly
- `TestListModels_Empty` — empty models response; verify empty slice (not error)

**Acceptance criteria:**
- With a valid API key and `curl`, a request to the proxy returns a streamed response
- 429 errors are retried automatically
- `go test ./internal/proxy/...` passes with mocked HTTP server

---

## Issue 0.6 — OpenAI-compatible REST API server (passthrough mode)

**Context:** The binary needs to expose `POST /v1/chat/completions` that is fully OpenAI-compatible, so any existing tool (Cursor, Continue.dev, etc.) can point at it immediately.

**Tasks:**
- Add dependency: `github.com/go-chi/chi/v5` (lightweight router)
- Create `internal/api/openai.go` with:
  - `NewOpenAIHandler(proxy *proxy.Client, pipeline *pipeline.Enricher) http.Handler`
  - `POST /v1/chat/completions`:
    - Parse request body as `ChatRequest`
    - In passthrough mode: forward directly to `proxy.Chat()`
    - Stream SSE response back: `text/event-stream`, `data: {...}\n\n` format
    - Handle `stream: false` (buffer full response, return as JSON)
  - `GET /v1/models`: return model list from OpenRouter
  - `GET /health`: simple health check endpoint
- Create `cmd/tbyd/main.go`:
  - Load config
  - Run Ollama startup checks
  - Open SQLite store
  - Start HTTP server on configured port
  - Graceful shutdown on SIGINT/SIGTERM
- Bind server address to `127.0.0.1` only (never expose to network by default)

**Unit tests** (`internal/api/openai_test.go`) — use `httptest.NewRecorder` and a mock `proxy.Client`:
- `TestHealth` — GET `/health`; verify 200 and `{"status":"ok"}`
- `TestChatCompletions_Streaming` — POST with `stream: true`; mock proxy returns SSE; verify `Content-Type: text/event-stream` and body forwarded
- `TestChatCompletions_NonStreaming` — POST with `stream: false`; verify JSON response body
- `TestChatCompletions_InvalidBody` — POST malformed JSON; verify 400 status
- `TestChatCompletions_MissingMessages` — POST with empty messages array; verify 400 status
- `TestModels` — GET `/v1/models`; mock proxy returns model list; verify JSON array
- `TestBindsToLoopback` — verify server listener address starts with `127.0.0.1`

**Integration test** (`internal/api/openai_integration_test.go`) — use `go test -tags integration`:
- `TestPassthroughRoundTrip` — start a full test server with a mock OpenRouter backend; POST a chat request; verify SSE response streams through unchanged and completes

**Acceptance criteria:**
- `./bin/tbyd` starts, prints `"tbyd listening on localhost:4000"`
- `curl -N localhost:4000/v1/chat/completions` with a valid request body returns a streamed response
- Ctrl+C shuts down cleanly
- Server only listens on loopback interface

---

## Phase 0 Verification

Run this checklist before declaring Phase 0 complete:

1. `make build` succeeds, binary runs
2. With no config: startup prints helpful error about missing API key
3. With config: startup checks Ollama, pulls models if missing
4. `curl localhost:4000/health` → `{"status":"ok"}`
5. `curl localhost:4000/v1/models` → JSON array of available models
6. `curl -N -X POST localhost:4000/v1/chat/completions -d '{"model":"anthropic/claude-haiku-4-5-20251001","messages":[{"role":"user","content":"hello"}],"stream":true}'` → SSE stream
7. `go test ./...` passes
8. `go test -tags integration ./...` passes
