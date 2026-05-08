package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/learn"
)

// learnTool is the user-facing entry point to the learn-about feature: an
// intentional, project-namespaced index of files with small-model summaries
// plus summary embeddings. learn is the canonical path for discovering and
// mapping a project's codebase.
type learnTool struct {
	db    *db.DB
	embed *EmbedClient
}

func Learn(database *db.DB, embed *EmbedClient) agent.Tool {
	return learnTool{db: database, embed: embed}
}

func (learnTool) Name() string { return "learn" }
func (learnTool) Description() string {
	return `Build and query the per-project map of summarized files. Searches file-summary embeddings within a named project. Use this for codebase questions over a known project — it is the recommended path for semantic code search.
Actions:
- add: async — returns immediately; the index is built in a background subprocess. Use
  action=status to poll for progress; the threads panel also shows a live bar. Args: path
  (required), project (optional — defaults to basename of path), summary_model (optional override).
- search: semantic-search a project's file summaries. Args: project (required), query (required),
  limit (optional, default 10).
- list: list known projects with file counts and last-updated time.
- describe: get a project's auto-generated description. Args: project (required).
- forget: delete a project and all its indexed files. Args: project (required).
- status: show how the latest add task is progressing. Args: project (optional).`
}

func (learnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":        propEnum("Operation to perform.", []string{"add", "search", "list", "describe", "forget", "status"}),
			"path":          prop("string", "Directory to learn about — required for add."),
			"project":       prop("string", "Project name. Required for search/describe/forget; optional for add (defaults to path basename)."),
			"summary_model": prop("string", "Override the summary model for this add (optional)."),
			"query":         prop("string", "Natural-language query — required for search."),
			"limit":         prop("integer", "Max results for search (default 10)."),
		},
		"required": []string{"action"},
	}
}

func (t learnTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	// Per-action discipline tiers:
	//   list, search, describe, status → tier 1 (readonly): observable, no state change
	//   add → tier 3 (full): spawns a background subprocess and writes to DB
	//   forget → tier 3 (full): deletes indexed project data
	action := strArg(args, "action")
	switch action {
	case "add", "forget":
		if r, refused := checkDiscipline(ctx, "learn", action, 3); refused {
			return r
		}
	}
	return DispatchAction(args, "learn", map[string]func() agent.ToolResult{
		"add":      func() agent.ToolResult { return t.doAdd(args) },
		"search":   func() agent.ToolResult { return t.doSearch(args) },
		"list":     func() agent.ToolResult { return t.doList() },
		"describe": func() agent.ToolResult { return t.doDescribe(args) },
		"forget":   func() agent.ToolResult { return t.doForget(args) },
		"status":   func() agent.ToolResult { return t.doStatus(args) },
	})
}

// doAdd creates a placeholder task row for progress tracking and spawns a
// `cairo learn -task=N -background -path=... -project=...` subprocess. The
// subprocess runs the indexer; the parent session keeps going.
func (t learnTool) doAdd(args map[string]any) agent.ToolResult {
	path := strArg(args, "path")
	if path == "" {
		return agent.ToolResult{Content: "error: path is required for add", IsError: true}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	info, err := os.Stat(abs)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	if !info.IsDir() {
		return agent.ToolResult{Content: fmt.Sprintf("error: %s is not a directory", abs), IsError: true}
	}

	project := strArg(args, "project")
	if project == "" {
		project = filepath.Base(abs)
	}

	res, err := learn.SpawnBackground(learn.SpawnRequest{
		DB:           t.db,
		Project:      project,
		Root:         abs,
		SummaryModel: strArg(args, "summary_model"),
	}, detached)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}

	return agent.ToolResult{
		Content: fmt.Sprintf("learn add started (task %d, pid %d) — project %q from %s\n"+
			"watch progress in the threads panel (Ctrl+T) or via learn(action=\"status\", project=%q).",
			res.TaskID, res.PID, project, abs, project),
		Details: map[string]any{
			"task_id": res.TaskID,
			"pid":     res.PID,
			"project": project,
			"root":    abs,
		},
	}
}

func (t learnTool) doSearch(args map[string]any) agent.ToolResult {
	project := strArg(args, "project")
	query := strArg(args, "query")
	if project == "" || query == "" {
		return agent.ToolResult{Content: "error: project and query are required for search", IsError: true}
	}
	limit := intArg(args, "limit", 10)
	if limit <= 0 {
		limit = 10
	}

	if t.embed == nil || t.embed.Embedder == nil || t.embed.Model == "" {
		return agent.ToolResult{Content: "error: embed_model not configured — run: cairo config set embed_model <model>", IsError: true}
	}
	// Resolve the code embed model — the indexer uses embed_model_code (with
	// fallback to embed_model). Searching with the prose model would silently
	// return no results when the two differ, since cross-model rows are
	// filtered out at scan time.
	codeModel, err := db.ResolveCodeEmbedModel(t.db)
	if err != nil || codeModel == "" {
		codeModel = t.embed.Model
	}
	vec, err := t.embed.Embedder.Embed(context.Background(), codeModel, query)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error embedding query: %v", err), IsError: true}
	}
	if len(vec) == 0 {
		return agent.ToolResult{Content: "error: empty embedding", IsError: true}
	}

	results := mergeLearnResults(t.db, project, query, vec, codeModel, limit)
	if len(results) == 0 {
		return agent.ToolResult{Content: fmt.Sprintf("no results in project %q (run learn add first?)", project)}
	}
	var sb strings.Builder
	// Surface the project root so the caller can resolve rel_path → absolute
	// path without an extra learn(action="list") round-trip.
	if proj, err := t.db.Projects.Get(project); err == nil && proj != nil && proj.RootPath != "" {
		fmt.Fprintf(&sb, "project %q · root: %s\n\n", project, proj.RootPath)
	}
	for i, r := range results {
		if r.IsChunk {
			fmt.Fprintf(&sb, "[%.3f] %s:%d  (%s)  [symbol: %s]\n  %s\n",
				r.Score, r.RelPath, r.StartLine, r.Label, r.Name, r.Snippet)
		} else {
			fmt.Fprintf(&sb, "[%.3f] %s  (%s, %d B)\n  %s\n",
				r.Score, r.RelPath, r.FileType, r.Bytes, r.Summary)
		}
		if i < len(results)-1 {
			sb.WriteByte('\n')
		}
	}
	return agent.ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
}

// learnResult is a unified result from file-summary or chunk search.
type learnResult struct {
	Score     float32
	Embedding []float32 // used for MMR dedup; cleared before returning to callers
	RelPath   string
	IsChunk   bool
	// File-summary fields
	FileType string
	Bytes    int
	Summary  string
	// Chunk fields
	StartLine int
	Label     string
	Name      string
	Snippet   string // first 200 chars of chunk content
}

// mergeLearnResults queries both indexed_files summaries and indexed_chunks,
// merges by score descending, and returns the top-k unified results.
// Chunk results from the same file are de-duplicated to the best-scoring chunk.
//
// The query string drives a symbol-name boost: when a chunk's Name field
// matches the query (case-insensitive), its score is boosted so symbol-name
// queries surface the named chunk over the file summary that just mentions it.
// Pass "" to disable the boost.
func mergeLearnResults(database *db.DB, project, query string, vec []float32, model string, k int) []learnResult {
	var combined []learnResult

	// File-summary results.
	files, fileScores, err := database.IndexedFiles.SearchSummaries(project, vec, model, k)
	if err == nil {
		for i, f := range files {
			combined = append(combined, learnResult{
				Score:     fileScores[i],
				Embedding: f.Embedding,
				RelPath:   f.RelPath,
				IsChunk:   false,
				FileType:  f.FileType,
				Bytes:     f.Bytes,
				Summary:   f.Summary,
			})
		}
	}

	// Chunk results — de-duplicate to best chunk per file.
	chunkResults, _, err := database.Chunks.Search(project, vec, model, k)
	if err == nil {
		// Track files already represented by a chunk result to avoid duplicates.
		seenChunkFile := make(map[string]bool)
		queryLower := strings.ToLower(strings.TrimSpace(query))
		for _, cr := range chunkResults {
			if seenChunkFile[cr.RelPath] {
				continue
			}
			seenChunkFile[cr.RelPath] = true
			// Use the best chunk (first in sorted order from ChunkQ.Search).
			if len(cr.Chunks) == 0 {
				continue
			}
			best := cr.Chunks[0]
			snippet := best.Content
			if len(snippet) > 200 {
				snippet = snippet[:200] + "…"
			}
			score := cr.Score
			// Symbol-name boost: when the query matches the chunk's Name field,
			// elevate the chunk's score so it ranks above file summaries that
			// merely mention the symbol. Exact match → near-ceiling; substring
			// match → multiplicative bump.
			if queryLower != "" && best.Name != "" {
				nameLower := strings.ToLower(best.Name)
				if nameLower == queryLower {
					// Exact symbol-name match — push above any cosine score.
					// 1.5 is intentionally > 1.0 so this beats even perfect
					// cosine matches on file summaries.
					score = 1.5
				} else if strings.Contains(nameLower, queryLower) || strings.Contains(queryLower, nameLower) {
					score *= 1.3
				}
			}
			combined = append(combined, learnResult{
				Score:     score,
				Embedding: best.Embedding,
				RelPath:   cr.RelPath,
				IsChunk:   true,
				StartLine: best.StartLine,
				Label:     best.Label,
				Name:      best.Name,
				Snippet:   snippet,
			})
		}
	}

	// MMR reranking: diversify results before capping at k.
	scored := make([]db.ScoredEmbedding, len(combined))
	for i, r := range combined {
		scored[i] = db.ScoredEmbedding{Score: r.Score, Embedding: r.Embedding, Index: i}
	}
	selected := db.MMR(scored, k, 0.7, 0.92)
	out := make([]learnResult, len(selected))
	for i, idx := range selected {
		out[i] = combined[idx]
		out[i].Embedding = nil // don't leak storage to callers
	}
	return out
}

func (t learnTool) doList() agent.ToolResult {
	projects, err := t.db.Projects.List()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	if len(projects) == 0 {
		return agent.ToolResult{Content: "no projects yet — run learn(action=\"add\", path=\"...\") to index one"}
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].LastUpdated.After(projects[j].LastUpdated) })
	var sb strings.Builder
	for _, p := range projects {
		fmt.Fprintf(&sb, "%-24s %4d files  · last updated %s\n  root: %s\n",
			p.Name, p.FileCount, p.LastUpdated.Format("2006-01-02 15:04"), p.RootPath)
		if p.Description != "" {
			fmt.Fprintf(&sb, "  %s\n", p.Description)
		}
	}
	return agent.ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
}

func (t learnTool) doDescribe(args map[string]any) agent.ToolResult {
	project := strArg(args, "project")
	if project == "" {
		return agent.ToolResult{Content: "error: project is required for describe", IsError: true}
	}
	p, err := t.db.Projects.Get(project)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: project %q not found", project), IsError: true}
	}
	desc := p.Description
	if desc == "" {
		desc = "(no description yet — re-run learn(action=\"add\", path=...) to regenerate)"
	}
	return agent.ToolResult{
		Content: fmt.Sprintf("%s\n\nroot:         %s\nfile count:   %d\nindexed at:   %s\nlast update:  %s\n\n%s",
			project, p.RootPath, p.FileCount,
			p.IndexedAt.Format("2006-01-02 15:04"),
			p.LastUpdated.Format("2006-01-02 15:04"),
			desc),
	}
}

func (t learnTool) doForget(args map[string]any) agent.ToolResult {
	project := strArg(args, "project")
	if project == "" {
		return agent.ToolResult{Content: "error: project is required for forget", IsError: true}
	}
	if _, err := t.db.Projects.Get(project); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: project %q not found", project), IsError: true}
	}
	if err := t.db.Projects.Delete(project); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("forgotten: project %q (and its indexed files)", project)}
}

func (t learnTool) doStatus(args map[string]any) agent.ToolResult {
	project := strArg(args, "project")
	tasks, err := t.db.Tasks.RunningWithProgress()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	var matching []string
	for _, tk := range tasks {
		if project != "" && !strings.Contains(tk.ProgressLabel, project) {
			continue
		}
		pct := 0
		if tk.ProgressTotal > 0 {
			pct = tk.ProgressCurrent * 100 / tk.ProgressTotal
		}
		matching = append(matching, fmt.Sprintf("task %d  [%3d%%]  %s · %s",
			tk.ID, pct, tk.ProgressLabel, tk.ProgressDetail))
	}
	if len(matching) == 0 {
		return agent.ToolResult{Content: "no learn tasks running"}
	}
	return agent.ToolResult{Content: strings.Join(matching, "\n")}
}
