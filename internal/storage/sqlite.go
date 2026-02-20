package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a SQLite database with methods for interactions, profiles, and docs.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database in dataDir and runs pending migrations.
// Pass ":memory:" as dataDir for an in-memory database (used by tests).
func Open(dataDir string) (*Store, error) {
	var dsn string
	if dataDir == ":memory:" {
		dsn = ":memory:"
	} else {
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating data directory: %w", err)
		}
		dsn = filepath.Join(dataDir, "tbyd.db")
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	// Limit to single connection to avoid "database is locked" errors.
	db.SetMaxOpenConns(1)

	// Set busy timeout so concurrent access waits briefly instead of failing immediately.
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy timeout: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting journal mode: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate reads embedded SQL migration files and applies any that haven't been run yet.
func (s *Store) migrate() error {
	// Ensure schema_version table exists (bootstrap).
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		return fmt.Errorf("creating schema_version table: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("reading migrations directory: %w", err)
	}

	// Sort by filename to guarantee ascending order.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		version, err := parseMigrationVersion(entry.Name())
		if err != nil {
			return err
		}

		// Check if already applied.
		var exists int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_version WHERE version = ?", version).Scan(&exists); err != nil {
			return fmt.Errorf("checking migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}

		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", entry.Name(), err)
		}

		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("beginning transaction for migration %d: %w", version, err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying migration %d: %w", version, err)
		}

		if _, err := tx.Exec("INSERT INTO schema_version (version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", version, err)
		}
	}

	return nil
}

func parseMigrationVersion(filename string) (int, error) {
	var version int
	if _, err := fmt.Sscanf(filename, "%d_", &version); err != nil {
		return 0, fmt.Errorf("parsing migration version from %q: %w", filename, err)
	}
	return version, nil
}

// AppliedMigrations returns the list of applied migration versions in ascending order.
func (s *Store) AppliedMigrations() ([]int, error) {
	rows, err := s.db.Query("SELECT version FROM schema_version ORDER BY version ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var versions []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		versions = append(versions, v)
	}
	return versions, rows.Err()
}

// --- Interactions ---

func (s *Store) SaveInteraction(i Interaction) error {
	status := i.Status
	if status == "" {
		status = "completed"
	}
	_, err := s.db.Exec(`
		INSERT INTO interactions (id, created_at, user_query, enriched_prompt, cloud_model, cloud_response, status, feedback_score, feedback_notes, vector_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.CreatedAt.UTC().Format(time.RFC3339), i.UserQuery, i.EnrichedPrompt,
		i.CloudModel, i.CloudResponse, status, i.FeedbackScore, i.FeedbackNotes, i.VectorIDs,
	)
	return err
}

func (s *Store) GetInteraction(id string) (Interaction, error) {
	var i Interaction
	var createdAt string
	err := s.db.QueryRow(`
		SELECT id, created_at, user_query, enriched_prompt, cloud_model, cloud_response, status, feedback_score, feedback_notes, vector_ids
		FROM interactions WHERE id = ?`, id,
	).Scan(&i.ID, &createdAt, &i.UserQuery, &i.EnrichedPrompt, &i.CloudModel, &i.CloudResponse, &i.Status, &i.FeedbackScore, &i.FeedbackNotes, &i.VectorIDs)
	if err == sql.ErrNoRows {
		return Interaction{}, ErrNotFound
	}
	if err != nil {
		return Interaction{}, err
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Interaction{}, fmt.Errorf("parsing created_at: %w", err)
	}
	i.CreatedAt = t
	return i, nil
}

func (s *Store) UpdateFeedback(id string, score int, notes string) error {
	res, err := s.db.Exec(`UPDATE interactions SET feedback_score = ?, feedback_notes = ? WHERE id = ?`, score, notes, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) GetRecentInteractions(limit int) ([]Interaction, error) {
	rows, err := s.db.Query(`
		SELECT id, created_at, user_query, enriched_prompt, cloud_model, cloud_response, status, feedback_score, feedback_notes, vector_ids
		FROM interactions ORDER BY created_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []Interaction
	for rows.Next() {
		var i Interaction
		var createdAt string
		if err := rows.Scan(&i.ID, &createdAt, &i.UserQuery, &i.EnrichedPrompt, &i.CloudModel, &i.CloudResponse, &i.Status, &i.FeedbackScore, &i.FeedbackNotes, &i.VectorIDs); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		i.CreatedAt = t
		results = append(results, i)
	}
	return results, rows.Err()
}

// --- User Profile ---

func (s *Store) SetProfileKey(key, value string) error {
	_, err := s.db.Exec(`
		INSERT INTO user_profile (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (s *Store) GetProfileKey(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM user_profile WHERE key = ?", key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", ErrNotFound
	}
	return value, err
}

func (s *Store) GetAllProfileKeys() (map[string]string, error) {
	rows, err := s.db.Query("SELECT key, value FROM user_profile")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		result[k] = v
	}
	return result, rows.Err()
}

// --- Context Docs ---

func (s *Store) SaveContextDoc(doc ContextDoc) error {
	_, err := s.db.Exec(`
		INSERT INTO context_docs (id, title, content, source, tags, created_at, vector_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Title, doc.Content, doc.Source, doc.Tags,
		doc.CreatedAt.UTC().Format(time.RFC3339), doc.VectorID,
	)
	return err
}

func (s *Store) GetContextDoc(id string) (ContextDoc, error) {
	var d ContextDoc
	var createdAt string
	err := s.db.QueryRow(`
		SELECT id, title, content, source, tags, created_at, vector_id
		FROM context_docs WHERE id = ?`, id,
	).Scan(&d.ID, &d.Title, &d.Content, &d.Source, &d.Tags, &createdAt, &d.VectorID)
	if err == sql.ErrNoRows {
		return ContextDoc{}, ErrNotFound
	}
	if err != nil {
		return ContextDoc{}, err
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return ContextDoc{}, fmt.Errorf("parsing created_at: %w", err)
	}
	d.CreatedAt = t
	return d, nil
}

func (s *Store) ListContextDocs(limit int) ([]ContextDoc, error) {
	rows, err := s.db.Query(`
		SELECT id, title, content, source, tags, created_at, vector_id
		FROM context_docs ORDER BY created_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []ContextDoc
	for rows.Next() {
		var d ContextDoc
		var createdAt string
		if err := rows.Scan(&d.ID, &d.Title, &d.Content, &d.Source, &d.Tags, &createdAt, &d.VectorID); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parsing created_at: %w", err)
		}
		d.CreatedAt = t
		results = append(results, d)
	}
	return results, rows.Err()
}

// --- Jobs ---

func (s *Store) EnqueueJob(job Job) error {
	now := time.Now().UTC().Format(time.RFC3339)
	runAfter := now
	if !job.RunAfter.IsZero() {
		runAfter = job.RunAfter.UTC().Format(time.RFC3339)
	}
	maxAttempts := job.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	_, err := s.db.Exec(`
		INSERT INTO jobs (id, type, payload_json, status, attempts, max_attempts, run_after, created_at, updated_at)
		VALUES (?, ?, ?, 'pending', 0, ?, ?, ?, ?)`,
		job.ID, job.Type, job.PayloadJSON, maxAttempts, runAfter, now, now,
	)
	return err
}

func (s *Store) ClaimNextJob(types []string) (*Job, error) {
	if len(types) == 0 {
		return nil, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	placeholders := strings.Repeat(",?", len(types)-1)
	query := `SELECT id, type, payload_json, status, attempts, max_attempts, run_after, created_at, updated_at, last_error
		FROM jobs
		WHERE status = 'pending' AND run_after <= ? AND type IN (?` + placeholders + `)
		ORDER BY run_after ASC, created_at ASC
		LIMIT 1`

	args := make([]interface{}, 0, len(types)+1)
	args = append(args, now)
	for _, t := range types {
		args = append(args, t)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning claim transaction: %w", err)
	}

	var j Job
	var runAfter, createdAt, updatedAt string
	var lastError sql.NullString
	err = tx.QueryRow(query, args...).Scan(
		&j.ID, &j.Type, &j.PayloadJSON, &j.Status, &j.Attempts, &j.MaxAttempts,
		&runAfter, &createdAt, &updatedAt, &lastError,
	)
	if err == sql.ErrNoRows {
		tx.Rollback()
		return nil, nil
	}
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("selecting next job: %w", err)
	}

	res, err := tx.Exec(`UPDATE jobs SET status = 'running', updated_at = ? WHERE id = ? AND status = 'pending'`, now, j.ID)
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("updating job status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("checking updated job rows: %w", err)
	}
	if n != 1 {
		tx.Rollback()
		return nil, nil
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing claim: %w", err)
	}

	j.Status = "running"
	j.LastError = lastError.String
	if j.RunAfter, err = time.Parse(time.RFC3339, runAfter); err != nil {
		return nil, fmt.Errorf("parsing run_after for job %s: %w", j.ID, err)
	}
	if j.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
		return nil, fmt.Errorf("parsing created_at for job %s: %w", j.ID, err)
	}
	if j.UpdatedAt, err = time.Parse(time.RFC3339, now); err != nil {
		return nil, fmt.Errorf("parsing updated_at for job %s: %w", j.ID, err)
	}
	return &j, nil
}

func (s *Store) CompleteJob(id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`UPDATE jobs SET status = 'completed', updated_at = ? WHERE id = ?`, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FailJob(id string, errMsg string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("beginning fail transaction: %w", err)
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	err = tx.QueryRow(`SELECT attempts, max_attempts FROM jobs WHERE id = ?`, id).Scan(&attempts, &maxAttempts)
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	attempts++

	if attempts >= maxAttempts {
		_, err = tx.Exec(`UPDATE jobs SET status = 'failed', attempts = ?, last_error = ?, updated_at = ? WHERE id = ?`,
			attempts, errMsg, now.Format(time.RFC3339), id)
	} else {
		backoff := time.Duration(math.Pow(2, float64(attempts))) * time.Second
		runAfter := now.Add(backoff)
		_, err = tx.Exec(`UPDATE jobs SET status = 'pending', attempts = ?, last_error = ?, run_after = ?, updated_at = ? WHERE id = ?`,
			attempts, errMsg, runAfter.Format(time.RFC3339), now.Format(time.RFC3339), id)
	}

	if err != nil {
		return err
	}

	return tx.Commit()
}
