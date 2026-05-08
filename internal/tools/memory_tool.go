package tools

// memory_tool.go — consolidated memory layer: add + search across memories, facts, summaries.
// Predecessors (memory/summary_search/fact_search/fact_promote) remain in place until
// a follow-up dispatch confirms full coverage and drops them.

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

// allowedWriteRoles is the set of roles permitted to write or delete long-term
// memories. Task-scoped roles (coder, reviewer, planner, researcher) are
// intentionally excluded to prevent transient task state from polluting the
// store. Shared between doAdd and doDelete.
var allowedWriteRoles = map[string]bool{
	"thinking_partner": true,
	"dream":            true,
	"orchestrator":     true,
}

type memoryToolConsolidated struct {
	db    *db.DB
	embed *EmbedClient
}

// MemoryTool returns the consolidated memory_tool.
func MemoryTool(database *db.DB, embed *EmbedClient) agent.Tool {
	return memoryToolConsolidated{db: database, embed: embed}
}

func (memoryToolConsolidated) Name() string { return "memory_tool" }
func (memoryToolConsolidated) Description() string {
	return `Add, search, delete, pin, or unpin entries in Cairo's memory layer.
memory_tool(action="add", content="...") writes a memory (optional: importance 0–1).
Near-duplicate detection runs before write: if a semantically similar memory already
exists (cosine similarity > memory_dedup_threshold config, default 0.85), a warning is
returned instead of writing. Pass force=true to bypass and write unconditionally.
memory_tool(action="search", query="...", scope="memories,facts,summaries", mode="hybrid", limit=10)
searches across memories, facts, and conversation summaries with semantic + FTS5 retrieval.
Use scope to restrict the source. Results include a source field indicating origin.
memory_tool(action="delete", id=<int>) soft-deletes the memory with the given ID. The row is
retained for audit but excluded from all future reads and searches.
memory_tool(action="pin", id=<int>) marks a memory as pinned — it will survive nightly auto-dump.
memory_tool(action="unpin", id=<int>) removes the pin from a memory.`
}

func (memoryToolConsolidated) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "search", "delete", "pin", "unpin"},
				"description": "Operation: add (write a memory), search (query across memory layer), delete (soft-delete by id), pin (protect from auto-dump), or unpin. Required.",
			},
			"content":    prop("string", "Memory content. Required for add."),
			"query":      prop("string", "Natural-language query. Required for search."),
			"id":         propOptional("integer", "Memory ID to soft-delete. Required for delete.", ""),
			"importance": propOptional("number", "Importance score 0–1 for add", "0.5"),
			"tags":       propOptional("string", "Comma-separated tags for add", "none"),
			"force":      propOptional("boolean", "Set true to bypass the near-duplicate dedup check", "false"),
			"mode":       propOptional("string", `Search mode: "semantic", "exact" (FTS5), or "hybrid" (default)`, "hybrid"),
			"scope":      propOptional("string", `Comma-separated sources to search: memories, facts, summaries, or "all" (default)`, "all"),
			"limit":      propOptional("integer", "Max results per source", "10"),
		},
		"required": []string{"action"},
	}
}

func (t memoryToolConsolidated) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	return DispatchAction(args, "memory_tool", map[string]func() agent.ToolResult{
		"add":    func() agent.ToolResult { return t.doAdd(args, ctx) },
		"search": func() agent.ToolResult { return t.doSearch(args) },
		"delete": func() agent.ToolResult { return t.doDelete(args, ctx) },
		"pin":    func() agent.ToolResult { return t.doPin(args, ctx) },
		"unpin":  func() agent.ToolResult { return t.doUnpin(args, ctx) },
	})
}

func (t memoryToolConsolidated) doAdd(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "memory_tool", "add", 3); refused {
		return r
	}
	if ctx != nil && ctx.Session != nil && ctx.Session.Role != "" {
		if !allowedWriteRoles[ctx.Session.Role] {
			return agent.ToolResult{Content: fmt.Sprintf(
				"memory write skipped: role %q is not permitted to write long-term memories (only thinking_partner, dream, orchestrator may write). Use action=\"search\" to read.",
				ctx.Session.Role,
			)}
		}
	}
	content := strArg(args, "content")
	if content == "" {
		return agent.ToolResult{Content: "error: content is required for add", IsError: true}
	}
	rawTags := strArg(args, "tags")
	tags := formatTags(rawTags)
	forceFlag := boolArg(args, "force")

	vec, err := t.embed.Embed(content)
	if err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("error: embed failed (%v) — memory not written. Check the embed_model config and Ollama endpoint.", err),
			IsError: true,
		}
	}
	if vec == nil {
		return agent.ToolResult{
			Content: "error: embed returned no vector — memory not written. Check the embed_model config.",
			IsError: true,
		}
	}
	embedding := vec
	embedModel := ""
	if t.embed != nil {
		embedModel = t.embed.Model
	}

	// Dedup check: compare against top candidates before writing.
	if embedding != nil && !forceFlag {
		thresholdStr, _ := t.db.Config.Get(db.KeyMemoryDedupThreshold)
		threshold := 0.85
		if thresholdStr != "" {
			if v, err := strconv.ParseFloat(thresholdStr, 64); err == nil {
				threshold = v
			}
		}
		candidates, _ := t.db.Memories.Search(embedding, embedModel, 5)
		for _, c := range candidates {
			sim := db.Cosine(embedding, c.Embedding)
			if float64(sim) > threshold {
				return agent.ToolResult{Content: fmt.Sprintf(
					"near-duplicate found (id: %d, similarity: %.2f): %s\nUse memory_tool(action=\"search\") to review before adding. Pass force=true to override.",
					c.ID, sim, truncateMemory(c.Content, 80),
				)}
			}
		}
	}

	m, err := t.db.Memories.Add(content, tags, embedModel, embedding)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: add: %v", err), IsError: true}
	}
	// importance=0 at insert; dream rates on next pass.
	// If the agent provides an explicit importance, apply it now to bypass async rating.
	if imp := floatArg(args, "importance", -1); imp >= 0 && imp <= 1 {
		_ = t.db.Memories.SetImportance(m.ID, imp)
	}
	// Fire the fact_promoted hook when the caller flagged this memory as a
	// promoted fact (per the dream agent's "promote valuable, non-duplicate
	// facts" instruction in role:dream — see seed.go). Tag-based trigger:
	// any memory written with `promoted-fact` in its tags fires the event.
	// External handlers (CAIRO_MEMORY_ID + CAIRO_TAGS in env) can react.
	if strings.Contains(rawTags, "promoted-fact") {
		role := ""
		if ctx != nil && ctx.Session != nil {
			role = ctx.Session.Role
		}
		agent.RunHooks(t.db, "fact_promoted", role, []string{
			fmt.Sprintf("CAIRO_MEMORY_ID=%d", m.ID),
			"CAIRO_TAGS=" + rawTags,
		})
	}

	suffix := ""
	if len(embedding) > 0 {
		suffix = fmt.Sprintf(" (%d-dim embedding)", len(embedding))
	}
	return agent.ToolResult{Content: fmt.Sprintf("memory saved (id: %d)%s", m.ID, suffix)}
}

func (t memoryToolConsolidated) doDelete(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "memory_tool", "delete", 3); refused {
		return r
	}
	if ctx != nil && ctx.Session != nil && ctx.Session.Role != "" {
		if !allowedWriteRoles[ctx.Session.Role] {
			return agent.ToolResult{Content: fmt.Sprintf(
				"memory delete skipped: role %q is not permitted to delete long-term memories (only thinking_partner, dream, orchestrator may delete). Use action=\"search\" to read.",
				ctx.Session.Role,
			)}
		}
	}
	id := intArg(args, "id", 0)
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for delete", IsError: true}
	}
	if err := t.db.Memories.Delete(int64(id)); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: delete: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("memory %d deleted", id)}
}

func (t memoryToolConsolidated) doPin(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "memory_tool", "pin", 3); refused {
		return r
	}
	if ctx != nil && ctx.Session != nil && ctx.Session.Role != "" {
		if !allowedWriteRoles[ctx.Session.Role] {
			return agent.ToolResult{Content: fmt.Sprintf(
				"memory pin skipped: role %q is not permitted to pin memories (only thinking_partner, dream, orchestrator may pin).",
				ctx.Session.Role,
			)}
		}
	}
	id := intArg(args, "id", 0)
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for pin", IsError: true}
	}
	if err := t.db.Memories.Pin(int64(id)); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: pin: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("memory %d pinned", id)}
}

func (t memoryToolConsolidated) doUnpin(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "memory_tool", "unpin", 3); refused {
		return r
	}
	if ctx != nil && ctx.Session != nil && ctx.Session.Role != "" {
		if !allowedWriteRoles[ctx.Session.Role] {
			return agent.ToolResult{Content: fmt.Sprintf(
				"memory unpin skipped: role %q is not permitted to unpin memories (only thinking_partner, dream, orchestrator may unpin).",
				ctx.Session.Role,
			)}
		}
	}
	id := intArg(args, "id", 0)
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for unpin", IsError: true}
	}
	if err := t.db.Memories.Unpin(int64(id)); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: unpin: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("memory %d unpinned", id)}
}

// memoryResult is a unified result row returned by the search subcommand.
type memoryResult struct {
	Source  string `json:"source"`
	ID      int64  `json:"id"`
	Content string `json:"content"`
}

type scopeFlags struct {
	memories  bool
	facts     bool
	summaries bool
}

func parseScope(raw string) (scopeFlags, error) {
	if raw == "" || raw == "all" {
		raw = "memories,facts,summaries"
	}
	var f scopeFlags
	for _, s := range strings.Split(raw, ",") {
		switch strings.TrimSpace(s) {
		case "memories":
			f.memories = true
		case "facts":
			f.facts = true
		case "summaries":
			f.summaries = true
		}
	}
	if !f.memories && !f.facts && !f.summaries {
		return f, fmt.Errorf("scope must include at least one of: memories, facts, summaries")
	}
	return f, nil
}

func (t memoryToolConsolidated) searchByScope(query string, vec []float32, mode string, limit int, scope scopeFlags) ([]memoryResult, error) {
	var results []memoryResult
	if scope.memories {
		rows, err := t.searchMemories(query, vec, mode, limit)
		if err != nil {
			return nil, fmt.Errorf("memories search: %w", err)
		}
		results = append(results, rows...)
	}
	if scope.facts {
		rows, err := t.searchFacts(query, vec, mode, limit)
		if err != nil {
			return nil, fmt.Errorf("facts search: %w", err)
		}
		results = append(results, rows...)
	}
	if scope.summaries {
		rows, err := t.searchSummaries(vec, limit)
		if err != nil {
			return nil, fmt.Errorf("summaries search: %w", err)
		}
		results = append(results, rows...)
	}
	return results, nil
}

func (t memoryToolConsolidated) doSearch(args map[string]any) agent.ToolResult {
	query := strArg(args, "query")
	if query == "" {
		return agent.ToolResult{Content: "error: query is required for search", IsError: true}
	}
	limit := intArg(args, "limit", 10)
	mode := strArg(args, "mode")
	if mode == "" {
		mode = "hybrid"
	}

	scope, err := parseScope(strArg(args, "scope"))
	if err != nil {
		return agent.ToolResult{Content: "error: " + err.Error(), IsError: true}
	}

	var vec []float32
	if mode != "exact" {
		vec, err = t.embed.Embed(query)
		if err != nil {
			return agent.ToolResult{Content: "failed to embed query — is the embed model running?", IsError: true}
		}
	}

	results, err := t.searchByScope(query, vec, mode, limit, scope)
	if err != nil {
		return agent.ToolResult{Content: "error: " + err.Error(), IsError: true}
	}

	if len(results) == 0 {
		return agent.ToolResult{Content: "no matching results found"}
	}

	// Bump retrieval weight for memory hits only (facts/summaries lack weight columns).
	var memIDs []int64
	for _, r := range results {
		if r.Source == "memories" {
			memIDs = append(memIDs, r.ID)
		}
	}
	if len(memIDs) > 0 {
		if err := t.db.Memories.BumpRetrieval(memIDs); err != nil {
			log.Printf("memory_tool: BumpRetrieval: %v", err)
		}
	}

	var b strings.Builder
	for _, r := range results {
		fmt.Fprintf(&b, "[%s:%d] %s\n", r.Source, r.ID, r.Content)
	}
	return agent.ToolResult{Content: strings.TrimSpace(b.String()), Details: results}
}

func (t memoryToolConsolidated) searchMemories(query string, vec []float32, mode string, limit int) ([]memoryResult, error) {
	var rows []*db.Memory
	switch mode {
	case "exact":
		var err error
		rows, err = t.db.Memories.SearchFTS(query, limit)
		if err != nil {
			return nil, err
		}
	case "hybrid":
		if vec != nil {
			sem, err := t.db.Memories.Search(vec, t.embed.Model, limit)
			if err != nil {
				return nil, err
			}
			rows = sem
		}
		fts, err := t.db.Memories.SearchFTS(query, limit)
		if err != nil {
			return nil, err
		}
		seen := make(map[int64]bool, len(rows))
		for _, m := range rows {
			seen[m.ID] = true
		}
		for _, m := range fts {
			if !seen[m.ID] {
				seen[m.ID] = true
				rows = append(rows, m)
			}
		}
	default: // semantic
		if vec == nil {
			return nil, nil
		}
		var err error
		rows, err = t.db.Memories.Search(vec, t.embed.Model, limit)
		if err != nil {
			return nil, err
		}
	}
	out := make([]memoryResult, 0, len(rows))
	for _, m := range rows {
		out = append(out, memoryResult{Source: "memories", ID: m.ID, Content: m.Content})
	}
	return out, nil
}

func (t memoryToolConsolidated) searchFacts(query string, vec []float32, mode string, limit int) ([]memoryResult, error) {
	if vec == nil && mode != "exact" {
		return nil, nil
	}
	// Facts only have semantic search (no FTS5); fall back gracefully for exact.
	if mode == "exact" {
		return nil, nil
	}
	facts, err := t.db.Facts.Search(vec, t.embed.Model, limit)
	if err != nil {
		return nil, err
	}
	out := make([]memoryResult, 0, len(facts))
	for _, f := range facts {
		out = append(out, memoryResult{Source: "facts", ID: f.ID, Content: f.Content})
	}
	return out, nil
}

func (t memoryToolConsolidated) searchSummaries(vec []float32, limit int) ([]memoryResult, error) {
	if vec == nil {
		return nil, nil
	}
	summaries, err := t.db.Summaries.Search(vec, t.embed.Model, limit)
	if err != nil {
		return nil, err
	}
	out := make([]memoryResult, 0, len(summaries))
	for _, s := range summaries {
		out = append(out, memoryResult{Source: "summaries", ID: s.ID, Content: s.Content})
	}
	return out, nil
}

// truncateMemory returns the first n runes of s, appending "…" if truncated.
func truncateMemory(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
