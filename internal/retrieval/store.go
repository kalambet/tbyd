package retrieval

import (
	"container/heap"
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
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

func validateTable(table string) error {
	if table != expectedTable {
		return fmt.Errorf("unsupported table %q, expected %q", table, expectedTable)
	}
	return nil
}

// Insert adds records to the context_vectors table.
func (s *SQLiteStore) Insert(table string, records []Record) error {
	if err := validateTable(table); err != nil {
		return err
	}

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
	if err := validateTable(table); err != nil {
		return nil, err
	}
	if topK <= 0 {
		return nil, nil
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

		score := cosineSimilarity(vector, buf, queryNorm)
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

	records, err := s.fetchRecordsByIDs(context.Background(), topIDs)
	if err != nil {
		return nil, err
	}

	results := make([]ScoredRecord, 0, len(records))
	for _, r := range records {
		results = append(results, ScoredRecord{Record: r, Score: scores[r.ID]})
	}

	// Sort results by score descending (IN query doesn't preserve order).
	sortByScore(results)

	return results, nil
}

// sortByScore sorts ScoredRecords by Score descending. Used for small slices (topK).
func sortByScore(results []ScoredRecord) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

// Delete removes a record by ID from the context_vectors table.
func (s *SQLiteStore) Delete(table string, id string) error {
	if err := validateTable(table); err != nil {
		return err
	}
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

// CreateTable validates the table name. Tables are managed by SQLite migrations.
func (s *SQLiteStore) CreateTable(name string) error {
	return validateTable(name)
}

// ExportAll returns all records from the context_vectors table.
// Used for data migration to another VectorStore backend.
func (s *SQLiteStore) ExportAll(table string) ([]Record, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
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
	if err := validateTable(table); err != nil {
		return 0, err
	}
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM context_vectors").Scan(&count)
	return count, err
}

// GetByIDs returns records matching the given IDs from the context_vectors table.
func (s *SQLiteStore) GetByIDs(ctx context.Context, table string, ids []string) ([]Record, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	return s.fetchRecordsByIDs(ctx, ids)
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

// cosineSimilarity computes cosine similarity as dot(a,b) / (aNorm * bNorm).
// aNorm is the precomputed L2 norm of vector a.
func cosineSimilarity(a, b []float32, aNorm float32) float32 {
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

// SearchKeyword performs BM25 keyword search via the FTS5 virtual table.
// Scores are normalized to 0–1 using min-max normalization.
func (s *SQLiteStore) SearchKeyword(table string, query string, topK int, filter string) ([]ScoredRecord, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	if topK <= 0 || query == "" {
		return nil, nil
	}

	// FTS5 rank returns negative BM25 scores (more negative = better match).
	// We retrieve extra candidates to allow for min-max normalization.
	rows, err := s.db.Query(`
		SELECT doc_id, rank
		FROM context_vectors_fts
		WHERE text_chunk MATCH ?
		ORDER BY rank
		LIMIT ?`, query, topK*2)
	if err != nil {
		return nil, fmt.Errorf("FTS5 keyword search: %w", err)
	}
	defer rows.Close()

	type ftsHit struct {
		docID string
		rank  float64 // raw BM25 rank (negative)
	}
	var hits []ftsHit
	for rows.Next() {
		var h ftsHit
		if err := rows.Scan(&h.docID, &h.rank); err != nil {
			return nil, fmt.Errorf("scanning FTS5 result: %w", err)
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating FTS5 results: %w", err)
	}
	if len(hits) == 0 {
		return nil, nil
	}

	// Min-max normalize the raw BM25 ranks to 0–1.
	// BM25 ranks are negative; more negative = better match.
	minRank := hits[0].rank
	maxRank := hits[0].rank
	for _, h := range hits[1:] {
		if h.rank < minRank {
			minRank = h.rank
		}
		if h.rank > maxRank {
			maxRank = h.rank
		}
	}

	normalizedScores := make(map[string]float32, len(hits))
	rankRange := maxRank - minRank
	for _, h := range hits {
		var score float32
		if rankRange == 0 {
			score = 1.0 // all same rank → all get max score
		} else {
			// Invert: most negative (best) → 1.0, least negative → 0.0
			score = float32((maxRank - h.rank) / rankRange)
		}
		normalizedScores[h.docID] = score
	}

	// Trim to topK doc IDs.
	if len(hits) > topK {
		hits = hits[:topK]
	}

	// Fetch full records.
	docIDs := make([]string, len(hits))
	for i, h := range hits {
		docIDs[i] = h.docID
	}

	records, err := s.fetchRecordsByIDs(context.Background(), docIDs)
	if err != nil {
		return nil, err
	}

	results := make([]ScoredRecord, 0, len(records))
	for _, r := range records {
		results = append(results, ScoredRecord{Record: r, Score: normalizedScores[r.ID]})
	}

	sortByScore(results)
	return results, nil
}

// SearchHybrid combines vector similarity and BM25 keyword search using
// weighted Reciprocal Rank Fusion (RRF). vectorWeight controls the blend:
// vector RRF scores are scaled by vectorWeight, keyword scores by (1-vectorWeight).
// Results are deduplicated by record ID.
func (s *SQLiteStore) SearchHybrid(table string, vector []float32, query string, topK int, vectorWeight float32, filter string) ([]ScoredRecord, error) {
	if err := validateTable(table); err != nil {
		return nil, err
	}
	if topK <= 0 {
		return nil, nil
	}

	// If no keyword query, fall back to vector-only search.
	if query == "" {
		return s.Search(table, vector, topK, filter)
	}

	// Clamp vectorWeight to [0, 1].
	if vectorWeight < 0 {
		vectorWeight = 0
	}
	if vectorWeight > 1 {
		vectorWeight = 1
	}

	// Retrieve more candidates than needed for fusion.
	candidateK := topK * 4

	// Run vector and keyword searches in parallel.
	type searchResult struct {
		records []ScoredRecord
		err     error
	}
	vectorCh := make(chan searchResult, 1)
	keywordCh := make(chan searchResult, 1)

	go func() {
		r, err := s.Search(table, vector, candidateK, filter)
		vectorCh <- searchResult{r, err}
	}()
	go func() {
		r, err := s.SearchKeyword(table, query, candidateK, filter)
		keywordCh <- searchResult{r, err}
	}()

	vecResult := <-vectorCh
	kwResult := <-keywordCh

	// If both fail, return error. If one fails, use the other.
	if vecResult.err != nil && kwResult.err != nil {
		return nil, fmt.Errorf("both searches failed: vector: %w, keyword: %v", vecResult.err, kwResult.err)
	}

	vectorResults := vecResult.records
	keywordResults := kwResult.records

	// Weighted Reciprocal Rank Fusion with k=60.
	// Vector contributions are scaled by vectorWeight, keyword by (1-vectorWeight).
	const rrfK = 60
	keywordWeight := float64(1 - vectorWeight)
	vecW := float64(vectorWeight)

	type fusedEntry struct {
		record ScoredRecord
		score  float64
	}
	fused := make(map[string]*fusedEntry)

	for rank, sr := range vectorResults {
		id := sr.ID
		rrfScore := vecW / float64(rrfK+rank+1) // rank is 0-based, +1 to make 1-based
		if entry, ok := fused[id]; ok {
			entry.score += rrfScore
		} else {
			fused[id] = &fusedEntry{record: sr, score: rrfScore}
		}
	}

	for rank, sr := range keywordResults {
		id := sr.ID
		rrfScore := keywordWeight / float64(rrfK+rank+1)
		if entry, ok := fused[id]; ok {
			entry.score += rrfScore
		} else {
			fused[id] = &fusedEntry{record: sr, score: rrfScore}
		}
	}

	// Collect and sort by fused score.
	results := make([]ScoredRecord, 0, len(fused))
	for _, entry := range fused {
		entry.record.Score = float32(entry.score)
		results = append(results, entry.record)
	}

	sortByScore(results)

	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

// fetchRecordsByIDs fetches full records from context_vectors by their IDs.
func (s *SQLiteStore) fetchRecordsByIDs(ctx context.Context, ids []string) ([]Record, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	queryArgs := make([]interface{}, len(ids))
	for i, id := range ids {
		queryArgs[i] = id
	}
	q := `SELECT id, source_id, source_type, text_chunk, embedding, created_at, tags
		FROM context_vectors WHERE id IN (?` + strings.Repeat(",?", len(ids)-1) + `)`

	rows, err := s.db.QueryContext(ctx, q, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("fetching records by IDs: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		var r Record
		var blob []byte
		var createdAt string
		if err := rows.Scan(&r.ID, &r.SourceID, &r.SourceType, &r.TextChunk, &blob, &createdAt, &r.Tags); err != nil {
			return nil, fmt.Errorf("scanning record: %w", err)
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


