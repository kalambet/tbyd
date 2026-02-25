package api

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/kalambet/tbyd/internal/profile"
	"github.com/kalambet/tbyd/internal/retrieval"
	"github.com/kalambet/tbyd/internal/storage"
)

// MCPRetriever abstracts semantic search for the MCP layer.
type MCPRetriever interface {
	Retrieve(ctx context.Context, query string, topK int) ([]retrieval.ContextChunk, error)
}

// MCPEngine abstracts local LLM calls for summarization.
type MCPEngine interface {
	Chat(ctx context.Context, model string, messages []MCPMessage, jsonSchema *MCPSchema) (string, error)
}

// MCPMessage is a chat message for the engine.
type MCPMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// MCPSchema describes expected JSON output.
type MCPSchema struct {
	Type       string                      `json:"type"`
	Properties map[string]MCPSchemaProperty `json:"properties"`
	Required   []string                    `json:"required,omitempty"`
}

// MCPSchemaProperty describes a single field.
type MCPSchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// EngineAdapter wraps an engine.Engine to satisfy MCPEngine.
// Import cycle prevention: engine types are mirrored in this package.
type EngineAdapter struct {
	ChatFn func(ctx context.Context, model string, messages []MCPMessage, jsonSchema *MCPSchema) (string, error)
}

func (a *EngineAdapter) Chat(ctx context.Context, model string, messages []MCPMessage, jsonSchema *MCPSchema) (string, error) {
	return a.ChatFn(ctx, model, messages, jsonSchema)
}

// MCPDeps holds dependencies for the MCP server.
type MCPDeps struct {
	Store     *storage.Store
	Profile   *profile.Manager
	Retriever MCPRetriever
	Engine    MCPEngine       // optional; if nil, summarize_session returns an error
	DeepModel string          // model name for summarization
}

// NewMCPServer creates an MCP server with all tbyd tools and resources registered.
func NewMCPServer(deps MCPDeps) *server.MCPServer {
	s := server.NewMCPServer(
		"tbyd",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, true),
		server.WithInstructions("tbyd — local knowledge base for personal context, recall, and preferences."),
		server.WithRecovery(),
	)

	// Tools
	s.AddTool(
		mcp.NewTool("add_context",
			mcp.WithDescription("Store a piece of context into the local knowledge base for later retrieval."),
			mcp.WithString("title", mcp.Description("Title for the context entry")),
			mcp.WithString("content", mcp.Description("The text content to store"), mcp.Required()),
			mcp.WithArray("tags", mcp.Description("Optional tags for categorization")),
		),
		mcpAddContext(deps),
	)

	s.AddTool(
		mcp.NewTool("recall",
			mcp.WithDescription("Semantically search the local knowledge base and return relevant context chunks."),
			mcp.WithString("query", mcp.Description("Search query"), mcp.Required()),
			mcp.WithNumber("limit", mcp.Description("Maximum number of results (default 5)")),
		),
		mcpRecall(deps),
	)

	s.AddTool(
		mcp.NewTool("set_preference",
			mcp.WithDescription("Update a user profile preference field."),
			mcp.WithString("key", mcp.Description("Profile field key (e.g. communication.tone)"), mcp.Required()),
			mcp.WithString("value", mcp.Description("Value to set"), mcp.Required()),
		),
		mcpSetPreference(deps),
	)

	s.AddTool(
		mcp.NewTool("summarize_session",
			mcp.WithDescription("Summarize a conversation session and store the summary as context for future recall."),
			mcp.WithString("messages", mcp.Description("JSON array of {role, content} message objects"), mcp.Required()),
		),
		mcpSummarizeSession(deps),
	)

	// Resources
	s.AddResource(
		mcp.NewResource(
			"user://profile",
			"User Profile",
			mcp.WithResourceDescription("Current user profile as JSON"),
			mcp.WithMIMEType("application/json"),
		),
		mcpResourceProfile(deps),
	)

	s.AddResource(
		mcp.NewResource(
			"user://recent",
			"Recent Interactions",
			mcp.WithResourceDescription("Last 10 stored interactions (summaries only)"),
			mcp.WithMIMEType("application/json"),
		),
		mcpResourceRecent(deps),
	)

	return s
}

func mcpAddContext(deps MCPDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, err := req.RequireString("content")
		if err != nil {
			return mcpError("content is required"), nil
		}

		title := req.GetString("title", "")

		tags := req.GetStringSlice("tags", nil)

		docID := uuid.New().String()
		tagsJSON := "[]"
		if len(tags) > 0 {
			b, err := json.Marshal(tags)
			if err != nil {
				return mcpError(fmt.Sprintf("failed to marshal tags: %v", err)), nil
			}
			tagsJSON = string(b)
		}

		doc := storage.ContextDoc{
			ID:        docID,
			Title:     title,
			Content:   content,
			Source:    "mcp",
			Tags:      tagsJSON,
			CreatedAt: time.Now().UTC(),
		}
		if err := deps.Store.SaveContextDoc(doc); err != nil {
			return mcpError(fmt.Sprintf("failed to save: %v", err)), nil
		}

		// Enqueue enrichment job.
		payload, err := json.Marshal(map[string]string{"context_doc_id": docID})
		if err != nil {
			return mcpError(fmt.Sprintf("failed to marshal enrichment payload: %v", err)), nil
		}
		job := storage.Job{
			ID:          uuid.New().String(),
			Type:        "ingest_enrich",
			PayloadJSON: string(payload),
		}
		if err := deps.Store.EnqueueJob(job); err != nil {
			return mcpError(fmt.Sprintf("saved doc but failed to queue enrichment: %v", err)), nil
		}

		return mcpText(fmt.Sprintf("Stored context doc %s", docID)), nil
	}
}

func mcpRecall(deps MCPDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query, err := req.RequireString("query")
		if err != nil {
			return mcpError("query is required"), nil
		}

		limit := req.GetInt("limit", 5)
		if limit <= 0 {
			limit = 5
		}
		if limit > 50 {
			limit = 50
		}

		chunks, err := deps.Retriever.Retrieve(ctx, query, limit)
		if err != nil {
			return mcpError(fmt.Sprintf("recall failed: %v", err)), nil
		}

		if len(chunks) == 0 {
			return mcpText("[]"), nil
		}

		type chunkResult struct {
			ID         string  `json:"id"`
			SourceID   string  `json:"source_id"`
			SourceType string  `json:"source_type"`
			Text       string  `json:"text"`
			Score      float32 `json:"score"`
			Tags       string  `json:"tags,omitempty"`
		}

		results := make([]chunkResult, len(chunks))
		for i, c := range chunks {
			results[i] = chunkResult{
				ID:         c.ID,
				SourceID:   c.SourceID,
				SourceType: c.SourceType,
				Text:       c.Text,
				Score:      c.Score,
				Tags:       c.Tags,
			}
		}

		b, err := json.Marshal(results)
		if err != nil {
			return mcpError(fmt.Sprintf("failed to marshal results: %v", err)), nil
		}

		return mcpText(string(b)), nil
	}
}

func mcpSetPreference(deps MCPDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		key, err := req.RequireString("key")
		if err != nil {
			return mcpError("key is required"), nil
		}
		value, err := req.RequireString("value")
		if err != nil {
			return mcpError("value is required"), nil
		}

		if err := deps.Profile.SetField(key, value); err != nil {
			return mcpError(fmt.Sprintf("failed to set preference: %v", err)), nil
		}

		return mcpText(fmt.Sprintf("Set %s = %s", key, value)), nil
	}
}

func mcpSummarizeSession(deps MCPDeps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if deps.Engine == nil {
			return mcpError("summarization not available: no local model configured"), nil
		}

		messagesJSON, err := req.RequireString("messages")
		if err != nil {
			return mcpError("messages is required"), nil
		}

		var messages []MCPMessage
		if err := json.Unmarshal([]byte(messagesJSON), &messages); err != nil {
			return mcpError(fmt.Sprintf("invalid messages JSON: %v", err)), nil
		}

		// Build summarization prompt.
		var conversationText string
		for _, m := range messages {
			conversationText += fmt.Sprintf("[%s]: %s\n", m.Role, m.Content)
		}

		summaryPrompt := []MCPMessage{
			{
				Role:    "system",
				Content: "Summarize the following conversation concisely, focusing on key topics discussed, decisions made, and any user preferences expressed. Output a single paragraph summary.",
			},
			{
				Role:    "user",
				Content: conversationText,
			},
		}

		model := deps.DeepModel
		summary, err := deps.Engine.Chat(ctx, model, summaryPrompt, nil)
		if err != nil {
			return mcpError(fmt.Sprintf("summarization failed: %v", err)), nil
		}

		// Store summary as a context doc.
		docID := uuid.New().String()
		doc := storage.ContextDoc{
			ID:        docID,
			Title:     fmt.Sprintf("Session summary %s", time.Now().UTC().Format("2006-01-02 15:04")),
			Content:   summary,
			Source:    "session_summary",
			Tags:      `["session_summary"]`,
			CreatedAt: time.Now().UTC(),
		}
		if err := deps.Store.SaveContextDoc(doc); err != nil {
			return mcpError(fmt.Sprintf("summary generated but failed to save: %v", err)), nil
		}

		// Enqueue for embedding.
		payload, err := json.Marshal(map[string]string{"context_doc_id": docID})
		if err != nil {
			return mcpError(fmt.Sprintf("failed to marshal enrichment payload: %v", err)), nil
		}
		job := storage.Job{
			ID:          uuid.New().String(),
			Type:        "ingest_enrich",
			PayloadJSON: string(payload),
		}
		if err := deps.Store.EnqueueJob(job); err != nil {
			return mcpError(fmt.Sprintf("summary saved but failed to queue enrichment: %v", err)), nil
		}

		return mcpText(summary), nil
	}
}

func mcpResourceProfile(deps MCPDeps) server.ResourceHandlerFunc {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		p, err := deps.Profile.GetProfile()
		if err != nil {
			return nil, fmt.Errorf("failed to get profile: %w", err)
		}

		b, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal profile: %w", err)
		}

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(b),
			},
		}, nil
	}
}

func mcpResourceRecent(deps MCPDeps) server.ResourceHandlerFunc {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		interactions, err := deps.Store.GetRecentInteractions(10)
		if err != nil {
			return nil, fmt.Errorf("failed to get recent interactions: %w", err)
		}

		type interactionSummary struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			Query     string `json:"query"`
		}

		summaries := make([]interactionSummary, len(interactions))
		for i, ix := range interactions {
			query := ix.UserQuery
			if utf8.RuneCountInString(query) > 200 {
				runes := []rune(query)
				query = string(runes[:200]) + "..."
			}
			summaries[i] = interactionSummary{
				ID:        ix.ID,
				CreatedAt: ix.CreatedAt.Format(time.RFC3339),
				Query:     query,
			}
		}

		b, err := json.Marshal(summaries)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal interactions: %w", err)
		}

		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      req.Params.URI,
				MIMEType: "application/json",
				Text:     string(b),
			},
		}, nil
	}
}

func mcpText(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: text},
		},
	}
}

func mcpError(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}
