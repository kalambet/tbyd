# Vector Store Migration Guide

## Current: SQLite Brute-Force (`SQLiteStore`)

The current implementation stores embeddings as BLOBs in SQLite and performs
brute-force cosine similarity search in Go. This is sufficient for ~50-100K
vectors with query latency under 250ms on Apple Silicon.

## When to Migrate

Consider migrating when:
- Vector count exceeds ~100K and query latency is noticeable (>200ms)
- You need ANN indexes (HNSW, IVF-PQ) for sub-10ms queries at scale
- Auto-ingest features (RSS, browser extension) push growth to 500+ vectors/day

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
        log.Fatalf("migration failed at batch %d: %v", i/1000, err)
    }
}
```

### Step 4: Switch in config

Add a config option to select the backend:

```toml
[retrieval]
backend = "lancedb"  # or "sqlite" (default)
lancedb_url = "http://localhost:4002"
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
- **Full-text search**: built-in FTS alongside vector search
- **Lance format**: columnar, versioned, optimized for ML workloads
