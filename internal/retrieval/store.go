package retrieval

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
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

// expectedTable is the only table name the SQLite backend supports.
const expectedTable = "context_vectors"

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

// idScore holds only the ID and score during the scan phase of Search.
// Full record details are fetched only for top-K winners.
type idScore struct {
	ID    string
	Score float32
}

// Search performs brute-force cosine similarity search over all vectors,
// returning the top-K most similar records.
// NOTE: the filter parameter is accepted for VectorStore interface compatibility
// but is not yet implemented in the SQLite backend. A future LanceDB backend
// will support DataFusion SQL predicates for metadata filtering.
func (s *SQLiteStore) Search(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error) {
	if table != expectedTable {
		return nil, fmt.Errorf("unsupported table %q, expected %q", table, expectedTable)
	}

	// Phase 1: scan only id + embedding to find top-K candidates.
	rows, err := s.db.Query(`SELECT id, embedding FROM context_vectors`)
	if err != nil {
		return nil, fmt.Errorf("querying vectors: %w", err)
	}
	defer rows.Close()

	queryNorm := norm(vector)
	if queryNorm == 0 {
		return nil, nil
	}

	h := &idScoreHeap{}
	heap.Init(h)

	// Reusable buffer for decoding embeddings to avoid per-row allocations.
	var buf []float32

	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		buf, err = decodeFloat32sInto(buf, blob)
		if err != nil {
			return nil, fmt.Errorf("decoding embedding for %s: %w", id, err)
		}

		score := dotProduct(vector, buf, queryNorm)
		if h.Len() < topK {
			heap.Push(h, idScore{ID: id, Score: score})
		} else if score > (*h)[0].Score {
			(*h)[0] = idScore{ID: id, Score: score}
			heap.Fix(h, 0)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating rows: %w", err)
	}

	if h.Len() == 0 {
		return nil, nil
	}

	// Phase 2: fetch full records only for the top-K IDs.
	topIDs := make([]string, h.Len())
	scores := make(map[string]float32, h.Len())
	for i := len(topIDs) - 1; i >= 0; i-- {
		item := heap.Pop(h).(idScore)
		topIDs[i] = item.ID
		scores[item.ID] = item.Score
	}

	queryArgs := make([]interface{}, len(topIDs))
	for i, id := range topIDs {
		queryArgs[i] = id
	}
	fullQuery := `SELECT id, source_id, source_type, text_chunk, embedding, created_at, tags
		FROM context_vectors WHERE id IN (?` + strings.Repeat(",?", len(topIDs)-1) + `)`

	fullRows, err := s.db.Query(fullQuery, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("fetching top-K records: %w", err)
	}
	defer fullRows.Close()

	var results []ScoredRecord
	for fullRows.Next() {
		var r Record
		var blob []byte
		var createdAt string
		if err := fullRows.Scan(&r.ID, &r.SourceID, &r.SourceType, &r.TextChunk, &blob, &createdAt, &r.Tags); err != nil {
			return nil, fmt.Errorf("scanning full record: %w", err)
		}
		embedding, err := decodeFloat32s(blob)
		if err != nil {
			return nil, fmt.Errorf("decoding embedding for %s: %w", r.ID, err)
		}
		r.Embedding = embedding
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		r.CreatedAt = t
		results = append(results, ScoredRecord{Record: r, Score: scores[r.ID]})
	}
	if err := fullRows.Err(); err != nil {
		return nil, fmt.Errorf("iterating full records: %w", err)
	}

	// Sort results by score descending (IN query doesn't preserve order).
	sortByScore(results)

	return results, nil
}

// sortByScore sorts ScoredRecords by Score descending. Used for small slices (topK).
func sortByScore(results []ScoredRecord) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
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
		embedding, err := decodeFloat32s(blob)
		if err != nil {
			return nil, fmt.Errorf("decoding embedding for %s: %w", r.ID, err)
		}
		r.Embedding = embedding
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

// GetByIDs returns records matching the given IDs from the context_vectors table.
func (s *SQLiteStore) GetByIDs(ctx context.Context, table string, ids []string) ([]Record, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	queryArgs := make([]interface{}, len(ids))
	for i, id := range ids {
		queryArgs[i] = id
	}

	query := `SELECT id, source_id, source_type, text_chunk, embedding, created_at, tags
		FROM context_vectors WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`

	rows, err := s.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying by IDs: %w", err)
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
		embedding, err := decodeFloat32s(blob)
		if err != nil {
			return nil, fmt.Errorf("decoding embedding for %s: %w", r.ID, err)
		}
		r.Embedding = embedding
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at for id %s: %w", r.ID, err)
		}
		r.CreatedAt = t
		records = append(records, r)
	}
	return records, rows.Err()
}

// encodeFloat32s serializes a float32 slice to little-endian bytes.
func encodeFloat32s(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeFloat32s deserializes little-endian bytes into a new float32 slice.
// Returns an error if the byte slice length is not a multiple of 4 (indicates data corruption).
func decodeFloat32s(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("byte slice length %d is not a multiple of 4", len(b))
	}
	n := len(b) / 4
	v := make([]float32, n)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v, nil
}

// decodeFloat32sInto decodes little-endian bytes into the provided buffer,
// reusing it to avoid per-row allocations during search scans.
func decodeFloat32sInto(buf []float32, b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("byte slice length %d is not a multiple of 4", len(b))
	}
	n := len(b) / 4
	if cap(buf) < n {
		buf = make([]float32, n)
	} else {
		buf = buf[:n]
	}
	for i := range buf {
		buf[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return buf, nil
}

// norm returns the L2 norm of a vector.
func norm(v []float32) float32 {
	var sum float64
	for _, f := range v {
		sum += float64(f) * float64(f)
	}
	return float32(math.Sqrt(sum))
}

// dotProduct computes cosine similarity as dot(a,b) / (aNorm * bNorm).
// aNorm is the precomputed L2 norm of vector a.
func dotProduct(a, b []float32, aNorm float32) float32 {
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
	return float32(dot / (float64(aNorm) * bNorm))
}

// idScoreHeap is a min-heap of idScore ordered by Score.
// Used during the scan phase of Search to track top-K candidates by ID only.
type idScoreHeap []idScore

func (h idScoreHeap) Len() int            { return len(h) }
func (h idScoreHeap) Less(i, j int) bool  { return h[i].Score < h[j].Score }
func (h idScoreHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *idScoreHeap) Push(x interface{}) { *h = append(*h, x.(idScore)) }
func (h *idScoreHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

// scoredHeap is a min-heap of ScoredRecord ordered by Score.
type scoredHeap []ScoredRecord

func (h scoredHeap) Len() int            { return len(h) }
func (h scoredHeap) Less(i, j int) bool  { return h[i].Score < h[j].Score }
func (h scoredHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *scoredHeap) Push(x interface{}) { *h = append(*h, x.(ScoredRecord)) }
func (h *scoredHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
