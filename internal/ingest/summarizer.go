package ingest

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kalambet/tbyd/internal/engine"
)

// ChatEngine abstracts local model chat for summarization.
type ChatEngine interface {
	Chat(ctx context.Context, model string, messages []ChatMessage, opts *engine.ChatOptions) (string, error)
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
	now    func() time.Time
}

// NewLLMSummarizer creates a summarizer that uses the given model.
func NewLLMSummarizer(engine ChatEngine, model string) *LLMSummarizer {
	return &LLMSummarizer{engine: engine, model: model, now: time.Now}
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

	// Truncate inputs to avoid blowing the local model's context budget.
	// User queries are typically short but could be large document pastes.
	truncatedQuery := truncateRuneSafe(userQuery, 500)
	truncatedResponse := truncateRuneSafe(assistantResponse, 2000)

	today := s.now().Format("2006-01-02")
	messages := []ChatMessage{
		{Role: "system", Content: fmt.Sprintf("%s\nToday's date: %s", summarizeSystemPrompt, today)},
		{Role: "user", Content: truncatedQuery},
		{Role: "assistant", Content: truncatedResponse},
		{Role: "user", Content: "Summarize the above interaction in one sentence using the specified format."},
	}

	temp := 0.0
	result, err := s.engine.Chat(ctx, s.model, messages, &engine.ChatOptions{Temperature: &temp})
	if err != nil {
		return "", fmt.Errorf("summarize chat: %w", err)
	}

	result = strings.TrimSpace(result)
	if result == "" {
		return "", fmt.Errorf("summarizer returned empty result")
	}

	return result, nil
}

// truncateRuneSafe truncates s to at most maxRunes runes, ensuring the result
// is always valid UTF-8 (never splits a multi-byte character).
func truncateRuneSafe(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes])
}
