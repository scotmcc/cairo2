package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var (
	ErrModelNotFound = errors.New("model not found")
)

type ModelInfo struct {
	ContextLength int
}

// FetchModelInfo calls LiteLLM's /model_group/info endpoint and returns key
// model metadata. apiKey is sent as a Bearer token when non-empty.
// Returns ErrModelNotFound when the model is absent from the data array.
// A 30s timeout is applied on top of the caller's ctx.
func FetchModelInfo(ctx context.Context, baseURL, apiKey, model string) (ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutShortCall)
	defer cancel()
	url := strings.TrimRight(baseURL, "/") + "/model_group/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("build request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ModelInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return ModelInfo{}, fmt.Errorf("model_group/info HTTP %d: %w", resp.StatusCode, ErrUnauthorized)
		}
		return ModelInfo{}, fmt.Errorf("model_group/info HTTP %d", resp.StatusCode)
	}
	var result struct {
		Data []struct {
			ModelGroup     string  `json:"model_group"`
			MaxInputTokens float64 `json:"max_input_tokens"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ModelInfo{}, fmt.Errorf("decode model_group/info: %w", err)
	}
	for _, entry := range result.Data {
		if entry.ModelGroup == model {
			return ModelInfo{ContextLength: int(entry.MaxInputTokens)}, nil
		}
	}
	return ModelInfo{}, ErrModelNotFound
}
