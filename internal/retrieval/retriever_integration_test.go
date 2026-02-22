//go:build integration

package retrieval

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/kalambet/tbyd/internal/engine"
	"github.com/kalambet/tbyd/internal/intent"
	_ "modernc.org/sqlite"
)

func TestRetrieveSemanticMatch(t *testing.T) {
	eng := engine.NewOllamaEngine("http://localhost:11434")
	if !eng.IsRunning(context.Background()) {
		t.Skip("Ollama is not running, skipping integration test")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

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

	store := NewSQLiteStore(db)
	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	// Insert a document.
	docText := "Go is a compiled programming language designed at Google"
	vec, err := embedder.Embed(context.Background(), docText)
	if err != nil {
		t.Fatalf("embedding doc: %v", err)
	}

	err = store.Insert("context_vectors", []Record{{
		ID:         uuid.New().String(),
		SourceID:   "doc1",
		SourceType: "note",
		TextChunk:  docText,
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `["go", "programming"]`,
	}})
	if err != nil {
		t.Fatalf("inserting record: %v", err)
	}

	// Retrieve with a semantically similar query.
	chunks, err := retriever.Retrieve(context.Background(), "compiled programming language", 5)
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	if len(chunks) == 0 {
		t.Fatal("expected at least one result")
	}
	if chunks[0].Score < 0.7 {
		t.Errorf("score = %f, want > 0.7", chunks[0].Score)
	}
	if chunks[0].Text != docText {
		t.Errorf("text = %q, want %q", chunks[0].Text, docText)
	}
}

func TestRetrieveForIntentSemanticMatch(t *testing.T) {
	eng := engine.NewOllamaEngine("http://localhost:11434")
	if !eng.IsRunning(context.Background()) {
		t.Skip("Ollama is not running, skipping integration test")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	defer db.Close()

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

	store := NewSQLiteStore(db)
	embedder := NewEmbedder(eng, "nomic-embed-text")
	retriever := NewRetriever(embedder, store)

	// Insert a document about a database schema decision.
	docText := "We decided to use PostgreSQL for the main database schema because of its JSON support"
	vec, err := embedder.Embed(context.Background(), docText)
	if err != nil {
		t.Fatalf("embedding doc: %v", err)
	}

	err = store.Insert("context_vectors", []Record{{
		ID:         uuid.New().String(),
		SourceID:   "decision1",
		SourceType: "note",
		TextChunk:  docText,
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `["architecture", "decisions"]`,
	}})
	if err != nil {
		t.Fatalf("inserting record: %v", err)
	}

	// Retrieve using intent with entities.
	chunks := retriever.RetrieveForIntent(context.Background(), "database architecture", intent.Intent{
		IntentType: "recall",
		Entities:   []string{"database schema"},
		Topics:     []string{"architecture", "decisions"},
	}, 5)

	if len(chunks) == 0 {
		t.Fatal("expected at least one result")
	}
	if chunks[0].Score < 0.7 {
		t.Errorf("score = %f, want > 0.7", chunks[0].Score)
	}
	if chunks[0].Text != docText {
		t.Errorf("text = %q, want %q", chunks[0].Text, docText)
	}
}
