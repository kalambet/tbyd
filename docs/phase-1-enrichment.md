# Phase 1 — Basic Enrichment Pipeline

> **Goal:** Every query passing through the proxy is enriched with user context before being forwarded to the cloud. By the end of this phase, the system retrieves relevant context, injects user profile, and composes a structured prompt — even if the user's knowledge base is initially sparse.

---

## Issue 1.1 — Vector store integration and embedding pipeline

**Context:** Context retrieval is based on semantic search over stored embeddings. The VectorStore interface abstracts vector storage. The default backend stores embeddings as BLOBs in SQLite with brute-force cosine similarity search, sufficient for ~100K vectors. `nomic-embed-text` via Ollama generates the embeddings.

**Tasks:**
- Create `internal/retrieval/store.go` with:
  - `Store` struct with connection to VectorStore (SQLite backend)
  - `CreateTable(name string, schema Schema) error`
  - `Insert(table string, records []Record) error`
  - `Search(table string, vector []float32, topK int, filter string) ([]Record, error)`
  - `Delete(table string, id string) error`
- Define record schemas for:
  - `context_vectors` table: `{id, source_id, source_type, text_chunk, embedding, created_at, tags}`
- Create `internal/retrieval/embedder.go`:
  - `Embedder` struct wrapping `ollama.Client`
  - `Embed(text string) ([]float32, error)` — calls Ollama `nomic-embed-text`
  - `EmbedBatch(texts []string) ([][]float32, error)` — batch embedding for ingestion
- Create `internal/retrieval/retriever.go`:
  - `Retrieve(query string, topK int) ([]ContextChunk, error)` — embeds query, searches VectorStore, returns ranked chunks
  - `RetrieveByIDs(ids []string) ([]ContextChunk, error)`
- On startup: initialize VectorStore tables if not present

**Unit tests** (`internal/retrieval/embedder_test.go`) — mock `ollama.Client`:
- `TestEmbed_ReturnsDimension` — mock returns a 768-dim vector; verify length
- `TestEmbed_OllamaError` — mock returns error; verify error propagated (not panicked)
- `TestEmbedBatch_CountMatches` — call with 3 texts; verify 3 vectors returned
- `TestEmbedBatch_EmptyInput` — call with nil slice; verify empty result (not error)

**Unit tests** (`internal/retrieval/store_test.go`) — use an in-memory SQLite:
- `TestInsertAndSearch` — insert a record with a known vector; search with the same vector; verify record returned with score > 0.99
- `TestSearch_TopK` — insert 10 records; search with topK=3; verify exactly 3 returned
- `TestSearch_EmptyTable` — search before any inserts; verify empty slice (not error)
- `TestDelete` — insert a record, delete it, search; verify it no longer appears
- `TestTableCreation_Idempotent` — call `CreateTable` twice with the same name; verify no error

**Integration test** (`internal/retrieval/retriever_integration_test.go`) — tag `integration`, requires Ollama running:
- `TestRetrieveSemanticMatch` — insert "Go is a compiled language" doc; retrieve with query "compiled programming language"; verify doc returned with score > 0.7

**Acceptance criteria:**
- Can insert a text chunk and retrieve it by semantic similarity
- `go test ./internal/retrieval/...` passes with in-memory SQLite (`:memory:`)
- Embedding round-trip latency < 200ms for a single sentence on Apple Silicon

---

## Issue 1.2 — Intent extraction via local LLM

**Context:** Before retrieving context, the system asks the fast local model (phi3.5) to extract structured intent from the user's query. This drives more precise context retrieval than embedding the raw query alone.

**Tasks:**
- Create `internal/intent/extractor.go` with:
  - `Extractor` struct wrapping `ollama.Client`
  - `Extract(query string, recentHistory []Message) (Intent, error)`
  - `Intent` struct:
    ```go
    type Intent struct {
        IntentType   string   // "recall", "task", "question", "preference_update"
        Entities     []string // named entities mentioned
        Topics       []string // semantic topic tags
        ContextNeeds []string // what kind of context would help
        IsPrivate    bool     // user flagged as sensitive
    }
    ```
  - System prompt for extraction (calibrated based on user profile — inject profile summary into system prompt)
  - Use Ollama `format: "json"` with explicit JSON schema in the prompt
  - Timeout: 3s (fast model, should not block UX)
  - Fallback: if extraction times out or fails, return a zero-value Intent and proceed with raw query embedding only
- Define the JSON schema prompt template in `internal/intent/prompt.go`
- Write unit tests with mock Ollama responses

**Unit tests** (`internal/intent/extractor_test.go`) — mock `ollama.Client`:
- `TestExtract_RecallIntent` — mock returns `{"intent_type":"recall","entities":["database schema"],...}`; verify `Intent.IntentType == "recall"` and entities populated
- `TestExtract_TaskIntent` — mock returns task-type JSON; verify type and topics parsed
- `TestExtract_MalformedJSON` — mock returns invalid JSON; verify zero-value `Intent` returned (not error)
- `TestExtract_Timeout` — mock hangs > 3s; verify function returns within 3.5s with zero-value intent
- `TestExtract_OllamaDown` — mock returns error; verify zero-value intent returned, no panic
- `TestExtract_PrivateFlag` — mock returns `{"is_private":true,...}`; verify `Intent.IsPrivate == true`
- `TestExtract_EmptyQuery` — pass empty string; verify no panic, returns zero-value intent

**Unit tests** (`internal/intent/prompt_test.go`):
- `TestPromptContainsSchema` — call `BuildPrompt(query, history, profile)`; verify output contains the JSON schema definition
- `TestPromptInjectsProfile` — pass a profile with tone "direct"; verify profile summary appears in the prompt string
- `TestPromptHistory` — pass 3-message history; verify all 3 appear in the prompt

**Acceptance criteria:**
- Given a query like `"what did I decide about the database schema last week"`, extracts:
  ```json
  {"intent_type": "recall", "entities": ["database schema"], "topics": ["architecture", "decisions"], "context_needs": ["past_decisions", "technical_notes"]}
  ```
- Extraction completes in < 3s on Apple Silicon with phi3.5
- Timeout/failure does not crash the pipeline

---

## Issue 1.3 — Local engine abstraction (backend-agnostic inference)

**Context:** The codebase currently depends directly on `internal/ollama.Client` for chat, embedding, and model management. MLX-based inference servers (mlx-lm, oMLX) offer 20–40% faster inference and ~50% less memory on Apple Silicon, but the ecosystem is still immature. By abstracting the local inference backend behind an `Engine` interface now, we can swap from Ollama to MLX (or any OpenAI-compatible local server) later without touching consumers like intent extraction, embedding, or the enrichment pipeline.

**Tasks:**
- Create `internal/engine/engine.go` with the `Engine` interface:
  ```go
  type Engine interface {
      // Chat sends messages to the given model and returns the assistant's response.
      // When jsonSchema is non-nil, structured JSON output is requested.
      Chat(ctx context.Context, model string, messages []Message, jsonSchema *Schema) (string, error)

      // Embed returns the embedding vector for the given text using the specified model.
      Embed(ctx context.Context, model string, text string) ([]float32, error)

      // IsRunning reports whether the inference backend is reachable.
      IsRunning(ctx context.Context) bool

      // ListModels returns the names of all locally available models.
      ListModels(ctx context.Context) ([]string, error)

      // HasModel reports whether the given model name is available locally.
      HasModel(ctx context.Context, name string) bool

      // PullModel downloads a model. The optional callback receives progress updates.
      PullModel(ctx context.Context, name string, onProgress func(PullProgress)) error
  }
  ```
- Define shared types in `internal/engine/types.go`:
  - `Message` (move from `ollama.Message`)
  - `Schema`, `SchemaProperty` (move from `ollama.Schema`, `ollama.SchemaProperty`)
  - `PullProgress` (mirrors `ollama.pullProgress`, now exported)
- Create `internal/engine/startup.go`:
  - `EnsureReady(ctx context.Context, e Engine, fastModel, embedModel string, w io.Writer) error` — move logic from `ollama.EnsureReady`, now takes `Engine` instead of `*ollama.Client`
- Create `internal/engine/ollama.go`:
  - `OllamaEngine` struct wrapping the existing `internal/ollama.Client`
  - Implement all `Engine` methods by delegating to the underlying `ollama.Client`
  - Constructor: `NewOllamaEngine(baseURL string) *OllamaEngine`
  - The `internal/ollama` package remains as-is (HTTP client); `OllamaEngine` is the adapter
- Create `internal/engine/detect.go`:
  - `Detect(cfg Config) (Engine, error)` — for now, always returns `OllamaEngine`; future: probe for MLX server on a configurable port, return `MLXEngine` if available
  - Accept a simple config struct or the relevant fields (base URL, etc.)
- Create `internal/engine/mlx.go` (stub):
  - `MLXEngine` struct with placeholder fields
  - All `Engine` methods return `fmt.Errorf("mlx engine not yet implemented")`
  - Comment documenting the planned MLX integration path
- Update `cmd/tbyd/main.go`:
  - Replace `ollama.New(...)` + `ollama.EnsureReady(...)` with `engine.Detect(...)` + `engine.EnsureReady(...)`
  - Pass the `Engine` to downstream consumers instead of `*ollama.Client`
- Update `internal/retrieval/embedder.go` (from issue 1.1):
  - Change `Embedder` to accept `engine.Engine` instead of `ollama.Client`
- Do **not** change config keys yet — `ollama.*` keys are fine since Ollama is the only backend; generalize when MLX support lands

**Unit tests** (`internal/engine/ollama_test.go`) — use httptest server (migrate relevant tests from `internal/ollama/client_test.go`):
- `TestOllamaEngine_Chat` — mock HTTP returns assistant response; verify `Engine.Chat` returns it
- `TestOllamaEngine_Embed` — mock HTTP returns embedding vector; verify `Engine.Embed` returns it
- `TestOllamaEngine_IsRunning` — mock returns 200 on `/api/tags`; verify `true`
- `TestOllamaEngine_IsRunning_Down` — closed server; verify `false`
- `TestOllamaEngine_HasModel` — mock returns model list; verify present/absent detection
- `TestOllamaEngine_PullModel` — mock streams progress JSON; verify progress callback invoked

**Unit tests** (`internal/engine/startup_test.go`) — mock `Engine`:
- `TestEnsureReady_AllModelsPresent` — mock `HasModel` returns true for both; verify no `PullModel` calls
- `TestEnsureReady_PullsMissing` — mock `HasModel` returns false; verify `PullModel` called
- `TestEnsureReady_EngineDown` — mock `IsRunning` returns false; verify error returned

**Unit tests** (`internal/engine/detect_test.go`):
- `TestDetect_ReturnsOllama` — verify `Detect` returns an `*OllamaEngine`

**Acceptance criteria:**
- All existing consumers (`retrieval.Embedder`, `intent.Extractor`, `cmd/tbyd/main.go`) use `engine.Engine` instead of `*ollama.Client` directly
- `go test ./internal/engine/...` passes
- `go test ./internal/ollama/...` still passes (package unchanged, still used internally by `OllamaEngine`)
- No behavioral change — the system works identically to before the refactor
- Future MLX integration requires only implementing `engine.Engine` in `mlx.go` and updating `Detect()`

---

## Issue 1.4 — Context retrieval integration

**Context:** Using extracted intent + original query, retrieve the most relevant stored context chunks from the VectorStore. The retrieval step combines semantic search with metadata filtering.

**Tasks:**
- Extend `internal/retrieval/retriever.go`:
  - `RetrieveForIntent(query string, intent Intent, topK int) ([]ContextChunk, error)`:
    1. Embed original query
    2. If intent has entities: also embed each entity separately and merge results
    3. Filter by `intent.Topics` if present (VectorStore filter expression (structured fields, not free-form SQL))
       > Note: SQLite VectorStore backend currently ignores filter strings; filtering is best-effort via metadata matching. Full filter support comes with the LanceDB migration.
    4. Deduplicate by `source_id`
    5. Return top-K ranked by score
  - `ContextChunk` struct: `{ID, SourceID, SourceType, Text, Score, Tags, CreatedAt}`
- Define `topK` default: 5 chunks, configurable in config

**Unit tests** (`internal/retrieval/retriever_test.go`) — mock `Embedder` and `Store`:
- `TestRetrieveForIntent_NoEntities` — intent with no entities; verify single embed + search called
- `TestRetrieveForIntent_WithEntities` — intent with 2 entities; verify embed called 3 times (query + 2 entities), results merged
- `TestRetrieveForIntent_Deduplication` — mock returns same `source_id` from two searches; verify deduplicated in output
- `TestRetrieveForIntent_TopKRespected` — mock returns 10 results; topK=3; verify exactly 3 returned
- `TestRetrieveForIntent_EmptyKnowledgeBase` — mock returns empty results; verify empty slice (not error)
- `TestRetrieveForIntent_EmbedFails` — embedder returns error; verify empty slice returned gracefully

**Acceptance criteria:**
- Given stored context about "database schema decision", a query about "database architecture" retrieves it with score > 0.7
- When knowledge base is empty, returns empty slice (no error)
- `go test ./internal/retrieval/...` includes retrieval integration test

---

## Issue 1.5 — User profile manager

**Context:** The user profile is the "digital self" — structured JSON stored in SQLite. It is read at enrichment time and injected into the prompt. Must be fast to read.

**Tasks:**
- Create `internal/profile/manager.go`:
  - `Manager` struct wrapping `storage.Store`
  - `GetProfile() (Profile, error)` — reads and parses all profile keys from SQLite
  - `SetField(key string, value interface{}) error`
  - `GetSummary() string` — returns a compressed string representation for prompt injection (< 500 tokens)
  - Cache profile in memory with 60s TTL (profile doesn't change frequently)
- Define `Profile` struct in `internal/profile/types.go`:
  ```go
  type Profile struct {
      Identity      IdentityProfile
      Communication CommunicationProfile
      Interests     []string
      Expertise     map[string]string
      Opinions      []string
      Preferences   []string
  }
  ```
- Implement `GetSummary()` to produce a compact string:
  ```
  User: software engineer, expert in Go/distributed systems. Prefers: direct tone, markdown+code, no hedging. Interests: privacy tech, AI infra, distributed systems.
  ```
  This is what gets injected into the system prompt.

**Unit tests** (`internal/profile/manager_test.go`) — use in-memory storage mock:
- `TestGetProfile_Empty` — empty store; verify zero-value `Profile` returned (not error)
- `TestSetAndGetField` — set `communication.tone = "direct"`; call `GetProfile()`; verify field is present
- `TestGetSummary_Empty` — empty profile; verify non-empty string returned with at least a placeholder
- `TestGetSummary_Full` — populate all fields; verify summary contains role, tone, and at least one interest
- `TestGetSummary_TokenBudget` — populate 50 preferences; verify `len(GetSummary())/4 < 500`
- `TestCacheTTL` — set field; immediately call `GetProfile()` twice; verify store is only queried once (cache hit on second call)
- `TestCacheInvalidation` — set field; wait for TTL to expire (use injectable clock); verify store is re-queried

**Acceptance criteria:**
- `GetProfile()` returns a zero-value `Profile` (not an error) when profile is empty
- `GetSummary()` returns a non-empty string even for a partially populated profile
- Profile updates via `SetField()` reflect in `GetProfile()` within the cache TTL

---

## Issue 1.6 — Prompt composer

**Context:** Assembles the enriched prompt from: user profile summary, retrieved context chunks, and the original user query. The output is a structured `ChatRequest` ready to send to OpenRouter.

**Tasks:**
- Create `internal/composer/prompt.go`:
  - `Composer` struct with configurable `MaxContextTokens int` (default: 4000)
  - `Compose(originalReq ChatRequest, intent Intent, chunks []ContextChunk, profile Profile) ChatRequest`
    - Build a new system message:
      ```
      [User Profile]
      <profile summary>

      [Relevant Context]
      <chunk 1: title + text>
      <chunk 2: title + text>
      ...
      ```
    - Prepend to the original messages array
    - If original request already has a system message, merge (profile + context go first)
    - Apply token budget: if context exceeds `MaxContextTokens`, truncate lowest-scoring chunks first
    - Preserve original user messages unchanged
  - `EstimateTokens(text string) int` — heuristic: `len(text)/4`
- Write tests for composition with empty, partial, and full context

**Unit tests** (`internal/composer/prompt_test.go`):
- `TestCompose_EmptyContext` — no chunks, no profile; verify original request returned with minimal system message
- `TestCompose_ProfileInjected` — profile with tone "direct"; verify system message contains the summary
- `TestCompose_ChunksAppended` — 2 chunks; verify both appear in system message in score order
- `TestCompose_ExistingSystemMessage` — original request has a system message; verify it is preserved and appended after profile+context
- `TestCompose_TokenBudget` — 20 chunks totalling > 4000 tokens; verify only highest-scoring chunks included and total stays under budget
- `TestCompose_LowestScoringChunkDropped` — exactly at budget with chunk A (score 0.9) and chunk B (score 0.5) that together exceed limit; verify B dropped, A kept
- `TestCompose_UserMessagesUnchanged` — verify every user message in output is byte-identical to input
- `TestEstimateTokens` — verify `EstimateTokens("hello world") == 2` (10 chars / 4 = 2)

**Acceptance criteria:**
- Composed request's system message contains both profile summary and context chunks
- Token budget is respected — no chunk added that would exceed the limit
- Original user messages are byte-identical to input

---

## Issue 1.7 — Enrichment pipeline orchestrator

**Context:** Wires together intent extraction, retrieval, profile loading, and prompt composition into a single pipeline called by the API handler.

**Tasks:**
- Create `internal/pipeline/enrichment.go`:
  - `Enricher` struct holding references to: `intent.Extractor`, `retrieval.Retriever`, `profile.Manager`, `composer.Composer`, `storage.Store`
  - `Enrich(ctx context.Context, req ChatRequest) (ChatRequest, EnrichmentMetadata, error)`
    1. Extract intent from last user message (with 3s timeout, fallback on failure)
    2. Retrieve context chunks for intent
    3. Load profile summary
    4. Compose enriched request
    5. Return enriched request + metadata (which chunks were used, intent, timing)
  - `EnrichmentMetadata` struct: `{IntentExtracted, ChunksUsed []string, EnrichmentDurationMs int64}`
- Update `internal/api/openai.go`:
  - Switch from passthrough to enrichment mode
  - Call `Enricher.Enrich()` before forwarding to OpenRouter
  - Log `EnrichmentMetadata` for debugging (debug log level)

**Unit tests** (`internal/pipeline/enrichment_test.go`) — mock all dependencies:
- `TestEnrich_FullPipeline` — mock extractor returns intent, mock retriever returns 2 chunks, mock profile returns summary; verify composed request contains all three
- `TestEnrich_IntentExtractorFails` — mock extractor returns error; verify pipeline continues with zero-value intent and original request still enriched with profile
- `TestEnrich_RetrievalFails` — mock retriever returns error; verify pipeline continues with empty chunks (profile still injected)
- `TestEnrich_ProfileEmpty` — mock profile returns empty; verify enrichment still succeeds
- `TestEnrich_MetadataPopulated` — after enrichment, verify `ChunksUsed` matches IDs from retrieved chunks
- `TestEnrich_DurationTracked` — mock retriever adds 100ms delay; verify `EnrichmentDurationMs >= 100`
- `TestEnrich_ContextCancelled` — cancel context before call; verify function returns promptly

**Integration test** (`internal/pipeline/enrichment_integration_test.go`) — tag `integration`, requires Ollama:
- `TestEnrichEndToEnd` — insert a context doc, run enrichment for a related query, verify the doc's text appears in the composed system message

**Acceptance criteria:**
- End-to-end: a request with stored context is enriched and the enriched prompt contains the context
- If enrichment fails at any step, the original request is forwarded unchanged (graceful degradation)
- Enrichment adds < 1.5s latency on Apple Silicon (phi3.5 fast model)

---

## Phase 1 Verification

Run this checklist before declaring Phase 1 complete:

1. Add a context document manually to the VectorStore (via test script or SQLite insert)
2. Send a related query via `curl` → verify the enriched prompt in the interaction log contains the context
3. Send an unrelated query → verify no spurious context is injected
4. Kill Ollama mid-request → verify the proxy gracefully falls back to passthrough
5. Set profile field `tone = "direct"` → verify it appears in the enriched system prompt
6. Check enrichment latency with `time curl ...` — should be under 2s total
7. `go test ./...` passes
8. `go test -tags integration ./...` passes
