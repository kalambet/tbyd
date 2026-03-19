package retrieval

import (
	"database/sql"
	"fmt"
	"strings"
)

// AdjustQualityScores increments or decrements the quality_score of the given
// vector IDs by a fixed asymmetric delta, clamping the result to [0.1, 2.0].
//
// Positive feedback (positive=true) adds 0.05; negative feedback subtracts 0.1.
// The asymmetry reflects that irrelevant context is more harmful than relevant
// context is helpful.
//
// An empty vectorIDs slice is a no-op and returns nil immediately.
// IDs that do not exist in context_vectors are silently ignored.
// The caller must ensure len(vectorIDs) < 999 (SQLite's default variable limit).
// In practice, enrichment pipelines return 3-10 chunks so this is not a concern.
func AdjustQualityScores(db *sql.DB, vectorIDs []string, positive bool) error {
	if len(vectorIDs) == 0 {
		return nil
	}

	var delta float64
	if positive {
		delta = 0.05
	} else {
		delta = -0.1
	}

	placeholders := "?" + strings.Repeat(",?", len(vectorIDs)-1)
	query := fmt.Sprintf(
		`UPDATE context_vectors SET quality_score = MIN(2.0, MAX(0.1, quality_score + ?)) WHERE id IN (%s)`,
		placeholders,
	)

	args := make([]interface{}, 0, 1+len(vectorIDs))
	args = append(args, delta)
	for _, id := range vectorIDs {
		args = append(args, id)
	}

	_, err := db.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("adjusting quality scores: %w", err)
	}
	return nil
}
