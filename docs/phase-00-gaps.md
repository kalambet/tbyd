# Phase 00 — Foundation Gaps

> **Goal:** Bring the existing Phase 0 implementation up to spec before starting Phase 1. Each task is independently shippable and testable.

---

## Issue 00.1 — Add `LogConfig` to config

**Context:** The updated spec requires a log level config. Config is now stored in platform-native backends (UserDefaults on macOS, XDG JSON on Linux) — not TOML files.

**Tasks:**
- Add `LogConfig` struct to `internal/config/config.go`:
  ```go
  type LogConfig struct {
      Level string // default "info"
  }
  ```
- Add `Log LogConfig` field to `Config` struct
- Set default `Level: "info"` in `defaults()`
- Add key spec in `keys.go`: key `"log.level"`, env `TBYD_LOG_LEVEL`
- Initialize `log/slog` in `main.go` based on `cfg.Log.Level` (use `slog.LevelInfo` / `slog.LevelDebug`)

**Tests** (`internal/config/config_test.go`):
- `TestDefaults_LogLevel` — load with empty backend; verify `Log.Level == "info"`
- `TestBackendOverride_LogLevel` — set `"log.level"` in mock backend to `"debug"`; verify parsed correctly
- `TestEnvOverride_LogLevel` — set `TBYD_LOG_LEVEL=debug`; verify override applied

**Acceptance criteria:**
- `go test ./internal/config/...` passes with new tests
- `defaults write com.tbyd.app log.level -string debug` or `TBYD_LOG_LEVEL=debug` produces debug-level output

---

## Issue 00.2 — Add `status` column to `interactions` table

**Context:** The streaming/response capture design requires tracking whether a response completed, was aborted, or errored. The migration and model need updating.

**Tasks:**
- Add to `internal/storage/migrations/001_initial.sql`, after `cloud_response TEXT,`:
  ```sql
  status TEXT NOT NULL DEFAULT 'completed',
  ```
- Add `Status string` field to `Interaction` model in `internal/storage/models.go`
- Update `SaveInteraction` and `GetInteraction` / `GetRecentInteractions` to include the `status` column

**Tests** (`internal/storage/sqlite_test.go`):
- `TestSaveAndGetInteraction_Status` — save interaction with `status = "aborted"`; retrieve; verify field matches
- `TestSaveInteraction_DefaultStatus` — save interaction without explicit status; verify it defaults to `"completed"`

**Acceptance criteria:**
- Existing tests still pass (default status = "completed" is backward-compatible)
- New tests pass
- `go test ./internal/storage/...` passes

---

## Issue 00.3 — Add indexes to `interactions` table

**Context:** Phase 3 feedback synthesis queries by `feedback_score`, and most listing queries sort by `created_at`. Indexes should exist from day one.

**Tasks:**
- Add to `internal/storage/migrations/001_initial.sql`:
  ```sql
  CREATE INDEX IF NOT EXISTS idx_interactions_feedback ON interactions(feedback_score);
  CREATE INDEX IF NOT EXISTS idx_interactions_created ON interactions(created_at);
  ```

**Tests** (`internal/storage/sqlite_test.go`):
- `TestIndexesExist` — open store; query `sqlite_master` for `idx_interactions_feedback` and `idx_interactions_created`; verify both exist

**Acceptance criteria:**
- `go test ./internal/storage/...` passes
- Indexes confirmed via `sqlite_master` query in test

---

## Issue 00.4 — Add `context_vectors` table to migration

**Context:** The `VectorStore` interface and `SQLiteStore` implementation exist in `internal/retrieval/` but the migration creating the `context_vectors` table is missing from `001_initial.sql`. The retrieval code depends on this table.

**Tasks:**
- Add to `internal/storage/migrations/001_initial.sql`:
  ```sql
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
  ```

**Tests** (`internal/storage/sqlite_test.go`):
- `TestContextVectorsTableExists` — open store; `INSERT` and `SELECT` on `context_vectors`; verify round-trip works

**Acceptance criteria:**
- `go test ./internal/storage/...` passes
- `go test ./internal/retrieval/...` passes (retrieval tests can now use the shared migration)

---

## Issue 00.5 — Add `jobs` table and model

**Context:** The background job system (ingestion enrichment, feedback extraction, nightly synthesis) needs a durable SQLite-backed queue. This task adds the table and the `Job` model — methods come in the next task.

**Tasks:**
- Add to `internal/storage/migrations/001_initial.sql`:
  ```sql
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
- Add `Job` struct to `internal/storage/models.go`:
  ```go
  type Job struct {
      ID          string
      Type        string
      PayloadJSON string
      Status      string    // "pending", "running", "completed", "failed"
      Attempts    int
      MaxAttempts int
      RunAfter    time.Time
      CreatedAt   time.Time
      UpdatedAt   time.Time
      LastError   string
  }
  ```

**Tests** (`internal/storage/sqlite_test.go`):
- `TestJobsTableExists` — open store; raw `INSERT` into `jobs`; raw `SELECT`; verify round-trip

**Acceptance criteria:**
- `go test ./internal/storage/...` passes
- Jobs table and index confirmed via test

---

## Issue 00.6 — Add job queue methods to Store

**Context:** With the `jobs` table and model in place, add the CRUD methods that the background worker will use.

**Tasks:**
- Add to `internal/storage/sqlite.go`:
  - `EnqueueJob(job Job) error` — insert with status `"pending"`
  - `ClaimNextJob(types []string) (*Job, error)` — select the oldest `pending` job where `run_after <= now` and `type IN (types)`, atomically set status to `"running"` and `updated_at = now`; return `nil, nil` if no jobs available
  - `CompleteJob(id string) error` — set status to `"completed"`, `updated_at = now`
  - `FailJob(id string, errMsg string) error` — increment `attempts`, set `last_error`, if `attempts >= max_attempts` set status to `"failed"`, else set status back to `"pending"` and `run_after = now + backoff` (backoff = `2^attempts` seconds)

**Tests** (`internal/storage/sqlite_test.go`):
- `TestEnqueueAndClaimJob` — enqueue a job; claim it; verify all fields match and status is `"running"`
- `TestClaimNextJob_Empty` — no jobs; verify `ClaimNextJob` returns `nil, nil`
- `TestClaimNextJob_RespectRunAfter` — enqueue with `run_after` 1 hour from now; verify not claimed
- `TestClaimNextJob_TypeFilter` — enqueue jobs of type `"a"` and `"b"`; claim with `types=["a"]`; verify only type `"a"` claimed
- `TestClaimNextJob_SkipsRunning` — enqueue and claim a job; enqueue another; claim again; verify second job claimed (not the running one)
- `TestCompleteJob` — enqueue, claim, complete; verify status is `"completed"`
- `TestFailJob_IncrementsAttempts` — enqueue, claim, fail; verify `attempts == 1`, status back to `"pending"`
- `TestFailJob_MaxAttemptsReached` — enqueue with `max_attempts=1`; claim; fail; verify status is `"failed"`
- `TestFailJob_SetsBackoff` — fail a job; verify `run_after` is in the future

**Acceptance criteria:**
- `go test ./internal/storage/...` passes with all new tests
- Job lifecycle: enqueue → claim → complete/fail works correctly

---

## Issue 00.7 — API token generation and Keychain storage

**Context:** All non-OpenAI endpoints need bearer token auth to prevent CSRF attacks from malicious web pages targeting localhost. The token is generated on first run and stored in Keychain.

**Tasks:**
- Add to `internal/config/config.go`:
  - `GetAPIToken(kc keychain) (string, error)` — reads `tbyd-api-token` from Keychain; if not found, generates a random 256-bit (32-byte) hex-encoded token, stores it in Keychain, and returns it
- Add Keychain write support:
  - `keychain` interface: add `Set(service, account, value string) error`
  - `keychainReader` → rename to `keychainClient` and add `Set` using `security add-generic-password` CLI
  - Update `keychain_darwin.go` and `keychain_other.go` accordingly
- Call `GetAPIToken()` in `main.go` at startup and log (at info level) that the token is available

**Tests** (`internal/config/config_test.go`):
- Extend `mockKeychain` with a `Set` method and an in-memory map
- `TestGetAPIToken_GeneratesOnFirstCall` — empty Keychain mock; call `GetAPIToken`; verify non-empty 64-char hex string returned; verify `Set` was called
- `TestGetAPIToken_ReturnsExisting` — pre-populate mock with token; call `GetAPIToken`; verify same token returned; verify `Set` was NOT called
- `TestGetAPIToken_Deterministic` — call twice with same mock; verify same token both times

**Acceptance criteria:**
- `go test ./internal/config/...` passes
- Token is 64 hex chars (32 bytes)
- Token persists across restarts (stored in Keychain)

---

## Phase 00 Verification

1. `go test ./...` passes — all existing + new tests
2. `go test -tags integration ./...` passes
3. `make build` succeeds
4. Database opened with new migration has all tables: `interactions` (with `status`), `user_profile`, `context_docs`, `context_vectors`, `jobs`
5. Indexes confirmed on `interactions` and `context_vectors` and `jobs`
6. API token generated on first run and readable on second run
7. `defaults write com.tbyd.app log.level -string debug` produces debug output

---

## Suggested implementation order

```
00.1 (LogConfig) ─────────────────┐
00.2 (status column) ─────────────┤
00.3 (indexes) ───────────────────┼── all independent, can parallelize
00.4 (context_vectors table) ─────┤
00.5 (jobs table + model) ────────┘
                                  │
00.6 (job queue methods) ─────────┘ depends on 00.5
00.7 (API token) ─────────────────── independent
```
