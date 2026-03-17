package intent

import (
	"fmt"
	"strings"

	"github.com/kalambet/tbyd/internal/ollama"
	"github.com/kalambet/tbyd/internal/profile"
)

const systemPromptTemplate = `You are an intent extraction engine. Analyze the user's query and conversation history. Your output must be ONLY a single valid JSON object matching this schema. Do not include any other text, prose, or markdown.

Output schema:
{
  "intent_type": "string (one of: recall, task, question, preference_update)",
  "entities": ["string (named entities mentioned)"],
  "topics": ["string (semantic topic tags)"],
  "context_needs": ["string (what kind of context would help)"],
  "is_private": "boolean",
  "search_strategy": "string (one of: vector_only, hybrid, keyword_heavy)",
  "hybrid_ratio": "number (0.0 = all keyword, 1.0 = all vector, default 0.7)",
  "suggested_top_k": "integer (suggested number of results, 0 = use default)"
}

Intent types:
- "recall": user wants to remember or retrieve something from the past
- "task": user wants to accomplish a specific action
- "question": user is asking a general knowledge or technical question
- "preference_update": user is expressing or updating a preference

Search strategies:
- "vector_only": use only semantic/vector search (best for conceptual, abstract queries)
- "hybrid": combine vector search with keyword/BM25 search (default, good for most queries)
- "keyword_heavy": emphasize keyword/BM25 search over vector search (best for entity-heavy queries with specific names, IDs, or technical terms)

Rules:
- Extract all named entities (people, projects, technologies, concepts).
- Infer relevant topic tags for semantic search.
- Determine what stored context would help answer the query.
- Set is_private to true only if the query contains clearly sensitive personal information.
- Choose search_strategy based on query characteristics: use "keyword_heavy" when the query contains specific names or technical terms, "vector_only" for abstract/conceptual queries, "hybrid" otherwise.
- Set hybrid_ratio to control the blend: lower values (e.g. 0.3) favor keyword search, higher values (e.g. 0.8) favor vector search.`

// BuildPrompt constructs the Ollama chat messages for intent extraction.
// calibration is injected before the profile section to prime the model with
// the user's domain expertise before it sees the broader profile summary.
func BuildPrompt(query string, history []ollama.Message, profileSummary string, calibration profile.CalibrationContext) []ollama.Message {
	var sb strings.Builder
	sb.WriteString(systemPromptTemplate)

	if calibration.Hints != "" {
		fmt.Fprintf(&sb, "\n\n[Calibration]\n%s", calibration.Hints)
	}

	if profileSummary != "" {
		fmt.Fprintf(&sb, "\n\n[User Profile]\n%s", profileSummary)
	}

	messages := []ollama.Message{
		{Role: "system", Content: sb.String()},
	}

	messages = append(messages, history...)

	messages = append(messages, ollama.Message{
		Role:    "user",
		Content: query,
	})

	return messages
}
