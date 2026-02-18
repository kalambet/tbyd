# tbyd — Architecture & System Design Plan

## Context

Users interact with cloud LLMs (Claude, GPT, Gemini) through tools like Claude Code, Cursor, and native apps. In doing so, they:
- Lose ownership of their interaction data (stored on vendor servers)
- Repeat context with every new conversation (no persistent personal memory)
- Send raw, unoptimized prompts (wasting tokens)
- Have no agency over how their preferences influence model behavior

TBYD (working title) is a **local-first data sovereignty layer** that sits between the user and cloud LLMs. It intercepts, enriches, and proxies requests while accumulating a private user knowledge base on-device. The system uses a small local LLM to organize and refine user context, and interacts with cloud LLMs via structured, efficient formats rather than raw natural language.

---

## Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                        USER SURFACE                              │
│   Native macOS App  │  CLI Tool  │  Browser/Other tools          │
└───────────┬──────────────────────────────────────────────────────┘
            │
┌───────────▼──────────────────────────────────────────────────────┐
│                   TBYD CORE (Go binary)                          │
│                                                                  │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────────┐     │
│  │  MCP Server │  │ OpenAI-compat│  │  Config & Profile    │     │
│  │  (native)   │  │  REST API    │  │  Manager             │     │
│  └──────┬──────┘  └──────┬───────┘  └──────────────────────┘     │
│         └────────────────┼───────────────────┐                   │
│                          ▼                   │                   │
│               ┌─────────────────────┐        │                   │
│               │  ENRICHMENT PIPELINE│        │                   │
│               │  (Orchestrator)     │        │                   │
│               └────────┬────────────┘        │                   │
│                        │                     │                   │
│          ┌─────────────▼──────────────┐      │                   │
│          │   INTENT EXTRACTOR         │      │                   │
│          │   (calls local LLM)        │      │                   │
│          └─────────────┬──────────────┘      │                   │
│                        │                     │                   │
│          ┌─────────────▼──────────────┐      │                   │
│          │   CONTEXT RETRIEVER        │◄─────┘                   │
│          │   (VectorStore search)     │                          │
│          └─────────────┬──────────────┘                          │
│                        │                                         │
│          ┌─────────────▼──────────────┐                          │
│          │   PROMPT COMPOSER          │                          │
│          │   (structured format)      │                          │
│          └─────────────┬──────────────┘                          │
│                        │                                         │
└────────────────────────┼─────────────────────────────────────────┘
                         │
          ┌──────────────▼─────────────────┐
          │   LOCAL LLM (Ollama)           │
          │   Phi-3.5-mini or Llama 3.2 3B │
          │   localhost:11434              │
          └────────────────────────────────┘

          ┌──────────────────────────────────┐
          │   LOCAL STORAGE                  │
          │   SQLite (interactions, profile, │
          │           vectors, embeddings)   │
          └──────────────────────────────────┘
                         │
          ┌──────────────▼──────────────────┐
          │   CLOUD PROXY                   │
          │   OpenRouter API                │
          │   (routes to any cloud LLM)     │
          └─────────────────────────────────┘
```

---

## Component Breakdown

### 1. TBYD Core — Go Binary
Single compiled binary for macOS (ARM64 + AMD64). Manages lifecycle of all subsystems.

**Responsibilities:**
- Verify Ollama is running on startup (exit with instructions if not — tbyd does not manage the Ollama process)
- Initialize databases on first run
- Expose API surfaces
- Orchestrate enrichment pipeline

**Key packages:**
```
cmd/tbyd/          ← main entrypoint, CLI flags
internal/api/      ← OpenAI-compat REST server + MCP server
internal/pipeline/ ← enrichment orchestration
internal/intent/   ← intent extraction (calls local LLM)
internal/retrieval/ ← VectorStore context retrieval
internal/composer/ ← prompt composition logic
internal/storage/  ← SQLite wrappers (data + vectors)
internal/proxy/    ← cloud LLM HTTP client (OpenRouter)
internal/profile/  ← user profile management
internal/config/   ← platform-native config (UserDefaults on macOS, XDG JSON on Linux)
```

### 2. API Surface — Three Entry Points

**A. OpenAI-Compatible REST API** (`localhost:4000/v1/`)
- `POST /v1/chat/completions` — intercept, enrich, proxy, store
- `GET /v1/models` — return available cloud models via OpenRouter
- Standard OpenAI request/response format
- Works with ANY tool supporting OpenAI API (Cursor, Continue.dev, etc.)

**B. MCP Server**
- Exposes TBYD as an MCP server for Claude Code and MCP-aware tools
- Tools exposed:
  - `add_context` — explicitly add data to personal knowledge base
  - `recall` — retrieve relevant past context
  - `set_preference` — update user preferences
  - `summarize_session` — distill current session into memory
- Resources exposed:
  - `user://profile` — current user profile
  - `user://context` — retrieved relevant context for current conversation

**C. Native macOS App (SwiftUI)**
- Menubar app (no dock icon) — always running in background
- Status indicator (active/idle/error)
- Quick access to: view stored data, manage preferences, cloud model selector
- Onboarding wizard (first run: API key setup, model selection, privacy settings)
- Built with SwiftUI + XPC/HTTP bridge to the Go binary
- Exposes a **macOS Share Extension** — usable from Mail, Finder, Safari, and any app with the system share sheet: send selected text/files/URLs directly into the local knowledge base
- Extensible ingestion surface (see Data Ingestion section below)

### 3. Enrichment Pipeline

Request flow through the pipeline:

```
User Query
    │
    ▼
[1. Mode Check]
    Determine request mode:
    - normal: full enrichment pipeline → cloud
    - bypass (no_enrich): skip enrichment, forward to cloud unchanged, do not store
    - local_only (future): route to local Ollama, no cloud call
    │
    ▼
[2. Intent Extraction] — local LLM call
    Input:  user query + recent conversation history
    Output: JSON { intent_type, entities, topics, context_needs[] }
    Model:  Phi-3.5-mini via Ollama
    Format: JSON schema with constrained output
    │
    ▼
[3. Context Retrieval] — VectorStore
    Semantic search on entity + topic embeddings
    Returns: top-K relevant documents/interactions
    Sources: past interactions, user-uploaded docs, manual notes
    │
    ▼
[4. Profile Injection]
    Append user preference summary from profile store
    (tone, detail level, domain expertise, recurring preferences)
    │
    ▼
[5. Prompt Composition]
    Compose enriched prompt:
    - System section: user profile summary (compressed JSON)
    - Context section: retrieved relevant past data
    - Query section: original user query (unmodified)
    Use OpenRouter's function calling / structured output when available
    │
    ▼
[6. Cloud Dispatch] — OpenRouter HTTP client
    POST enriched prompt to configured cloud model
    Stream response back to caller
    │
    ▼
[7. Storage] — async, non-blocking
    Store: original query, enriched prompt, response, timestamp
    Update: embeddings for retrieval, interaction count, topics
    Queue: preference update if user provides feedback
```

### 4. Local LLM — Ollama + Dual-Model Strategy

Given the expanded scope of local processing (real-time intent extraction AND background synthesis, document enrichment, profile updates), a single model is a poor fit. The system uses two Ollama models optimized for different workloads:

**Why Ollama:**
- macOS native, excellent Apple Silicon optimization
- Manages model downloads and versioning
- OpenAI-compatible API at `localhost:11434/v1/`
- TBYD checks if Ollama is running on startup; exits with instructions if not
- Fast model + embed model are pulled on first run; deep model is pulled on first background task that needs it

| Role | Model | Parameters | RAM | Latency | Use when |
|------|-------|-----------|-----|---------|----------|
| **Fast model** (hot path) | `phi3.5` | 3.8B | ~5GB | <1s | Real-time intent extraction, per-query enrichment |
| **Deep model** (background) | `mistral-nemo` or `gemma2:9b` | 12B / 9B | ~10-14GB | 2-10min OK | Nightly synthesis, document enrichment, preference analysis, fine-tune data prep |

**Fast model — `phi3.5` (Phi-3.5-mini, 3.8B):**
- Best reasoning-to-size ratio in the 3B class
- Excellent structured JSON output for intent extraction
- Fits in 8GB RAM Macs without memory pressure
- Used in the query hot path where latency matters

**Deep model — `mistral-nemo` (12B, default) or `gemma2:9b` (fallback for 8GB RAM):**
- Significantly stronger at long-context document comprehension
- Better cross-document reasoning for synthesis passes
- Better world knowledge for domain/interest classification of diverse content
- Runs asynchronously in background — no latency constraint
- `mistral-nemo` preferred; `gemma2:9b` if RAM is constrained
- **Opt-in:** deep model features are enabled only when configured; system is fully functional with fast model alone

**Model assignment by task:**
- Intent extraction → fast model
- Ingestion enrichment (document tagging, key point extraction) → deep model (async)
- Nightly profile synthesis → deep model
- Session summarization → deep model (triggered post-session)
- Fine-tune data preparation → deep model

### 5. Storage Layer

**SQLite (via `modernc.org/sqlite` — pure Go, no CGO)**

Tables:
```sql
interactions (
    id TEXT PRIMARY KEY,        -- UUID
    created_at DATETIME,
    user_query TEXT,            -- original, unmodified
    enriched_prompt TEXT,       -- what was sent to cloud
    cloud_model TEXT,           -- which model was used
    cloud_response TEXT,        -- what came back
    status TEXT DEFAULT 'completed',  -- completed | aborted | error
    feedback_score INT,         -- -1, 0, 1
    feedback_notes TEXT,        -- user correction/notes
    vector_ids TEXT             -- JSON array of vector doc IDs used
)

user_profile (
    key TEXT PRIMARY KEY,
    value TEXT,                 -- JSON value
    updated_at DATETIME
)

context_docs (
    id TEXT PRIMARY KEY,
    title TEXT,
    content TEXT,
    source TEXT,                -- "manual", "extracted", "interaction"
    tags TEXT,                  -- JSON array
    created_at DATETIME,
    vector_id TEXT              -- corresponding vector store entry
)
```

**Vector Store (SQLite brute-force, upgradeable to LanceDB)**
- Embeddings stored as BLOBs in SQLite `context_vectors` table with brute-force cosine similarity search in Go
- Sufficient for ~50–100K vectors with query latency under 250ms on Apple Silicon
- All access goes through a `VectorStore` interface — backend is swappable without changing retrieval logic
- Embedding model: `nomic-embed-text` via Ollama (768 dimensions, runs locally)
- Migration path to LanceDB (ANN indexes for sub-10ms search at scale) documented in `docs/vectorstore-migration.md`

### 6. Cloud Proxy — OpenRouter

**Why OpenRouter:**
- Single API key, access to all major models
- Standard OpenAI API format (minimal code for multi-model)
- Streaming support
- Cost tracking built in

**Implementation:**
- HTTP client in Go with proper timeout/retry logic
- Stream SSE responses back to caller transparently
- Store cost metadata per interaction
- Support model override via header or config

### 7. User Profile & Preferences

Stored as structured JSON in SQLite. Updated via:
- MCP tool `set_preference`
- Preferences UI in native app
- Automatic extraction (with consent) from feedback signals

Profile schema:
```json
{
  "communication": {
    "tone": "direct|friendly|formal",
    "detail_level": "concise|balanced|thorough",
    "format": "prose|markdown|structured"
  },
  "domains": ["software engineering", "product design"],
  "expertise": { "software_engineering": "expert" },
  "preferences": [
    "prefers code examples over prose explanations",
    "dislikes over-qualified answers"
  ],
  "language": "en",
  "cloud_model_preference": "anthropic/claude-opus-4"
}
```

---

## Data Philosophy

**Opt-in explicit collection:**
- Nothing stored without user action OR explicit consent to store interactions
- First-run onboarding clearly explains what is collected and where it lives
- All data stored at `~/Library/Application Support/TBYD/` on macOS
- Data never transmitted to cloud except as part of enriched prompts
- User can view, export, or delete all stored data from the app

**What is stored:**
- Interactions (query + enriched prompt + response) — only if "Save interactions" is enabled
- User profile and preferences — always local
- Manually added context documents
- Embeddings for the above

**What is NEVER stored:**
- Raw API keys (stored in platform secret store: macOS Keychain / Linux env vars)
- Session data from other apps
- Data from intercepted traffic beyond what user explicitly routes through TBYD

---

## Personalization Progression

**Phase 1 — Context Layer (launch):**
- Manual context documents + preferences
- RAG retrieval enriches every prompt
- No model training

**Phase 2 — Interaction Memory:**
- Store interactions (opt-in)
- Build semantic index of past conversations
- "What did I decide about X" queries become answerable

**Phase 3 — Preference Learning:**
- User feedback (thumbs up/down, edit) signals collected
- Local model runs periodic summarization of preference patterns
- Profile auto-updated with inferred preferences

**Phase 4 — Local Model Fine-tuning (advanced, future):**
- Once 500+ feedback-labeled interactions available
- Fine-tune local model on user preference examples
- Improves extraction and context relevance quality
- Run locally using MLX (Apple Silicon native fine-tuning)

---

## Technology Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Core language | Go | Single binary, fast, native HTTP, goroutines |
| Local LLM runtime | Ollama | macOS-native, model management, OpenAI-compat API |
| Fast local model (hot path) | phi3.5 (3.8B) | <1s latency, excellent JSON output, fits 8GB RAM |
| Deep local model (background) | mistral-nemo (12B) / gemma2:9b | Strong document comprehension + synthesis, async |
| Embedding model | nomic-embed-text (Ollama) | Free, local, 768-dim embeddings |
| Vector store | SQLite brute-force (VectorStore interface) | Simple, no extra deps, upgradeable to LanceDB |
| Relational store | SQLite (modernc pure-Go) | No CGO needed, reliable |
| Logging | Go `log/slog` (structured) | Stdlib, leveled, JSON-capable |
| Cloud gateway | OpenRouter | Single API for all cloud models |
| macOS UI | SwiftUI + Share Extension | Native look/feel, system-level integration |
| MCP implementation | Go MCP SDK (mark3labs/mcp-go) | Native MCP server for Claude Code |
| Config backend | UserDefaults (macOS) / XDG JSON (Linux) | Platform-native, shared with SwiftUI app |
| Distribution | Single Go binary + Ollama prerequisite | Easy macOS install via Homebrew |

---

## Localhost Security

All non-OpenAI endpoints (`/ingest`, `/profile`, `/interactions`, MCP) require authentication via a **bearer token** generated on first run and stored in the platform secret store.

- On first run: generate a random 256-bit token, store in the platform secret store under `tbyd-api-token`
  - **macOS:** Keychain via `security` CLI
  - **Linux:** `$XDG_DATA_HOME/tbyd/secrets.json` (0600 permissions; future: `libsecret`)
- All requests to management endpoints must include `Authorization: Bearer <token>`
- OpenAI-compatible endpoints (`/v1/chat/completions`, `/v1/models`) are unauthenticated (to maintain compatibility with third-party clients) but bound strictly to `127.0.0.1`
- Browser extension and Share Extension read the token from Keychain / App Group (macOS)
- CLI reads the token from the secret store automatically

This prevents CSRF-style attacks from malicious web pages targeting `localhost`.

---

## Logging & Observability

**Structured logging** via Go `log/slog` with consistent event schema:
- Fields: `component`, `request_id`, `interaction_id`, `duration_ms`, `model`, `error`
- Levels: `info` (default), `debug` (enabled via `--debug` flag or `[log] level = "debug"` in config)

**Redaction policy:**
- API keys are NEVER logged at any level
- Full prompts/responses are logged only at `debug` level and only when `save_interactions` is enabled
- At `info` level: log query length, model, enrichment latency, chunk count — never content

---

## Streaming & Response Capture

When `save_interactions` is enabled and the response is streamed (SSE):
- The SSE stream is tee'd: one copy goes to the client, one accumulates in a capped buffer
- On stream completion: store the full response in SQLite asynchronously
- On client cancellation: store partial response with `status = "aborted"` (not lost)
- On upstream error: store error details with `status = "error"`
- Response status is tracked in the `interactions` table: `completed | aborted | error`

---

## Background Job System

All async work (ingestion enrichment, interaction summarization, feedback extraction, nightly synthesis, fine-tuning) runs through a **durable SQLite-backed job queue**, not in-memory channels. This prevents data loss on process restart.

```sql
jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,          -- "ingest_enrich", "summarize", "feedback_extract", "nightly_synthesis"
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',  -- "pending", "running", "completed", "failed"
    attempts INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    run_after DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT
)
```

- Worker goroutines poll the jobs table on a configurable interval (default 1s)
- Failed jobs are retried with exponential backoff up to `max_attempts`
- Completed jobs are retained for 7 days then garbage-collected

---

## Data Lifecycle

Users can export, inspect, and delete all stored data:
- `tbyd data export` — exports knowledge base, interactions, and profile as JSONL
- `tbyd data purge` — deletes all data with confirmation prompt (or `--confirm` flag for scripts)
- Orphaned vectors (vectors whose source doc/interaction was deleted) are garbage-collected on a weekly schedule
- `tbyd status` reports storage size and record counts

---

## Data Ingestion Surface

The system is built around a **universal ingestion interface** — a common protocol that any source (app extension, browser plugin, CLI, API) can use to push data into the local knowledge base. The Go binary exposes a local HTTP ingestion API that all surfaces call.

### Ingestion API (internal, localhost only)

```
POST /ingest
{
  "source": "share_extension" | "browser_plugin" | "reeder" | "cli" | "api",
  "type":   "text" | "url" | "file" | "note" | "article" | "email",
  "title":  "optional title",
  "content": "...",
  "url":    "https://...",       // if applicable
  "tags":   ["interest", "..."],
  "metadata": {}                 // source-specific extras
}
```

After ingestion, the local LLM processes the content (via background job queue) to:
- Extract entities, topics, and key points (structured JSON)
- Classify into interest domains
- Generate embeddings and store in VectorStore
- Update the user digital profile

### Ingestion Sources

**1. macOS Share Extension (SwiftUI)**
- System-level share sheet integration
- Works from: Mail.app, Finder, Safari, Chrome, Notes, any app with share support
- User selects text or file → taps Share → picks the app name → optionally adds a note → sends to local ingestion API
- Supports: selected text, URLs, files (PDF, images → OCR), email excerpts

**2. Browser Extension (Safari + Chrome)**
- Toolbar button + right-click context menu
- "Save to [app name]" for: selected text, full article (readability-parsed), current URL
- Optional: highlight-to-save (selected text instantly queued)
- Communicates via `localhost` ingestion API
- Safari extension: Swift/WebExtensions; Chrome: standard WebExtension

**3. Reeder Integration**
- Reeder supports share sheet → covered by macOS Share Extension
- Optional: Reeder uses iCloud sync; can also tap RSS feed sources directly

**4. CLI (`tbyd ingest`)**
```bash
tbyd ingest --file report.pdf --tags "work,q4-planning"
tbyd ingest --url "https://..." --note "interesting approach to pricing"
tbyd ingest --text "I prefer short, direct answers with code examples"
```

**5. Explicit Profile Editor (SwiftUI App)**
- Direct editing of the user's digital representation
- Structured forms for:
  - Communication preferences (tone, detail, format)
  - Expertise areas + skill levels
  - Interests and domains
  - Points of view / opinions (free-form text, processed by local LLM)
  - Recurring working contexts (current projects, team, goals)
- These explicit entries are highest-priority in enrichment (always injected)

---

## Local Data Enrichment Pipeline (Deep)

The local model does more than just intent extraction at query time. It runs a continuous background enrichment loop over stored data.

### Real-Time Enrichment (on every ingestion)

```
New content arrives (any source)
    │
    ▼
[1. Content Extraction]
    - PDF: extract text (pdftotext or native)
    - URL: fetch + readability parse
    - Image: OCR via Vision framework (macOS native)
    - Email: strip headers, threading, signatures
    │
    ▼
[2. Local LLM Processing] — Phi-3.5-mini
    Input:  raw content
    Output: {
      summary: "...",
      entities: ["..."],
      topics: ["..."],
      key_points: ["..."],
      sentiment: "positive|neutral|critical",
      relevance_to_user: "high|medium|low",
      suggested_tags: ["..."]
    }
    │
    ▼
[3. Embedding + Storage]
    - Embed summary + key points via nomic-embed-text
    - Store in VectorStore with metadata
    - Store structured extraction in SQLite context_docs
    │
    ▼
[4. Profile Update]
    - Update topic interest frequency
    - Detect emerging new interests
    - Flag for periodic profile synthesis
```

### Periodic Background Synthesis (scheduled, e.g. nightly)

The local LLM runs a summarization pass over recent ingested content and interaction history to update the user's digital profile:

```
[1. Gather Recent Data]
    - Last N days of interactions
    - Recently ingested content
    - Feedback signals since last synthesis
    │
    ▼
[2. Pattern Detection] — local LLM
    - What topics appeared repeatedly?
    - What preferences were confirmed/contradicted by feedback?
    - What new interests emerged from ingested content?
    - What communication style preferences are evident from feedback?
    │
    ▼
[3. Profile Delta]
    - Produce a diff: { add: [...], update: {...}, remove: [...] }
    - User reviews and confirms (or auto-applies if configured)
    │
    ▼
[4. Updated Profile Written to SQLite]
```

---

## Local Model Tuning

The local model starts as a general-purpose 3B-7B model and progressively improves to better serve the specific user.

### Tuning Strategy: Three Layers

**Layer 1 — Prompt Calibration (immediate, no training)**
- The local LLM's system prompt is calibrated based on user profile
- Better prompts = better extraction without any weight changes
- Example: if user is a senior engineer, extraction prompts include domain-specific terminology

**Layer 2 — Preference Fine-tuning (medium-term, ~500+ examples)**
- Trigger: user has provided 500+ feedback-labeled interactions
- Method: LoRA fine-tuning using MLX (Apple Silicon native, fast, no GPU required beyond M-series)
- Training data: `(user_query, enriched_prompt, cloud_response, user_feedback)` tuples
- Goal: local model learns which context retrieval and prompt structure patterns produce high-rated responses
- Run: background process, overnight, user can schedule
- Output: LoRA adapter saved locally, swapped into Ollama model

**Layer 3 — Interest Model Fine-tuning (long-term)**
- After accumulating 1000+ ingested documents across diverse sources
- Fine-tune local model on user's reading/interest corpus
- Goal: local model develops better domain-specific extraction for the user's specific fields
- Method: continued pre-training on ingested corpus (MLX)

### Fine-tuning Architecture (macOS / Apple Silicon)

```
Feedback Data (SQLite)
    +
Ingested Corpus (VectorStore + SQLite)
    │
    ▼
[Data Preparation Script] (Python or Go)
    - Format as instruction-tuning JSONL
    - Split train/eval
    │
    ▼
[MLX Fine-tuning] (Python + mlx-lm)
    - LoRA configuration
    - Runs on Apple Neural Engine / GPU
    - Typical: 1-4 hours on M2/M3 Mac
    │
    ▼
[Adapter + Base Model → GGUF conversion]
    - Merge LoRA adapter into base model
    - Convert to GGUF for Ollama
    │
    ▼
[Ollama model swap]
    - Register new model version
    - A/B test: compare old vs. new extraction quality on held-out examples
    - User approves swap (or auto-swap if quality improves)
```

### Digital Self Representation

The user's "digital self" is a structured document maintained in SQLite and continuously updated. It is the richest, most explicit context injected into every enriched prompt.

```json
{
  "identity": {
    "role": "software engineer, founder",
    "expertise": {
      "go": "expert",
      "distributed_systems": "expert",
      "product_design": "intermediate"
    },
    "working_context": {
      "current_projects": ["..."],
      "team_size": "...",
      "tech_stack": ["..."]
    }
  },
  "communication": {
    "preferred_tone": "direct, no fluff",
    "preferred_format": "markdown with code",
    "detail_level": "medium — skip basics, show trade-offs"
  },
  "interests": {
    "primary": ["distributed systems", "privacy tech", "AI infrastructure"],
    "reading": ["HN", "Feedly tech feeds", "research papers"],
    "emerging": ["...detected from recent ingestion"]
  },
  "opinions": [
    "Strong preference for local-first, privacy-preserving software",
    "Skeptical of vendor lock-in",
    "Values simplicity over feature-completeness"
  ],
  "preferences": [
    "Always show code examples, not just theory",
    "Never hedge with 'it depends' without explaining what it depends on",
    "Prefer Go idioms over generic solutions"
  ],
  "feedback_signals": {
    "positive_patterns": ["..."],
    "negative_patterns": ["..."]
  },
  "last_synthesized": "2026-02-18T00:00:00Z"
}
```

---

## Phased Implementation Roadmap

Each phase has a detailed issue breakdown in `docs/`:

| Phase | File | Focus |
|-------|------|-------|
| Phase 00 | [docs/phase-00-gaps.md](docs/phase-00-gaps.md) | Foundation gaps: LogConfig, status column, indexes, context_vectors, jobs table, API token |
| Phase 0 | [docs/phase-0-foundation.md](docs/phase-0-foundation.md) | Go scaffold, config, SQLite, Ollama, passthrough proxy |
| Phase 1 | [docs/phase-1-enrichment.md](docs/phase-1-enrichment.md) | VectorStore, intent extraction, context retrieval, prompt composer |
| Phase 2 | [docs/phase-2-user-surfaces.md](docs/phase-2-user-surfaces.md) | MCP server, CLI, SwiftUI menubar app, Share Extension |
| Phase 3 | [docs/phase-3-personalization.md](docs/phase-3-personalization.md) | Feedback, profile editor, preference learning, nightly synthesis |
| Phase 4 | [docs/phase-4-extended-ingestion.md](docs/phase-4-extended-ingestion.md) | Browser extension, Feedly sync, content extraction, MLX fine-tuning |
| Phase 5 | [docs/phase-5-polish-distribution.md](docs/phase-5-polish-distribution.md) | Project rename, onboarding, encryption, Homebrew, App Store |

### Phase 0 — Foundation
- [x] **0.1** Go module init and project layout
- [x] **0.2** Config loader (UserDefaults + Keychain for API keys)
- [x] **0.3** SQLite storage: schema and migrations
- [x] **0.4** Ollama lifecycle manager
- [x] **0.5** OpenRouter HTTP client (passthrough)
- [x] **0.6** OpenAI-compatible REST API server (passthrough mode)

### Phase 00 — Foundation Gaps
- [ ] **00.1** Add `LogConfig` to config
- [ ] **00.2** Add `status` column to `interactions` table
- [ ] **00.3** Add indexes to `interactions` table
- [ ] **00.4** Add `context_vectors` table to migration
- [ ] **00.5** Add `jobs` table and model
- [ ] **00.6** Add job queue methods to Store
- [ ] **00.7** API token generation and platform secret store

### Phase 1 — Basic Enrichment
- [x] **1.1** VectorStore integration + nomic-embed-text embedding pipeline
- [ ] **1.2** Intent extraction via local LLM (phi3.5)
- [ ] **1.3** Context retrieval integration
- [ ] **1.4** User profile manager
- [ ] **1.5** Prompt composer (structured format)
- [ ] **1.6** Enrichment pipeline orchestrator

### Phase 2 — User Surfaces
- [ ] **2.1** Universal ingestion HTTP API
- [ ] **2.2** MCP server (`add_context`, `recall`, `set_preference`, `summarize_session`)
- [ ] **2.3** CLI interface (`tbyd ingest`, `tbyd status`, `tbyd profile`, `tbyd recall`)
- [ ] **2.4** Interaction storage (opt-in)
- [ ] **2.5** macOS SwiftUI menubar app
- [ ] **2.6** macOS Share Extension (Mail, Finder, Safari, any app)

### Phase 3 — Personalization
- [ ] **3.1** Feedback collection API and UI
- [ ] **3.2** User profile editor (explicit digital self)
- [ ] **3.3** Preference extraction from feedback (background job)
- [ ] **3.4** Profile injection into enrichment pipeline (calibration)
- [ ] **3.5** Nightly profile synthesis (deep model background pass)

### Phase 4 — Extended Ingestion & Model Tuning
- [ ] **4.1** Browser extension (Safari + Chrome)
- [ ] **4.2** Feedly integration (OAuth + periodic sync)
- [ ] **4.3** Content extraction improvements (PDF chunking, OCR, HTML)
- [ ] **4.4** Fine-tuning data preparation pipeline
- [ ] **4.5** MLX fine-tuning script and model swap

### Phase 5 — Polish & Distribution
- [ ] **5.1** Project rename and identity
- [ ] **5.2** Onboarding flow (SwiftUI wizard + `tbyd setup`)
- [ ] **5.3** Encryption at rest (macOS Data Protection)
- [ ] **5.4** Homebrew formula + GitHub Actions release pipeline
- [ ] **5.5** App Store preparation (optional)
- [ ] **5.6** Comprehensive documentation

---

## File Structure

```
tbyd/
├── cmd/
│   └── tbyd/
│       └── main.go
├── internal/
│   ├── api/
│   │   ├── openai.go        ← OpenAI-compat HTTP handlers
│   │   ├── mcp.go           ← MCP server implementation
│   │   └── ingest.go        ← Universal ingestion HTTP API (localhost)
│   ├── pipeline/
│   │   └── enrichment.go    ← Main enrichment orchestrator
│   ├── intent/
│   │   └── extractor.go     ← Local LLM intent extraction
│   ├── retrieval/
│   │   ├── vectorstore.go   ← VectorStore interface
│   │   ├── store.go         ← SQLite brute-force implementation
│   │   ├── embedder.go      ← Ollama embedding client
│   │   └── retriever.go     ← Semantic search orchestration
│   ├── composer/
│   │   └── prompt.go        ← Prompt composition
│   ├── ingest/
│   │   ├── processor.go     ← Content normalization (PDF, URL, text)
│   │   └── enricher.go      ← Local LLM extraction on ingested content
│   ├── synthesis/
│   │   └── profile.go       ← Periodic background profile synthesis
│   ├── tuning/
│   │   ├── prepare.go       ← Training data preparation
│   │   └── schedule.go      ← MLX fine-tuning scheduler
│   ├── storage/
│   │   ├── sqlite.go        ← Interaction/profile/doc storage
│   │   └── migrations/
│   ├── proxy/
│   │   └── openrouter.go    ← Cloud LLM HTTP client
│   ├── profile/
│   │   └── manager.go       ← Digital self representation CRUD
│   ├── ollama/
│   │   └── client.go        ← Ollama lifecycle + API client
│   └── config/
│       ├── config.go        ← Config loading + keychain
│       ├── backend.go       ← ConfigBackend interface
│       ├── backend_darwin.go ← UserDefaults (com.tbyd.app)
│       ├── backend_other.go  ← XDG JSON file backend
│       └── keys.go          ← Key specs + env overrides
├── macos/                   ← SwiftUI macOS app (Xcode project)
│   ├── App/
│   │   ├── MenubarApp.swift
│   │   ├── StatusView.swift
│   │   ├── ProfileEditorView.swift
│   │   └── DataBrowserView.swift
│   ├── ShareExtension/      ← macOS Share Extension target
│   │   └── ShareViewController.swift
│   └── tbyd.xcodeproj
├── browser-extension/       ← WebExtension (Safari + Chrome)
│   ├── manifest.json
│   ├── background.js
│   └── popup/
├── scripts/
│   └── finetune.py          ← MLX LoRA fine-tuning script
├── docs/
│   └── architecture.md
├── go.mod
├── go.sum
└── config.toml.example       ← Deprecated; documents env vars and defaults CLI
```

---

## Verification Plan

After implementation, test end-to-end:

1. **Basic proxy test:**
   - Start TBYD, point any OpenAI client to `localhost:4000/v1/`
   - Send a message → verify it reaches OpenRouter → verify response returned

2. **Enrichment test:**
   - Add context via MCP `add_context` tool
   - Send related query → inspect enriched prompt in interaction log → verify context was injected

3. **Claude Code MCP test:**
   - Register TBYD as MCP server in Claude Code settings
   - Use `recall` tool → verify it returns relevant stored context

4. **Profile injection test:**
   - Set preference "always respond in bullet points"
   - Send a query → verify cloud LLM response matches preference

5. **Data sovereignty test:**
   - Enable network logging (Charles Proxy / Wireshark)
   - Verify only enriched prompts reach OpenRouter, not unintended raw data

6. **Bypass mode test:**
   - Set a request header or config flag to enable bypass (no_enrich) mode
   - Verify marked queries are forwarded to cloud unchanged, with no enrichment and no local storage

7. **Localhost auth test:**
   - Verify `POST /ingest` without bearer token returns 401
   - Verify `POST /v1/chat/completions` works without token (OpenAI compat)

8. **Data lifecycle test:**
   - `tbyd data export` produces valid JSONL
   - `tbyd data purge --confirm` removes all data; verify empty state
