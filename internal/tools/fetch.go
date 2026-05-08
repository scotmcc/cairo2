package tools

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/learn"
	"github.com/scotmcc/cairo2/internal/llm"
)

type fetchTool struct {
	database  *db.DB
	llmClient *llm.Client
	embed     *EmbedClient
}

// Fetch returns a fetch tool wired to persist and index every page it retrieves.
// database, llmClient, and embed may be nil — missing deps simply skip the
// background ingest while the synchronous fetch still succeeds.
func Fetch(database *db.DB, llmClient *llm.Client, embed *EmbedClient) agent.Tool {
	return fetchTool{database: database, llmClient: llmClient, embed: embed}
}

func (fetchTool) Name() string { return "fetch" }
func (fetchTool) Description() string {
	return "Fetch a web page and return its content as clean markdown."
}
func (fetchTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":        prop("string", "The URL to fetch"),
			"max_length": prop("integer", "Character cap on returned content (default 8000, max 32000)"),
		},
		"required": []string{"url"},
	}
}

func (t fetchTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	rawURL := strArg(args, "url")
	if rawURL == "" {
		return agent.ToolResult{Content: "error: url is required", IsError: true}
	}

	maxLength := intArg(args, "max_length", 8000)
	if maxLength < 1 {
		maxLength = 8000
	}
	if maxLength > 32000 {
		maxLength = 32000
	}

	httpCtx, cancel := context.WithTimeout(ctx.Ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(httpCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error building request: %v", err), IsError: true}
	}
	req.Header.Set("User-Agent", "cairo/1.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error fetching URL: %v", err), IsError: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return agent.ToolResult{
			Content: fmt.Sprintf("fetch failed: HTTP %d", resp.StatusCode),
			IsError: true,
		}
	}

	htmlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error reading response body: %v", err), IsError: true}
	}

	markdown, err := htmltomarkdown.ConvertString(string(htmlBytes))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error converting HTML to markdown: %v", err), IsError: true}
	}

	if len(markdown) > maxLength {
		markdown = markdown[:maxLength] + "\n[truncated]"
	}

	sanitized, err := sanitizeExternalContent(markdown)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error sanitizing content: %v", err), IsError: true}
	}
	markdown = sanitized

	// Kick off background persist + ingest. Errors here must not fail the
	// fetch — Selene already has the content; logging is sufficient.
	t.ingestAsync(ctx.Ctx, rawURL, markdown)

	content := fmt.Sprintf("<external-content source=%q>\n%s\n</external-content>", rawURL, markdown)
	return agent.ToolResult{Content: content}
}

// ingestAsync persists the fetched page and queues a learn ingest in a
// background goroutine. All errors are logged; none surface to the caller.
// parentCtx is the agent session context — cancellation propagates into the
// goroutine so shutdown cannot race with a closed DB or deleted tempfiles.
func (t fetchTool) ingestAsync(parentCtx context.Context, rawURL, markdown string) {
	if t.database == nil || t.llmClient == nil || t.embed == nil || t.embed.Model == "" {
		return // missing deps — skip silently
	}

	summaryModel, _ := t.database.Config.Get(db.KeySummaryModel)
	if summaryModel == "" {
		return // no summary model configured — skip
	}

	go func() {
		if parentCtx.Err() != nil {
			return // already cancelled before we start
		}
		cfg := learn.Config{
			DB:           t.database,
			LLM:          t.llmClient,
			SummaryModel: summaryModel,
			EmbedModel:   t.embed.Model,
		}
		ctx, cancel := context.WithTimeout(parentCtx, 5*time.Minute)
		defer cancel()
		if err := learn.IngestURL(ctx, cfg, rawURL, markdown); err != nil {
			log.Printf("fetch: background ingest of %s failed: %v", rawURL, err)
		}
	}()
}

// sanitizeExternalContent strips known prompt-injection patterns from external content.
// Returns an error if sanitization cannot be applied (callers must fail the tool call
// rather than passing potentially-corrupted content to the model).
func sanitizeExternalContent(s string) (string, error) {
	patterns := []string{
		"ignore previous instructions",
		"ignore all instructions",
		"ignore prior instructions",
		"disregard your",
		"disregard the",
		"[system:",
		"<system>",
		"</system>",
		"[INST]",
		"[/INST]",
	}
	lower := strings.ToLower(s)
	for _, p := range patterns {
		lp := strings.ToLower(p)
		for {
			idx := strings.Index(lower, lp)
			if idx == -1 {
				break
			}
			s = s[:idx] + "[removed]" + s[idx+len(p):]
			lower = lower[:idx] + "[removed]" + lower[idx+len(p):]
		}
	}
	return s, nil
}
