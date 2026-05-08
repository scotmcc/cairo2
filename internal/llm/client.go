package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const DefaultURL = "http://localhost:11434"

const (
	timeoutChat      = 5 * time.Minute
	timeoutEmbed     = 60 * time.Second
	timeoutShortCall = 30 * time.Second
)

var (
	ErrUnauthorized = errors.New("unauthorized")
)

type Client struct {
	url       string
	apiKey    string
	http      *http.Client // no timeout — streaming uses request context deadline
	shortHTTP *http.Client // 15s timeout for health checks and non-streaming calls
}

func New(url, apiKey string) *Client {
	if url == "" {
		url = DefaultURL
	}
	return &Client{
		url:    url,
		apiKey: apiKey,
		http:   &http.Client{},
		shortHTTP: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// newRequest builds an HTTP request with Content-Type and Bearer auth headers.
// Auth header is only added when apiKey is non-empty.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	return req, nil
}

// parseOpenAIError formats a non-2xx HTTP response as an error.
// Tries to decode the OpenAI error envelope; falls back to raw body.
// Wraps 401/403 with ErrUnauthorized so callers can detect auth failures.
func parseOpenAIError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	var err error
	if jerr := json.Unmarshal(body, &envelope); jerr == nil && envelope.Error.Message != "" {
		code := fmt.Sprintf("%v", envelope.Error.Code)
		err = fmt.Errorf("openai api error (HTTP %d): %s [type=%s, code=%s]",
			status, envelope.Error.Message, envelope.Error.Type, code)
	} else {
		err = fmt.Errorf("openai api error (HTTP %d): %s", status, strings.TrimSpace(string(body)))
	}
	if status == 401 || status == 403 {
		err = fmt.Errorf("%w: %w", err, ErrUnauthorized)
	}
	return err
}

func (c *Client) Ping() error {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutShortCall)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return fmt.Errorf("llm unreachable at %s: %w", c.url, err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return parseOpenAIError(resp.StatusCode, body)
	}
	return nil
}

// ListModels returns the model IDs available on the server.
func (c *Client) ListModels() ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeoutShortCall)
	defer cancel()
	req, err := c.newRequest(ctx, http.MethodGet, "/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("list models read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseOpenAIError(resp.StatusCode, body)
	}

	var respBody struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &respBody); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	names := make([]string, 0, len(respBody.Data))
	for _, m := range respBody.Data {
		names = append(names, m.ID)
	}
	return names, nil
}
