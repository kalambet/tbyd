package synthesis

import (
	"encoding/json"

	"github.com/kalambet/tbyd/internal/storage"
)

// jaccardThreshold is the minimum Jaccard similarity required to group two documents.
const jaccardThreshold = 0.3

// GroupByTopics groups documents with overlapping pass-1 topics using Jaccard
// similarity on their topic sets. Documents whose topic overlap with any member
// of an existing group exceeds the threshold are merged into that group.
// Documents with no topics, or whose topics don't overlap sufficiently with any
// group, form a "mixed" batch at the end.
//
// Returns a slice of groups; the mixed batch (if non-empty) is the last element.
func GroupByTopics(docs []storage.ContextDoc) [][]storage.ContextDoc {
	if len(docs) == 0 {
		return nil
	}

	type entry struct {
		doc    storage.ContextDoc
		topics map[string]struct{}
	}

	entries := make([]entry, len(docs))
	for i, d := range docs {
		entries[i] = entry{doc: d, topics: parseTopics(d)}
	}

	// Union-find grouping: each doc starts in its own group.
	groupID := make([]int, len(entries))
	for i := range groupID {
		groupID[i] = i
	}

	var find func(x int) int
	find = func(x int) int {
		if groupID[x] != x {
			groupID[x] = find(groupID[x])
		}
		return groupID[x]
	}
	union := func(x, y int) {
		rx, ry := find(x), find(y)
		if rx != ry {
			groupID[rx] = ry
		}
	}

	// Merge docs that share sufficient topic overlap.
	// NOTE: O(n^2) pairwise comparison. Acceptable for the current deep enrichment
	// batch claim limit (default 5000). If this grows significantly, consider an
	// inverted-index approach to avoid exhaustive comparison.
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			if len(entries[i].topics) == 0 || len(entries[j].topics) == 0 {
				continue
			}
			if jaccard(entries[i].topics, entries[j].topics) > jaccardThreshold {
				union(i, j)
			}
		}
	}

	// Collect groups.
	groupMap := make(map[int][]storage.ContextDoc)
	var noTopicDocs []storage.ContextDoc

	for i, e := range entries {
		if len(e.topics) == 0 {
			noTopicDocs = append(noTopicDocs, e.doc)
			continue
		}
		root := find(i)
		groupMap[root] = append(groupMap[root], e.doc)
	}

	// Split into: topic-grouped groups (size > 1 or with topics) and mixed.
	var groups [][]storage.ContextDoc
	var mixed []storage.ContextDoc

	for _, g := range groupMap {
		// Singletons with topics stay as their own group (don't mix unrelated content).
		// Multi-member groups are kept as-is.
		groups = append(groups, g)
	}

	// Docs with no topics go to the mixed batch.
	mixed = append(mixed, noTopicDocs...)

	if len(mixed) > 0 {
		groups = append(groups, mixed)
	}

	return groups
}

// parseTopics extracts topics from a ContextDoc's Tags field (JSON array)
// or from a "topics" key in its Metadata JSON object.
func parseTopics(doc storage.ContextDoc) map[string]struct{} {
	topics := make(map[string]struct{})

	// Try Tags field first (JSON array of strings).
	if doc.Tags != "" && doc.Tags != "[]" {
		var tags []string
		if err := json.Unmarshal([]byte(doc.Tags), &tags); err == nil {
			for _, t := range tags {
				if t != "" {
					topics[t] = struct{}{}
				}
			}
		}
	}

	// Also try Metadata["topics"] (JSON array of strings).
	if doc.Metadata != "" && doc.Metadata != "{}" {
		var meta map[string]json.RawMessage
		if err := json.Unmarshal([]byte(doc.Metadata), &meta); err == nil {
			if rawTopics, ok := meta["topics"]; ok {
				var metaTopics []string
				if err := json.Unmarshal(rawTopics, &metaTopics); err == nil {
					for _, t := range metaTopics {
						if t != "" {
							topics[t] = struct{}{}
						}
					}
				}
			}
		}
	}

	return topics
}

// jaccard computes the Jaccard similarity between two sets: |A∩B| / |A∪B|.
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}

	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
