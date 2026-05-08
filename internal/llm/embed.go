package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type embedRequest struct {
	Model          string `json:"model"`
	Input          string `json:"input"`
	EncodingFormat string `json:"encoding_format"`
}

// Embed returns a vector embedding for the given text.
// Returns nil (no error) if the embed model is not available — callers
// should treat nil embeddings as "no semantic search for this entry."
func (c *Client) Embed(ctx context.Context, model, text string) ([]float32, error) {
	ctx, cancel := context.WithTimeout(ctx, timeoutEmbed)
	defer cancel()
	body, err := json.Marshal(embedRequest{Model: model, Input: text, EncodingFormat: "float"})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	resp, err := c.shortHTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, parseOpenAIError(resp.StatusCode, respBody)
	}

	var r struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &r); err != nil {
		return nil, fmt.Errorf("embed decode: %w", err)
	}
	if len(r.Data) == 0 {
		return nil, fmt.Errorf("embed: empty data in response")
	}
	return r.Data[0].Embedding, nil
}
