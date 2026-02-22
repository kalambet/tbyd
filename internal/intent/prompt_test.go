package intent

import (
	"strings"
	"testing"

	"github.com/kalambet/tbyd/internal/ollama"
)

func TestPromptContainsInstructions(t *testing.T) {
	messages := BuildPrompt("test query", nil, "")

	system := messages[0].Content
	if !strings.Contains(system, "intent extraction engine") {
		t.Error("system prompt does not contain role instruction")
	}
	if !strings.Contains(system, "recall") {
		t.Error("system prompt does not contain intent type definitions")
	}
	if !strings.Contains(system, "is_private") {
		t.Error("system prompt does not contain privacy rule")
	}
}

func TestPromptInjectsProfile(t *testing.T) {
	messages := BuildPrompt("query", nil, "User: software engineer. Prefers: direct tone.")

	system := messages[0].Content
	if !strings.Contains(system, "direct tone") {
		t.Error("system prompt does not contain profile summary")
	}
	if !strings.Contains(system, "[User Profile]") {
		t.Error("system prompt does not contain profile section header")
	}
}

func TestPromptHistory(t *testing.T) {
	history := []ollama.Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "first reply"},
		{Role: "user", Content: "second message"},
	}

	messages := BuildPrompt("current query", history, "")

	// system + 3 history + 1 user query = 5
	if len(messages) != 5 {
		t.Fatalf("got %d messages, want 5", len(messages))
	}

	if messages[1].Content != "first message" {
		t.Errorf("messages[1].Content = %q, want %q", messages[1].Content, "first message")
	}
	if messages[2].Content != "first reply" {
		t.Errorf("messages[2].Content = %q, want %q", messages[2].Content, "first reply")
	}
	if messages[3].Content != "second message" {
		t.Errorf("messages[3].Content = %q, want %q", messages[3].Content, "second message")
	}
	if messages[4].Content != "current query" {
		t.Errorf("messages[4].Content = %q, want %q", messages[4].Content, "current query")
	}
}
