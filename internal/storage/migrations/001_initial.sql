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
