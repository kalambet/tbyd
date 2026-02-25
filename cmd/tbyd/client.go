package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kalambet/tbyd/internal/config"
)

type apiClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

var newAPIClient = func() (*apiClient, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}

	token, err := config.GetAPIToken(config.NewKeychain())
	if err != nil {
		return nil, fmt.Errorf("getting API token: %w", err)
	}

	return &apiClient{
		baseURL:    fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port),
		token:      token,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *apiClient) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshalling request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("server not reachable — is tbyd running? (%w)", err)
	}
	return resp, nil
}

func (c *apiClient) get(ctx context.Context, path string) (*http.Response, error) {
	return c.do(ctx, "GET", path, nil)
}

func (c *apiClient) post(ctx context.Context, path string, body any) (*http.Response, error) {
	return c.do(ctx, "POST", path, body)
}

func (c *apiClient) patch(ctx context.Context, path string, body any) (*http.Response, error) {
	return c.do(ctx, "PATCH", path, body)
}

func (c *apiClient) delete(ctx context.Context, path string) (*http.Response, error) {
	return c.do(ctx, "DELETE", path, nil)
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("server returned %d (failed to read body: %w)", resp.StatusCode, err)
		}
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
