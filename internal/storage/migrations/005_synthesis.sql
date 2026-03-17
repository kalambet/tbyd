CREATE TABLE IF NOT EXISTS pending_profile_deltas (
    id TEXT PRIMARY KEY,
    delta_json TEXT NOT NULL,
    description TEXT NOT NULL,
    source TEXT NOT NULL,
    accepted INTEGER,
    reviewed_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_pending_deltas_source_unreviewed
    ON pending_profile_deltas (source, created_at)
    WHERE accepted IS NULL;
