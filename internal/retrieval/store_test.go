package retrieval

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDB creates an in-memory SQLite database with the context_vectors table.
// MaxOpenConns is set to 1 to prevent in-memory SQLite from creating separate
// databases per connection (each `:memory:` connection is a unique database).
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
		CREATE TABLE context_vectors (
			id TEXT PRIMARY KEY,
			source_id TEXT NOT NULL,
			source_type TEXT NOT NULL,
			text_chunk TEXT NOT NULL,
			embedding BLOB NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			tags TEXT DEFAULT '[]'
		)`)
	if err != nil {
		t.Fatalf("creating table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func makeTestVector(dim int, seed float32) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = seed + float32(i)*0.001
	}
	return v
}

func TestInsertAndSearch(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(768, 0.1)
	err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "Go is a compiled language",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `["go"]`,
	}})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.Search("context_vectors", vec, 1, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Score < 0.99 {
		t.Errorf("score = %f, want > 0.99", results[0].Score)
	}
	if results[0].ID != "r1" {
		t.Errorf("ID = %q, want %q", results[0].ID, "r1")
	}
}

func TestSearch_TopK(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	var records []Record
	for i := 0; i < 10; i++ {
		records = append(records, Record{
			ID:         fmt.Sprintf("r%d", i),
			SourceID:   "src",
			SourceType: "doc",
			TextChunk:  "text",
			Embedding:  makeTestVector(768, float32(i)*0.01),
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		})
	}
	if err := s.Insert("context_vectors", records); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.Search("context_vectors", makeTestVector(768, 0.05), 3, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
}

func TestSearch_EmptyTable(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	results, err := s.Search("context_vectors", makeTestVector(768, 0.1), 5, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestDelete(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(768, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "to be deleted",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := s.Delete("context_vectors", "r1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify a second delete fails as the record is gone.
	if err := s.Delete("context_vectors", "r1"); err == nil {
		t.Error("expected error when deleting non-existent record, got nil")
	}

	results, err := s.Search("context_vectors", vec, 1, "")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results after delete, want 0", len(results))
	}
}

func TestExportAll(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	records := []Record{
		{ID: "r1", SourceID: "src1", SourceType: "doc", TextChunk: "first", Embedding: makeTestVector(768, 0.1), CreatedAt: time.Now().UTC(), Tags: `["a"]`},
		{ID: "r2", SourceID: "src2", SourceType: "note", TextChunk: "second", Embedding: makeTestVector(768, 0.2), CreatedAt: time.Now().UTC(), Tags: `["b"]`},
	}
	if err := s.Insert("context_vectors", records); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	exported, err := s.ExportAll("context_vectors")
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}
	if len(exported) != 2 {
		t.Errorf("got %d records, want 2", len(exported))
	}
	if exported[0].ID != "r1" || exported[1].ID != "r2" {
		t.Errorf("IDs = [%q, %q], want [r1, r2]", exported[0].ID, exported[1].ID)
	}
	if len(exported[0].Embedding) != 768 {
		t.Errorf("embedding dim = %d, want 768", len(exported[0].Embedding))
	}
}

func TestCount(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	count, err := s.Count("context_vectors")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Errorf("empty count = %d, want 0", count)
	}

	if err := s.Insert("context_vectors", []Record{
		{ID: "r1", SourceID: "s", SourceType: "d", TextChunk: "t", Embedding: makeTestVector(768, 0.1), CreatedAt: time.Now().UTC(), Tags: `[]`},
		{ID: "r2", SourceID: "s", SourceType: "d", TextChunk: "t", Embedding: makeTestVector(768, 0.2), CreatedAt: time.Now().UTC(), Tags: `[]`},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	count, err = s.Count("context_vectors")
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Errorf("count = %d, want 2", count)
	}
}

func TestVectorStoreInterface(t *testing.T) {
	db := openTestDB(t)
	// Verify SQLiteStore satisfies VectorStore at usage site too.
	var store VectorStore = NewSQLiteStore(db)
	if err := store.CreateTable("context_vectors"); err != nil {
		t.Errorf("CreateTable via interface: %v", err)
	}
}

func TestInvalidTableName(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	if err := s.CreateTable("wrong_table"); err == nil {
		t.Error("expected error for invalid table name in CreateTable")
	}
	if err := s.Insert("wrong_table", nil); err == nil {
		t.Error("expected error for invalid table name in Insert")
	}
	if _, err := s.Search("wrong_table", makeTestVector(768, 0.1), 5, ""); err == nil {
		t.Error("expected error for invalid table name in Search")
	}
	if err := s.Delete("wrong_table", "id"); err == nil {
		t.Error("expected error for invalid table name in Delete")
	}
}

func TestSearch_TopKZero(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	results, err := s.Search("context_vectors", makeTestVector(768, 0.1), 0, "")
	if err != nil {
		t.Fatalf("Search with topK=0: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results for topK=0, got %d", len(results))
	}
}

func TestTableCreation_Idempotent(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	if err := s.CreateTable("context_vectors"); err != nil {
		t.Errorf("first CreateTable: %v", err)
	}
	if err := s.CreateTable("context_vectors"); err != nil {
		t.Errorf("second CreateTable: %v", err)
	}
}

// openTestDBWithFTS creates an in-memory SQLite database with context_vectors
// and the FTS5 virtual table plus sync triggers.
func openTestDBWithFTS(t *testing.T) *sql.DB {
	t.Helper()
	db := openTestDB(t)
	_, err := db.Exec(`
		CREATE VIRTUAL TABLE context_vectors_fts USING fts5(
			doc_id,
			text_chunk
		);
		CREATE TRIGGER context_vectors_ai AFTER INSERT ON context_vectors BEGIN
			INSERT INTO context_vectors_fts(doc_id, text_chunk) VALUES (new.id, new.text_chunk);
		END;
		CREATE TRIGGER context_vectors_ad AFTER DELETE ON context_vectors BEGIN
			DELETE FROM context_vectors_fts WHERE doc_id = old.id;
		END;
		CREATE TRIGGER context_vectors_au AFTER UPDATE ON context_vectors BEGIN
			DELETE FROM context_vectors_fts WHERE doc_id = old.id;
			INSERT INTO context_vectors_fts(doc_id, text_chunk) VALUES (new.id, new.text_chunk);
		END;
	`)
	if err != nil {
		t.Fatalf("creating FTS5 table and triggers: %v", err)
	}
	return db
}

func TestSearchKeyword_MatchesExact(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	if err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "Kubernetes deployment strategies for production",
		Embedding:  makeTestVector(768, 0.1),
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.SearchKeyword("context_vectors", "Kubernetes", 5, "")
	if err != nil {
		t.Fatalf("SearchKeyword: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].ID != "r1" {
		t.Errorf("ID = %q, want %q", results[0].ID, "r1")
	}
}

func TestSearchKeyword_NoMatch(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	if err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "Go is a compiled language",
		Embedding:  makeTestVector(768, 0.1),
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.SearchKeyword("context_vectors", "Kubernetes", 5, "")
	if err != nil {
		t.Fatalf("SearchKeyword: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSearchKeyword_TopK(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	var records []Record
	for i := 0; i < 10; i++ {
		records = append(records, Record{
			ID:         fmt.Sprintf("r%d", i),
			SourceID:   fmt.Sprintf("src%d", i),
			SourceType: "doc",
			TextChunk:  fmt.Sprintf("document %d about deployment strategies", i),
			Embedding:  makeTestVector(768, float32(i)*0.01),
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		})
	}
	if err := s.Insert("context_vectors", records); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.SearchKeyword("context_vectors", "deployment", 3, "")
	if err != nil {
		t.Fatalf("SearchKeyword: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
}

func TestSearchHybrid_BlendsScores(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	// Doc that matches keyword but has a different vector.
	if err := s.Insert("context_vectors", []Record{
		{
			ID:         "keyword-match",
			SourceID:   "src1",
			SourceType: "doc",
			TextChunk:  "Kubernetes deployment is crucial for production",
			Embedding:  makeTestVector(768, 0.9), // far from query vector
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		},
		{
			ID:         "vector-match",
			SourceID:   "src2",
			SourceType: "doc",
			TextChunk:  "container orchestration systems for cloud",
			Embedding:  makeTestVector(768, 0.1), // close to query vector
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		},
	}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	results, err := s.SearchHybrid("context_vectors", makeTestVector(768, 0.1), "Kubernetes", 5, 0.5, "")
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}
	if len(results) < 1 {
		t.Fatal("expected at least 1 result")
	}

	// Both docs should appear since one matches keyword and the other matches vector.
	ids := make(map[string]bool)
	for _, r := range results {
		ids[r.ID] = true
	}
	if !ids["keyword-match"] {
		t.Error("keyword-match doc not found in hybrid results")
	}
	if !ids["vector-match"] {
		t.Error("vector-match doc not found in hybrid results")
	}
}

func TestSearchHybrid_DeduplicatesResults(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(768, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "Kubernetes deployment for production systems",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// This doc should appear in both keyword and vector results; verify single entry.
	results, err := s.SearchHybrid("context_vectors", vec, "Kubernetes", 5, 0.5, "")
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("got %d results, want 1 (deduplicated)", len(results))
	}
}

func TestSearchHybrid_VectorOnlyFallback(t *testing.T) {
	db := openTestDBWithFTS(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(768, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "r1",
		SourceID:   "src1",
		SourceType: "doc",
		TextChunk:  "Go is a compiled language",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Empty keyword query should fall back to vector-only.
	results, err := s.SearchHybrid("context_vectors", vec, "", 5, 0.7, "")
	if err != nil {
		t.Fatalf("SearchHybrid: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].ID != "r1" {
		t.Errorf("ID = %q, want %q", results[0].ID, "r1")
	}
}
