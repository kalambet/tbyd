CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS interactions (
    id TEXT PRIMARY KEY,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    user_query TEXT NOT NULL,
    enriched_prompt TEXT,
    cloud_model TEXT,
    cloud_response TEXT,
    status TEXT NOT NULL DEFAULT 'completed',
    feedback_score INTEGER DEFAULT 0,
    feedback_notes TEXT,
    vector_ids TEXT DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_interactions_feedback ON interactions(feedback_score);
CREATE INDEX IF NOT EXISTS idx_interactions_created ON interactions(created_at);

CREATE TABLE IF NOT EXISTS user_profile (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS context_docs (
    id TEXT PRIMARY KEY,
    title TEXT,
    content TEXT NOT NULL,
    source TEXT NOT NULL,
    tags TEXT DEFAULT '[]',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    vector_id TEXT
);

CREATE TABLE IF NOT EXISTS context_vectors (
    id TEXT PRIMARY KEY,
    source_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    text_chunk TEXT NOT NULL,
    embedding BLOB NOT NULL,
    created_at TEXT NOT NULL,
    tags TEXT NOT NULL DEFAULT '[]'
);

CREATE INDEX IF NOT EXISTS idx_context_vectors_source_id ON context_vectors(source_id);
CREATE INDEX IF NOT EXISTS idx_context_vectors_source_type ON context_vectors(source_type);

CREATE TABLE IF NOT EXISTS jobs (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER DEFAULT 0,
    max_attempts INTEGER DEFAULT 3,
    run_after DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT
);

CREATE INDEX IF NOT EXISTS idx_jobs_status_run_after ON jobs(status, run_after);
