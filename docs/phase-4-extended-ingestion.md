# Phase 4 — Extended Ingestion & Local Model Tuning

> **Goal:** Broaden the data pipeline to capture content from RSS readers (Feedly), browser extensions (Safari + Chrome), and prepare the fine-tuning infrastructure. The local model starts improving based on the accumulated user corpus.

---

## Issue 4.1 — Browser extension (Safari + Chrome)

**Context:** Browser integration is the highest-frequency ingestion surface for most knowledge workers. The extension must be lightweight, non-intrusive, and work across Safari and Chrome.

**Tasks:**
- Create `browser-extension/` as a WebExtension project (Manifest V3)
- Files:
  - `manifest.json` — declare permissions: `activeTab`, `contextMenus`, `storage`; no broad host permissions
  - `background.js` / `background.ts` — service worker:
    - Register context menu items: "Save to tbyd", "Save selection to tbyd"
    - Handle `chrome.contextMenus.onClicked`: POST to `http://localhost:4000/ingest`
    - Store server URL in `chrome.storage.local` (configurable)
    - Store the bearer token (from Keychain / initial setup) in `chrome.storage.local`; include `Authorization: Bearer <token>` on all requests to localhost
  - `popup/` — extension toolbar popup:
    - "Save current page" button
    - Tags input field
    - Note field
    - Status indicator (server running / not running)
    - Link to open Data Browser (deep link into macOS app)
  - `content.js` — optional: highlight-to-save (select text → floating "Save" tooltip appears)
- Safari adaptation:
  - Build using Xcode's "Convert Web Extension" tool
  - Add as a target in the macOS Xcode project
  - Share Info.plist with Share Extension target
- Handle CSP / localhost restrictions in Safari (may need to use `browser.runtime.sendMessage` + native messaging)
- Server-side: extension sends `source: "browser"`, `type: "url"` or `"text"` to `/ingest`

**JavaScript unit tests** (`browser-extension/tests/`) — use Jest or Vitest with `chrome` API mocks:
- `background.test.js / TestContextMenu_SaveSelection` — simulate `contextMenus.onClicked` with selected text; mock `fetch`; verify POST to `/ingest` with `type: "text"` and correct content
- `background.test.js / TestContextMenu_SavePage` — simulate save page click; mock `fetch`; verify POST with `type: "url"` and current tab URL
- `background.test.js / TestServerNotRunning` — mock `fetch` throwing network error; verify status indicator updated to "not running" (no unhandled rejection)
- `background.test.js / TestTagsParsed` — simulate save with tags "go,privacy"; verify POST body has `tags: ["go", "privacy"]`
- `popup.test.js / TestStatusCheck` — mock fetch to `/health`; verify status indicator shows "running" on 200
- `popup.test.js / TestStatusCheck_Offline` — mock fetch failure; verify "not running" state
- `background.test.js / TestAuth_HeaderIncluded` — verify all fetch calls to localhost include the `Authorization: Bearer <token>` header

**Acceptance criteria:**
- In Chrome: right-click on selected text → "Save selection to tbyd" → text stored in knowledge base
- In Chrome: toolbar popup → "Save current page" → page content extracted and stored
- In Safari: same flows work (may require extension to be enabled in Safari preferences)
- When server not running: popup shows "tbyd is not running" clearly
- No data sent to any external server by the extension; all traffic goes to localhost

---

## Issue 4.2 — Feedly integration

**Context:** Feedly is a primary RSS reading surface. Articles the user saves or likes in Feedly should flow automatically into the knowledge base, building a rich interest corpus.

**Tasks:**
- Create `internal/ingest/feedly.go`:
  - `FeedlyClient` struct with OAuth token management
  - `GetSavedEntries(since time.Time) ([]FeedlyEntry, error)` — GET `/v3/streams/contents?streamId=user/...%2Ftag%2Fglobal.saved`
  - `GetLikedEntries(since time.Time) ([]FeedlyEntry, error)` — GET `/v3/streams/contents?streamId=user/...%2Ftag%2Fglobal.must-reads`
  - `FeedlyEntry` struct: `{ID, Title, URL, Content, Published, Categories}`
  - OAuth token refresh logic (Feedly uses OAuth 2.0)
- Add `[feedly]` config section:
  ```toml
  [feedly]
  enabled = false
  access_token = ""    # stored in Keychain
  sync_interval = "6h"
  sync_saved = true
  sync_liked = false
  ```
- Create sync job in `internal/ingest/feedly_sync.go`:
  - `SyncJob` struct
  - `Run(ctx context.Context)` — pull new entries since last sync timestamp, POST each to `/ingest` with `source: "feedly"` and Feedly categories as tags
  - Persist last-synced timestamp in SQLite (new key in `user_profile` table: `feedly.last_sync`)
  - Runs on configured interval via scheduler
- Add Feedly OAuth setup flow:
  - `tbyd config feedly setup` — opens browser to Feedly OAuth URL, listens on localhost callback, stores token in Keychain
- In SwiftUI Preferences: Feedly section with connect/disconnect button, sync interval, toggles for saved/liked

**Unit tests** (`internal/ingest/feedly_test.go`) — mock HTTP server for Feedly API:
- `TestGetSavedEntries_ParsesResponse` — mock Feedly API returns 3 entries; verify slice length and field mapping
- `TestGetSavedEntries_SinceFilter` — verify `since` timestamp included in request URL
- `TestGetSavedEntries_EmptyResponse` — API returns empty items array; verify empty slice (not error)
- `TestGetSavedEntries_AuthHeader` — verify `Authorization: Bearer <token>` header set on request
- `TestTokenRefresh_ExpiredToken` — mock returns 401 then 200 after refresh; verify retry succeeds
- `TestTokenRefresh_RefreshFails` — mock refresh endpoint returns error; verify error propagated

**Unit tests** (`internal/ingest/feedly_sync_test.go`):
- `TestSyncJob_IngestsNewEntries` — 3 new entries since last sync; verify 3 `/ingest` POSTs made
- `TestSyncJob_SkipsAlreadySynced` — last_sync timestamp set; mock returns 0 new entries; verify no POSTs
- `TestSyncJob_UpdatesLastSync` — after run; verify `feedly.last_sync` key updated in profile store
- `TestSyncJob_DisabledConfig` — `feedly.enabled = false`; verify sync does not run
- `TestSyncJob_ContextCancellation` — cancel context during run; verify exits promptly, no partial state

**Acceptance criteria:**
- After `tbyd config feedly setup`, articles saved in Feedly sync within the configured interval
- Each synced article's categories map to tags in the stored document
- `tbyd status` shows Feedly sync status and last-sync timestamp
- Disabling Feedly in config stops sync without restarting the server

---

## Issue 4.3 — Content extraction improvements

**Context:** Phase 1 handled basic text and URL ingestion. Phase 4 upgrades this for richer content types encountered via browser extension and Feedly.

**Tasks:**
- `internal/ingest/processor.go` upgrades:
  - **URL extraction**: use `go-shiori/go-readability` for article extraction; strip nav, ads, footers; extract: title, author, published date, main body
  - **PDF extraction**: use `pdfcpu` (preferred, actively maintained); extract text per page; chunk if > 4000 tokens
  - **HTML file**: same as URL extraction
  - **Markdown**: preserve as-is, extract frontmatter if present
  - **Image**: use macOS Vision framework via CGO or subprocess for OCR; extract text
  - **Email content** (from Share Extension): strip quoted text, signatures; preserve body
- Implement smart chunking for long documents:
  - Split at paragraph/section boundaries, not arbitrary character count
  - Overlap adjacent chunks by one paragraph for context continuity
  - Each chunk stored as a separate VectorStore record with `source_id` linking them to the parent `context_docs` entry
- Add `internal/ingest/metadata.go`:
  - Extract metadata from processed content: `{word_count, reading_time_minutes, language, detected_topics[]}`
  - Store in `context_docs.metadata` JSON column

**Unit tests** (`internal/ingest/processor_test.go`):
- `TestProcessText_ReturnsAsIs` — plain text input; verify output equals input, no transformation
- `TestProcessMarkdown_PreservesFrontmatter` — markdown with YAML frontmatter; verify frontmatter fields extracted to metadata
- `TestProcessHTML_StripsNavigation` — HTML with nav/footer; verify output contains only article body
- `TestProcessHTML_ExtractsTitle` — HTML with `<title>` and `<h1>`; verify title extracted to metadata
- `TestProcessEmail_StripsQuotedText` — email with `>` quoted lines and signature delimiter; verify those stripped from output
- `TestChunking_ParagraphBoundaries` — long text with paragraph breaks; verify no chunk ends mid-sentence
- `TestChunking_OverlapExists` — 3 chunks produced; verify last sentence of chunk N appears at start of chunk N+1
- `TestChunking_ShortDocument` — document < chunk size; verify single chunk returned
- `TestMetadata_WordCount` — 100-word text; verify `word_count == 100`
- `TestMetadata_ReadingTime` — 600-word text; verify `reading_time_minutes == 3` (200 wpm)

**Unit tests** (`internal/ingest/processor_pdf_test.go`):
- `TestProcessPDF_ExtractsText` — use a small test PDF fixture; verify non-empty text extracted
- `TestProcessPDF_ChunksLongDocument` — PDF fixture > 4000 tokens; verify multiple chunks produced
- `TestProcessPDF_AllChunksLinked` — verify all chunks share the same `source_id` linking to the parent `context_docs` entry

**Acceptance criteria:**
- A 50-page PDF is chunked into ~15-20 overlapping chunks, all retrievable
- OCR on a screenshot of code returns the code text correctly
- Long web articles are split into paragraph-aligned chunks, not mid-sentence
- `go test ./internal/ingest/...` covers all content types

---

## Issue 4.4 — Fine-tuning data preparation pipeline

**Context:** After 500+ feedback-labeled interactions are accumulated, the system can prepare training data for LoRA fine-tuning of the local fast model (phi3.5). This issue implements the data preparation layer.

**Tasks:**
- Create `internal/tuning/prepare.go`:
  - `DataPreparer` struct
  - `CanFineTune() (bool, string)` — check prerequisites:
    - Minimum 500 feedback-labeled interactions
    - Minimum 100 positive AND 100 negative examples
    - Apple Silicon detected (`runtime.GOARCH == "arm64"` on macOS)
    - `mlx` Python package available
  - `PrepareDataset(outputDir string) (DatasetStats, error)`:
    1. Query SQLite for all feedback-labeled interactions
    2. For each positive interaction: format as instruction-tuning example:
       ```json
       {"instruction": "<extracted intent>", "input": "<user query>", "output": "<cloud response>", "context": "<enriched prompt excerpt>"}
       ```
    3. For each negative interaction: format with negative label for DPO (Direct Preference Optimization) training
    4. Write `train.jsonl` (80%) and `eval.jsonl` (20%) to `outputDir`
    5. Return stats: total examples, positive/negative split, unique topics
  - `DatasetStats` struct: `{TotalExamples, PositiveExamples, NegativeExamples, TopicDistribution map[string]int}`
- Add CLI command: `tbyd tune prepare [--output <dir>]`
  - Runs `CanFineTune()` and prints checklist
  - Runs `PrepareDataset()` and prints stats
  - Prints instructions for next step (running `scripts/finetune.py`)

**Unit tests** (`internal/tuning/prepare_test.go`):
- `TestCanFineTune_TooFewExamples` — store has 100 interactions; verify returns `false` with count message
- `TestCanFineTune_ImbalancedClasses` — 490 positive, 10 negative; verify returns `false` with class balance message
- `TestCanFineTune_MissingMLX` — mock `mlx` check fails; verify returns `false` with install instructions
- `TestPrepareDataset_SplitRatio` — 1000 interactions; verify `train.jsonl` has ~800, `eval.jsonl` ~200
- `TestPrepareDataset_SplitReproducible` — call twice with same seed; verify identical file contents
- `TestPrepareDataset_ValidJSONL` — parse every line of output; verify each is valid JSON with required fields
- `TestPrepareDataset_PositiveFormat` — positive interaction formatted correctly with `instruction`, `input`, `output`
- `TestPrepareDataset_NegativeFormat` — negative interaction formatted with DPO negative label
- `TestPrepareDataset_Stats` — 600 interactions (400 positive, 200 negative); verify stats match

**Acceptance criteria:**
- `tbyd tune prepare` prints clear "prerequisites not met" with counts when < 500 examples
- Prepared `train.jsonl` is valid JSONL with well-formed instruction-tuning format
- 80/20 train/eval split is random but reproducible (seeded)
- `go test ./internal/tuning/...` covers data preparation logic

---

## Issue 4.5 — MLX fine-tuning script and model swap

**Context:** The actual fine-tuning runs via Python + MLX (Apple Silicon native ML). This issue implements the script and the mechanism to hot-swap the improved model into Ollama.

**Tasks:**
- Create `scripts/finetune.py`:
  - Dependencies: `mlx-lm`, `mlx`, `huggingface-hub`
  - Usage: `python scripts/finetune.py --train train.jsonl --eval eval.jsonl --model phi3.5 --output ./adapter`
  - Steps:
    1. Download base model from HuggingFace (if not cached)
    2. Run LoRA fine-tuning with configurable hyperparameters (rank=8, lr=1e-4, epochs=3 — defaults optimized for preference learning)
    3. Evaluate on `eval.jsonl` — compute loss improvement
    4. Merge adapter into base model weights
    5. Export merged model to GGUF format (using `llama.cpp` convert scripts)
    6. Print path to output GGUF file
  - Configurable via CLI args; progress output; estimated time display
- Create `internal/tuning/schedule.go`:
  - `Scheduler` struct
  - `ScheduleNextTune()` — checks prerequisites, schedules fine-tuning for next available low-activity window (configurable, default: 2 AM if > 500 examples). Enqueues a `finetune` job in the SQLite job queue
  - `RunTuneJob(ctx context.Context)` — runs `finetune.py` as subprocess, monitors progress, handles errors
  - On completion: register new GGUF with Ollama via `ollama create tbyd-phi3.5-v<n> -f ./Modelfile`
  - Run quality check: compare old vs. new model on held-out eval examples
  - If new model improves by > 5% on eval: notify user for approval; if auto-approve configured: swap automatically
- Add CLI: `tbyd tune run` — manual trigger of fine-tuning job
- Add CLI: `tbyd tune status` — show: last tune date, model version, eval metrics, pending data count
- In SwiftUI Preferences: fine-tuning section with last-tune date, quality metrics, manual trigger button

**Unit tests** (`internal/tuning/schedule_test.go`):
- `TestRunTuneJob_SubprocessLaunched` — mock `exec.Command`; verify `finetune.py` called with correct args
- `TestRunTuneJob_SubprocessFails` — mock subprocess exits non-zero; verify original model NOT swapped (still active)
- `TestRunTuneJob_QualityCheckPasses` — mock eval shows 10% improvement; verify model swap triggered
- `TestRunTuneJob_QualityCheckFails` — mock eval shows 1% improvement (< 5% threshold); verify swap NOT triggered
- `TestRunTuneJob_ContextCancellation` — cancel context mid-run; verify subprocess killed, original model still active
- `TestScheduleNextTune_SetsCorrectTime` — verify scheduled time is 2 AM (or configured time) on next day

**Python tests** (`scripts/tests/test_finetune.py`) — use pytest with small synthetic dataset:
- `test_dataset_loading` — load a 10-line JSONL; verify no parse errors
- `test_output_file_created` — run with `--dry-run` flag; verify output directory created
- `test_args_required` — run without `--train`; verify non-zero exit with usage message

**Acceptance criteria:**
- `python scripts/finetune.py` runs to completion on Apple Silicon with 500 examples in < 4 hours
- Output GGUF is registered as a new Ollama model
- Quality check correctly identifies improvement (lower eval loss = better)
- `tbyd tune run` provides progress feedback and estimated completion time
- If fine-tuning fails mid-run, the original model remains active (no disruption to service)

---

## Phase 4 Verification

1. Install browser extension in Chrome → share selected text from an article → verify in Data Browser
2. Connect Feedly → wait for sync → verify saved articles appear in knowledge base
3. Share a PDF from Finder via Share Extension → verify it's chunked and all chunks retrievable
4. Run `tbyd tune prepare` → verify it correctly counts labeled interactions and produces valid JSONL
5. With 500+ labeled interactions: run full fine-tuning → verify improved model loads in Ollama → send query → verify it uses the fine-tuned model
6. With the fine-tuned model: verify extraction quality is subjectively better for user's domain
7. `go test ./...` passes
8. `go test -tags integration ./...` passes
9. JavaScript tests pass: `npm test` in `browser-extension/`
10. Python tests pass: `pytest scripts/tests/`
