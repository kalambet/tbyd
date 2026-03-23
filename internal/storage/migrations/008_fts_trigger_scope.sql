-- Narrow the FTS UPDATE trigger to fire only when text_chunk changes.
-- The original trigger (002_add_fts5.sql) fires on ANY UPDATE, causing
-- unnecessary FTS delete+reinsert when only quality_score is adjusted.
DROP TRIGGER IF EXISTS context_vectors_au;

CREATE TRIGGER IF NOT EXISTS context_vectors_au AFTER UPDATE OF text_chunk ON context_vectors BEGIN
    DELETE FROM context_vectors_fts WHERE doc_id = old.id;
    INSERT INTO context_vectors_fts(doc_id, text_chunk) VALUES (new.id, new.text_chunk);
END;
