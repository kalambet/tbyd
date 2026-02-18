package retrieval

import (
	"context"
	"time"
)

// VectorStore is the interface for vector storage and similarity search backends.
// The current implementation uses SQLite with brute-force cosine similarity.
// Future implementations may use LanceDB (via Rust sidecar HTTP API) or other
// ANN-capable vector databases.
//
// Migration notes:
//   - All record data uses the same Record/ScoredRecord types regardless of backend.
//   - The "table" parameter is included for backends that support multiple tables
//     (e.g., LanceDB). The SQLite implementation ignores it (single table via migrations).
//   - The "filter" parameter in Search accepts a SQL-like predicate string. LanceDB
//     supports DataFusion SQL predicates natively; SQLite implementation may parse
//     or ignore it.
//   - Embeddings are []float32; LanceDB expects FixedSizeList<Float32> in Arrow format.
//   - When migrating: implement ExportAll() on the old store and use Insert() on the
//     new store to transfer data.
type VectorStore interface {
	// Insert adds records to the given table.
	Insert(table string, records []Record) error

	// Search performs vector similarity search, returning the top-K most similar records.
	// filter is an optional SQL-like predicate for metadata filtering (may be ignored).
	Search(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error)

	// GetByIDs returns records matching the given IDs from the given table.
	GetByIDs(ctx context.Context, table string, ids []string) ([]Record, error)

	// Delete removes a record by ID from the given table.
	Delete(table string, id string) error

	// CreateTable ensures the named table exists. Idempotent.
	CreateTable(name string) error

	// ExportAll returns all records from the given table.
	// Used for data migration between backends.
	ExportAll(table string) ([]Record, error)

	// Count returns the number of records in the given table.
	Count(table string) (int, error)
}

// Record represents a row in the vector store.
type Record struct {
	ID         string
	SourceID   string
	SourceType string
	TextChunk  string
	Embedding  []float32
	CreatedAt  time.Time
	Tags       string // JSON array stored as text
}

// ScoredRecord is a Record with a similarity score attached.
type ScoredRecord struct {
	Record
	Score float32
}
