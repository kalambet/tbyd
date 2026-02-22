package intent

import (
	"fmt"
	"strings"

	"github.com/kalambet/tbyd/internal/ollama"
)

const systemPromptTemplate = `You are an intent extraction engine. Analyze the user's query and conversation history. Your output must be ONLY a single valid JSON object that conforms to the provided schema. Do not include any other text, prose, or markdown.

Intent types:
- "recall": user wants to remember or retrieve something from the past
- "task": user wants to accomplish a specific action
- "question": user is asking a general knowledge or technical question
- "preference_update": user is expressing or updating a preference

Rules:
- Extract all named entities (people, projects, technologies, concepts).
- Infer relevant topic tags for semantic search.
- Determine what stored context would help answer the query.
- Set is_private to true only if the query contains clearly sensitive personal information.`

// BuildPrompt constructs the Ollama chat messages for intent extraction.
func BuildPrompt(query string, history []ollama.Message, profileSummary string) []ollama.Message {
	var sb strings.Builder
	sb.WriteString(systemPromptTemplate)

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
