package main

import (
	"bytes"
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

func newAPIClient() (*apiClient, error) {
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

func (c *apiClient) do(method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshalling request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
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

func (c *apiClient) get(path string) (*http.Response, error) {
	return c.do("GET", path, nil)
}

func (c *apiClient) post(path string, body any) (*http.Response, error) {
	return c.do("POST", path, body)
}

func (c *apiClient) patch(path string, body any) (*http.Response, error) {
	return c.do("PATCH", path, body)
}

func (c *apiClient) delete(path string) (*http.Response, error) {
	return c.do("DELETE", path, nil)
}

func decodeJSON(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}
