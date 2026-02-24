package composer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kalambet/tbyd/internal/proxy"
	"github.com/kalambet/tbyd/internal/retrieval"
)

const defaultMaxContextTokens = 4000

// Composer assembles enriched prompts from user profile, retrieved context
// chunks, and the original user query. It produces a ChatRequest ready for
// the cloud proxy.
type Composer struct {
	MaxContextTokens int
}

// New creates a Composer with the given token budget for injected context.
// If maxContextTokens <= 0, the default (4000) is used.
func New(maxContextTokens int) *Composer {
	if maxContextTokens <= 0 {
		maxContextTokens = defaultMaxContextTokens
	}
	return &Composer{MaxContextTokens: maxContextTokens}
}

// Compose builds an enriched ChatRequest by prepending a system message
// containing the profile summary and relevant context chunks. If the original
// request already has a system message, the enrichment content is prepended
// to it. Original user messages are preserved unchanged.
func (c *Composer) Compose(req proxy.ChatRequest, chunks []retrieval.ContextChunk, profileSummary string) (proxy.ChatRequest, error) {
	msgs, err := parseMessages(req.Messages)
	if err != nil {
		return req, fmt.Errorf("parsing messages: %w", err)
	}

	enrichment := c.buildEnrichment(chunks, profileSummary)
	if enrichment == "" {
		return req, nil
	}

	if len(msgs) > 0 && getRole(msgs[0]) == "system" {
		existing := getContent(msgs[0])
		merged := enrichment + "\n\n---\n\n" + existing
		setContent(msgs[0], merged)
	} else {
		sys := makeSystemMessage(enrichment)
		msgs = append([]rawMsg{sys}, msgs...)
	}

	marshalled, err := json.Marshal(msgs)
	if err != nil {
		return req, fmt.Errorf("marshalling messages: %w", err)
	}

	out := req
	out.Messages = marshalled
	return out, nil
}

// buildEnrichment constructs the system message content from profile and chunks,
// respecting the token budget by dropping lowest-scoring chunks first.
func (c *Composer) buildEnrichment(chunks []retrieval.ContextChunk, profileSummary string) string {
	var sb strings.Builder

	// Profile section.
	if profileSummary != "" {
		sb.WriteString("[User Profile]\n")
		sb.WriteString(profileSummary)
	}

	if len(chunks) == 0 {
		return sb.String()
	}

	// Sort chunks by score descending.
	sorted := make([]retrieval.ContextChunk, len(chunks))
	copy(sorted, chunks)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})

	// Budget: total injected content must stay under MaxContextTokens.
	profileTokens := EstimateTokens(sb.String())
	contextHeader := "\n\n[Retrieved Context]\n"
	headerTokens := EstimateTokens(contextHeader)
	remaining := c.MaxContextTokens - profileTokens - headerTokens

	var selectedEntries []string
	for _, ch := range sorted {
		entry := formatChunk(ch)
		tokens := EstimateTokens(entry)
		if tokens > remaining {
			continue
		}
		selectedEntries = append(selectedEntries, entry)
		remaining -= tokens
	}

	if len(selectedEntries) > 0 {
		sb.WriteString(contextHeader)
		for _, entry := range selectedEntries {
			sb.WriteString(entry)
		}
	}

	return sb.String()
}

func formatChunk(ch retrieval.ContextChunk) string {
	return fmt.Sprintf("(Score: %.2f, Source: %s:%s)\n%s\n\n", ch.Score, ch.SourceType, ch.SourceID, ch.Text)
}

// EstimateTokens provides a rough token count using 4 chars per token heuristic.
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// rawMsg preserves all JSON fields on a message while allowing role/content access.
type rawMsg map[string]json.RawMessage

func parseMessages(data json.RawMessage) ([]rawMsg, error) {
	var msgs []rawMsg
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, err
	}
	return msgs, nil
}

func getRole(m rawMsg) string {
	v, ok := m["role"]
	if !ok {
		return ""
	}
	var role string
	json.Unmarshal(v, &role)
	return role
}

func getContent(m rawMsg) string {
	v, ok := m["content"]
	if !ok {
		return ""
	}
	var content string
	json.Unmarshal(v, &content)
	return content
}

func setContent(m rawMsg, s string) {
	b, _ := json.Marshal(s)
	m["content"] = b
}

func makeSystemMessage(content string) rawMsg {
	m := make(rawMsg)
	m["role"], _ = json.Marshal("system")
	m["content"], _ = json.Marshal(content)
	return m
}
