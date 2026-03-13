# Phase 3 — Personalization

> **Goal:** The system learns from usage. Feedback signals feed into a continuously updated user profile. The deep local model runs background synthesis passes to surface preference patterns. The digital self representation becomes richer with every interaction.

---

## Issue 3.1 — Feedback collection API and UI

**Context:** User feedback (positive/negative) on cloud responses is the primary signal for preference learning. Feedback must be low-friction to collect and reliably stored for later synthesis.

**Prerequisite:** Issue 3.7 must land first — the interaction ID must be surfaced in API responses before the MCP `rate_response` tool can reference it.

**Go backend tasks:**
- Add feedback endpoint: `POST /interactions/:id/feedback`
  - Body: `{"score": 1 | -1, "notes": "optional correction or comment"}`
  - Updates `feedback_score` and `feedback_notes` in SQLite `interactions` table
  - Queues a `feedback_extract` job (see Issue 3.3)
  - Returns `{"status": "ok"}`
  - Returns 404 if interaction not found; 400 if score is not 1 or -1
- Add MCP tool: `rate_response`
  - Args: `{interaction_id: string, score: "positive"|"negative", notes?: string}`
  - Calls the feedback endpoint
  - Allows Claude Code users to rate responses inline using the ID surfaced via Issue 3.7
- In CLI: `tbyd interactions rate <id> [--positive | --negative] [--note "..."]`
- Schema: ensure `feedback_score` is indexed in SQLite for efficient synthesis queries

**Apple platform tasks (macos/):**
- Extend `TBYDKit/APIClient.swift` `Interaction` model: add `feedbackScore: Int?` and `feedbackNotes: String?` properties (map from `feedback_score`, `feedback_notes` JSON fields)
- Add `APIClient.postFeedback(interactionId: String, score: Int, notes: String?) async throws` method
- In `DataBrowserView`: add thumbs up / thumbs down buttons on each interaction row
  - Use SF Symbols `hand.thumbsup` / `hand.thumbsdown` (outline); switch to `.fill` variant when that score is already set
  - Apply `.buttonStyle(.borderless)` to avoid row selection conflicts
  - Optimistic UI: update button state immediately on tap; revert on network error
  - Show an inline error indicator (e.g., `Image(systemName: "exclamationmark.circle")`) if the request fails
  - Minimum 22×22pt hit area for both buttons
  - Accessibility: explicit `.accessibilityLabel("Rate positive")` / `.accessibilityLabel("Rate negative")` on each button; `.accessibilityValue("Selected")` when the score is already set
- Notes field: tapping the active thumb button opens a popover with a `TextField` for optional notes; confirm with "Save" or dismiss to rate without notes

**Unit tests** (`internal/api/feedback_test.go`):
- `TestFeedback_Positive` — POST score=1; verify `UpdateFeedback` called on store with score=1
- `TestFeedback_Negative` — POST score=-1 with notes; verify notes stored
- `TestFeedback_InvalidScore` — POST score=2; verify 400 response
- `TestFeedback_InteractionNotFound` — POST to unknown ID; verify 404
- `TestFeedback_QueuesPreferenceJob` — verify `feedback_extract` job enqueued after successful feedback save
- `TestFeedback_IndexExists` — verify `feedback_score` column has an index in the migrations SQL

**Swift tests** (`macos/Tests/DataBrowserViewModelTests.swift` — new file):
- `TestFeedback_OptimisticUpdate` — call `postFeedback`; verify `interaction.feedbackScore` updated immediately before response returns
- `TestFeedback_RevertsOnError` — mock `APIClient.postFeedback` throws; verify score reverted to original value
- `TestFeedback_AlreadyRated` — interaction with `feedbackScore: 1`; verify thumbs-up button shows fill variant

**Acceptance criteria:**
- A response can be rated via MCP tool from within Claude Code (using the ID from `X-TBYD-Interaction-ID` header / `tbyd-metadata` SSE event)
- A response can be rated via CLI
- Rated interactions appear with score in `tbyd interactions list`
- Thumbs buttons correctly reflect existing score state on load
- `go test ./internal/api/...` covers feedback endpoint

---

## Issue 3.2 — User profile editor (explicit profile building)

**Context:** Users can explicitly declare their identity, expertise, interests, and opinions rather than waiting for the system to infer them. Explicit entries are highest-priority in enrichment.

**Prerequisite:** Issue 3.9 (typed Swift `Profile` model in TBYDKit) must land before this issue. The `ProfileEditorViewModel` must use the typed model — the existing `[String: AnyCodable]` approach cannot support structured nested editing.

**Go backend tasks:**
- Extend `internal/profile/types.go` with full `Profile` schema (see ARCHITECTURE.md Digital Self Representation section)
- Implement `internal/profile/manager.go` CRUD operations for all profile fields
- Extend profile HTTP endpoints (basic GET/PATCH added in Phase 2):
  - `DELETE /profile/:field` — remove a field or array item
    - Supports dot-notation paths: `communication.tone`, `interests.primary[0]`, `expertise.go`
    - Returns 404 if field does not exist
- In CLI: `tbyd profile edit` opens `$EDITOR` with full profile JSON

**Apple platform tasks (macos/):**
- Rewrite `App/ProfileEditorView.swift` as a multi-section editor (the current 5-field form is a placeholder; this is a ground-up rewrite):
  - Use `NavigationSplitView` or a `Form` with collapsible `Section`s — decide based on expected section count and macOS HIG
  - **Section: Identity** — `TextField` for role; key-value list for expertise (each row: `TextField` for skill name + `Picker` for level: "beginner/intermediate/expert"); `TextField` list for current projects
  - **Section: Communication** — `Picker` for tone ("direct/friendly/formal"); `Picker` for detail level ("concise/balanced/thorough"); `Picker` for format ("prose/markdown/structured")
  - **Section: Interests** — tag list: `TextField` with on-commit-add pattern + horizontal flow of removable `Text` chips below (no native SwiftUI tokenField on macOS; use custom `FlowLayout` or a `List` of tags with add/remove buttons — `List` approach is more accessible)
  - **Section: Opinions** — free-form reorderable list (see list spec below)
  - **Section: Preferences** — free-form reorderable list (see list spec below)
  - **Section: Raw JSON** — retain the existing raw JSON editor as an escape hatch; collapse by default; warn that raw edits bypass validation
  - Save button → `PATCH /profile`; disable Save while request in flight; show inline error on failure
- **Reorderable list spec (Opinions and Preferences):** Use `struct EditableItem: Identifiable { let id: UUID; var text: String }` as the element type — never use the raw `String` as the identity since duplicates are valid. Implement as a standalone `List` with `ForEach($items) { $item in ... }.onMove { ... }.onDelete { ... }` outside a `Form` container (`.onMove` inside `Form` is unreliable on macOS). Use an `EditButton` or an always-on edit mode. Each row: `TextField` bound to `item.text` + drag handle indicator.
- **ProfileEditorViewModel** (rewrite): typed fields matching the `Profile` schema from Issue 3.9; `load()` decodes `Profile` from `GET /profile`; `save()` encodes and `PATCH /profile`; validation: role is non-empty; each opinion/preference is ≤ 500 chars; expertise skill names are non-empty
- Validation rules: role non-empty; preference/opinion items ≤ 500 characters; expertise skill name non-empty

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

**Swift tests** (`macos/Tests/ProfileEditorViewModelTests.swift` — extend):
- `TestLoad_PopulatesAllFields` — mock `GET /profile` returns full JSON; verify all `ProfileEditorViewModel` fields populated
- `TestSave_SendsPATCH` — modify tone; call `save()`; verify `PATCH /profile` called with updated tone only
- `TestValidation_EmptyRoleBlocked` — set role to ""; verify `save()` returns validation error, no HTTP call made
- `TestValidation_LongOpinionBlocked` — add opinion > 500 chars; verify validation error
- `TestReorderableList_StableIdentity` — add two identical opinions "A" and "A"; reorder; verify both preserved with distinct IDs

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
    - Input to LLM: `interaction.UserQuery` and `interaction.CloudResponse` **only** — do NOT include `EnrichedPrompt`. The enriched prompt contains injected retrieved context that belongs to the retrieval system, not the user; including it risks the model confusing past context with actual user preferences.
    - Prompt: given the original query, the cloud response, the feedback score, and any notes, what does this feedback reveal about the user's preferences?
    - Returns structured `PreferenceSignal`:
      ```go
      type PreferenceSignal struct {
          Type    string  // "positive" | "negative"
          Pattern string  // "user prefers concise responses without preamble"
      }
      ```
    - Note: no `Confidence` field. LLM self-reported confidence scores are poorly calibrated and produce unpredictable behavior at fixed thresholds. Confidence is replaced by the count rule in the aggregator.
  - Runs asynchronously via the SQLite-backed job queue (enqueues a `feedback_extract` job — does not block the feedback endpoint response)
- Create `internal/synthesis/aggregator.go`:
  - `Aggregator` accumulates `PreferenceSignal` over time
  - `Aggregate(signals []PreferenceSignal) ProfileDelta`
  - `ProfileDelta` struct: `{AddPreferences []string, RemovePreferences []string, UpdateFields map[string]string}`
  - **Activation rules (both applied, either is sufficient):**
    1. **Count rule (primary):** a pattern appears ≥ 3 times as the same type (positive or negative) → apply
    2. **Net score rule:** for a pattern with both positive and negative signals, compute `net = positive_count - negative_count`; if `net ≥ 2` → add preference; if `net ≤ -2` → remove preference; otherwise no change. This handles asymmetric evidence (4 positive vs 1 negative correctly adds the preference, unlike a simple "conflict = no change" rule).
  - `ApplyDelta(delta ProfileDelta)` on `profile.Manager` **must** call `cache.Invalidate()` — profile changes affect enrichment and must bust the query cache. Add this explicitly to the `ApplyDelta` implementation task.
- Wire into `profile.Manager.ApplyDelta(delta ProfileDelta)`
- Write unit tests with synthetic feedback scenarios

**Unit tests** (`internal/synthesis/feedback_test.go`) — mock `ollama.Client`:
- `TestExtractFromFeedback_PositiveScore` — mock LLM returns signals; verify signals have `Type: "positive"`
- `TestExtractFromFeedback_NegativeScore` — verify `Type: "negative"` on negative feedback
- `TestExtractFromFeedback_UsesOriginalQueryOnly` — verify the prompt sent to the LLM contains `UserQuery` and `CloudResponse` but NOT `EnrichedPrompt`
- `TestExtractFromFeedback_LLMFails` — mock LLM errors; verify empty slice returned (not panic)
- `TestExtractFromFeedback_MalformedLLMResponse` — mock returns non-JSON; verify empty slice

**Unit tests** (`internal/synthesis/aggregator_test.go`) — pure logic, no mocks needed:
- `TestAggregate_BelowCount` — pattern appears 2 times as positive; verify delta has no changes (count rule requires 3)
- `TestAggregate_CountRuleApplies` — pattern appears 3 times as positive; verify preference added
- `TestAggregate_NetScoreAdds` — 4 positive signals + 1 negative for same pattern; verify preference added (net = +3 ≥ 2)
- `TestAggregate_NetScoreRemoves` — 1 positive signal + 3 negative for same pattern; verify preference in RemovePreferences (net = -2)
- `TestAggregate_TrueConflict` — 2 positive + 2 negative for same pattern; verify no change (net = 0, below threshold)
- `TestAggregate_RemovesNegated` — existing preference X in profile; 3 negative signals for X; verify X in `RemovePreferences`
- `TestApplyDelta_AddsPreferences` — apply delta with 2 new preferences; verify both in profile after apply
- `TestApplyDelta_RemovesPreferences` — apply delta removing 1; verify gone from profile
- `TestApplyDelta_Idempotent` — apply same delta twice; verify no duplicates created
- `TestApplyDelta_InvalidatesCache` — apply delta; verify `cache.Invalidate()` called exactly once

**Acceptance criteria:**
- After 5 negative feedback instances for "verbose responses", the profile preference "concise responses" is auto-added
- After 3 positive feedback instances for "code examples included", the preference "include code examples" is added
- 4-positive-vs-1-negative for the same pattern results in the preference being added (not blocked as a "conflict")
- Low-evidence signals (< 3 occurrences, net score < 2) do not modify the profile
- `go test ./internal/synthesis/...` passes

---

## Issue 3.4 — Profile injection into enrichment pipeline

**Context:** The profile summary must be calibrated to the user's actual profile content, not just a static template. As profile grows, injection becomes more targeted.

**Tasks:**
- Extend `internal/profile/manager.go`:
  - `GetCalibrationContext() CalibrationContext` — returns hints for calibrating the intent extractor's system prompt
    - Example: if user is a Go expert, intent extractor system prompt includes "User is an expert Go developer. Technical jargon is expected."
  - `GetSummary()` update: prioritize explicitly-set preferences over inferred ones; truncate lower-priority items if token budget exceeded. **Truncation uses explicit priority ordering (position in the `preferences` array), not recency.** A preference added 6 months ago retains its position unless the user reorders it; positional ordering is the user's signal of importance.
- Update `internal/intent/extractor.go`:
  - Add `CalibrationContext` via a **functional option**: `WithCalibration(ctx CalibrationContext) ExtractorOption`, where `type ExtractorOption func(*Extractor)`. Constructor signature remains `NewExtractor(client OllamaChatter, model string, opts ...ExtractorOption)`. This is the least disruptive change to existing callers — callers that don't need calibration pass no options.
  - Inject calibration into the extraction system prompt when provided
  - Benefit: the local model makes better extraction choices when it knows the user's domain expertise
- Update `internal/composer/prompt.go`:
  - Separate `[Explicit Preferences]` section (always injected, highest priority) from `[Retrieved Context]` section (injected if budget allows)
  - Explicit preferences come directly from `profile.Preferences` and `profile.Opinions`
  - **Never truncate explicit preferences** — only truncate retrieved context chunks
  - Hard cap: if explicit preferences exceed 200 tokens, include the highest-priority N that fit (first items in the `preferences` array), not the most recently added. Remaining preferences are accessible via the MCP `recall` tool.

**Unit tests** (`internal/profile/calibration_test.go`):
- `TestGetCalibrationContext_GoExpert` — profile has `expertise.go = "expert"`; verify calibration string includes "expert Go"
- `TestGetCalibrationContext_EmptyProfile` — empty profile; verify calibration string is non-empty but generic
- `TestGetCalibrationContext_MultipleExpertise` — profile has 3 expertise entries; verify all mentioned in calibration

**Unit tests** (`internal/composer/prompt_test.go` — extend from Phase 1):
- `TestCompose_ExplicitPreferencesNeverTruncated` — 30 explicit preferences + 20 chunks totalling > budget; verify all 30 preferences present, chunks truncated instead
- `TestCompose_ExplicitSectionBeforeContext` — verify `[Explicit Preferences]` section appears before `[Relevant Context]` in system message
- `TestCompose_InferredPreferencesMayBeTruncated` — large inferred preference list + full context; verify inferred list truncated to fit budget
- `TestCompose_TruncationByPriority` — 10 preferences in known order; budget fits only 5; verify first 5 (highest priority) included, last 5 excluded

**Unit tests** (`internal/intent/extractor_test.go` — extend from Phase 1):
- `TestExtract_WithCalibration` — pass `WithCalibration(ctx)` option with domain "Go"; verify calibration text appears in prompt sent to Ollama mock
- `TestExtract_WithoutCalibration` — construct extractor with no options; verify no calibration text in prompt (no nil panic)

**Acceptance criteria:**
- With `expertise: {go: "expert"}` in profile, the enriched prompt includes expert-level calibration
- Explicit preferences are always present in the system prompt regardless of context volume
- A profile with 30 preferences + 10 context chunks fits within token budget without losing explicit preferences
- Preference truncation keeps the first (highest-priority) items, not the most recently added

---

## Issue 3.5 — Nightly profile synthesis (background job)

**Context:** Beyond per-feedback updates, the deep model runs a holistic synthesis pass over recent activity to detect emerging patterns and update the profile comprehensively.

**Go backend tasks:**
- Create `internal/synthesis/nightly.go`:
  - `NightlySynthesizer` struct
  - `Run(ctx context.Context) error` — main synthesis pass:
    1. Query SQLite for interactions in last 7 days with feedback — **sample strategy for context budget:** if > 100 interactions, select the 50 with highest absolute feedback score + 25 most recent + 25 random; this prevents context window overflow on heavy usage. Log the sample strategy used.
    2. Query SQLite for context docs added in last 7 days
    3. Query SQLite for recent feedback signals
    4. Estimate total token count before calling deep model; if > 80% of model context window, apply additional summarization pass using the fast model to compress interaction descriptions
    5. Build synthesis prompt for deep model (mistral-nemo)
    6. Parse response as `ProfileDelta`
    7. Write delta to `pending_profile_deltas` table (user reviews before applying)
    8. Notify via local notification if changes detected (see Apple platform tasks below)
  - `Schedule(scheduledTimeOfDay time.Time, checkInterval time.Duration)` — the scheduled time is a time-of-day (e.g., 02:00), not a duration. Implementation: on startup, compute the duration until the next occurrence of `scheduledTimeOfDay`; fire an initial timer for that duration, then use a 24h ticker. Also fires when `IdleDetector.IsIdle()` returns true during the `checkInterval` poll. Uses the job queue to enqueue `nightly_synthesis` jobs.
- Add `pending_profile_deltas` table via new migration **`004_synthesis.sql`**:
  ```sql
  CREATE TABLE IF NOT EXISTS pending_profile_deltas (
      id TEXT PRIMARY KEY,
      delta_json TEXT NOT NULL,
      description TEXT NOT NULL,      -- human-readable summary: "Add preference: concise responses"
      source TEXT NOT NULL,            -- "nightly_synthesis" | "feedback_aggregation"
      accepted INTEGER,                -- NULL = not reviewed; 1 = accepted; 0 = rejected
      reviewed_at DATETIME,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
  );
  ```
- Add `GET /profile/pending-deltas` — returns array of unreviewed deltas (where `accepted IS NULL`)
- Add `POST /profile/pending-deltas/:id/accept` — sets `accepted=1`, `reviewed_at=now`; applies delta to profile via `ApplyDelta` (which triggers cache invalidation); returns 409 if already reviewed
- Add `POST /profile/pending-deltas/:id/reject` — sets `accepted=0`, `reviewed_at=now`; does NOT apply delta; returns 409 if already reviewed
- Store a hash of the synthesis input in the delta record; if synthesis produces a delta identical to an already-rejected one (same hash), skip writing it
- Extend `GET /health` response to include `"pending_deltas_count": <int>` — enables the macOS app to poll this without a separate HTTP call
- In CLI:
  - `tbyd profile synthesize` — manually trigger synthesis (enqueues `nightly_synthesis` job, waits for completion)
  - `tbyd profile pending` — list pending deltas with ID, description, and created_at
  - `tbyd profile accept <id>` — accept a pending delta
  - `tbyd profile reject <id>` — reject a pending delta

**Apple platform tasks (macos/):**
- **Local notification infrastructure** (`App/NotificationManager.swift` — new file):
  - `NotificationManager` class with `requestPermission() async`
  - `sendPendingDeltaNotification(count: Int)` — delivers `UNUserNotificationCenter` notification with title "Profile Update Available" and body "tbyd detected \(count) preference update(s) to review."
  - Register notification category `"PROFILE_DELTA"` with action `"REVIEW"` that deep-links to the Profile Editor
  - Request permission on first synthesis run (not on app launch — request only when there is something to notify about)
  - Handle `UNUserNotificationCenterDelegate.userNotificationCenter(_:didReceive:withCompletionHandler:)` to open `ProfileEditorView` when the user taps the notification
- **Pending delta state in `AppState`:**
  - Add `hasPendingDeltas: Bool` property driven by `health.pendingDeltasCount > 0`
  - `StatusPoller` already polls `/health`; extend the health response `HealthResponse` model to decode `pendingDeltasCount: Int`; update `AppState.hasPendingDeltas` after each poll
- **Menubar badge indicator (`App/StatusView.swift`):**
  - When `appState.hasPendingDeltas == true`, overlay a small filled circle badge on the menubar icon
  - Use SF Symbols symbol compositing (available in SF Symbols 5+, macOS 14+): compose the existing status symbol with `.badge` or render a `ZStack` with a small `Circle()` overlay in the top-right corner (8×8pt, `Color.orange`)
  - Do not use `NSApp.dockTile.badgeLabel` — this app has no dock icon
  - The badge disappears when `hasPendingDeltas == false`
- **Delta review UI (`App/ProfileEditorView.swift`):**
  - Add a `"Pending Updates"` section at the top of the Profile Editor, visible only when `appState.hasPendingDeltas == true`
  - Each delta row shows: the `description` field (e.g., "Add preference: concise responses"), an "Accept" button, and a "Reject" button
  - On Accept/Reject: call `APIClient.acceptDelta(id:)` / `APIClient.rejectDelta(id:)`, remove the row optimistically, revert on error
  - When all deltas are reviewed, the section disappears
- **`TBYDKit/APIClient.swift` extensions:**
  - `struct PendingDelta: Codable, Identifiable { let id: String; let description: String; let source: String; let createdAt: Date }`
  - `getPendingDeltas() async throws -> [PendingDelta]`
  - `acceptDelta(id: String) async throws`
  - `rejectDelta(id: String) async throws`
  - Extend `HealthResponse` to decode `pendingDeltasCount: Int` (default 0 if field absent — backward compatible)

**Unit tests** (`internal/synthesis/nightly_test.go`) — mock store, mock LLM:
- `TestRun_NoInteractions` — empty store; verify `Run()` completes without error, no delta written
- `TestRun_ProducesDeltas` — store has 10 feedback-labeled interactions; mock LLM returns valid delta JSON; verify delta written to pending table with `accepted IS NULL`
- `TestRun_LLMMalformedResponse` — mock LLM returns invalid JSON; verify no delta written, error logged
- `TestRun_ContextCancellation` — cancel context; verify `Run()` exits promptly
- `TestRun_ContextBudgetSampling` — store has 200 interactions; verify at most 100 passed to LLM prompt (sampling applied)
- `TestRun_SkipsDuplicateRejectedDelta` — store has a rejected delta with hash H; Run produces delta with same hash H; verify no new delta written
- `TestSchedule_FiresAtScheduledTime` — mock clock at 01:58; configure schedule for 02:00; verify `Run()` called within next 2 minutes of simulated clock
- `TestSchedule_StopsOnContextCancel` — cancel context; verify no further `Run()` calls after cancellation

**Unit tests** (`internal/api/deltas_test.go`):
- `TestGetPendingDeltas_Empty` — no pending deltas; verify empty array returned
- `TestGetPendingDeltas_ReturnsList` — 2 pending deltas; verify both returned with full JSON
- `TestAcceptDelta` — POST accept; verify `accepted=1` and `reviewed_at` set in store; verify profile updated via `ApplyDelta`; verify cache invalidated
- `TestRejectDelta` — POST reject; verify `accepted=0`, `reviewed_at` set; verify profile NOT updated
- `TestAcceptDelta_AlreadyReviewed` — accept an already-reviewed delta; verify 409 conflict
- `TestRejectedDeltaNotReapplied` — reject delta; run synthesis again with same data; verify same delta (same hash) not recreated

**Swift tests** (`macos/Tests/ProfileEditorViewModelTests.swift` — extend):
- `TestPendingDeltas_Loaded` — mock `getPendingDeltas()` returns 2 deltas; verify view model exposes both
- `TestAcceptDelta_RemovesFromList` — accept delta; verify it is removed from the pending list optimistically
- `TestAcceptDelta_RevertsOnError` — mock `acceptDelta` throws; verify delta reappears in list

**Integration test** (`internal/synthesis/nightly_integration_test.go`) — tag `integration`, requires Ollama:
- `TestSynthesisEndToEnd` — insert 5 feedback-labeled interactions with consistent negative feedback on verbosity; run `NightlySynthesizer.Run()`; verify pending delta contains "concise" preference addition

**Acceptance criteria:**
- Synthesis runs without crashing on a user with 0 interactions
- Synthesis correctly identifies a pattern from 10+ similar interactions (e.g., user always asks follow-up about performance)
- User can accept or reject individual deltas from both CLI and SwiftUI
- Rejected deltas are never re-applied
- Accepting a delta updates the profile AND invalidates the query cache
- Menubar icon shows a badge indicator when pending deltas exist
- Local notification delivered when synthesis produces new deltas (if permission granted)
- `go test ./internal/synthesis/...` passes

---

## Issue 3.6 — Deep enrichment pass

> **Note: Issue 3.6 has been moved to Phase 4 as Issue 4.0.** Deep enrichment is ingestion infrastructure and does not belong in the personalization phase. It requires its own idle detection, batching, and background worker that are orthogonal to preference learning. Phase 3 is fully functional without it. See `docs/phase-4-extended-ingestion.md` Issue 4.0.

---

## Issue 3.7 — Interaction ID surfacing in API responses

**Context:** The MCP `rate_response` tool (Issue 3.1) requires callers to know the `interaction_id` of the response they just received. The OpenAI-compatible proxy currently streams cloud responses transparently with no way to correlate them to a stored interaction. This issue adds that correlation for both streaming and non-streaming responses.

**Tasks:**
- The `interaction_id` (UUID) is already generated when the interaction record is created at the start of the request in `internal/api/openai.go`. No new ID generation is needed — only surfacing it.
- **Non-streaming responses:** add `X-TBYD-Interaction-ID: <uuid>` response header before writing the response body
- **Streaming (SSE) responses:** after all content `data:` chunks but before `data: [DONE]`, emit a non-content event:
  ```
  event: tbyd-metadata
  data: {"interaction_id": "<uuid>"}

  data: [DONE]
  ```
  Clients that do not handle `event: tbyd-metadata` will ignore it (SSE spec: unknown event types are silently discarded). The `data: [DONE]` terminator follows unchanged.
- Update `internal/api/openai.go` handler for both streaming and non-streaming paths
- Update the MCP `rate_response` tool documentation to explain how to obtain the interaction ID

**Unit tests** (`internal/api/openai_test.go` — extend):
- `TestChatCompletions_NonStreaming_HasInteractionIDHeader` — POST non-streaming request; verify `X-TBYD-Interaction-ID` response header is a valid UUID
- `TestChatCompletions_Streaming_HasMetadataEvent` — POST streaming request; parse SSE events; verify `event: tbyd-metadata` event with valid `interaction_id` appears before `data: [DONE]`
- `TestChatCompletions_InteractionIDMatchesStore` — complete a request; read the ID from the header; query the store by that ID; verify record exists with matching content

**Acceptance criteria:**
- Non-streaming responses include `X-TBYD-Interaction-ID` header
- Streaming responses include the `tbyd-metadata` SSE event before `[DONE]`
- The ID in the response matches the ID stored in the `interactions` table
- Existing SSE clients that do not handle `tbyd-metadata` continue to work unchanged

---

## Issue 3.8 — Retrieval quality feedback loop

**Context:** Thumbs-down feedback identifies not only a bad response but also a bad retrieval. The context chunks that were injected into a negatively-rated interaction probably had low relevance. Decrementing their quality scores creates an immediate, lightweight retrieval improvement without waiting for Phase 4 fine-tuning.

**Tasks:**
- Add migration **`005_retrieval_quality.sql`**:
  ```sql
  ALTER TABLE context_vectors ADD COLUMN quality_score REAL NOT NULL DEFAULT 1.0;
  CREATE INDEX IF NOT EXISTS idx_context_vectors_quality ON context_vectors(quality_score);
  ```
- Update `internal/storage/sqlite.go`: add `UpdateVectorQuality(id string, delta float64) error`
  - Applies: `quality_score = MAX(0.1, MIN(2.0, quality_score + delta))`
  - Clamped range: [0.1, 2.0]
- Extend the feedback endpoint body with an optional `reason` field:
  `{"score": -1, "notes": "...", "reason": "irrelevant_context" | "poor_generation" | "wrong_tone" | "other"}`
  - `reason` is optional; defaults to `"other"` when absent
  - Store `reason` in a new `feedback_reason TEXT` column on the `interactions` table (add via `005_retrieval_quality.sql`)
- Wire into feedback processing in `internal/api/openai.go` (or the feedback handler):
  - Parse `interactions.vector_ids` (JSON array of vector IDs used in the enriched prompt)
  - On **negative feedback** (score = -1):
    - If `reason == "irrelevant_context"`: call `UpdateVectorQuality(id, -0.1)` — explicit retrieval attribution, full penalty
    - If `reason` is absent or `"other"`: call `UpdateVectorQuality(id, -0.02)` — weak signal; bad generation may have nothing to do with context quality
    - If `reason == "poor_generation"` or `"wrong_tone"`: do **not** update quality scores — the context may have been perfectly relevant; penalizing it would degrade future retrieval incorrectly
  - On **positive feedback** (score = +1): call `UpdateVectorQuality(id, +0.05)` for each vector ID regardless of reason — if the response was good and these chunks were used, they were at minimum not harmful
  - Run asynchronously (non-blocking, same job queue pattern as preference extraction)
  - Update MCP `rate_response` and CLI `tbyd interactions rate` to accept the optional `reason` argument
- Update `internal/retrieval/store.go`:
  - In `Search` and `SearchHybrid`: multiply the final blended score by `quality_score` before returning
  - Chunks with `quality_score = 0.1` still appear in results (not filtered) but rank much lower
  - Update `ContextChunk` struct: add `QualityScore float64` field for observability

**Unit tests** (`internal/storage/sqlite_test.go` — extend):
- `TestUpdateVectorQuality_Decrement` — initial score 1.0; apply -0.1; verify score 0.9
- `TestUpdateVectorQuality_ClampMin` — initial score 0.15; apply -0.1; verify score 0.1 (not 0.05)
- `TestUpdateVectorQuality_ClampMax` — initial score 1.95; apply +0.1; verify score 2.0
- `TestUpdateVectorQuality_NotFound` — update non-existent vector ID; verify returns error

**Unit tests** (`internal/retrieval/store_test.go` — extend):
- `TestSearch_QualityScoreApplied` — two chunks with equal cosine similarity but quality_score 0.5 vs 1.0; verify higher-quality chunk ranks first
- `TestSearch_LowQualityChunkStillReturned` — chunk with quality_score 0.1; verify it still appears in results (not filtered out)
- `TestSearchHybrid_QualityScalesBothSignals` — verify quality_score multiplier applies to the final blended score, not just the vector component

**Unit tests** (`internal/api/feedback_test.go` — extend):
- `TestFeedback_NegativeWithIrrelevantReason_FullPenalty` — POST score=-1 with `reason: "irrelevant_context"`; verify `UpdateVectorQuality(-0.1)` called for each vector ID
- `TestFeedback_NegativeNoReason_WeakPenalty` — POST score=-1 with no `reason`; verify `UpdateVectorQuality(-0.02)` called
- `TestFeedback_NegativePoorGeneration_NoPenalty` — POST score=-1 with `reason: "poor_generation"`; verify `UpdateVectorQuality` NOT called
- `TestFeedback_NegativeWrongTone_NoPenalty` — POST score=-1 with `reason: "wrong_tone"`; verify `UpdateVectorQuality` NOT called
- `TestFeedback_PositiveUpdatesVectorQuality` — POST score=+1 (any reason); verify `UpdateVectorQuality(+0.05)` called

**Acceptance criteria:**
- After 5 negative ratings with `reason: "irrelevant_context"` for chunk X, chunk X's quality score ≤ 0.5
- After 5 negative ratings with `reason: "poor_generation"` for chunk X, chunk X's quality score remains 1.0 (unchanged)
- After 5 negative ratings with no reason for chunk X, chunk X's quality score is 0.9 (5 × -0.02)
- Chunk X still appears in search results but ranks below unpenalized chunks of equal semantic similarity
- After positive ratings, quality score recovers toward 1.0
- `go test ./internal/retrieval/...` and `go test ./internal/storage/...` pass

---

## Issue 3.9 — Typed Swift Profile model in TBYDKit

**Context:** The current `ProfileEditorViewModel` uses `[String: AnyCodable]` and `JSONSerialization`. This approach cannot support structured editing for nested objects (Communication), key-value maps (Expertise), and reorderable arrays (Opinions, Preferences). A typed `Codable` model is a prerequisite for Issue 3.2.

**Tasks:**
- Create `TBYDKit/Profile.swift`:
  ```swift
  public struct Profile: Codable, Equatable {
      public struct Communication: Codable, Equatable {
          public var tone: String?           // "direct" | "friendly" | "formal"
          public var detailLevel: String?    // "concise" | "balanced" | "thorough"
          public var format: String?         // "prose" | "markdown" | "structured"
          public init() {}
      }
      public struct Identity: Codable, Equatable {
          public var role: String?
          public var expertise: [String: String]  // skill → level
          public var workingContext: WorkingContext?
          public init() { expertise = [:] }
      }
      public struct WorkingContext: Codable, Equatable {
          public var currentProjects: [String]
          public var teamSize: String?
          public var techStack: [String]
          public init() { currentProjects = []; techStack = [] }
      }
      public struct Interests: Codable, Equatable {
          public var primary: [String]
          public var emerging: [String]
          public init() { primary = []; emerging = [] }
      }
      // Non-optional root properties: safe for UI binding; defaults ensure no nil-checking in views
      public var identity: Identity
      public var communication: Communication
      public var interests: Interests
      public var opinions: [String]
      public var preferences: [String]
      public var language: String?
      public var cloudModelPreference: String?
      public var lastSynthesized: Date?
      public init() {
          identity = Identity()
          communication = Communication()
          interests = Interests()
          opinions = []
          preferences = []
      }
  }

  /// Write-only model for PATCH /profile. All root fields are optional so only changed
  /// sections appear in the serialized JSON. JSONEncoder omits nil optionals via
  /// encodeIfPresent, producing a true partial update with no custom encoding logic.
  public struct ProfilePatch: Encodable {
      public struct CommunicationPatch: Encodable {
          public var tone: String?
          public var detailLevel: String?
          public var format: String?
          public init() {}
      }
      public struct IdentityPatch: Encodable {
          public var role: String?
          public var expertise: [String: String]?
          public var workingContext: Profile.WorkingContext?
          public init() {}
      }
      public struct InterestsPatch: Encodable {
          public var primary: [String]?
          public var emerging: [String]?
          public init() {}
      }
      // All root fields optional: absent fields are omitted from the PATCH body entirely.
      // nil = don't touch this section; non-nil (even empty array) = send this value.
      public var identity: IdentityPatch?
      public var communication: CommunicationPatch?
      public var interests: InterestsPatch?
      public var opinions: [String]?
      public var preferences: [String]?
      public var language: String?
      public var cloudModelPreference: String?
      public init() {}
  }
  ```
  - `Profile` is the **read/display model** — non-optional root properties eliminate nil-checking throughout the UI
  - `ProfilePatch` is the **write model** — all root properties optional; `JSONEncoder` with default settings produces partial JSON via `encodeIfPresent` for optional fields; no custom encoding needed
  - Use `CodingKeys` on both types to map Swift camelCase to Go snake_case JSON fields (`detail_level`, `cloud_model_preference`, etc.)
  - `expertise` decodes from a JSON object (`{"go": "expert", "python": "intermediate"}`)
  - `lastSynthesized` decodes from ISO 8601 string via `JSONDecoder.dateDecodingStrategy = .iso8601`
- Extend `APIClient`:
  - `getProfile() async throws -> Profile`
  - `patchProfile(_ patch: ProfilePatch) async throws` — encodes the `ProfilePatch`; only fields set on the patch are included in the request body
  - `deleteProfileField(path: String) async throws` — DELETE /profile/:field
- `ProfileEditorViewModel` holds a `Profile` for display and builds a `ProfilePatch` on save by diffing current state against the loaded snapshot — only changed sections are populated on the patch:
  ```swift
  func buildPatch(from original: Profile, current: Profile) -> ProfilePatch {
      var patch = ProfilePatch()
      if current.communication != original.communication {
          var cp = ProfilePatch.CommunicationPatch()
          if current.communication.tone != original.communication.tone { cp.tone = current.communication.tone }
          // ... repeat per sub-field
          patch.communication = cp
      }
      if current.opinions != original.opinions { patch.opinions = current.opinions }
      // ... repeat per root section
      return patch
  }
  ```
- Update `ProfileEditorViewModel` to use `Profile` and `ProfilePatch`; remove `[String: AnyCodable]` and `JSONSerialization` from the profile flow

**Unit tests** (`macos/Tests/ProfileTests.swift` — new file):
- `TestProfile_RoundTrip` — encode a fully populated `Profile` to JSON; decode back; verify deep equality
- `TestProfile_DecodesSnakeCase` — JSON with `detail_level`, `cloud_model_preference`; verify Swift properties populated
- `TestProfile_ExpertiseDict` — JSON with `{"expertise": {"go": "expert"}}`; verify `identity.expertise["go"] == "expert"`
- `TestProfile_EmptyOptionals` — JSON missing optional fields; verify decode succeeds with nil values (no crash)
- `TestProfilePatch_OnlyChangedSectionSent` — original profile has tone "direct"; update tone to "formal"; call `buildPatch`; verify encoded JSON contains only `communication` key, no `identity` or `opinions` keys
- `TestProfilePatch_ArrayClearable` — set `patch.opinions = []`; verify encoded JSON contains `"opinions": []` (explicitly clearing vs. nil which would omit the key)
- `TestProfilePatch_NilSectionOmitted` — `ProfilePatch` with only `communication` set; verify encoded JSON has no `identity` key at all

**Acceptance criteria:**
- `Profile` round-trips through JSON without data loss for all fields
- `ProfileEditorViewModel` uses `Profile` and `ProfilePatch` throughout; no `AnyCodable` or `JSONSerialization` in the profile editing flow
- PATCH request body contains only the sections that changed — verified by `TestProfilePatch_OnlyChangedSectionSent`
- An empty array in a patch (`opinions: []`) is distinguishable from an absent field (`opinions: nil`) — the former sends `"opinions": []`, the latter omits the key entirely
- All existing Profile-related tests pass
- Compile-time safety: adding a new field to the Go schema requires a corresponding Swift change (no silent drops via `Any`)

---

## Phase 3 Verification

1. Rate 5 consecutive responses as negative because they were "too long" → check if a "concise" preference appears in profile after aggregation (count rule: 3+ signals)
2. Rate 4 responses positive + 1 negative for "code examples" → verify preference added (net score rule)
3. Open ProfileEditorView → add opinion "I value privacy over convenience" → send a query → verify opinion appears in enriched system prompt
4. Run nightly synthesis manually via `tbyd profile synthesize` → verify pending deltas appear for review via `tbyd profile pending`
5. Accept a delta → verify profile updated → query cache invalidated → send query → verify new preference reflected
6. Reject a delta → verify it does not reappear in next synthesis (same-hash check)
7. Explicitly rate a response via MCP `rate_response` tool from Claude Code (using interaction ID from `tbyd-metadata` SSE event)
8. Profile with 50 items → verify `GetSummary()` stays under 500 tokens; verify highest-priority (first) items are retained on truncation
9. Ingest a document → rate the next interaction that retrieved that document as negative → query the document's vector quality score → verify it decreased
10. Menubar icon shows badge indicator when pending deltas exist; disappears after all deltas reviewed
11. Local notification delivered when synthesis produces new deltas
12. `go test ./...` passes
13. `go test -tags integration ./...` passes
