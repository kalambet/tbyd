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

## Issue 1.8 — Hybrid retrieval (BM25 + vector search with adaptive blending)

**Context:** The current retrieval pipeline uses vector-only search (cosine similarity on embeddings). This works well for semantic/conceptual queries but underperforms for entity-heavy queries where exact keyword matches are more reliable. Combining BM25 keyword scoring (via SQLite FTS5) with vector similarity provides better recall across query types. The intent extractor drives the blend ratio based on query classification.

**Tasks:**
- Add SQLite FTS5 virtual table for keyword search:
  - Create migration `XXX_add_fts5.sql`:
    ```sql
    CREATE VIRTUAL TABLE context_vectors_fts USING fts5(
        text_chunk,
        content='context_vectors',
        content_rowid='rowid'
    );
    -- Triggers to keep FTS index in sync with context_vectors
    CREATE TRIGGER context_vectors_ai AFTER INSERT ON context_vectors BEGIN
        INSERT INTO context_vectors_fts(rowid, text_chunk) VALUES (new.rowid, new.text_chunk);
    END;
    CREATE TRIGGER context_vectors_ad AFTER DELETE ON context_vectors BEGIN
        INSERT INTO context_vectors_fts(context_vectors_fts, rowid, text_chunk) VALUES('delete', old.rowid, old.text_chunk);
    END;
    CREATE TRIGGER context_vectors_au AFTER UPDATE ON context_vectors BEGIN
        INSERT INTO context_vectors_fts(context_vectors_fts, rowid, text_chunk) VALUES('delete', old.rowid, old.text_chunk);
        INSERT INTO context_vectors_fts(rowid, text_chunk) VALUES (new.rowid, new.text_chunk);
    END;
    ```
  - Backfill: migration populates FTS from existing `context_vectors` rows
- Extend `VectorStore` interface in `internal/retrieval/vectorstore.go`:
  ```go
  SearchKeyword(table string, query string, topK int, filter string) ([]ScoredRecord, error)
  SearchHybrid(table string, vector []float32, query string, topK int, vectorWeight float32, filter string) ([]ScoredRecord, error)
  ```
  > The `filter` parameter matches the existing `Search()` signature. This ensures hybrid search can restrict results by `source_type`, `tags`, or `intent.Topics` — without it, hybrid search would be a functional regression over vector-only search which already accepts filters.
- Implement `SearchKeyword` on `SQLiteStore`:
  - Query FTS5: `SELECT rowid, rank FROM context_vectors_fts WHERE text_chunk MATCH ? ORDER BY rank LIMIT ?`
  - Fetch full records by rowid
  - Normalize BM25 scores to 0–1 range (min-max normalization)
- Implement `SearchHybrid` on `SQLiteStore`:
  - Run vector search and keyword search in parallel (goroutines)
  - Score fusion (configurable via `retrieval.fusion_method`):
    - **"rrf"** (default, Reciprocal Rank Fusion): `score = 1/(k + vector_rank) + 1/(k + keyword_rank)` with k=60. Uses rank positions only — robust to outlier scores and stable across unbounded BM25 score ranges.
    - **"weighted"**: min-max normalize each signal to 0–1 per-query (using min/max from the current result set), blend: `final_score = vectorWeight * vector_score + (1 - vectorWeight) * keyword_score`. More interpretable but sensitive to outliers since BM25 scores are unbounded.
  - Deduplicate by record ID, keep highest fused score
  - Return top-K sorted by fused score
- Extend `Intent` struct in `internal/intent/extractor.go`:
  ```go
  type Intent struct {
      IntentType     string   // "recall", "task", "question", "preference_update"
      Entities       []string
      Topics         []string
      ContextNeeds   []string
      IsPrivate      bool
      SearchStrategy string   // "vector_only", "hybrid", "keyword_heavy"
      HybridRatio    float64  // 0.0 = all keyword, 1.0 = all vector (default 0.7)
      SuggestedTopK  int      // 0 = use default (5)
  }
  ```
- Update intent extraction prompt in `internal/intent/prompt.go` to include new output fields
- Update `Retriever.RetrieveForIntent()` to use hybrid search:
  - Read `SearchStrategy` and `HybridRatio` from intent
  - If "vector_only" or intent extraction failed: use existing `Search()` (no change)
  - If "hybrid" or "keyword_heavy": use `SearchHybrid()` with intent's ratio
  - Use `SuggestedTopK` if non-zero, else default topK

**Unit tests** (`internal/retrieval/store_test.go`):
- `TestSearchKeyword_MatchesExact` — insert doc with "Kubernetes deployment"; keyword search "Kubernetes"; verify doc returned
- `TestSearchKeyword_NoMatch` — keyword search with unrelated term; verify empty result
- `TestSearchKeyword_TopK` — insert 10 docs; search with topK=3; verify exactly 3 returned
- `TestSearchHybrid_BlendsScores` — insert doc that scores high on keyword but low on vector; verify hybrid score reflects blend ratio
- `TestSearchHybrid_DeduplicatesResults` — same doc appears in both keyword and vector results; verify single entry with blended score
- `TestSearchHybrid_VectorOnlyFallback` — empty keyword query; verify vector-only results returned

**Unit tests** (`internal/intent/extractor_test.go`):
- `TestExtract_SearchStrategy` — mock returns `{"search_strategy":"hybrid","hybrid_ratio":0.6,...}`; verify fields populated
- `TestExtract_DefaultStrategy` — mock returns intent without search fields; verify defaults applied (SearchStrategy="" treated as "hybrid", HybridRatio=0.7)

**Acceptance criteria:**
- Entity-heavy query "find docs about Kubernetes" retrieves docs containing "Kubernetes" even if embedding similarity is moderate
- Conceptual query "how to approach testing" retrieves semantically relevant docs even without keyword overlap
- Hybrid search outperforms vector-only search on a mixed benchmark (entity + semantic queries)
- FTS5 triggers keep keyword index in sync with insert/delete/update on `context_vectors`
- `go test ./internal/retrieval/...` passes

---

## Issue 1.9 — Retrieval reranking

**Context:** After hybrid retrieval returns top-K candidates ranked by a blend of cosine similarity and BM25, the ordering may not reflect true query relevance. An LLM-based reranker re-scores each (query, chunk) pair to produce a more accurate relevance ranking before prompt composition. This catches "related but not useful" chunks that score well on surface similarity. Phase 1 uses the existing fast local model (phi3.5) for reranking; a dedicated cross-encoder model (faster, more accurate) is planned as a post-Phase 1 optimization once the `Reranker` interface is proven.

**Tasks:**
- Create `internal/reranking/reranker.go`:
  - `Reranker` interface:
    ```go
    type Reranker interface {
        Rerank(ctx context.Context, query string, chunks []retrieval.ContextChunk) ([]retrieval.ContextChunk, error)
    }
    ```
  - `LLMReranker` struct wrapping `engine.Engine`:
    - For each chunk, calls the fast model (phi3.5) with a scoring prompt:
      ```
      Rate the relevance of the following text to the query on a scale of 0.0 to 1.0.
      Query: {query}
      Text: {chunk.Text}
      Respond with only a JSON object: {"score": <float>}
      ```
    - Batches scoring calls concurrently (bounded concurrency, e.g., 3 at a time)
    - Timeout: configurable, default 5s total (not per-chunk). Uses early-return: once at least `topK` chunks have been scored (e.g., 5 of 10), return the scored subset immediately without waiting for remaining chunks. The timeout is only a hard cap for the case where even the minimum hasn't been scored yet.
    - Re-sorts chunks by reranker score descending
    - Drops chunks with score < threshold (default: 0.3, configurable)
  - `NoOpReranker` — returns chunks unchanged (passthrough)
  - Constructor: `NewReranker(engine engine.Engine, enabled bool, timeout time.Duration) Reranker`
    - Returns `LLMReranker` if enabled, `NoOpReranker` otherwise
- Update `internal/pipeline/enrichment.go`:
  - Add `reranker reranking.Reranker` field to `Enricher` struct
  - Insert reranking step between retrieval and composition:
    ```go
    chunks := e.retriever.RetrieveForIntent(ctx, lastUserMsg, extracted, topK)
    chunks, err = e.reranker.Rerank(ctx, lastUserMsg, chunks)
    if err != nil {
        slog.Warn("reranking failed, using original order", "error", err)
        // proceed with original chunks — graceful degradation
    }
    ```
  - Track reranking duration in `EnrichmentMetadata`
- Config keys:
  - `enrichment.reranking_enabled` (default: true)
  - `enrichment.reranking_timeout` (default: "5s")
  - `enrichment.reranking_threshold` (default: 0.3)

**Unit tests** (`internal/reranking/reranker_test.go`) — mock `engine.Engine`:
- `TestLLMReranker_ReordersChunks` — mock returns scores [0.9, 0.3, 0.7] for 3 chunks; verify output order is [0.9, 0.7, 0.3]
- `TestLLMReranker_DropsLowScore` — mock returns score 0.1 for one chunk (below threshold 0.3); verify chunk is dropped
- `TestLLMReranker_Timeout` — create reranker with 2s timeout; mock hangs; verify function returns within 2.5s with original chunks (graceful degradation)
- `TestLLMReranker_MalformedJSON` — mock returns invalid JSON for one chunk; verify chunk retains its original retrieval score (not dropped, not 0.0, not crash). Preserving the original score is safer than penalizing a chunk just because the reranker failed to parse its response.
- `TestLLMReranker_EarlyReturn` — 10 chunks, mock scores 5 quickly and hangs on the rest; verify function returns the 5 scored chunks without waiting for timeout
- `TestLLMReranker_EmptyChunks` — pass empty slice; verify empty slice returned (no error)
- `TestNoOpReranker` — verify chunks returned in original order unchanged
- `TestNewReranker_Enabled` — verify returns `*LLMReranker`
- `TestNewReranker_Disabled` — verify returns `*NoOpReranker`

**Unit tests** (`internal/pipeline/enrichment_test.go`):
- `TestEnrich_WithReranker` — mock reranker reorders chunks; verify composed request uses reranked order
- `TestEnrich_RerankerFails` — mock reranker returns error; verify enrichment proceeds with original chunk order

**Acceptance criteria:**
- Reranker improves retrieval precision: top-3 chunks after reranking are more query-relevant than top-3 by cosine similarity alone
- Reranking adds < 3s latency for 10 chunks on Apple Silicon (bounded concurrency of 3, ~1s per LLM call)
- Pipeline gracefully degrades if reranker fails (logs warning, uses original order)
- `go test ./internal/reranking/...` passes
- `go test ./internal/pipeline/...` passes

---

## Issue 1.10 — Query-level caching (exact + semantic)

**Context:** Every query currently runs the full enrichment pipeline (intent extraction + embedding + search + reranking + composition), even for identical or very similar queries. A two-level cache avoids redundant work: Level 1 catches exact repeated queries, Level 2 catches semantically similar rephrasings.

**Tasks:**
- Create `internal/cache/query.go`:
  - `QueryCache` struct:
    ```go
    type QueryCache struct {
        mu             sync.RWMutex
        exactCache     map[string]CachedEnrichment       // SHA256(normalized_query) → result
        semanticCache  map[string]SemanticEntry           // query hash → embedding + result
        semanticIndex  *VPTree                            // vantage-point tree for ANN lookup
        embedder       retrieval.Embedder
        enabled        bool
        semThreshold   float64        // cosine similarity threshold for L2 hit (default 0.92)
        exactTTL       time.Duration  // default 5m
        semanticTTL    time.Duration  // default 30m
    }

    type CachedEnrichment struct {
        EnrichedRequest proxy.ChatRequest
        Metadata        pipeline.EnrichmentMetadata
        CachedAt        time.Time
    }

    type SemanticEntry struct {
        QueryHash  string
        Embedding  []float32
        Result     CachedEnrichment
        CachedAt   time.Time
    }
    ```
    > **Scaling note:** The semantic cache uses a vantage-point tree (VP-tree) for
    > O(log n) nearest-neighbor lookup instead of linear scanning. A VP-tree is
    > simple to implement for cosine distance over 768-d embeddings and avoids the
    > O(n) bottleneck of a slice scan.
    >
    > **Lazy rebuild:** The tree is NOT rebuilt on every `Set()` — that would be
    > O(N) per insert and a bottleneck during bulk operations. Instead, `Set()`
    > marks the tree as dirty. The tree is rebuilt lazily on the next `Get()` that
    > requires an L2 lookup, amortizing the cost. The background eviction goroutine
    > also rebuilds if the tree is dirty after evicting expired entries.
    >
    > If the cache grows beyond ~10K entries in the future, swap to an HNSW index
    > (e.g., via a Go binding to `hnswlib`).
  - `Get(ctx context.Context, query string) (CachedEnrichment, []float32, bool)`:
    1. Normalize query (lowercase, collapse whitespace)
    2. Level 1: check exact cache by SHA256 hash → return if found and not expired
    3. Level 2: embed query **once**, query VP-tree for nearest neighbor within threshold → return if found and not expired
    4. On miss: return the pre-computed embedding as second return value so the caller can reuse it for retrieval (avoids re-embedding the same query)
  - `Set(ctx context.Context, query string, queryEmbedding []float32, result CachedEnrichment)`:
    1. Normalize and hash query
    2. Store in exact cache
    3. Store pre-computed embedding in semantic cache map, mark VP-tree as dirty (lazy rebuild on next `Get()`)
  - `Invalidate()` — clear both caches and reset VP-tree (called on profile update or bulk operations)
  - `InvalidateByTopics(topics []string)` — selective invalidation:
    1. Evict cached entries whose intent topics overlap with the given topics
    2. Evict cached entries that have no topic metadata (since we can't determine if they're affected)
    3. Preserve cached entries with topic metadata that does NOT overlap (known-safe)
    This is more precise than full invalidation — entries with clear, non-overlapping topics remain cached.
  - Background goroutine: evict expired entries every 60s; if entries were evicted and tree was already dirty, rebuild VP-tree
- Constructor: `NewQueryCache(embedder, enabled, semThreshold, exactTTL, semanticTTL) *QueryCache`
- Update `internal/pipeline/enrichment.go`:
  - Add `cache *cache.QueryCache` field to `Enricher`
  - Before pipeline steps: check cache → on hit, return cached result with `meta.CacheHit = true`
  - On cache miss: `Get()` returns the pre-computed query embedding → pass it to `Retriever.RetrieveForIntent()` via a `WithEmbedding` option to avoid re-embedding the same query
  - After pipeline completes: store result in cache with the same pre-computed embedding (include intent topics in `CachedEnrichment` for selective invalidation)
  - Add `CacheHit bool` and `CacheLevel string` fields to `EnrichmentMetadata`
  > **Embedding reuse:** The query embedding is computed at most once per request. The cache `Get()` computes it for L2 lookup and returns it on miss. The retriever and cache `Set()` both accept the pre-computed vector, eliminating redundant embedding calls.
- Wire cache invalidation:
  - In `internal/profile/manager.go`: on `SetField()`, call `cache.Invalidate()` (full — profile changes affect all enrichments)
  - In ingestion handler: after storing new context doc, call `cache.InvalidateByTopics(doc.Topics)` if topics available, else `cache.Invalidate()`
- Config keys:
  - `enrichment.cache_enabled` (default: true)
  - `enrichment.cache_semantic_threshold` (default: 0.92)
  - `enrichment.cache_exact_ttl` (default: "5m")
  - `enrichment.cache_semantic_ttl` (default: "30m")

**Unit tests** (`internal/cache/query_test.go`) — mock embedder:
- `TestExactCacheHit` — set entry, get with identical query; verify hit
- `TestExactCacheMiss` — get with different query; verify miss
- `TestExactCacheTTL` — set entry, advance clock past TTL; verify miss
- `TestSemanticCacheHit` — set entry; get with query whose embedding has cosine similarity > threshold; verify hit
- `TestSemanticCacheMiss` — get with query whose embedding has low similarity; verify miss
- `TestSemanticCacheTTL` — set entry, advance clock past semantic TTL; verify miss
- `TestInvalidate` — set entries, call Invalidate(), get; verify all misses
- `TestCacheDisabled` — create with enabled=false; set + get; verify always miss
- `TestNormalization` — "Hello World" and "  hello  world  " should produce same cache key
- `TestInvalidateByTopics_SelectiveEviction` — cache 2 entries with different topics; invalidate one topic; verify only the matching entry is evicted, non-matching entry preserved
- `TestInvalidateByTopics_NoMetadataEvicted` — cache entry with no topic metadata; invalidate by topics; verify the no-metadata entry is evicted (can't prove it's safe)
- `TestInvalidateByTopics_PreservesKnownSafe` — cache 3 entries: one matching topic, one non-matching topic, one with no metadata; invalidate by topic; verify matching + no-metadata evicted, non-matching preserved

**Unit tests** (`internal/pipeline/enrichment_test.go`):
- `TestEnrich_CacheHit` — pre-populate cache; verify pipeline steps (extractor, retriever) are NOT called
- `TestEnrich_CacheMiss` — empty cache; verify full pipeline runs and result is cached
- `TestEnrich_CacheInvalidatedOnProfileUpdate` — set cache, update profile, query again; verify cache miss

**Acceptance criteria:**
- Identical query served from cache with < 5ms latency (no LLM call, no embedding, no search)
- Semantically similar query (cosine ≥ 0.92) served from cache
- Cache invalidated when profile or context changes
- Cache disabled when `enrichment.cache_enabled = false`
- `go test ./internal/cache/...` passes
- `go test ./internal/pipeline/...` passes

---

## Phase 1 Verification

Run this checklist before declaring Phase 1 complete:

**Core enrichment (issues 1.1–1.7):**
1. Add a context document manually to the VectorStore (via test script or SQLite insert)
2. Send a related query via `curl` → verify the enriched prompt in the interaction log contains the context
3. Send an unrelated query → verify no spurious context is injected
4. Kill Ollama mid-request → verify the proxy gracefully falls back to passthrough
5. Set profile field `tone = "direct"` → verify it appears in the enriched system prompt
6. Check enrichment latency with `time curl ...` — should be under 2s total

**Hybrid retrieval (issue 1.8):**
7. Add context docs with specific entity names (e.g., "Kubernetes", "PostgreSQL")
8. Query using exact entity name → verify keyword/BM25 search contributes to retrieval
9. Query using semantic paraphrase → verify vector search contributes to retrieval
10. Compare: hybrid search should return more relevant results than vector-only for entity-heavy queries

**Reranking (issue 1.9):**
11. Add 10+ context docs with varying relevance to a test query
12. Query with reranking enabled → verify top chunks are more relevant than cosine-only ordering
13. Disable reranker via config → verify pipeline still works (NoOp passthrough)
14. Timeout reranker (e.g., by setting very low timeout) → verify graceful degradation

**Query cache (issue 1.10):**
15. Send identical query twice → verify second request completes significantly faster (cache hit logged)
16. Send semantically similar query → verify semantic cache hit (logged as L2 hit)
17. Add new context document → verify next query is a cache miss (invalidation worked)
18. Update profile field → verify next query is a cache miss (invalidation worked)
19. Disable cache via config → verify every query runs full pipeline

**All issues:**
20. `go test ./...` passes
21. `go test -tags integration ./...` passes
