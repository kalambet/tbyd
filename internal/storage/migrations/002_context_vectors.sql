CREATE TABLE IF NOT EXISTS context_vectors (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    text_chunk TEXT NOT NULL,
    embedding BLOB NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    tags TEXT DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_context_vectors_source_id ON context_vectors(source_id);
CREATE INDEX IF NOT EXISTS idx_context_vectors_source_type ON context_vectors(source_type);
