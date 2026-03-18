package synthesis

import (
	"encoding/json"
	"testing"

	"github.com/kalambet/tbyd/internal/storage"
)

// docWithTags creates a ContextDoc with a JSON-encoded tags array.
func docWithTags(id string, tags []string) storage.ContextDoc {
	b, _ := json.Marshal(tags)
	return storage.ContextDoc{ID: id, Tags: string(b)}
}

func TestGroupByTopics_EmptyInput(t *testing.T) {
	groups := GroupByTopics(nil)
	if len(groups) != 0 {
		t.Errorf("nil input: got %d groups, want 0", len(groups))
	}

	groups = GroupByTopics([]storage.ContextDoc{})
	if len(groups) != 0 {
		t.Errorf("empty slice: got %d groups, want 0", len(groups))
	}
}

func TestGroupByTopics_EmptyTopics(t *testing.T) {
	// Docs with no topics should all land in the mixed batch.
	docs := []storage.ContextDoc{
		{ID: "a", Tags: "[]"},
		{ID: "b", Tags: ""},
		{ID: "c"},
	}
	groups := GroupByTopics(docs)

	// All 3 should appear exactly once, all in a mixed batch (last group).
	seen := countDocOccurrences(groups)
	for _, doc := range docs {
		if seen[doc.ID] != 1 {
			t.Errorf("doc %s appeared %d times, want 1", doc.ID, seen[doc.ID])
		}
	}
}

func TestGroupByTopics_OverlappingTopics(t *testing.T) {
	// 3 docs all share "go" — they must end up in the same group.
	docs := []storage.ContextDoc{
		docWithTags("a", []string{"go", "concurrency"}),
		docWithTags("b", []string{"go", "channels"}),
		docWithTags("c", []string{"go", "goroutines"}),
	}
	groups := GroupByTopics(docs)

	// Find the group containing doc "a".
	aGroup := findGroup(groups, "a")
	if aGroup == nil {
		t.Fatal("doc a not found in any group")
	}

	// All three should be in the same group (they share "go").
	groupIDs := make(map[string]bool)
	for _, d := range aGroup {
		groupIDs[d.ID] = true
	}

	for _, id := range []string{"a", "b", "c"} {
		if !groupIDs[id] {
			t.Errorf("doc %s not in same group as doc a", id)
		}
	}
}

func TestGroupByTopics_NoOverlap(t *testing.T) {
	// 3 docs with completely disjoint topics.
	// Jaccard similarity = 0 → they each end up in separate groups.
	docs := []storage.ContextDoc{
		docWithTags("a", []string{"rust"}),
		docWithTags("b", []string{"python"}),
		docWithTags("c", []string{"java"}),
	}
	groups := GroupByTopics(docs)

	// Each group should contain exactly one doc (no merging).
	// All docs must appear exactly once total.
	seen := countDocOccurrences(groups)
	for _, doc := range docs {
		if seen[doc.ID] != 1 {
			t.Errorf("doc %s appeared %d times, want 1", doc.ID, seen[doc.ID])
		}
	}
}

func TestGroupByTopics_PartialOverlap(t *testing.T) {
	// 5 docs:
	//   a,b share "kubernetes"
	//   c,d share "privacy"
	//   e is isolated
	docs := []storage.ContextDoc{
		docWithTags("a", []string{"kubernetes", "devops"}),
		docWithTags("b", []string{"kubernetes", "containers"}),
		docWithTags("c", []string{"privacy", "gdpr"}),
		docWithTags("d", []string{"privacy", "data"}),
		docWithTags("e", []string{"cooking"}),
	}
	groups := GroupByTopics(docs)

	// a and b must be in the same group.
	aGroup := findGroup(groups, "a")
	if aGroup == nil {
		t.Fatal("doc a not found in any group")
	}
	if !groupContains(aGroup, "b") {
		t.Error("doc b should be grouped with doc a (they share 'kubernetes')")
	}

	// c and d must be in the same group.
	cGroup := findGroup(groups, "c")
	if cGroup == nil {
		t.Fatal("doc c not found in any group")
	}
	if !groupContains(cGroup, "d") {
		t.Error("doc d should be grouped with doc c (they share 'privacy')")
	}

	// a and c must not be in the same group.
	if groupContains(aGroup, "c") {
		t.Error("doc c should not be in the same group as doc a")
	}

	// All docs must appear exactly once.
	seen := countDocOccurrences(groups)
	for _, doc := range docs {
		if seen[doc.ID] != 1 {
			t.Errorf("doc %s appeared %d times, want 1", doc.ID, seen[doc.ID])
		}
	}
}

// --- helpers ---

func findGroup(groups [][]storage.ContextDoc, id string) []storage.ContextDoc {
	for _, g := range groups {
		for _, d := range g {
			if d.ID == id {
				return g
			}
		}
	}
	return nil
}

func groupContains(group []storage.ContextDoc, id string) bool {
	for _, d := range group {
		if d.ID == id {
			return true
		}
	}
	return false
}

func countDocOccurrences(groups [][]storage.ContextDoc) map[string]int {
	m := make(map[string]int)
	for _, g := range groups {
		for _, d := range g {
			m[d.ID]++
		}
	}
	return m
}
