# Feature Team

You are the **orchestrator** for the feature team. Your job is to coordinate a team of specialized agents to implement and review the following task:

**Task:** $ARGUMENTS

---

## Team Roster

| Handle | Agent Definition | Role |
|--------|-----------------|------|
| `ai-apple-engineer` | `ai-apple-engineer` | Architect & router — reads context, designs solution, assigns work |
| `apple-builder` | `apple-engineer` | Implements all iOS/macOS/Apple platform code |
| `apple-reviewer` | `apple-reviewer` | Reviews `apple-builder`'s changes (read-only) |
| `ai-builder` | `ai-apple-engineer` | Implements all AI/ML/LLM/MLX code |
| `ai-reviewer` | `ml-reviewer` | Reviews `ai-builder`'s changes (read-only) |
| `reviewer` | `full-reviewer` | Final end-to-end review after both builder tracks finish |

---

## Execution Pipeline

### Phase 1 — Architecture & Routing (ai-apple-engineer)

Spawn **ai-apple-engineer** with this prompt:

```
You are the architect for this task: $ARGUMENTS

Do the following IN ORDER:
1. Read ARCHITECTURE.md (check repo root and docs/ directory).
2. Read any relevant files in docs/.
3. Analyse the task and produce a short implementation plan:
   - Describe the solution approach.
   - List which files will change.
   - Classify each change as: APPLE (iOS/macOS/SwiftUI/Swift), AI (LLM/ML/MLX/inference), or BOTH.
4. Output two sections:
   - ## Apple Track — tasks for apple-builder (leave empty if none)
   - ## AI Track — tasks for ai-builder (leave empty if none)

Return ONLY the plan. Do not modify any files.
```

Capture the plan output. Parse the **Apple Track** and **AI Track** sections.

---

### Phase 2 — Parallel Implementation with Inline Review

Run the Apple Track and AI Track **in parallel** if both are non-empty (skip an empty track).

#### Apple Track loop (apple-builder ↔ apple-reviewer)

Repeat until `apple-reviewer` outputs the token `APPROVED`:

1. Spawn **apple-builder** with:
   ```
   Implement the following Apple platform changes:

   <apple-track-tasks from Phase 1 plan>

   Instructions:
   - Follow existing code patterns and conventions.
   - Run `swift build` and `swift test` after changes.
   - Output a brief summary of every file you changed.
   ```

2. Spawn **apple-reviewer** (READ-ONLY — do not modify any files) with:
   ```
   Review the changes made by apple-builder.

   Changed files: <list from apple-builder summary>

   Perform a hyper-critical review covering:
   - HIG compliance and accessibility
   - SwiftUI patterns and state management
   - Concurrency safety (Swift 6 / actors)
   - Performance on real devices
   - App Store readiness

   Output either:
   - A numbered list of required changes (if any issues found), OR
   - The single token: APPROVED (if changes are correct and complete)

   Do NOT modify any files.
   ```

3. If `apple-reviewer` output contains `APPROVED` → exit loop.
   Otherwise pass the numbered feedback list back to `apple-builder` in the next iteration.

#### AI Track loop (ai-builder ↔ ai-reviewer)

Repeat until `ai-reviewer` outputs the token `APPROVED`:

1. Spawn **ai-builder** with:
   ```
   Implement the following AI/ML/LLM changes:

   <ai-track-tasks from Phase 1 plan>

   Instructions:
   - Follow existing code patterns and conventions.
   - Output a brief summary of every file you changed.
   ```

2. Spawn **ai-reviewer** (READ-ONLY — do not modify any files) with:
   ```
   Review the changes made by ai-builder.

   Changed files: <list from ai-builder summary>

   Perform a hyper-critical review covering:
   - Prompt injection risks
   - Token cost and context window management
   - Error handling and retry logic
   - Model selection and inference correctness
   - RAG/embedding pipeline correctness (if applicable)
   - MLX / CoreML / on-device inference patterns (if applicable)

   Output either:
   - A numbered list of required changes (if any issues found), OR
   - The single token: APPROVED (if changes are correct and complete)

   Do NOT modify any files.
   ```

3. If `ai-reviewer` output contains `APPROVED` → exit loop.
   Otherwise pass the numbered feedback list back to `ai-builder` in the next iteration.

---

### Phase 3 — Final End-to-End Review (reviewer)

Wait for **both** Phase 2 tracks to finish (both `apple-reviewer` and `ai-reviewer` must have output `APPROVED`).

Then spawn **reviewer** (READ-ONLY — do not modify any files) with:

```
You are the final reviewer. Both the Apple and AI implementation tracks have been approved by their specialist reviewers.

Task that was implemented: $ARGUMENTS

Changed files: <combined list from apple-builder and ai-builder summaries>

Perform a comprehensive end-to-end review using full-reviewer expertise:
- Cross-cutting concerns (does the Apple UI correctly drive the AI layer and vice versa?)
- Integration correctness (data flows, error propagation across layers)
- Security (prompt injection at the UI boundary, credential handling)
- Accessibility and HIG compliance
- AI/ML correctness and cost safety
- Anything the specialist reviewers may have missed

Output either:
- A numbered list of required changes grouped by owner (apple-builder / ai-builder), OR
- The single token: APPROVED (if the full implementation is correct and complete)

Do NOT modify any files.
```

If `reviewer` outputs required changes:
- Send Apple-labelled changes back to **apple-builder** (re-run Apple Track loop from step 1).
- Send AI-labelled changes back to **ai-builder** (re-run AI Track loop from step 1).
- Re-run Phase 3 after both fix loops complete.

Repeat until `reviewer` outputs `APPROVED`.

---

## Done

When `reviewer` outputs `APPROVED`, report to the user:

```
✅ Feature team complete.

- Apple Track: <N> review iteration(s)
- AI Track: <N> review iteration(s)
- Final review: <N> iteration(s)

Changed files:
<combined list>
```
