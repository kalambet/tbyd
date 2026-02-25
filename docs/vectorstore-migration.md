# Vector Store Migration Guide

## Current: SQLite Brute-Force (`SQLiteStore`)

The current implementation stores embeddings as BLOBs in the `context_vectors` table
in SQLite and performs brute-force cosine similarity search in Go. This is sufficient
for ~50–100K vectors with query latency under 250ms on Apple Silicon.

Embedding model: `nomic-embed-text` via Ollama (768 dimensions).

## When to Migrate

Consider migrating when:
- Vector count exceeds ~100K and query latency is noticeable (>200ms)
- You need ANN indexes (HNSW, IVF-PQ) for sub-10ms queries at scale
- Auto-ingest features (RSS, browser extension) push growth to 500+ vectors/day

## Current Limitations

- **No filter support**: SQLite backend currently ignores the `filter` parameter in `Search()`. All results are ranked by cosine similarity only. Basic metadata filtering (by `source_type`, `tags`) is planned as a pre-LanceDB improvement.
- **No ANN indexes**: brute-force scan means latency grows linearly with vector count.
- **Single embedding dimension**: hardcoded to 768-d (`nomic-embed-text`). Changing models requires re-embedding all stored vectors.

## Intermediate Step: Hybrid Search on SQLite (Phase 1)

Before migrating to LanceDB, the SQLite backend gains **hybrid search** (BM25 + vector) using SQLite FTS5. This is a no-new-dependency improvement that substantially improves retrieval quality, especially for entity-heavy and technical queries.

### What Changes

**FTS5 virtual table** added alongside the existing `context_vectors` table:
```sql
CREATE VIRTUAL TABLE context_vectors_fts USING fts5(
    text_chunk,
    content='context_vectors',
    content_rowid='rowid'
);
```
Triggers keep the FTS index in sync with inserts, deletes, and updates on `context_vectors`.

**New VectorStore methods:**
```go
SearchKeyword(table string, query string, topK int) ([]ScoredRecord, error)
SearchHybrid(table string, vector []float32, query string, topK int, vectorWeight float32) ([]ScoredRecord, error)
```

**Hybrid scoring:** Vector similarity and BM25 scores are independently min-max normalized, then blended with a configurable weight ratio (default: 0.7 vector / 0.3 keyword). The intent extractor drives the ratio per-query.

### Why This Matters Before LanceDB

- **Immediate quality gain**: entity names, proper nouns, and technical terms that embed poorly are caught by BM25 keyword matching
- **No new dependencies**: FTS5 is built into SQLite — no Rust sidecar, no new binary
- **Forward-compatible**: when LanceDB migration happens, hybrid search transfers naturally since LanceDB has built-in full-text search alongside vector search
- **Low risk**: existing vector-only `Search()` method unchanged; hybrid search is additive

## Architecture

All vector operations go through the `VectorStore` interface:

```go
type VectorStore interface {
    Insert(table string, records []Record) error
    Search(table string, vector []float32, topK int, filter string) ([]ScoredRecord, error)
    GetByIDs(ctx context.Context, table string, ids []string) ([]Record, error)
    Delete(table string, id string) error
    CreateTable(name string) error
    ExportAll(table string) ([]Record, error)
    Count(table string) (int, error)
}
```

The `Retriever` depends only on this interface. Swapping backends requires:
1. Implementing `VectorStore` for the new backend
2. Changing the constructor call (e.g., `NewSQLiteStore(db)` → `NewLanceDBStore(url)`)

## Migration Path: SQLite → LanceDB (Rust Sidecar)

### Step 1: Build the Rust sidecar

Create a thin axum HTTP server wrapping the LanceDB Rust crate. Endpoints:

```
POST   /tables                      → CreateTable
POST   /tables/:table/records       → Insert
POST   /tables/:table/search        → Search (body: {vector, topK, filter})
DELETE /tables/:table/records/:id   → Delete
GET    /tables/:table/records       → ExportAll
GET    /tables/:table/count         → Count
```

Arrow schema for the `context_vectors` table:
- `id`: Utf8
- `source_id`: Utf8
- `source_type`: Utf8
- `text_chunk`: Utf8
- `embedding`: FixedSizeList<Float32>[768]
- `created_at`: Utf8 (RFC3339)
- `tags`: Utf8 (JSON array)

### Step 2: Implement `LanceDBStore`

```go
// internal/retrieval/lancedb_store.go (future)
type LanceDBStore struct {
    baseURL    string
    httpClient *http.Client
}

func NewLanceDBStore(baseURL string) *LanceDBStore { ... }

// Each method maps to an HTTP call to the Rust sidecar.
```

### Step 3: Data migration

Use the built-in `ExportAll` / `Insert` methods:

```go
// Export from SQLite
records, err := sqliteStore.ExportAll("context_vectors")

// Import into LanceDB (in batches of 1000)
for i := 0; i < len(records); i += 1000 {
    end := i + 1000
    if end > len(records) {
        end = len(records)
    }
    if err := lancedbStore.Insert("context_vectors", records[i:end]); err != nil {
        log.Printf("WARNING: batch %d failed: %v (skipping)", i/1000, err)
        continue
    }
    log.Printf("migrated batch %d (%d records)", i/1000, end-i)
}
```

### Step 4: Switch in config

Add config keys to select the backend (UserDefaults on macOS, XDG JSON on Linux).
These keys are **planned for Phase 4** — add them to `keys.go` when implementing the migration:

```bash
# macOS
defaults write com.tbyd.app retrieval.backend -string "lancedb"
defaults write com.tbyd.app retrieval.lancedb_url -string "http://localhost:4002"

# Or via environment variables
TBYD_RETRIEVAL_BACKEND=lancedb
TBYD_RETRIEVAL_LANCEDB_URL=http://localhost:4002
```

### Step 5: Process lifecycle

Launch the Rust sidecar from the Go binary, similar to Ollama lifecycle management:
- Start on demand, bind to `127.0.0.1:<port>`
- Health check endpoint for readiness
- Graceful shutdown on SIGTERM

## LanceDB Advantages (when you need them)

- **ANN indexes**: HNSW, IVF-PQ, IVF-Flat for sub-10ms search at 1M+ vectors
- **DataFusion SQL**: rich metadata filtering without custom parsing
- **Scalar indexes**: BTree, Bitmap for fast filter pre-screening
- **Full-text search**: built-in FTS alongside vector search (replaces SQLite FTS5; hybrid search logic transfers directly)
- **Lance format**: columnar, versioned, optimized for ML workloads

Note: The hybrid search architecture (VectorStore interface with `SearchHybrid` method) is designed to be backend-agnostic. When migrating to LanceDB, implement `SearchHybrid` on `LanceDBStore` using LanceDB's native full-text + vector search — the `Retriever` and pipeline code require no changes.
