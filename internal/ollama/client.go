package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message represents a chat message in the Ollama API format.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Schema describes the expected JSON output structure for structured chat responses.
type Schema struct {
	Type       string                    `json:"type"`
	Properties map[string]SchemaProperty `json:"properties"`
	Required   []string                  `json:"required,omitempty"`
}

// SchemaProperty describes a single field within a Schema.
type SchemaProperty struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// Client communicates with a local Ollama instance over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// New creates a Client targeting the given Ollama base URL.
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 0,
		},
	}
}

// tagsResponse mirrors the JSON returned by GET /api/tags.
type tagsResponse struct {
	Models []modelEntry `json:"models"`
}

type modelEntry struct {
	Name string `json:"name"`
}

// IsRunning returns true if the Ollama server responds to GET /api/tags with 200.
func (c *Client) IsRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ListModels returns the names of all models available in the local Ollama instance.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting model list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var tags tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	names := make([]string, len(tags.Models))
	for i, m := range tags.Models {
		names[i] = m.Name
	}
	return names, nil
}

// HasModel reports whether the given model name is present locally.
func (c *Client) HasModel(ctx context.Context, name string) bool {
	models, err := c.ListModels(ctx)
	if err != nil {
		return false
	}
	for _, m := range models {
		// Ollama may return "phi3.5:latest" — match without tag suffix.
		if m == name || strings.HasPrefix(m, name+":") {
			return true
		}
	}
	return false
}

// pullRequest is the JSON body for POST /api/pull.
type pullRequest struct {
	Name   string `json:"name"`
	Stream bool   `json:"stream"`
}

// PullProgress is one line of the streamed pull response.
type PullProgress struct {
	Status    string `json:"status"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
}

// PullModel downloads a model, reading the streamed progress to completion.
// The optional progress callback receives each progress line; pass nil to ignore.
func (c *Client) PullModel(ctx context.Context, name string, onProgress func(PullProgress)) error {
	body, err := json.Marshal(pullRequest{Name: name, Stream: true})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("pulling model %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pull %s: unexpected status %d", name, resp.StatusCode)
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var p PullProgress
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("reading pull progress: %w", err)
		}
		if onProgress != nil {
			onProgress(p)
		}
	}

	return nil
}

// chatRequest is the JSON body for POST /api/chat.
type chatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Format   any       `json:"format,omitempty"`
}

// chatResponse is the JSON returned by POST /api/chat (non-streaming).
type chatResponse struct {
	Message Message `json:"message"`
}

// Chat sends messages to the given model and returns the assistant's response.
// When jsonSchema is non-nil, format is set to "json" to request structured output.
func (c *Client) Chat(ctx context.Context, model string, messages []Message, jsonSchema *Schema) (string, error) {
	cr := chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   false,
	}
	if jsonSchema != nil {
		cr.Format = jsonSchema
	}

	body, err := json.Marshal(cr)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating chat request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("chat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chat: unexpected status %d", resp.StatusCode)
	}

	var result chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding chat response: %w", err)
	}

	return result.Message.Content, nil
}

// embedRequest is the JSON body for POST /api/embed.
type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

// embedResponse is the JSON returned by POST /api/embed.
type embedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// Embed returns the embedding vector for the given text using the specified model.
func (c *Client) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	body, err := json.Marshal(embedRequest{Model: model, Input: text})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed: unexpected status %d", resp.StatusCode)
	}

	var result embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding embed response: %w", err)
	}

	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("embed: empty embeddings array")
	}
	return result.Embeddings[0], nil
}
