package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

type searchTool struct {
	db *db.DB
}

func Search(database *db.DB) agent.Tool { return searchTool{db: database} }

func (searchTool) Name() string { return "search" }
func (searchTool) Description() string {
	return "Search the web via a SearXNG instance. Returns titles, URLs, and snippets."
}
func (searchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": prop("string", "The search query"),
			"limit": prop("integer", "Number of results to return (default 10, max 20)"),
		},
		"required": []string{"query"},
	}
}

func (t searchTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	query := strArg(args, "query")
	if query == "" {
		return agent.ToolResult{Content: "error: query is required", IsError: true}
	}

	limit := intArg(args, "limit", 10)
	if limit < 1 {
		limit = 10
	}
	if limit > 20 {
		limit = 20
	}

	baseURL, err := t.db.Config.Get("searxng_url")
	if err != nil || baseURL == "" {
		return agent.ToolResult{
			Content: "searxng_url is not configured. Ask the user for their SearXNG URL, then set it with: config set searxng_url <value>",
			IsError: true,
		}
	}

	searchURL := buildSearchURL(baseURL, query)

	body, err := executeSearch(ctx.Ctx, searchURL)
	if err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}
	}

	out, err := formatResults(query, body, limit)
	if err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}
	}
	return agent.ToolResult{Content: out}
}

func buildSearchURL(baseURL, query string) string {
	return fmt.Sprintf("%s/search?q=%s&format=json&categories=general",
		strings.TrimRight(baseURL, "/"),
		url.QueryEscape(query),
	)
}

func executeSearch(ctx context.Context, searchURL string) ([]byte, error) {
	httpCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error building request: %v", err)
	}
	req.Header.Set("User-Agent", "cairo/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error fetching search results: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("search request failed: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response: %v", err)
	}
	return body, nil
}

func formatResults(query string, body []byte, limit int) (string, error) {
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("error parsing response: %v", err)
	}

	if len(parsed.Results) == 0 {
		return "No results found.", nil
	}

	results := parsed.Results
	if len(results) > limit {
		results = results[:limit]
	}

	var sb strings.Builder
	for i, r := range results {
		fmt.Fprintf(&sb, "%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Content)
	}

	out := sb.String()
	const maxBytes = 8000
	if len(out) > maxBytes {
		out = out[:maxBytes] + "\n[truncated]"
	}

	sanitized, err := sanitizeExternalContent(out)
	if err != nil {
		return "", fmt.Errorf("error sanitizing results: %v", err)
	}
	return fmt.Sprintf("<external-content source=%q>\n%s\n</external-content>", query, sanitized), nil
}
