package synthesis

import (
	"strings"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/storage"
)

// SignalCountReader provides read access to aggregated signal counts.
type SignalCountReader interface {
	GetSignalCounts() ([]storage.SignalCount, error)
}

// Activation thresholds for preference signals. A pattern is activated when
// EITHER the count rule OR the net score rule fires.
const (
	// MinCountThreshold: a pattern seen this many times as the same type
	// (all positive or all negative) triggers the count rule.
	MinCountThreshold = 3

	// MinNetAddThreshold: net = positive - negative; net >= this value adds.
	MinNetAddThreshold = 2

	// MaxNetRemoveThreshold: net <= this value removes.
	MaxNetRemoveThreshold = -2
)

// ShouldActivate applies the activation rules to a pair of positive/negative
// counts and returns (shouldAdd, shouldRemove). When both are true the pattern
// is a true conflict and the caller should skip it.
func ShouldActivate(pos, neg int) (shouldAdd, shouldRemove bool) {
	net := pos - neg
	shouldAdd = pos >= MinCountThreshold || net >= MinNetAddThreshold
	shouldRemove = neg >= MinCountThreshold || net <= MaxNetRemoveThreshold
	return
}

// Aggregate reduces a slice of PreferenceSignals into a ProfileDelta by
// counting positive and negative occurrences of each normalized pattern and
// applying activation rules.
//
// Activation rules (EITHER is sufficient to include a pattern):
//   - Count rule: pos >= MinCountThreshold → add; neg >= MinCountThreshold → remove
//   - Net score rule: net >= MinNetAddThreshold → add; net <= MaxNetRemoveThreshold → remove
//
// Conflicting patterns (both add and remove activate) are omitted.
// The original casing of the first signal seen for a pattern is preserved.
func Aggregate(signals []PreferenceSignal) profile.ProfileDelta {
	type counts struct {
		pos, neg int
	}

	normalized := make(map[string]*counts) // normalized key → counts
	original := make(map[string]string)    // normalized key → original casing (first seen)

	for _, s := range signals {
		key := strings.ToLower(strings.TrimSpace(s.Pattern))
		if key == "" {
			continue
		}
		if _, ok := normalized[key]; !ok {
			normalized[key] = &counts{}
			original[key] = s.Pattern
		}
		switch s.Type {
		case "positive":
			normalized[key].pos++
		case "negative":
			normalized[key].neg++
		}
	}

	// Deduplicate using maps so output slices contain no duplicates.
	addSet := make(map[string]struct{})
	removeSet := make(map[string]struct{})

	for key, c := range normalized {
		shouldAdd, shouldRemove := ShouldActivate(c.pos, c.neg)

		if shouldAdd && shouldRemove {
			// True conflict — skip.
			continue
		}
		orig := original[key]
		if shouldAdd {
			addSet[orig] = struct{}{}
		} else if shouldRemove {
			removeSet[orig] = struct{}{}
		}
	}

	var delta profile.ProfileDelta
	for p := range addSet {
		delta.AddPreferences = append(delta.AddPreferences, p)
	}
	for p := range removeSet {
		delta.RemovePreferences = append(delta.RemovePreferences, p)
	}

	return delta
}

// AggregateFromCounts reads pre-computed per-pattern signal counts from storage
// and applies the shared activation rules. This is the production aggregation
// path used by the FeedbackWorker — O(distinct patterns), not O(interactions).
func AggregateFromCounts(reader SignalCountReader) (profile.ProfileDelta, error) {
	counts, err := reader.GetSignalCounts()
	if err != nil {
		return profile.ProfileDelta{}, err
	}

	var delta profile.ProfileDelta
	for _, c := range counts {
		shouldAdd, shouldRemove := ShouldActivate(c.PositiveCount, c.NegativeCount)

		if shouldAdd && shouldRemove {
			continue // true conflict
		}
		if shouldAdd {
			delta.AddPreferences = append(delta.AddPreferences, c.PatternDisplay)
		} else if shouldRemove {
			delta.RemovePreferences = append(delta.RemovePreferences, c.PatternDisplay)
		}
	}
	return delta, nil
}
