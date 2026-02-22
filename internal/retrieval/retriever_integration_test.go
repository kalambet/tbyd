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

// setupIntegrationRetriever creates an in-memory SQLite store, embedder, and
// retriever backed by a running Ollama instance. It skips the test if Ollama
// is not available.
func setupIntegrationRetriever(t *testing.T) (*Retriever, *Embedder, *SQLiteStore) {
	t.Helper()

	eng := engine.NewOllamaEngine("http://localhost:11434")
	if !eng.IsRunning(context.Background()) {
		t.Skip("Ollama is not running, skipping integration test")
	}

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

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
	return retriever, embedder, store
}

// insertDoc embeds and inserts a document into the store.
func insertDoc(t *testing.T, embedder *Embedder, store *SQLiteStore, sourceID, text, tags string) {
	t.Helper()

	vec, err := embedder.Embed(context.Background(), text)
	if err != nil {
		t.Fatalf("embedding doc: %v", err)
	}

	err = store.Insert(expectedTable, []Record{{
		ID:         uuid.New().String(),
		SourceID:   sourceID,
		SourceType: "note",
		TextChunk:  text,
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       tags,
	}})
	if err != nil {
		t.Fatalf("inserting record: %v", err)
	}
}

func TestRetrieveSemanticMatch(t *testing.T) {
	retriever, embedder, store := setupIntegrationRetriever(t)

	docText := "Go is a compiled programming language designed at Google"
	insertDoc(t, embedder, store, "doc1", docText, `["go", "programming"]`)

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
	retriever, embedder, store := setupIntegrationRetriever(t)

	docText := "We decided to use PostgreSQL for the main database schema because of its JSON support"
	insertDoc(t, embedder, store, "decision1", docText, `["architecture", "decisions"]`)

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
