ALTER TABLE interactions ADD COLUMN extracted_signals TEXT DEFAULT '';

CREATE TABLE IF NOT EXISTS signal_counts (
    pattern_key TEXT NOT NULL,
    pattern_display TEXT NOT NULL,
    positive_count INTEGER NOT NULL DEFAULT 0,
    negative_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (pattern_key)
);
