package retrieval

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"
)

// Compile-time check that SQLiteStore implements VectorStore.
var _ VectorStore = (*SQLiteStore)(nil)

// SQLiteStore provides vector storage and brute-force cosine similarity search
// backed by SQLite. This is the default implementation of VectorStore.
//
// When the vector count exceeds ~100K and query latency becomes noticeable,
// consider migrating to a LanceDB-backed implementation with ANN indexes.
// Use ExportAll() to extract all records for migration.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore wraps an existing *sql.DB for vector operations.
// The context_vectors table must already exist (created via migrations).
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// Insert adds records to the context_vectors table.
func (s *SQLiteStore) Insert(table string, records []Record) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning insert transaction: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO context_vectors (id, source_id, source_type, text_chunk, embedding, created_at, tags)
		VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("preparing insert statement: %w", err)
	}
	defer stmt.Close()

	for _, r := range records {
		blob := encodeFloat32s(r.Embedding)
		createdAt := r.CreatedAt
		if createdAt.IsZero() {
			createdAt = time.Now().UTC()
		}
		if _, err := stmt.Exec(r.ID, r.SourceID, r.SourceType, r.TextChunk, blob, createdAt.Format(time.RFC3339), r.Tags); err != nil {
			tx.Rollback()
			return fmt.Errorf("inserting record %s: %w", r.ID, err)
		}
	}

	return tx.Commit()
}

// Search performs brute-force cosine similarity search over all vectors,
// returning the top-K most similar records.
// NOTE: the filter parameter is accepted for VectorStore interface compatibility
// but is not yet implemented in the SQLite backend. A future LanceDB backend
// will support DataFusion SQL predicates for metadata filtering.
func (s *SQLiteStore) Search(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error) {
	rows, err := s.db.Query(`
		SELECT id, source_id, source_type, text_chunk, embedding, created_at, tags
		FROM context_vectors`)
	if err != nil {
		return nil, fmt.Errorf("querying vectors: %w", err)
	}
	defer rows.Close()

	queryNorm := norm(vector)
	if queryNorm == 0 {
		return nil, nil
	}

	var results []ScoredRecord
	for rows.Next() {
		var r Record
		var blob []byte
		var createdAt string
		if err := rows.Scan(&r.ID, &r.SourceID, &r.SourceType, &r.TextChunk, &blob, &createdAt, &r.Tags); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		r.Embedding = decodeFloat32s(blob)
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		r.CreatedAt = t

		score := cosineSimilarity(vector, r.Embedding, queryNorm)
		results = append(results, ScoredRecord{Record: r, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topK {
		results = results[:topK]
	}

	return results, nil
}

// Delete removes a record by ID from the context_vectors table.
func (s *SQLiteStore) Delete(table string, id string) error {
	res, err := s.db.Exec("DELETE FROM context_vectors WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("deleting record %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("record %s not found", id)
	}
	return nil
}

// CreateTable is a no-op since tables are managed by SQLite migrations.
func (s *SQLiteStore) CreateTable(name string) error {
	return nil
}

// ExportAll returns all records from the context_vectors table.
// Used for data migration to another VectorStore backend.
func (s *SQLiteStore) ExportAll(table string) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT id, source_id, source_type, text_chunk, embedding, created_at, tags
		FROM context_vectors ORDER BY created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("querying all vectors: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var blob []byte
		var createdAt string
		if err := rows.Scan(&r.ID, &r.SourceID, &r.SourceType, &r.TextChunk, &blob, &createdAt, &r.Tags); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		r.Embedding = decodeFloat32s(blob)
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		r.CreatedAt = t
		records = append(records, r)
	}
	return records, rows.Err()
}

// Count returns the number of records in the context_vectors table.
func (s *SQLiteStore) Count(table string) (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM context_vectors").Scan(&count)
	return count, err
}

// DB returns the underlying *sql.DB. Used by Retriever for ID-based lookups.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// encodeFloat32s serializes a float32 slice to little-endian bytes.
func encodeFloat32s(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeFloat32s deserializes little-endian bytes back to a float32 slice.
// Panics if the byte slice length is not a multiple of 4 (indicates data corruption).
func decodeFloat32s(b []byte) []float32 {
	if len(b)%4 != 0 {
		panic(fmt.Sprintf("byte slice length %d is not a multiple of 4", len(b)))
	}
	n := len(b) / 4
	v := make([]float32, n)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// norm returns the L2 norm of a vector.
func norm(v []float32) float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	return float32(math.Sqrt(sum))
}

// cosineSimilarity computes cosine similarity between a and b.
// queryNorm is the precomputed norm of a.
func cosineSimilarity(a, b []float32, queryNorm float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot float64
	var bNormSq float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		bNormSq += float64(b[i]) * float64(b[i])
	}
	bNorm := math.Sqrt(bNormSq)
	if bNorm == 0 {
		return 0
	}
	return float32(dot / (float64(queryNorm) * bNorm))
}
