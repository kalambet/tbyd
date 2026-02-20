package storage

import (
	"fmt"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open(:memory:) failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// TestMigrationsIdempotent runs Open twice on the same database and verifies
// the schema_version count stays correct (migration not re-applied).
func TestMigrationsIdempotent(t *testing.T) {
	dir := t.TempDir()

	s1, err := Open(dir)
	if err != nil {
		t.Fatalf("first Open failed: %v", err)
	}

	v1, err := s1.AppliedMigrations()
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}
	s1.Close()

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("second Open failed: %v", err)
	}
	defer s2.Close()

	v2, err := s2.AppliedMigrations()
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}

	if len(v1) != len(v2) {
		t.Errorf("migration count changed: %d -> %d", len(v1), len(v2))
	}
}

// TestMigrationsOrdered verifies migrations are applied in ascending numeric order.
func TestMigrationsOrdered(t *testing.T) {
	s := openTestStore(t)

	versions, err := s.AppliedMigrations()
	if err != nil {
		t.Fatalf("AppliedMigrations: %v", err)
	}

	if len(versions) == 0 {
		t.Fatal("expected at least one applied migration")
	}

	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Errorf("migrations not in ascending order: %v", versions)
			break
		}
	}
}

// TestIndexesExist verifies that indexes on interactions table are created by the migration.
func TestIndexesExist(t *testing.T) {
	s := openTestStore(t)

	indexes := []string{"idx_interactions_feedback", "idx_interactions_created", "idx_jobs_status_run_after", "idx_context_vectors_source_id", "idx_context_vectors_source_type"}
	for _, idx := range indexes {
		var count int
		err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&count)
		if err != nil {
			t.Fatalf("querying sqlite_master for %q: %v", idx, err)
		}
		if count != 1 {
			t.Errorf("index %q not found in sqlite_master", idx)
		}
	}
}

// TestContextVectorsTableExists verifies that the context_vectors table is created by migration and supports round-trip.
func TestContextVectorsTableExists(t *testing.T) {
	s := openTestStore(t)

	_, err := s.db.Exec(`INSERT INTO context_vectors (id, source_id, source_type, text_chunk, embedding, created_at, tags)
		VALUES ('v1', 'src1', 'doc', 'hello world', X'00000000', '2025-01-01T00:00:00Z', '[]')`)
	if err != nil {
		t.Fatalf("INSERT into context_vectors: %v", err)
	}

	var id, sourceID, sourceType, textChunk, tags string
	err = s.db.QueryRow(`SELECT id, source_id, source_type, text_chunk, tags FROM context_vectors WHERE id = 'v1'`).
		Scan(&id, &sourceID, &sourceType, &textChunk, &tags)
	if err != nil {
		t.Fatalf("SELECT from context_vectors: %v", err)
	}
	if id != "v1" || sourceID != "src1" || sourceType != "doc" || textChunk != "hello world" {
		t.Errorf("round-trip mismatch: got id=%q source_id=%q source_type=%q text_chunk=%q", id, sourceID, sourceType, textChunk)
	}
}

// TestSaveAndGetInteraction saves an interaction and retrieves it by ID.
func TestSaveAndGetInteraction(t *testing.T) {
	s := openTestStore(t)

	now := time.Now().UTC().Truncate(time.Second)
	want := Interaction{
		ID:             "int-001",
		CreatedAt:      now,
		UserQuery:      "What is Go?",
		EnrichedPrompt: "enriched: What is Go?",
		CloudModel:     "anthropic/claude-opus-4",
		CloudResponse:  "Go is a programming language.",
		FeedbackScore:  0,
		FeedbackNotes:  "",
		VectorIDs:      "[]",
	}

	if err := s.SaveInteraction(want); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}

	got, err := s.GetInteraction("int-001")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}

	if got.ID != want.ID {
		t.Errorf("ID = %q, want %q", got.ID, want.ID)
	}
	if got.UserQuery != want.UserQuery {
		t.Errorf("UserQuery = %q, want %q", got.UserQuery, want.UserQuery)
	}
	if got.EnrichedPrompt != want.EnrichedPrompt {
		t.Errorf("EnrichedPrompt = %q, want %q", got.EnrichedPrompt, want.EnrichedPrompt)
	}
	if got.CloudModel != want.CloudModel {
		t.Errorf("CloudModel = %q, want %q", got.CloudModel, want.CloudModel)
	}
	if got.CloudResponse != want.CloudResponse {
		t.Errorf("CloudResponse = %q, want %q", got.CloudResponse, want.CloudResponse)
	}
	if got.FeedbackScore != want.FeedbackScore {
		t.Errorf("FeedbackScore = %d, want %d", got.FeedbackScore, want.FeedbackScore)
	}
	if got.VectorIDs != want.VectorIDs {
		t.Errorf("VectorIDs = %q, want %q", got.VectorIDs, want.VectorIDs)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, want.CreatedAt)
	}
}

// TestGetInteractionNotFound verifies that retrieving a non-existent ID returns ErrNotFound.
func TestGetInteractionNotFound(t *testing.T) {
	s := openTestStore(t)

	_, err := s.GetInteraction("does-not-exist")
	if err != ErrNotFound {
		t.Errorf("error = %v, want ErrNotFound", err)
	}
}

// TestUpdateFeedback saves an interaction, updates feedback, and verifies the change.
func TestUpdateFeedback(t *testing.T) {
	s := openTestStore(t)

	i := Interaction{
		ID:        "int-fb",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UserQuery: "test query",
		VectorIDs: "[]",
	}
	if err := s.SaveInteraction(i); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}

	if err := s.UpdateFeedback("int-fb", 5, "great answer"); err != nil {
		t.Fatalf("UpdateFeedback: %v", err)
	}

	got, err := s.GetInteraction("int-fb")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}
	if got.FeedbackScore != 5 {
		t.Errorf("FeedbackScore = %d, want 5", got.FeedbackScore)
	}
	if got.FeedbackNotes != "great answer" {
		t.Errorf("FeedbackNotes = %q, want %q", got.FeedbackNotes, "great answer")
	}
}

// TestGetRecentInteractions saves 10 interactions and verifies limit and descending order.
func TestGetRecentInteractions(t *testing.T) {
	s := openTestStore(t)

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for j := 0; j < 10; j++ {
		i := Interaction{
			ID:        fmt.Sprintf("int-%02d", j),
			CreatedAt: base.Add(time.Duration(j) * time.Hour),
			UserQuery: fmt.Sprintf("query %d", j),
			VectorIDs: "[]",
		}
		if err := s.SaveInteraction(i); err != nil {
			t.Fatalf("SaveInteraction %d: %v", j, err)
		}
	}

	got, err := s.GetRecentInteractions(5)
	if err != nil {
		t.Fatalf("GetRecentInteractions: %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("got %d interactions, want 5", len(got))
	}

	// Verify descending order by created_at.
	for k := 1; k < len(got); k++ {
		if got[k].CreatedAt.After(got[k-1].CreatedAt) {
			t.Errorf("not in descending order: [%d]=%v > [%d]=%v", k, got[k].CreatedAt, k-1, got[k-1].CreatedAt)
		}
	}

	// The most recent should be int-09.
	if got[0].ID != "int-09" {
		t.Errorf("first result ID = %q, want %q", got[0].ID, "int-09")
	}
}

// TestSaveAndGetInteraction_Status saves an interaction with explicit status and verifies it.
func TestSaveAndGetInteraction_Status(t *testing.T) {
	s := openTestStore(t)

	want := Interaction{
		ID:        "int-status-1",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UserQuery: "test query",
		Status:    "aborted",
		VectorIDs: "[]",
	}

	if err := s.SaveInteraction(want); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}

	got, err := s.GetInteraction("int-status-1")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}

	if got.Status != "aborted" {
		t.Errorf("Status = %q, want %q", got.Status, "aborted")
	}
}

// TestSaveInteraction_DefaultStatus saves an interaction without explicit status and verifies default.
func TestSaveInteraction_DefaultStatus(t *testing.T) {
	s := openTestStore(t)

	want := Interaction{
		ID:        "int-status-default",
		CreatedAt: time.Now().UTC().Truncate(time.Second),
		UserQuery: "test query",
		VectorIDs: "[]",
	}

	if err := s.SaveInteraction(want); err != nil {
		t.Fatalf("SaveInteraction: %v", err)
	}

	got, err := s.GetInteraction("int-status-default")
	if err != nil {
		t.Fatalf("GetInteraction: %v", err)
	}

	if got.Status != "completed" {
		t.Errorf("Status = %q, want %q", got.Status, "completed")
	}
}

// TestProfileKeyRoundTrip sets a key and gets it back.
func TestProfileKeyRoundTrip(t *testing.T) {
	s := openTestStore(t)

	if err := s.SetProfileKey("language", "Go"); err != nil {
		t.Fatalf("SetProfileKey: %v", err)
	}

	val, err := s.GetProfileKey("language")
	if err != nil {
		t.Fatalf("GetProfileKey: %v", err)
	}
	if val != "Go" {
		t.Errorf("value = %q, want %q", val, "Go")
	}

	// Overwrite and verify upsert works.
	if err := s.SetProfileKey("language", "Rust"); err != nil {
		t.Fatalf("SetProfileKey (overwrite): %v", err)
	}
	val, err = s.GetProfileKey("language")
	if err != nil {
		t.Fatalf("GetProfileKey (overwrite): %v", err)
	}
	if val != "Rust" {
		t.Errorf("value = %q, want %q", val, "Rust")
	}
}

// TestGetAllProfileKeys sets 5 keys and verifies GetAllProfileKeys returns all 5.
func TestGetAllProfileKeys(t *testing.T) {
	s := openTestStore(t)

	keys := map[string]string{
		"name":     "Alice",
		"lang":     "Go",
		"editor":   "Neovim",
		"os":       "macOS",
		"terminal": "Ghostty",
	}
	for k, v := range keys {
		if err := s.SetProfileKey(k, v); err != nil {
			t.Fatalf("SetProfileKey(%q): %v", k, err)
		}
	}

	got, err := s.GetAllProfileKeys()
	if err != nil {
		t.Fatalf("GetAllProfileKeys: %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("got %d keys, want 5", len(got))
	}
	for k, want := range keys {
		if got[k] != want {
			t.Errorf("key %q = %q, want %q", k, got[k], want)
		}
	}
}

// TestSaveAndListContextDocs saves 3 docs and verifies ListContextDocs(2) returns 2.
func TestSaveAndListContextDocs(t *testing.T) {
	s := openTestStore(t)

	base := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	for j := 0; j < 3; j++ {
		doc := ContextDoc{
			ID:        fmt.Sprintf("doc-%02d", j),
			Title:     fmt.Sprintf("Doc %d", j),
			Content:   fmt.Sprintf("Content of doc %d", j),
			Source:    "test",
			Tags:      "[]",
			CreatedAt: base.Add(time.Duration(j) * time.Hour),
		}
		if err := s.SaveContextDoc(doc); err != nil {
			t.Fatalf("SaveContextDoc %d: %v", j, err)
		}
	}

	got, err := s.ListContextDocs(2)
	if err != nil {
		t.Fatalf("ListContextDocs: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d docs, want 2", len(got))
	}

	// Verify descending order — most recent first.
	if got[0].ID != "doc-02" {
		t.Errorf("first doc ID = %q, want %q", got[0].ID, "doc-02")
	}
}

// TestJobsTableExists verifies the jobs table is created by migration and supports round-trip.
func TestJobsTableExists(t *testing.T) {
	s := openTestStore(t)

	_, err := s.db.Exec(`INSERT INTO jobs (id, type, payload_json) VALUES ('j1', 'enrichment', '{"doc_id":"d1"}')`)
	if err != nil {
		t.Fatalf("INSERT into jobs: %v", err)
	}

	var id, typ, payload, status string
	var attempts, maxAttempts int
	err = s.db.QueryRow(`SELECT id, type, payload_json, status, attempts, max_attempts FROM jobs WHERE id = 'j1'`).
		Scan(&id, &typ, &payload, &status, &attempts, &maxAttempts)
	if err != nil {
		t.Fatalf("SELECT from jobs: %v", err)
	}

	if id != "j1" {
		t.Errorf("id = %q, want %q", id, "j1")
	}
	if typ != "enrichment" {
		t.Errorf("type = %q, want %q", typ, "enrichment")
	}
	if payload != `{"doc_id":"d1"}` {
		t.Errorf("payload_json = %q, want %q", payload, `{"doc_id":"d1"}`)
	}
	if status != "pending" {
		t.Errorf("status = %q, want %q", status, "pending")
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0", attempts)
	}
	if maxAttempts != 3 {
		t.Errorf("max_attempts = %d, want 3", maxAttempts)
	}
}

func TestEnqueueAndClaimJob(t *testing.T) {
	s := openTestStore(t)

	job := Job{
		ID:          "j-claim-1",
		Type:        "enrichment",
		PayloadJSON: `{"doc":"d1"}`,
	}
	if err := s.EnqueueJob(job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	got, err := s.ClaimNextJob([]string{"enrichment"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if got == nil {
		t.Fatal("ClaimNextJob returned nil")
	}
	if got.ID != "j-claim-1" {
		t.Errorf("ID = %q, want %q", got.ID, "j-claim-1")
	}
	if got.Type != "enrichment" {
		t.Errorf("Type = %q, want %q", got.Type, "enrichment")
	}
	if got.PayloadJSON != `{"doc":"d1"}` {
		t.Errorf("PayloadJSON = %q, want %q", got.PayloadJSON, `{"doc":"d1"}`)
	}
	if got.Status != "running" {
		t.Errorf("Status = %q, want %q", got.Status, "running")
	}
	if got.MaxAttempts != 3 {
		t.Errorf("MaxAttempts = %d, want 3", got.MaxAttempts)
	}
}

func TestClaimNextJob_Empty(t *testing.T) {
	s := openTestStore(t)

	got, err := s.ClaimNextJob([]string{"enrichment"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestClaimNextJob_RespectRunAfter(t *testing.T) {
	s := openTestStore(t)

	job := Job{
		ID:          "j-future",
		Type:        "enrichment",
		PayloadJSON: `{}`,
		RunAfter:    time.Now().UTC().Add(1 * time.Hour),
	}
	if err := s.EnqueueJob(job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	got, err := s.ClaimNextJob([]string{"enrichment"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for future run_after, got %+v", got)
	}
}

func TestClaimNextJob_TypeFilter(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-a", Type: "a", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob a: %v", err)
	}
	if err := s.EnqueueJob(Job{ID: "j-b", Type: "b", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob b: %v", err)
	}

	got, err := s.ClaimNextJob([]string{"a"})
	if err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if got == nil {
		t.Fatal("ClaimNextJob returned nil")
	}
	if got.Type != "a" {
		t.Errorf("Type = %q, want %q", got.Type, "a")
	}
}

func TestClaimNextJob_SkipsRunning(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-first", Type: "x", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob first: %v", err)
	}
	if _, err := s.ClaimNextJob([]string{"x"}); err != nil {
		t.Fatalf("ClaimNextJob first: %v", err)
	}

	if err := s.EnqueueJob(Job{ID: "j-second", Type: "x", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob second: %v", err)
	}

	got, err := s.ClaimNextJob([]string{"x"})
	if err != nil {
		t.Fatalf("ClaimNextJob second: %v", err)
	}
	if got == nil {
		t.Fatal("ClaimNextJob returned nil")
	}
	if got.ID != "j-second" {
		t.Errorf("ID = %q, want %q", got.ID, "j-second")
	}
}

func TestCompleteJob(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-complete", Type: "x", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := s.ClaimNextJob([]string{"x"}); err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if err := s.CompleteJob("j-complete"); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	var status string
	if err := s.db.QueryRow(`SELECT status FROM jobs WHERE id = 'j-complete'`).Scan(&status); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want %q", status, "completed")
	}
}

func TestFailJob_IncrementsAttempts(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-fail-inc", Type: "x", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := s.ClaimNextJob([]string{"x"}); err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if err := s.FailJob("j-fail-inc", "something broke"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	var status, lastError string
	var attempts int
	if err := s.db.QueryRow(`SELECT status, attempts, last_error FROM jobs WHERE id = 'j-fail-inc'`).Scan(&status, &attempts, &lastError); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1", attempts)
	}
	if status != "pending" {
		t.Errorf("status = %q, want %q", status, "pending")
	}
	if lastError != "something broke" {
		t.Errorf("last_error = %q, want %q", lastError, "something broke")
	}
}

func TestFailJob_MaxAttemptsReached(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-fail-max", Type: "x", PayloadJSON: `{}`, MaxAttempts: 1}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := s.ClaimNextJob([]string{"x"}); err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}
	if err := s.FailJob("j-fail-max", "fatal"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	var status string
	if err := s.db.QueryRow(`SELECT status FROM jobs WHERE id = 'j-fail-max'`).Scan(&status); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if status != "failed" {
		t.Errorf("status = %q, want %q", status, "failed")
	}
}

func TestFailJob_SetsBackoff(t *testing.T) {
	s := openTestStore(t)

	if err := s.EnqueueJob(Job{ID: "j-backoff", Type: "x", PayloadJSON: `{}`}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := s.ClaimNextJob([]string{"x"}); err != nil {
		t.Fatalf("ClaimNextJob: %v", err)
	}

	before := time.Now().UTC()
	if err := s.FailJob("j-backoff", "retry"); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	var runAfterStr string
	if err := s.db.QueryRow(`SELECT run_after FROM jobs WHERE id = 'j-backoff'`).Scan(&runAfterStr); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	runAfter, err := time.Parse(time.RFC3339, runAfterStr)
	if err != nil {
		t.Fatalf("parsing run_after: %v", err)
	}
	if !runAfter.After(before) {
		t.Errorf("run_after %v should be after %v", runAfter, before)
	}
}
