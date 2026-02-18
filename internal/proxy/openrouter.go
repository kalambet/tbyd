package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL      = "https://openrouter.ai/api/v1"
	defaultTimeout      = 60 * time.Second
	streamingTimeout    = 300 * time.Second
	maxRetries          = 3
	initialBackoff      = 500 * time.Millisecond
)

// Client communicates with the OpenRouter API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	referer    string
	title      string
}

// NewClient creates an OpenRouter client with the given API key.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: defaultBaseURL,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
		referer: "https://github.com/kalambet/tbyd",
		title:   "tbyd",
	}
}

// NewClientWithBaseURL creates a client pointing at a custom base URL (for testing).
func NewClientWithBaseURL(apiKey, baseURL string) *Client {
	c := NewClient(apiKey)
	c.baseURL = strings.TrimRight(baseURL, "/")
	return c
}

// Chat sends a chat completion request and returns the response body as a
// ReadCloser. For streaming requests the body contains SSE events; the caller
// is responsible for closing it. For non-streaming requests the body contains
// the complete JSON response.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (io.ReadCloser, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	timeout := defaultTimeout
	if req.Stream {
		timeout = streamingTimeout
	}

	var lastErr error
	for attempt := range maxRetries {
		rc, err := c.doChat(ctx, body, timeout)
		if err == nil {
			return rc, nil
		}

		if !isRateLimit(err) {
			return nil, err
		}

		lastErr = err
		if attempt < maxRetries-1 {
			backoff := time.Duration(float64(initialBackoff) * math.Pow(2, float64(attempt)))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}

	return nil, fmt.Errorf("rate limited after %d retries: %w", maxRetries, lastErr)
}

// rateLimitError is returned on HTTP 429.
type rateLimitError struct {
	status int
}

func (e *rateLimitError) Error() string {
	return fmt.Sprintf("rate limited (HTTP %d)", e.status)
}

func isRateLimit(err error) bool {
	_, ok := err.(*rateLimitError)
	return ok
}

func (c *Client) doChat(ctx context.Context, body []byte, timeout time.Duration) (io.ReadCloser, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(httpReq)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		resp.Body.Close()
		cancel()
		return nil, &rateLimitError{status: resp.StatusCode}
	}

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(respBody))
	}

	// Wrap the body so the timeout context cancel is called when the caller closes it.
	return &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}, nil
}

// cancelOnClose wraps a ReadCloser and cancels a context on Close.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// ListModels returns the list of available models from OpenRouter.
func (c *Client) ListModels(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var list ModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("decoding models: %w", err)
	}

	if list.Data == nil {
		return []Model{}, nil
	}
	return list.Data, nil
}

func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("HTTP-Referer", c.referer)
	req.Header.Set("X-Title", c.title)
}
