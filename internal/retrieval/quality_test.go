package retrieval

import (
	"math"
	"testing"
	"time"
)

func TestAdjustQualityScores_NegativeDecrement(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(4, 0.1)
	ids := []string{"v1", "v2", "v3"}
	for _, id := range ids {
		if err := s.Insert("context_vectors", []Record{{
			ID:         id,
			SourceID:   "src",
			SourceType: "doc",
			TextChunk:  "text",
			Embedding:  vec,
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		}}); err != nil {
			t.Fatalf("Insert %s: %v", id, err)
		}
	}

	if err := AdjustQualityScores(db, ids, false); err != nil {
		t.Fatalf("AdjustQualityScores: %v", err)
	}

	for _, id := range ids {
		var qs float64
		if err := db.QueryRow(`SELECT quality_score FROM context_vectors WHERE id = ?`, id).Scan(&qs); err != nil {
			t.Fatalf("querying quality_score for %s: %v", id, err)
		}
		want := 0.9
		if math.Abs(qs-want) > 1e-6 {
			t.Errorf("id=%s: quality_score = %f, want %f", id, qs, want)
		}
	}
}

func TestAdjustQualityScores_PositiveIncrement(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(4, 0.1)
	ids := []string{"v1", "v2"}
	for _, id := range ids {
		if err := s.Insert("context_vectors", []Record{{
			ID:         id,
			SourceID:   "src",
			SourceType: "doc",
			TextChunk:  "text",
			Embedding:  vec,
			CreatedAt:  time.Now().UTC(),
			Tags:       `[]`,
		}}); err != nil {
			t.Fatalf("Insert %s: %v", id, err)
		}
	}

	if err := AdjustQualityScores(db, ids, true); err != nil {
		t.Fatalf("AdjustQualityScores: %v", err)
	}

	for _, id := range ids {
		var qs float64
		if err := db.QueryRow(`SELECT quality_score FROM context_vectors WHERE id = ?`, id).Scan(&qs); err != nil {
			t.Fatalf("querying quality_score for %s: %v", id, err)
		}
		want := 1.05
		if math.Abs(qs-want) > 1e-6 {
			t.Errorf("id=%s: quality_score = %f, want %f", id, qs, want)
		}
	}
}

func TestAdjustQualityScores_ClampLow(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(4, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "clamp-low",
		SourceID:   "src",
		SourceType: "doc",
		TextChunk:  "text",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Set quality_score to 0.15 — one negative step would produce 0.05, below the clamp floor.
	if _, err := db.Exec(`UPDATE context_vectors SET quality_score = 0.15 WHERE id = 'clamp-low'`); err != nil {
		t.Fatalf("setting quality_score: %v", err)
	}

	if err := AdjustQualityScores(db, []string{"clamp-low"}, false); err != nil {
		t.Fatalf("AdjustQualityScores: %v", err)
	}

	var qs float64
	if err := db.QueryRow(`SELECT quality_score FROM context_vectors WHERE id = 'clamp-low'`).Scan(&qs); err != nil {
		t.Fatalf("querying quality_score: %v", err)
	}
	want := 0.1
	if math.Abs(qs-want) > 1e-6 {
		t.Errorf("quality_score = %f, want %f (clamped to floor)", qs, want)
	}
}

func TestAdjustQualityScores_ClampHigh(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(4, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "clamp-high",
		SourceID:   "src",
		SourceType: "doc",
		TextChunk:  "text",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Set quality_score to 1.98 — one positive step would produce 2.03, above the clamp ceiling.
	if _, err := db.Exec(`UPDATE context_vectors SET quality_score = 1.98 WHERE id = 'clamp-high'`); err != nil {
		t.Fatalf("setting quality_score: %v", err)
	}

	if err := AdjustQualityScores(db, []string{"clamp-high"}, true); err != nil {
		t.Fatalf("AdjustQualityScores: %v", err)
	}

	var qs float64
	if err := db.QueryRow(`SELECT quality_score FROM context_vectors WHERE id = 'clamp-high'`).Scan(&qs); err != nil {
		t.Fatalf("querying quality_score: %v", err)
	}
	want := 2.0
	if math.Abs(qs-want) > 1e-6 {
		t.Errorf("quality_score = %f, want %f (clamped to ceiling)", qs, want)
	}
}

func TestAdjustQualityScores_EmptyIDs(t *testing.T) {
	db := openTestDB(t)

	// Should return nil immediately without touching the database.
	if err := AdjustQualityScores(db, []string{}, false); err != nil {
		t.Errorf("AdjustQualityScores with empty slice: %v", err)
	}
	if err := AdjustQualityScores(db, nil, true); err != nil {
		t.Errorf("AdjustQualityScores with nil slice: %v", err)
	}
}

func TestAdjustQualityScores_NonexistentID(t *testing.T) {
	db := openTestDB(t)

	// A non-existent ID should silently affect 0 rows — not an error.
	if err := AdjustQualityScores(db, []string{"does-not-exist"}, false); err != nil {
		t.Errorf("AdjustQualityScores with unknown ID should not error: %v", err)
	}
}

func TestAdjustQualityScores_RepeatedNegative(t *testing.T) {
	db := openTestDB(t)
	s := NewSQLiteStore(db)

	vec := makeTestVector(4, 0.1)
	if err := s.Insert("context_vectors", []Record{{
		ID:         "repeated",
		SourceID:   "src",
		SourceType: "doc",
		TextChunk:  "text",
		Embedding:  vec,
		CreatedAt:  time.Now().UTC(),
		Tags:       `[]`,
	}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// Apply 15 negative adjustments; floor at 0.1 means it can never drop below that.
	for i := 0; i < 15; i++ {
		if err := AdjustQualityScores(db, []string{"repeated"}, false); err != nil {
			t.Fatalf("AdjustQualityScores iteration %d: %v", i, err)
		}
	}

	var qs float64
	if err := db.QueryRow(`SELECT quality_score FROM context_vectors WHERE id = 'repeated'`).Scan(&qs); err != nil {
		t.Fatalf("querying quality_score: %v", err)
	}
	if qs < 0.1-1e-6 {
		t.Errorf("quality_score = %f, must never drop below 0.1", qs)
	}
}
