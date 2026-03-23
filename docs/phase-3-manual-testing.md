# Phase 3 — Manual Testing Plan

Prerequisites: `tbyd` built from the current branch, server running (`tbyd start`), macOS app built and running.

---

## 3.1 — Feedback Collection API and UI

### CLI

1. Ingest a test document:
   ```
   tbyd ingest --source "test" --content "Go concurrency patterns are powerful"
   ```
2. Send a chat through the proxy so an interaction is saved.
3. List interactions and note an interaction ID:
   ```
   tbyd interactions list
   ```
4. Rate positively:
   ```
   tbyd interactions rate <id> --positive
   ```
   - Expected: success message, `tbyd interactions list` shows score = 1.
5. Rate negatively with a note:
   ```
   tbyd interactions rate <id> --negative --note "not helpful"
   ```
   - Expected: success message, score updated to -1.

### SwiftUI

6. Open Data Browser in the macOS app.
7. Verify thumbs-up and thumbs-down buttons appear on each interaction row.
8. Tap thumbs-up — verify the button highlights and the score persists after refreshing.
9. Tap thumbs-down — same verification.

### MCP

10. In Claude Code with MCP configured, call `rate_response` tool with a valid interaction ID and `score: "positive"`.
    - Expected: tool returns success.

---

## 3.2 — User Profile Editor

### CLI

1. View current profile:
   ```
   tbyd profile show
   ```
2. Set a field:
   ```
   tbyd profile set identity.role "Backend Engineer"
   ```
   - Expected: success, `tbyd profile show` reflects the change.
3. Edit full profile in editor:
   ```
   tbyd profile edit
   ```
   - Expected: opens `$EDITOR` with JSON, saving applies changes.
4. Delete a scalar field via API:
   ```
   curl -X DELETE -H "Authorization: Bearer <token>" http://127.0.0.1:<port>/profile/identity.role
   ```
   - Expected: 200, field removed from profile.

### SwiftUI

5. Open Profile Editor in the macOS app.
6. Verify all five sections are present: Identity, Communication, Interests, Opinions, Preferences.
7. Edit the role field, click Save — verify the change persists after reopening.
8. Add an expertise entry (key-value), save, verify it appears.
9. Add an interest tag, save, verify it appears.
10. Add an opinion, save, verify it appears.

---

## 3.3 — Preference Extraction from Feedback

1. Rate an interaction negatively (see 3.1 step 5).
2. Check job queue (look at server logs for `feedback_extract` job).
   - Expected: a preference extraction job was enqueued and processed.
3. After the job completes, check if preference signals were persisted:
   ```
   tbyd profile show
   ```
   - Note: a single feedback won't trigger a profile change (needs >=3 signals or net score >=2). This step verifies the job ran without error.

---

## 3.4 — Profile Injection into Enrichment

1. Set expertise in profile:
   ```
   tbyd profile set identity.expertise.go "expert"
   ```
2. Send a Go-related query through the proxy.
3. Check server logs — look for calibration context being injected into the intent extractor prompt.
4. Verify the enriched prompt includes an `[Explicit Preferences]` section (visible in debug logs with `log.level: debug` in config).

---

## 3.5 — Nightly Profile Synthesis

### CLI — Trigger Synthesis

1. Trigger a synthesis run:
   ```
   tbyd profile synthesize
   ```
   - Expected: "Synthesis job queued".
2. Trigger again immediately:
   ```
   tbyd profile synthesize
   ```
   - Expected: "Synthesis job already queued" (dedup working).
3. Wait for the job to process (check server logs for `nightly_synthesis` completion).

### CLI — Pending Deltas

4. List pending deltas:
   ```
   tbyd profile pending-deltas
   ```
   - Expected: shows delta(s) with full ID, source, date, description. If no interactions with feedback exist from the last 7 days, output is "No pending deltas."
5. Accept a delta:
   ```
   tbyd profile pending-deltas accept <full-id>
   ```
   - Expected: "Delta <id> accepted", `tbyd profile show` reflects applied changes.
6. Trigger another synthesis (or create a second delta), then reject:
   ```
   tbyd profile pending-deltas reject <full-id>
   ```
   - Expected: "Delta <id> rejected", profile unchanged.

### SwiftUI — Menubar Badge

7. Ensure at least one pending delta exists (trigger synthesis if needed).
8. Look at the macOS menubar icon.
   - Expected: small red dot badge visible on the tbyd icon.
9. Accept/reject all deltas.
   - Expected: red dot disappears within 60 seconds (next poll cycle).

### SwiftUI — Profile Editor Banner

10. With pending deltas present, open Profile Editor.
    - Expected: orange banner at the top says "Profile suggestions available" with count and a "Review" button.
11. Click "Review" — expected: sheet opens showing `PendingDeltasView`.
12. Verify each delta row shows: description, source badge, created date, and Accept/Reject buttons.
13. Click disclosure "Raw JSON" on a delta — expected: monospaced JSON content appears.
14. Click "Reject" on a delta — expected: confirmation dialog appears ("Are you sure?").
15. Confirm reject — expected: delta removed from list.
16. Click "Accept" on a delta — expected: delta removed from list, profile fields in the editor update to reflect the applied delta.
17. Close the sheet — expected: banner count updates or banner disappears if no deltas remain.

---

## 3.6 — Deep Enrichment Pass

1. Enable deep enrichment:
   ```
   tbyd config set enrichment.deep_enabled true
   ```
2. Ingest several documents on related topics.
3. Wait for the system to be idle (or check logs for the deep enrichment worker firing).
   - Expected: server logs show `deep_enrich` jobs being processed, documents grouped by topic, batched, and enriched.
4. Verify deep metadata was written:
   ```
   tbyd recall "topic from ingested docs"
   ```
   - Expected: results come back; if debug logging is on, `deep_metadata` field is non-empty on enriched docs.

---

## 3.7 — Interaction ID Surfacing

### Non-Streaming

1. Send a non-streaming chat request through the proxy.
2. Check response headers:
   ```
   curl -i -X POST http://127.0.0.1:<port>/v1/chat/completions \
     -H "Authorization: Bearer <token>" \
     -H "Content-Type: application/json" \
     -d '{"model":"...","messages":[{"role":"user","content":"hello"}]}'
   ```
   - Expected: `X-TBYD-Interaction-ID` header present with a UUID value.

### Streaming

3. Send a streaming chat request (add `"stream": true`).
   - Expected: before the `[DONE]` event, a `event: tbyd-metadata` SSE event appears containing `{"interaction_id":"..."}`.

### Cross-Reference

4. Use the interaction ID from step 2 or 3:
   ```
   tbyd interactions rate <interaction-id> --positive
   ```
   - Expected: success (ID matches a stored interaction).

---

## 3.8 — Retrieval Quality Feedback Loop

1. Ingest a document, send a query that retrieves it, note the interaction ID.
2. Rate the interaction negatively:
   ```
   tbyd interactions rate <id> --negative
   ```
3. Check the quality score of the retrieved vectors (requires DB inspection or debug logs):
   - Expected: quality_score decreased by 0.1 (clamped to [0.1, 2.0]).
4. Rate a different interaction positively:
   - Expected: quality_score of its vectors increased by 0.05.
5. Send the same query again:
   - Expected: negatively-rated chunks rank lower in retrieval results than before.

---

## 3.9 — Typed Swift Profile Model

This is a code-level concern verified by compilation. No manual testing needed beyond confirming the Profile Editor (3.2 SwiftUI steps) works correctly with typed models.
