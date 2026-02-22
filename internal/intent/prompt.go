package intent

import (
	"fmt"
	"strings"

	"github.com/kalambet/tbyd/internal/ollama"
)

const systemPromptTemplate = `You are an intent extraction engine. Analyze the user's query and return ONLY valid JSON matching this schema:

{
  "intent_type": "recall | task | question | preference_update",
  "entities": ["named entities mentioned"],
  "topics": ["semantic topic tags"],
  "context_needs": ["what kind of context would help"],
  "is_private": false
}

Intent types:
- "recall": user wants to remember or retrieve something from the past
- "task": user wants to accomplish a specific action
- "question": user is asking a general knowledge or technical question
- "preference_update": user is expressing or updating a preference

Rules:
- Return ONLY the JSON object, no prose, no markdown, no explanation.
- Extract all named entities (people, projects, technologies, concepts).
- Infer relevant topic tags for semantic search.
- Determine what stored context would help answer the query.
- Set is_private to true only if the query contains clearly sensitive personal information.`

// BuildPrompt constructs the Ollama chat messages for intent extraction.
func BuildPrompt(query string, history []Message, profileSummary string) []ollama.Message {
	var sb strings.Builder
	sb.WriteString(systemPromptTemplate)

	if profileSummary != "" {
		fmt.Fprintf(&sb, "\n\n[User Profile]\n%s", profileSummary)
	}

	messages := []ollama.Message{
		{Role: "system", Content: sb.String()},
	}

	for _, m := range history {
		messages = append(messages, ollama.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	messages = append(messages, ollama.Message{
		Role:    "user",
		Content: query,
	})

	return messages
}
