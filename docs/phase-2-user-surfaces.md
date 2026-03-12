# Phase 2 — User Surfaces

> **Goal:** The system is usable without touching the terminal after initial setup. Users can interact with their knowledge base via MCP tools in Claude Code, the CLI, a macOS menubar app, and the Share Extension.

---

## Issue 2.1 — Universal ingestion HTTP API

**Context:** All ingestion surfaces (CLI, Share Extension, browser extension) call the same local HTTP endpoint. It receives content in a normalized format and queues it for local LLM enrichment and storage.

**Tasks:**
- Create `internal/api/ingest.go`:
  - `POST /ingest` handler
  - Request body:
    ```go
    type IngestRequest struct {
        Source   string            // "cli", "share_extension", "browser", "manual"
        Type     string            // "text", "url", "file", "note"
        Title    string
        Content  string
        URL      string
        Tags     []string
        Metadata map[string]string
    }
    ```
  - Validate: `content` or `url` required; `source` required
  - Require `Authorization: Bearer <token>` header (token from Keychain, generated in Phase 0)
  - For `type: "url"`: fetch URL content (readability-style extraction via `go-readability` or similar)
  - For `type: "file"`: accept base64-encoded content; detect MIME type; handle PDF (extract text), plain text, markdown
  - Generate a UUID for the doc, store in SQLite `context_docs`, queue for background enrichment
  - Return `{"id": "...", "status": "queued"}` immediately (non-blocking)
- Use the SQLite-backed job queue (from Phase 0) for durable background processing:
  - On ingest: write doc to `context_docs`, enqueue an `ingest_enrich` job
  - Worker picks up jobs from the `jobs` table, processes using local model
  - Failed jobs are retried with exponential backoff (max 3 attempts)
  - No data loss on process restart — pending jobs survive
- Expose data browsing and profile management endpoints:
  - `GET /profile` — return current profile JSON
  - `PATCH /profile` — partial update (merge fields)
  - `GET /interactions` — paginated list (when save_interactions enabled)
  - `GET /interactions/:id` — single interaction
  - `DELETE /interactions/:id` — hard delete from SQLite and VectorStore
  - `GET /context-docs` — paginated list
  - `DELETE /context-docs/:id` — hard delete from SQLite and VectorStore
- All management endpoints require bearer token auth

**Unit tests** (`internal/api/ingest_test.go`) — use `httptest.NewRecorder`, mock storage and queue:
- `TestIngest_TextContent` — POST valid text payload; verify 200 and `{"status":"queued"}` returned; verify doc saved to mock store
- `TestIngest_MissingSource` — omit `source`; verify 400 with descriptive error
- `TestIngest_MissingContent` — omit both `content` and `url`; verify 400
- `TestIngest_NoAuth` — POST without token; verify 401
- `TestIngest_ValidAuth` — POST with correct token; verify 200
- `TestIngest_URLType` — POST with `type: "url"` and a URL; mock HTTP fetch; verify fetched content stored
- `TestIngest_FileBase64` — POST base64-encoded plain text file; verify decoded content stored
- `TestIngest_QueuedImmediately` — verify response arrives before background processing completes (non-blocking)

**Unit tests** (`internal/ingest/worker_test.go`):
- `TestWorker_ProcessesJob` — insert a job into the `jobs` table; verify worker picks it up and calls processor within 1s
- `TestWorker_RetryOnFailure` — processor fails twice then succeeds; verify job processed on third attempt via job table retry count
- `TestWorker_MaxRetriesExceeded` — processor always fails; verify job marked as failed after 3 attempts (no infinite loop)
- `TestWorker_ConcurrentEnqueue` — insert 50 jobs into the `jobs` table from 5 goroutines concurrently; verify all processed, no deadlock

**Acceptance criteria:**
- `POST /ingest` with `{"source":"cli","type":"text","content":"I prefer Go over Python for backend services","tags":["preference"]}` returns `{"id":"...","status":"queued"}` immediately
- Within 30s (deep model processing time), the document appears in the VectorStore and is retrievable
- URL ingestion fetches and strips HTML, stores clean text
- `go test ./internal/api/...` and `./internal/ingest/...` pass

---

## Issue 2.2 — MCP server

**Context:** MCP integration makes the knowledge base available natively within Claude Code and any MCP-aware tool.

**Tasks:**
- Add dependency: `github.com/mark3labs/mcp-go` (or implement minimal MCP server from spec)
- Create `internal/api/mcp.go`:
  - Start MCP server on configured port (default 4001) or as stdio transport (for `claude mcp add` compatibility)
  - MCP server uses stdio transport for Claude Code (no HTTP auth needed) or HTTP with bearer token auth on the HTTP transport
  - Register tools:
    - `add_context`: args `{title: string, content: string, tags?: string[]}` → calls `/ingest`, returns doc ID
    - `recall`: args `{query: string, limit?: int}` → calls retriever, returns array of context chunks with scores
    - `set_preference`: args `{key: string, value: string}` → updates profile field, returns confirmation
    - `summarize_session`: args `{messages: Message[]}` → sends to deep model for summarization, stores result as context doc, returns summary text
  - Register resources:
    - `user://profile` — returns current profile as JSON
    - `user://recent` — returns last 10 stored interactions (summaries only)
- Generate MCP server configuration snippet for `claude_desktop_config.json` / `.claude/settings.json` on first run

**Unit tests** (`internal/api/mcp_test.go`) — use MCP test client or direct handler calls:
- `TestMCPTool_AddContext` — call `add_context` tool; verify doc written to store and ID returned
- `TestMCPTool_Recall_ReturnsChunks` — pre-populate retriever mock; call `recall`; verify chunks in response
- `TestMCPTool_Recall_EmptyResult` — retriever returns empty; verify tool returns empty array (not error)
- `TestMCPTool_SetPreference` — call `set_preference`; verify profile store updated
- `TestMCPResource_Profile` — read `user://profile`; verify JSON matches current profile
- `TestMCPServer_ConcurrentCalls` — make 10 concurrent tool calls; verify no panics, all respond

**Acceptance criteria:**
- After `claude mcp add tbyd --url http://localhost:4001`, tools appear in Claude Code
- `recall` tool returns relevant stored context for a semantic query
- `add_context` tool stores content and it becomes retrievable via `recall`
- `set_preference` tool updates profile and the next enriched prompt reflects it
- MCP server handles concurrent tool calls without panics

---

## Issue 2.3 — CLI interface

**Context:** Power users and scripts need a command-line interface for ingestion, status checking, and profile management.

**Tasks:**
- Add dependency: `github.com/spf13/cobra`
- Extend `cmd/tbyd/main.go` with subcommands:
  - `tbyd start` — start the server (foreground)
  - `tbyd stop` — stop running server (via PID file)
  - `tbyd status` — print: server running?, Ollama running?, models loaded, doc count, interaction count
  - `tbyd ingest` — ingest content:
    - `tbyd ingest --file <path> [--tags tag1,tag2] [--title "..."]`
    - `tbyd ingest --url <url> [--tags tag1,tag2]`
    - `tbyd ingest --text "..." [--tags tag1,tag2]`
    - Calls `POST /ingest` on the running server
  - `tbyd profile` — profile management:
    - `tbyd profile show` — print current profile as JSON
    - `tbyd profile set <key> <value>` — set a profile field
    - `tbyd profile edit` — open profile JSON in `$EDITOR`
  - `tbyd recall <query>` — semantic search over knowledge base, print results
  - `tbyd interactions` — list recent interactions
    - `tbyd interactions list [--limit N]`
    - `tbyd interactions show <id>`
  - `tbyd data` — export or purge all stored data
    - `tbyd data export [--output <file>]`
    - `tbyd data purge [--confirm]`
  - `tbyd config` — show/edit config
    - `tbyd config show`
    - `tbyd config set <key> <value>`
- PID file at `$DATA_DIR/tbyd.pid`
- Colorized output with `--no-color` flag for scripting

**Unit tests** (`cmd/tbyd/commands_test.go`) — execute cobra commands with captured stdout/stderr:
- `TestIngestCommand_Text` — run `tbyd ingest --text "hello" --tags foo`; mock server; verify POST sent with correct body
- `TestIngestCommand_MissingArgs` — run `tbyd ingest` with no flags; verify non-zero exit and usage hint
- `TestProfileSet` — run `tbyd profile set communication.tone direct`; mock server; verify PATCH sent
- `TestProfileShow` — mock GET /profile; run `tbyd profile show`; verify JSON printed to stdout
- `TestRecallCommand` — mock retriever; run `tbyd recall "go preferences"`; verify results printed
- `TestStatusCommand_Running` — mock health endpoint returns ok; verify "running" in output
- `TestStatusCommand_Stopped` — health endpoint unreachable; verify "stopped" in output
- `TestNoColorFlag` — run any command with `--no-color`; verify ANSI codes absent from output

**Acceptance criteria:**
- `tbyd status` shows all system components' state clearly
- `tbyd ingest --text "I prefer short answers"` prints `Queued doc <id>` and the doc is later retrievable
- `tbyd recall "communication preferences"` returns matching stored docs
- `tbyd start` and `tbyd stop` work reliably; double-start prints a warning
- All commands work when server is not running (with appropriate errors for commands that need it)

---

## Issue 2.4 — Interaction storage (opt-in)

**Context:** With user consent, every interaction (original query, enriched prompt, response) is stored locally for later retrieval and preference learning. Must be strictly opt-in.

**Tasks:**
- Add config field: `[storage] save_interactions = false` (default: off)
- Onboarding prompt: on first request, if not configured, print:
  ```
  tbyd can store your interactions locally for improved context retrieval.
  This data never leaves your machine. Enable with: tbyd config set storage.save_interactions true
  ```
- When enabled, after each cloud response:
  - Store full interaction in SQLite `interactions` table
  - Embed the interaction summary asynchronously and index in VectorStore
  - Interaction summary = `"[date] User asked about X. Response: Y."` (generated by deep model, async)
  - Interaction summary is generated by the fast model if deep model is not configured; deep model used when available
- Implement `GET /interactions` API endpoint: returns paginated list
- Implement `DELETE /interactions/:id` — hard delete from SQLite and VectorStore

**Unit tests** (`internal/api/interactions_test.go`):
- `TestSaveInteraction_OptInEnabled` — config has `save_interactions=true`; send a request; verify interaction saved to mock store
- `TestSaveInteraction_OptInDisabled` — config has `save_interactions=false`; send a request; verify store not called
- `TestGetInteractions_Paginated` — store has 20 interactions; GET with `limit=5&offset=0`; verify 5 returned
- `TestGetInteractions_Empty` — empty store; verify empty array returned (not 404)
- `TestDeleteInteraction` — save interaction; DELETE it; verify removed from both SQLite mock and VectorStore mock
- `TestDeleteInteraction_NotFound` — DELETE non-existent ID; verify 404

**Acceptance criteria:**
- With `save_interactions = false` (default), nothing is stored after a query
- With `save_interactions = true`, interactions appear in `tbyd interactions list`
- A stored interaction about topic X is retrievable by semantic search later
- Delete removes the record from both SQLite and VectorStore

---

## Issue 2.5 — macOS SwiftUI menubar app

**Context:** The app provides always-on status visibility and quick access to preferences, without a Dock presence. It acts as a launcher and monitor for the Go binary.

**Tasks:**
- Create Xcode project at `macos/` as a Swift package or Xcode project
- Target: macOS 14+, no Dock icon (`LSUIElement = YES` in Info.plist)
- App structure:
  - `MenubarApp.swift` — `@main` SwiftUI app, `MenuBarExtra` with icon
  - Status icon states: gray (stopped), green (running), orange (processing), red (error)
  - Menu items:
    - Status: "tbyd — running" / "tbyd — stopped"
    - Separator
    - "Open Data Browser" → opens `DataBrowserView` window
    - "Open Profile Editor" → opens `ProfileEditorView` window
    - Separator
    - "Preferences..." → opens settings sheet
    - "Start tbyd" / "Stop tbyd" — controls Go binary lifecycle
    - Separator
    - "Quit"
  - `StatusView.swift` — polls `GET /health` every 5s, updates icon
  - `PreferencesView.swift`:
    - OpenRouter API key field (stored in Keychain)
    - Default cloud model selector (fetched from `GET /v1/models`)
    - Toggle: save interactions
    - Toggle: auto-start at login (LaunchAgent plist)
    - Local model selectors (fast / deep)
  - `DataBrowserView.swift`:
    - List of recent interactions with search
    - List of context documents with delete capability
    - Simple stats: total docs, total interactions, storage size
- Go binary bundled inside the `.app` at `Contents/MacOS/tbyd`; app launches it on start

**Swift unit tests** (`macos/Tests/`):
- `StatusPollerTests` — inject a mock URLSession; verify poller transitions to `.running` on 200 and `.stopped` on connection error
- `PreferencesViewModelTests` — mock Keychain; verify API key saved on `save()` and loaded on init
- `DataBrowserViewModelTests` — mock API client; verify `loadInteractions()` populates `interactions` array and `deleteInteraction(id:)` removes entry from array
- `ProcessManagerTests` — mock `Process` launcher; verify `start()` spawns binary with correct path; verify `stop()` sends SIGTERM

**Acceptance criteria:**
- App appears in menubar only (no Dock icon)
- Status icon turns green when `GET /health` succeeds
- Clicking "Start tbyd" starts the Go binary; "Stop tbyd" stops it
- Profile changes in PreferencesView reflect in the next enriched prompt
- App launches Go binary on login if configured

---

## Issue 2.6 — macOS Share Extension

**Context:** The Share Extension lets users send selected text, URLs, and files from any app directly into the knowledge base with a single click.

**Tasks:**
- Add a new target to the Xcode project: Share Extension
- Extension type: Post type (allows arbitrary content types)
- Supported content types: `public.text`, `public.url`, `public.file-url`, `public.image`
- `ShareViewController.swift`:
  - Receive shared item(s) from NSExtensionItem
  - Display a compact UI:
    - Content preview (title + snippet)
    - Tags input field (comma-separated)
    - Optional note text field
    - "Save to tbyd" button + Cancel
  - On save: POST to `http://localhost:4000/ingest` with appropriate type/content
  - Handle server not running: show error "tbyd is not running. Start it from the menubar."
  - Handle large files: warn if > 10MB, suggest using CLI instead
- Set `NSExtensionActivationRule` to accept: text, URLs, file URLs (PDF, txt, md, html)
- App Group for shared Keychain access (if needed for API endpoint config)

**Swift unit tests** (`macos/Tests/ShareExtensionTests/`):
- `ShareViewControllerTests.testTextItem` — inject `NSExtensionItem` with plain text; mock URLSession; verify POST body contains `type: "text"` and correct content
- `ShareViewControllerTests.testURLItem` — inject URL item; verify POST body contains `type: "url"` and URL string
- `ShareViewControllerTests.testFileItem` — inject PDF file URL; verify POST body contains `type: "file"` and base64-encoded content
- `ShareViewControllerTests.testServerNotRunning` — mock URLSession returns connection error; verify error UI shown (not crash)
- `ShareViewControllerTests.testLargeFileWarning` — inject file > 10MB; verify warning shown before POST attempted
- `ShareViewControllerTests.testTagsParsed` — enter "go,privacy" in tags field; verify POST body tags array is `["go", "privacy"]`

**Acceptance criteria:**
- In Mail.app: select text → Share → tbyd → add tags → Save → doc appears in knowledge base
- In Finder: right-click PDF → Share → tbyd → PDF text is extracted and stored
- In Safari: Share button → tbyd → current page URL is fetched and stored
- When server is not running: clear error message (no crash)
- Extension responds in < 2s (queues immediately, does not wait for enrichment)

---

## Issue 2.7 — File MIME detection and content extraction

**Context:** The `POST /ingest` handler accepts `type: "file"` payloads with base64-encoded content, but currently casts the decoded bytes directly to a string. Binary formats (PDF, images) produce garbage text that corrupts the vector store and degrades retrieval quality.

**Tasks:**
- In `internal/api/ingest.go`, after base64-decoding the file payload:
  - Detect MIME type using `http.DetectContentType(decoded[:512])`
  - Route by MIME type:
    - `application/pdf` — extract plain text using a PDF-to-text library (e.g., `github.com/ledongthuc/pdf` or `pdfcpu`)
    - `text/plain`, `text/markdown`, `text/html` — store decoded bytes directly as UTF-8 string (strip HTML tags for `text/html`)
    - Unsupported MIME types — return `400` with message `"unsupported file type: <mime>"`
  - Store the extracted text (not raw bytes) in `context_docs.content`
  - Store the detected MIME type in `context_docs` metadata (extend `IngestRequest.Metadata` if needed)
- Add unit tests to `internal/api/ingest_test.go`:
  - `TestIngest_FilePDF` — POST base64-encoded minimal PDF; verify extracted text stored (not raw bytes)
  - `TestIngest_FileMarkdown` — POST base64-encoded markdown; verify content stored as-is
  - `TestIngest_FileUnsupportedMIME` — POST base64-encoded binary (e.g., PNG without image handling); verify 400 returned

**Acceptance criteria:**
- `tbyd ingest --file report.pdf` results in readable extracted text appearing in `tbyd recall`
- `tbyd ingest --file notes.md` stores the markdown content verbatim
- Ingesting an unsupported binary returns a clear error, not silent garbage

---

## Issue 2.8 — MCP HTTP/SSE transport and config snippet

**Context:** The MCP server currently only starts a stdio transport (suitable for `claude mcp add` via CLI). Claude Desktop and other HTTP-based MCP clients need an HTTP/SSE transport on port 4001. Additionally, there is no auto-generated config snippet to guide users through the initial setup.

**Tasks:**
- In `internal/api/mcp.go`:
  - Start an HTTP/SSE MCP transport on the configured port (default 4001) in addition to stdio
  - HTTP transport requires `Authorization: Bearer <token>` header (same token as the REST API)
  - Both transports register the same tools and resources; share the handler implementation
- On first server start (or when `tbyd status` is run and MCP is not yet configured), print a setup snippet:
  ```
  Add tbyd to Claude Code:
    claude mcp add tbyd --transport http --url http://localhost:4001

  Or add to ~/.claude/settings.json:
    { "mcpServers": { "tbyd": { "url": "http://localhost:4001", "headers": { "Authorization": "Bearer <token>" } } } }
  ```
  Token value should be read from Keychain and substituted in the printed snippet.
- Add unit tests to `internal/api/mcp_test.go`:
  - `TestMCPHTTP_AddContext` — start HTTP transport; POST tool call via HTTP with valid bearer token; verify doc stored
  - `TestMCPHTTP_Unauthorized` — POST tool call without token; verify 401
  - `TestMCPHTTP_ConcurrentCalls` — 10 concurrent HTTP tool calls; verify no panics, all respond

**Acceptance criteria:**
- `claude mcp add tbyd --transport http --url http://localhost:4001` makes tools available in Claude Code
- HTTP requests without bearer token return 401
- Stdio transport continues to work alongside HTTP transport
- First-run snippet is printed with the actual token substituted

---

## Issue 2.9 — Interaction storage onboarding prompt

**Context:** The `save_interactions` flag defaults to `false` and users may never discover it. The spec requires a one-time onboarding prompt on the first request so users can make an informed choice about local interaction storage.

**Tasks:**
- Track whether the onboarding prompt has been shown in the config (e.g., `[storage] onboarding_shown = false`)
- In the OpenAI-compatible request handler (`internal/api/openai.go`), before processing the first request after server start:
  - If `save_interactions` has never been explicitly set and `onboarding_shown = false`:
    - Print/log the onboarding message to stderr (visible in `tbyd start` foreground output) and to the MCP response metadata if applicable:
      ```
      tbyd can store your interactions locally for improved context retrieval.
      This data never leaves your machine. Enable with: tbyd config set storage.save_interactions true
      ```
    - Set `onboarding_shown = true` in config so the prompt is shown only once
- Add unit test to `internal/api/interactions_test.go`:
  - `TestOnboardingPrompt_ShownOnce` — send two requests with `onboarding_shown=false`; verify prompt emitted exactly once
  - `TestOnboardingPrompt_NotShownWhenConfigured` — send request with `save_interactions` explicitly set; verify no prompt emitted

**Acceptance criteria:**
- On the first ever request, the onboarding message appears in server output
- After `tbyd config set storage.save_interactions true/false`, the prompt never appears again
- Prompt is shown at most once across server restarts

---

## Issue 2.10 — Menubar app: PreferencesViewModelTests and StatusView

**Context:** Two spec deliverables are missing from the macOS menubar app: the `PreferencesViewModelTests` test suite (the only required test file not yet written) and a dedicated `StatusView.swift` component (the status polling logic is present in `StatusPoller`+`AppState` but not as a named view file).

**Tasks:**
- Create `macos/Tests/PreferencesViewModelTests.swift`:
  - `testSaveAPIKey` — call `save()` with a key value; mock `KeychainService`; verify key written to Keychain
  - `testLoadAPIKey` — pre-populate mock Keychain; init view model; verify `apiKey` field populated
  - `testSetSaveInteractions` — toggle `saveInteractions`; mock API client; verify `PATCH /profile` sent with correct field
  - `testSetAutoStart` — toggle `autoStart`; mock `LaunchAgentManager`; verify plist installed/removed
- Create `macos/Sources/App/StatusView.swift`:
  - A SwiftUI `View` that displays the current server status with the icon and label
  - Reads from `AppState` (already `@Observable`)
  - Extracted from the inline label closure in `TBYDApp.swift`/`MenuBarContentView.swift`
  - No new logic needed — purely a named extraction of existing UI code

**Unit tests:**
- The four `PreferencesViewModelTests` tests listed above
- Existing `StatusPollerTests` already cover the polling behaviour; `StatusView` itself needs no additional tests

**Acceptance criteria:**
- `swift test` passes with `PreferencesViewModelTests` included
- `StatusView.swift` exists and is used by `TBYDApp` or `MenuBarContentView`
- Saving an API key in `PreferencesView` is covered by an automated test

---

## Phase 2 Verification

1. Open Claude Code, run `tbyd recall "what are my Go preferences"` via MCP → verify relevant stored docs returned
2. From Safari, share a technical article → verify it appears in `tbyd interactions list` / Data Browser
3. From Mail.app, share an email excerpt with tag `work` → verify tag preserved and searchable
4. Run `tbyd ingest --url "https://..." --tags "research"` → verify stored and retrievable
5. Open Preferences, toggle "save interactions" → send a query → verify behavior matches toggle
6. Quit menubar app → Go binary stops; reopen → binary restarts
7. Profile Editor: set tone to "formal" → send query → verify "formal" appears in enriched system prompt
8. Verify `POST /ingest` without bearer token returns 401
9. Verify `tbyd data export` produces valid JSONL
10. `go test ./...` passes
11. Swift tests pass: `xcodebuild test -scheme tbyd -destination 'platform=macOS'`
