-- Regular FTS5 table for BM25 keyword search on context_vectors.
-- Stores its own copy of doc_id and text_chunk (not content-sync mode).
-- This avoids the data integrity risk of content-sync mode where implicit
-- rowids become stale after VACUUM. Regular mode stores the column values
-- directly in the FTS index, trading ~2x text storage for reliability.
CREATE VIRTUAL TABLE IF NOT EXISTS context_vectors_fts USING fts5(
    doc_id,
    text_chunk
);

-- Triggers keep FTS index in sync with context_vectors.
CREATE TRIGGER IF NOT EXISTS context_vectors_ai AFTER INSERT ON context_vectors BEGIN
    INSERT INTO context_vectors_fts(doc_id, text_chunk) VALUES (new.id, new.text_chunk);
END;

CREATE TRIGGER IF NOT EXISTS context_vectors_ad AFTER DELETE ON context_vectors BEGIN
    DELETE FROM context_vectors_fts WHERE doc_id = old.id;
END;

CREATE TRIGGER IF NOT EXISTS context_vectors_au AFTER UPDATE ON context_vectors BEGIN
    DELETE FROM context_vectors_fts WHERE doc_id = old.id;
    INSERT INTO context_vectors_fts(doc_id, text_chunk) VALUES (new.id, new.text_chunk);
END;

-- Backfill: populate FTS from existing context_vectors rows.
INSERT INTO context_vectors_fts(doc_id, text_chunk)
    SELECT id, text_chunk FROM context_vectors;
