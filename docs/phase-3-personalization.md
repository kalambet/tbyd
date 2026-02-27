# Phase 3 — Personalization

> **Goal:** The system learns from usage. Feedback signals feed into a continuously updated user profile. The deep local model runs background synthesis passes to surface preference patterns. The digital self representation becomes richer with every interaction.

---

## Issue 3.1 — Feedback collection API and UI

**Context:** User feedback (positive/negative) on cloud responses is the primary signal for preference learning. Feedback must be low-friction to collect and reliably stored for later synthesis.

**Tasks:**
- Add feedback endpoint: `POST /interactions/:id/feedback`
  - Body: `{"score": 1 | -1, "notes": "optional correction or comment"}`
  - Updates `feedback_score` and `feedback_notes` in SQLite `interactions` table
  - Queues a preference update job (see Issue 3.3)
  - Returns `{"status": "ok"}`
- Add MCP tool: `rate_response`
  - Args: `{interaction_id: string, score: "positive"|"negative", notes?: string}`
  - Calls the feedback endpoint
  - Allows Claude Code users to rate responses inline
- In SwiftUI Data Browser: add thumbs up/down buttons on each interaction row
- In CLI: `tbyd interactions rate <id> [--positive | --negative] [--note "..."]`
- Schema: ensure `feedback_score` is indexed in SQLite for efficient synthesis queries

**Unit tests** (`internal/api/feedback_test.go`):
- `TestFeedback_Positive` — POST score=1; verify `UpdateFeedback` called on store with score=1
- `TestFeedback_Negative` — POST score=-1 with notes; verify notes stored
- `TestFeedback_InvalidScore` — POST score=2; verify 400 response
- `TestFeedback_InteractionNotFound` — POST to unknown ID; verify 404
- `TestFeedback_QueuesPreferenceJob` — verify preference extractor job enqueued after successful feedback save
- `TestFeedback_IndexExists` — verify `feedback_score` column has an index in the migrations SQL

**Acceptance criteria:**
- A response can be rated via MCP tool from within Claude Code
- A response can be rated via CLI
- Rated interactions appear with score in `tbyd interactions list`
- `go test ./internal/api/...` covers feedback endpoint

---

## Issue 3.2 — User profile editor (explicit profile building)

**Context:** Users can explicitly declare their identity, expertise, interests, and opinions rather than waiting for the system to infer them. Explicit entries are highest-priority in enrichment.

**Tasks:**
- Extend `internal/profile/types.go` with full `Profile` schema (see PLAN.md Digital Self Representation section)
- Implement `internal/profile/manager.go` CRUD operations for all profile fields
- Extend profile HTTP endpoints (basic GET/PATCH added in Phase 2):
  - `DELETE /profile/:field` — remove a field or array item
- In SwiftUI `ProfileEditorView.swift`:
  - Section: Identity (role, expertise key-value pairs, current projects)
  - Section: Communication (tone dropdown, detail level, format)
  - Section: Interests (editable tag list)
  - Section: Opinions (free-form text list — each line is one opinion/point-of-view)
  - Section: Preferences (free-form list — explicit instruction lines like "never use bullet points")
  - Save button → PATCH /profile
  - Each free-form list supports add/remove/reorder
- In CLI: `tbyd profile edit` opens `$EDITOR` with full profile JSON

**Unit tests** (`internal/profile/manager_test.go` — extend from Phase 1):
- `TestPatchProfile_MergesFields` — existing profile has tone "direct"; PATCH with `{tone: "formal"}`; verify tone updated, all other fields unchanged
- `TestPatchProfile_AppendsToArrays` — existing interests has 2 items; PATCH adds 1; verify 3 items total
- `TestDeleteProfileField_Scalar` — set `communication.tone`; delete it; verify field absent in next `GetProfile()`
- `TestDeleteProfileField_ArrayItem` — interests has ["go", "privacy"]; delete "go"; verify only ["privacy"] remains
- `TestDeleteProfileField_NotFound` — delete field that doesn't exist; verify 404-equivalent error
- `TestGetSummary_ExplicitPreferencesFirst` — profile has both inferred and explicit preferences; verify explicit ones appear first in summary
- `TestProfileRoundTrip` — set complex nested profile via `SetField`; retrieve via `GetProfile`; verify deep equality

**Unit tests** (`internal/api/profile_test.go`):
- `TestGetProfile_ReturnsJSON` — GET /profile; verify Content-Type application/json and valid JSON body
- `TestPatchProfile_PartialUpdate` — PATCH one field; verify other fields unchanged via subsequent GET
- `TestDeleteProfileField` — DELETE /profile/communication.tone; verify field gone in GET response

**Acceptance criteria:**
- A preference added in the editor ("always show code examples") appears in the enriched system prompt on the next query
- Opinions and interests from the editor are reflected in the profile summary
- Profile edit via both SwiftUI and CLI editor round-trips without data loss
- Profile with 20+ preferences still produces a `GetSummary()` under 500 tokens

---

## Issue 3.3 — Preference extraction from feedback (background job)

**Context:** When feedback is received, the deep local model runs an extraction pass to identify what the feedback implies about user preferences. This updates the profile automatically.

**Tasks:**
- Create `internal/synthesis/feedback.go`:
  - `PreferenceExtractor` struct wrapping `ollama.Client` (deep model)
  - `ExtractFromFeedback(interaction Interaction, score int, notes string) ([]PreferenceSignal, error)`
    - Builds a prompt: given the original query, enriched prompt, response, and feedback, what does this tell us about the user's preferences?
    - Returns structured `PreferenceSignal`:
      ```go
      type PreferenceSignal struct {
          Type       string  // "positive" | "negative"
          Pattern    string  // "user likes concise responses without preamble"
          Confidence float64
      }
      ```
  - Runs asynchronously via the SQLite-backed job queue (enqueues a `feedback_extract` job) (does not block feedback endpoint response)
- Create `internal/synthesis/aggregator.go`:
  - `Aggregator` accumulates `PreferenceSignal` over time
  - `Aggregate(signals []PreferenceSignal) ProfileDelta`
  - `ProfileDelta` struct: `{AddPreferences []string, RemovePreferences []string, UpdateFields map[string]string}`
  - Only applies delta when confidence is above threshold (configurable, default 0.8) OR signal appears 3+ times
- Wire into `profile.Manager.ApplyDelta(delta ProfileDelta)`
- Write unit tests with synthetic feedback scenarios

**Unit tests** (`internal/synthesis/feedback_test.go`) — mock `ollama.Client`:
- `TestExtractFromFeedback_PositiveScore` — mock LLM returns signals; verify signals have `Type: "positive"`
- `TestExtractFromFeedback_NegativeScore` — verify `Type: "negative"` on negative feedback
- `TestExtractFromFeedback_LLMFails` — mock LLM errors; verify empty slice returned (not panic)
- `TestExtractFromFeedback_MalformedLLMResponse` — mock returns non-JSON; verify empty slice

**Unit tests** (`internal/synthesis/aggregator_test.go`) — pure logic, no mocks needed:
- `TestAggregate_BelowThreshold` — 1 signal with confidence 0.5; verify delta has no changes
- `TestAggregate_AboveThreshold` — 1 signal with confidence 0.9; verify preference added
- `TestAggregate_RepeatSignal` — same pattern 3 times at confidence 0.5; verify preference added (count rule)
- `TestAggregate_ConflictingSignals` — same pattern as both positive and negative; verify no change (conflict resolution)
- `TestAggregate_RemovesNegated` — existing preference X in profile; negative signal for X; verify X in `RemovePreferences`
- `TestApplyDelta_AddsPreferences` — apply delta with 2 new preferences; verify both in profile after apply
- `TestApplyDelta_RemovesPreferences` — apply delta removing 1; verify gone from profile
- `TestApplyDelta_Idempotent` — apply same delta twice; verify no duplicates created

**Acceptance criteria:**
- After 5 negative feedback instances for "verbose responses", the profile preference "concise responses" is auto-added
- After 3 positive feedback instances for "code examples included", the preference "include code examples" is added
- Low-confidence or one-off signals do not modify the profile
- `go test ./internal/synthesis/...` passes

---

## Issue 3.4 — Profile injection into enrichment pipeline

**Context:** The profile summary must be calibrated to the user's actual profile content, not just a static template. As profile grows, injection becomes more targeted.

**Tasks:**
- Extend `internal/profile/manager.go`:
  - `GetCalibrationContext() CalibrationContext` — returns hints for calibrating the intent extractor's system prompt
    - Example: if user is a Go expert, intent extractor system prompt includes "User is an expert Go developer. Technical jargon is expected."
  - `GetSummary()` update: prioritize explicitly-set preferences over inferred ones; truncate lower-priority items if token budget exceeded
- Update `internal/intent/extractor.go`:
  - Accept `CalibrationContext` in constructor
  - Inject calibration into the extraction system prompt
  - Benefit: the local model makes better extraction choices when it knows the user's domain expertise
- Update `internal/composer/prompt.go`:
  - Separate "explicit preferences" section (always injected, highest priority) from "context" section (injected if budget allows)
  - Explicit preferences come directly from `profile.Preferences` and `profile.Opinions`
  - Never truncate explicit preferences — only truncate retrieved context chunks
  - Hard cap: if explicit preferences exceed 200 tokens, include only the most recent N that fit. Remaining preferences are accessible via the MCP `recall` tool.

**Unit tests** (`internal/profile/calibration_test.go`):
- `TestGetCalibrationContext_GoExpert` — profile has `expertise.go = "expert"`; verify calibration string includes "expert Go"
- `TestGetCalibrationContext_EmptyProfile` — empty profile; verify calibration string is non-empty but generic
- `TestGetCalibrationContext_MultipleExpertise` — profile has 3 expertise entries; verify all mentioned in calibration

**Unit tests** (`internal/composer/prompt_test.go` — extend from Phase 1):
- `TestCompose_ExplicitPreferencesNeverTruncated` — 30 explicit preferences + 20 chunks totalling > budget; verify all 30 preferences present, chunks truncated instead
- `TestCompose_ExplicitSectionBeforeContext` — verify `[Explicit Preferences]` section appears before `[Relevant Context]` in system message
- `TestCompose_InferredPreferencesMayBeTruncated` — large inferred preference list + full context; verify inferred list truncated to fit budget

**Unit tests** (`internal/intent/extractor_test.go` — extend from Phase 1):
- `TestExtract_WithCalibration` — pass calibration context with domain "Go"; verify calibration text appears in prompt sent to Ollama mock

**Acceptance criteria:**
- With `expertise: {go: "expert"}` in profile, the enriched prompt includes expert-level calibration
- Explicit preferences are always present in the system prompt regardless of context volume
- A profile with 30 preferences + 10 context chunks fits within token budget without losing explicit preferences

---

## Issue 3.5 — Nightly profile synthesis (background job)

**Context:** Beyond per-feedback updates, the deep model runs a holistic synthesis pass over recent activity to detect emerging patterns and update the profile comprehensively.

**Tasks:**
- Create `internal/synthesis/nightly.go`:
  - `NightlySynthesizer` struct
  - `Run(ctx context.Context) error` — main synthesis pass:
    1. Query SQLite for interactions in last 7 days with feedback
    2. Query SQLite for context docs added in last 7 days
    3. Query SQLite for recent feedback signals
    4. Build synthesis prompt for deep model (mistral-nemo)
    5. Parse response as `ProfileDelta`
    6. Write delta to a pending-review table (user reviews before applying)
    7. Notify via local notification if changes detected
  - `Schedule(interval time.Duration)` — runs `Run()` on a ticker (default: daily at 2 AM)
    - Uses the job queue to schedule synthesis runs (enqueues a `nightly_synthesis` job)
- Add `pending_profile_deltas` table via new migration (002_synthesis.sql)
- Add `GET /profile/pending-deltas` and `POST /profile/pending-deltas/:id/accept|reject` endpoints
- In SwiftUI: show notification badge on menubar icon when pending profile updates exist; show review UI in ProfileEditorView

**Unit tests** (`internal/synthesis/nightly_test.go`) — mock store, mock LLM:
- `TestRun_NoInteractions` — empty store; verify `Run()` completes without error, no delta written
- `TestRun_ProducesDeltas` — store has 10 feedback-labeled interactions; mock LLM returns valid delta JSON; verify delta written to pending table
- `TestRun_LLMMalformedResponse` — mock LLM returns invalid JSON; verify no delta written, error logged
- `TestRun_ContextCancellation` — cancel context; verify `Run()` exits promptly
- `TestSchedule_FiresOnInterval` — inject 100ms interval and mock clock; verify `Run()` called at least twice in 250ms
- `TestSchedule_StopsOnContextCancel` — cancel context; verify no further `Run()` calls after cancellation

**Unit tests** (`internal/api/deltas_test.go`):
- `TestGetPendingDeltas_Empty` — no pending deltas; verify empty array returned
- `TestGetPendingDeltas_ReturnsList` — 2 pending deltas; verify both returned with full JSON
- `TestAcceptDelta` — POST accept; verify `accepted=1` and `reviewed_at` set in store; verify profile updated
- `TestRejectDelta` — POST reject; verify `accepted=0`, `reviewed_at` set; verify profile NOT updated
- `TestAcceptDelta_AlreadyReviewed` — accept an already-reviewed delta; verify 409 conflict
- `TestRejectedDeltaNotReapplied` — reject delta; run synthesis again with same data; verify same delta not recreated

**Integration test** (`internal/synthesis/nightly_integration_test.go`) — tag `integration`, requires Ollama:
- `TestSynthesisEndToEnd` — insert 5 feedback-labeled interactions with consistent negative feedback on verbosity; run `NightlySynthesizer.Run()`; verify pending delta contains "concise" preference addition

**Acceptance criteria:**
- Synthesis runs without crashing on a user with 0 interactions
- Synthesis correctly identifies a pattern from 10+ similar interactions (e.g., user always asks follow-up about performance)
- User can accept or reject individual deltas from both CLI and SwiftUI
- Rejected deltas are never re-applied
- `go test ./internal/synthesis/...` passes

---

## Issue 3.6 — Deep enrichment pass (two-pass ingestion model)

**Context:** The fast model (phi3.5) provides instant searchability on ingestion, but the deep model (mistral-nemo) produces significantly better extraction at any content length — better entity disambiguation, cross-document reasoning, domain classification, and key point extraction. This issue implements the second pass of the two-pass ingestion model: a batched deep enrichment that runs when the machine is idle or overnight, re-processing raw content with the deep model to additively improve the knowledge base.

Raw content is always preserved in `context_docs.content` after pass 1. Pass 2 reads this raw material and produces richer metadata without discarding the fast model's initial work. The system is fully functional on pass 1 alone; pass 2 is an eventually-consistent quality improvement.

**Tasks:**
- Implement idle detection in `internal/synthesis/idle.go`:
  - `IdleDetector` struct
  - `IsIdle() bool` — checks CPU usage < threshold AND available memory > threshold
  - macOS: use `host_statistics` via CGO or subprocess (`vm_stat`, `sysctl`)
  - Optional: detect screen lock via `CGSessionCopyCurrentDictionary` (macOS)
  - Config: `enrichment.deep_idle_cpu_max_percent` (default: 10) and `enrichment.deep_idle_mem_min_gb` (default: 12)
- Implement context-window-aware batching in `internal/synthesis/batcher.go`:
  - `Batcher` struct with configurable max token count per batch
  - `BatchDocuments(docs []ContextDoc) [][]ContextDoc` — packs documents into batches by token count
  - Token estimation: use word count * 1.5 as approximation (avoids tokenizer dependency; the higher multiplier accounts for code, technical terms, and non-English content). Reserve 10% of the context window as safety margin to avoid runtime overflow.
  - A single document exceeding the context window is placed alone in its own batch and chunked: split into sequential segments that each fit the context window, process each chunk independently, then merge the per-chunk `DeepEnrichment` results (union entities/topics, concatenate key points, deduplicate). Log a warning when chunking occurs so operators can tune batch sizes or document limits.
- Implement topic-similarity grouping in `internal/synthesis/grouper.go`:
  - `GroupByTopics(docs []ContextDoc) [][]ContextDoc` — groups documents with overlapping pass 1 topics
  - Uses Jaccard similarity on topic tag sets (threshold: > 0.3 overlap)
  - Ungrouped documents (no topic overlap with any other) form a single logical "mixed" group
  - All groups (topic-based and mixed) are then passed to the `Batcher` for context-window packing — a large mixed group will be split into multiple physical batches that each fit within the context window
- Create deep enrichment prompt template in `internal/synthesis/deep_enrich.go`:
  - `DeepEnricher` struct wrapping `ollama.Client` (deep model)
  - `EnrichBatch(ctx context.Context, docs []ContextDoc) ([]DeepEnrichment, error)`
  - Prompt: given batch of raw documents + their pass 1 topics, produce per-document enriched extraction
  - `DeepEnrichment` struct:
    ```go
    type DeepEnrichment struct {
        DocID                string
        EnrichedEntities     []string
        EnrichedTopics       []string
        DeepKeyPoints        []string
        CrossReferences      []string  // IDs of related docs in batch
        DomainClassification string
        RelationshipNotes    string
    }
    ```
  - Parse structured JSON response from deep model
- Wire `ingest_deep_enrich` job creation into ingestion flow:
  - After pass 1 completes in the ingestion pipeline, enqueue an `ingest_deep_enrich` job with `payload_json` containing the `context_docs.id`
- Implement batch worker in `internal/synthesis/deep_worker.go`:
  - `DeepEnrichmentWorker` struct
  - `Run(ctx context.Context) error` — claims up to 5,000 pending `ingest_deep_enrich` jobs per run (configurable via `enrichment.deep_batch_claim_limit`), loads raw content from `context_docs`, groups by topic, batches by token count, processes each batch with deep model. Loops until the queue is drained or the context is cancelled.
  - Job recovery: on startup, reset any jobs stuck in `running` status for longer than a configurable timeout (default: 30 minutes) back to `pending`, incrementing their `attempts` counter. Jobs exceeding `max_attempts` are marked `failed`. This prevents indefinite stalls after a worker crash.
  - Activation: triggered by `IdleDetector.IsIdle()` returning true OR by the scheduled overnight window
  - `Schedule(idleCheckInterval, scheduledTime)` — polls idle detector on interval; also fires at scheduled time
- Implement additive metadata update:
  - Add migration `003_deep_enrichment.sql`: `ALTER TABLE context_docs ADD COLUMN deep_metadata TEXT DEFAULT '{}'` (JSON column for deep extraction fields)
  - Merge `DeepEnrichment` results into `context_docs`: update tags, write deep extraction fields to `deep_metadata`
  - Optionally refine vector chunks: if deep extraction reveals key points missed by pass 1, add new vector entries with `source_type: "deep_enrichment"`
  - Preserve all pass 1 data — deep enrichment is additive, never destructive
- Add config keys to `internal/config/keys.go`:
  - `enrichment.deep_enabled` (bool, default: false)
  - `enrichment.deep_schedule` (string, default: "2:00")
  - `enrichment.deep_idle_cpu_max_percent` (int, default: 10)
  - `enrichment.deep_idle_mem_min_gb` (int, default: 12)

**Unit tests** (`internal/synthesis/batcher_test.go`):
- `TestBatchDocuments_FitsInOneBatch` — 5 short docs totalling < context window; verify single batch
- `TestBatchDocuments_SplitsIntoBatches` — 20 docs exceeding context window; verify multiple batches, each within limit
- `TestBatchDocuments_LargeDocAlone` — 1 doc exceeding context window; verify placed in its own batch
- `TestBatchDocuments_EmptyInput` — no docs; verify empty result (no error)
- `TestBatchDocuments_TokenEstimation` — doc with known word count; verify token estimate within 20% of actual

**Unit tests** (`internal/synthesis/grouper_test.go`):
- `TestGroupByTopics_OverlappingTopics` — 3 docs share topic "go"; verify grouped together
- `TestGroupByTopics_NoOverlap` — 3 docs with disjoint topics; verify mixed batch
- `TestGroupByTopics_PartialOverlap` — 5 docs, 2 share "kubernetes", 2 share "privacy", 1 isolated; verify 3 groups
- `TestGroupByTopics_EmptyTopics` — docs with no pass 1 topics; verify all placed in mixed batch

**Unit tests** (`internal/synthesis/deep_enrich_test.go`) — mock `ollama.Client`:
- `TestEnrichBatch_ParsesResponse` — mock LLM returns valid JSON for 3 docs; verify 3 `DeepEnrichment` results with correct doc IDs
- `TestEnrichBatch_CrossReferences` — mock LLM identifies 2 related docs; verify `CrossReferences` populated
- `TestEnrichBatch_LLMFails` — mock LLM errors; verify empty result (not panic), error returned
- `TestEnrichBatch_MalformedJSON` — mock returns invalid JSON; verify graceful error handling

**Unit tests** (`internal/synthesis/deep_worker_test.go`) — mock store, mock LLM:
- `TestRun_EmptyQueue` — no pending jobs; verify exits without error, model not loaded
- `TestRun_ProcessesBatch` — 5 pending jobs; verify all claimed, processed, marked completed
- `TestRun_AdditiveUpdate` — verify pass 1 tags preserved after deep enrichment updates metadata
- `TestRun_ContextCancellation` — cancel context mid-batch; verify partial progress saved, remaining jobs stay pending

**Unit tests** (`internal/synthesis/idle_test.go`):
- `TestIsIdle_BelowThreshold` — mock CPU 5%, memory 16GB available; verify returns true
- `TestIsIdle_AboveThreshold` — mock CPU 50%; verify returns false
- `TestIsIdle_MemoryConstrained` — mock memory 4GB available; verify returns false

**Acceptance criteria:**
- Ingesting a document enqueues an `ingest_deep_enrich` job in the jobs table
- Manually triggering deep enrichment updates `context_docs` metadata with richer extraction
- Pass 1 data (tags, summary, entities) is preserved after pass 2 runs
- Querying a document works before deep enrichment (pass 1 sufficient for retrieval)
- Querying after deep enrichment shows improved metadata in results
- System works normally with `enrichment.deep_enabled = false` (no deep enrichment jobs created)
- Batching respects context window limits and groups related topics
- `go test ./internal/synthesis/...` passes

---

## Phase 3 Verification

1. Rate 5 consecutive responses as negative because they were "too long" → check if a "concise" preference appears in profile after synthesis
2. Open ProfileEditorView → add opinion "I value privacy over convenience" → send a query → verify opinion appears in enriched system prompt
3. Run nightly synthesis manually via `tbyd profile synthesize` → verify pending deltas appear for review
4. Accept a delta → verify profile updated → send query → verify new preference reflected
5. Reject a delta → verify it does not reappear in next synthesis
6. Profile with 50 items → verify `GetSummary()` stays under 500 tokens
7. Ingest a document → verify `ingest_deep_enrich` job appears in jobs table
8. Manually trigger deep enrichment → verify `context_docs` metadata updated with richer extraction, pass 1 data preserved
9. Ingest 20 documents with mixed topics → trigger deep enrichment → verify topic-aware batching groups related documents
10. Disable deep enrichment (`enrichment.deep_enabled = false`) → ingest a document → verify no `ingest_deep_enrich` job created
11. `go test ./...` passes
12. `go test -tags integration ./...` passes
