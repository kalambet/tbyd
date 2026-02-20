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

	indexes := []string{"idx_interactions_feedback", "idx_interactions_created"}
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
