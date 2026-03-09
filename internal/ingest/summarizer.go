package ingest

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ChatOptions holds optional parameters for chat calls (e.g. temperature).
type ChatOptions struct {
	Temperature *float64
}

// ChatEngine abstracts local model chat for summarization.
type ChatEngine interface {
	Chat(ctx context.Context, model string, messages []ChatMessage, opts *ChatOptions) (string, error)
}

// ChatMessage is a minimal message for the chat engine.
type ChatMessage struct {
	Role    string
	Content string
}

// LLMSummarizer generates interaction summaries using a local LLM.
type LLMSummarizer struct {
	engine ChatEngine
	model  string
}

// NewLLMSummarizer creates a summarizer that uses the given model.
func NewLLMSummarizer(engine ChatEngine, model string) *LLMSummarizer {
	return &LLMSummarizer{engine: engine, model: model}
}

const summarizeSystemPrompt = `You are a concise summarizer. Summarize interactions in exactly one sentence.
Format: "[DATE] User asked about TOPIC. Response: KEY_POINT."
Output ONLY the summary sentence, nothing else.`

// Summarize generates a short summary of an interaction.
// Uses structural role separation to prevent prompt injection from user
// content: system message holds instructions, user/assistant messages hold
// untrusted content in their natural roles.
func (s *LLMSummarizer) Summarize(ctx context.Context, userQuery, assistantResponse string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Truncate long responses to avoid blowing context budget.
	truncatedResponse := assistantResponse
	if len(truncatedResponse) > 500 {
		truncatedResponse = truncatedResponse[:500]
	}

	today := time.Now().Format("2006-01-02")
	messages := []ChatMessage{
		{Role: "system", Content: fmt.Sprintf("%s\nToday's date: %s", summarizeSystemPrompt, today)},
		{Role: "user", Content: userQuery},
		{Role: "assistant", Content: truncatedResponse},
		{Role: "user", Content: "Summarize the above interaction in one sentence using the specified format."},
	}

	temp := 0.0
	result, err := s.engine.Chat(ctx, s.model, messages, &ChatOptions{Temperature: &temp})
	if err != nil {
		return "", fmt.Errorf("summarize chat: %w", err)
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", fmt.Errorf("summarizer returned empty result")
	}

	return result, nil
}
