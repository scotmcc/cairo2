package learn

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/llm"
)

// Config drives one indexing run. Embed and SummaryModel are mandatory;
// everything else has sensible defaults.
type Config struct {
	DB           *db.DB
	LLM          *llm.Client
	Project      string // human-friendly project name (PRIMARY KEY in projects)
	Root         string // absolute path to walk
	SummaryModel string // small model used per file
	EmbedModel   string // model used to embed the summary
	ExtraExclude []string
	// ProgressFn is called after each file is processed. Cheap callers can
	// use it to push to a UI; the indexer also writes to the tasks table
	// when TaskID is set.
	ProgressFn func(current, total int, label, detail string)
	// TaskID, when non-zero, has the indexer write progress to the tasks
	// table via DB.Tasks.SetProgress on every file.
	TaskID int64
	// ForceReembed bypasses SHA-based change detection so every file is
	// re-summarized and re-embedded regardless of whether its content has
	// changed. Set this when the embedding model changes — the new model
	// produces vectors in a different space, so all existing chunks must be
	// replaced even if the file bytes are identical.
	ForceReembed bool
}

// summaryPrompt is paired with fileSummarySchema via Ollama's structured-
// output mode. Schema pins the wire shape (one "summary" field, capped
// length); prompt teaches the model what to put in it.
const summaryPrompt = `You summarize a single source-code or docs file into one short paragraph.

Definitions:
- "summary": 1-2 sentences naming what the file does or contains. Mention the language/package for source, the topic for docs, the domain for config. Plain prose, no markdown.

Rules:
- Hard limit: 2 sentences, ~280 characters. Brevity is the point.
- Describe purpose, not line-by-line walkthrough.
- Do not include markdown, headings, code fences, or lists.`

// fileSummarySchema constrains Ollama's sampler to produce {"summary": "..."}
// directly. maxLength is advisory — Ollama may not always honor it, so the
// indexer also hard-truncates after parsing.
var fileSummarySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "1-2 sentences naming what this file does or contains. Plain prose, no markdown.",
			"maxLength":   400,
		},
	},
	"required":             []string{"summary"},
	"additionalProperties": false,
}

// fileSummaryResponse is the decoded shape we expect back. Kept in sync
// with fileSummarySchema.
type fileSummaryResponse struct {
	Summary string `json:"summary"`
}

// projectDescPrompt + projectDescSchema constrain the auto-generated
// project description to a single short paragraph suitable for `learn list`
// and the panel preview. Long architectural docs were the failure mode in
// the unconstrained version.
const projectDescPrompt = `You write one-paragraph descriptions of software projects from a list of file summaries.

Definitions:
- "description": 2-3 sentences describing what the project IS — purpose, language/stack, shape (CLI? library? service? docs?). Plain prose.

Rules:
- Hard limit: 3 sentences, ~500 characters. This is a tagline, not a manual.
- No markdown, headings, lists, or tables. No "This project ..." preamble — start with the substance.
- Do not list every file or enumerate the architecture. One paragraph, broad strokes.`

var projectDescSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"description": map[string]any{
			"type":        "string",
			"description": "2-3 sentences describing what this project IS. Plain prose.",
			"maxLength":   600,
		},
	},
	"required":             []string{"description"},
	"additionalProperties": false,
}

type projectDescResponse struct {
	Description string `json:"description"`
}

// Hard caps applied after parsing — belt and suspenders against models
// that don't honor schema maxLength.
const (
	maxFileSummaryChars = 600
	maxProjectDescChars = 800
)

// Stats reports what an indexing run did.
type Stats struct {
	Total    int
	Indexed  int
	Skipped  int
	Errors   int
	Duration time.Duration
}

// Run walks the project root, summarizes + embeds each file, and writes
// rows into the projects / indexed_files tables. Idempotent: rerunning on
// the same project skips files whose SHA-256 hasn't changed.
//
// Reports progress to cfg.ProgressFn (if set) and to the tasks table (if
// cfg.TaskID > 0). Honors ctx for cancellation between files.
func Run(ctx context.Context, cfg Config) (*Stats, error) {
	if cfg.DB == nil || cfg.LLM == nil {
		return nil, fmt.Errorf("learn: DB and LLM are required")
	}
	if cfg.Project == "" || cfg.Root == "" {
		return nil, fmt.Errorf("learn: project and root are required")
	}
	if cfg.SummaryModel == "" {
		return nil, fmt.Errorf("learn: summary_model is required")
	}
	if cfg.EmbedModel == "" {
		return nil, fmt.Errorf("learn: embed_model is required")
	}

	// Apply the max-chunk-token cap from config, falling back to the package
	// default (400) when the key is absent or unparseable.
	if capStr, _ := cfg.DB.Config.Get(db.KeyLearnMaxChunkTokens); capStr != "" {
		if n, err := strconv.Atoi(capStr); err == nil && n > 0 {
			MaxChunkTokens = n
		}
	}

	start := time.Now()

	// Ensure the project row exists before we start writing files (the
	// foreign-key constraint requires it). Description stays empty until
	// we generate one at the end of the run.
	if err := cfg.DB.Projects.Upsert(cfg.Project, cfg.Root, ""); err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}

	candidates, err := Walk(cfg.Root, cfg.ExtraExclude)
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}

	stats := &Stats{Total: len(candidates)}
	progress := func(label, detail string) {
		current := stats.Indexed + stats.Skipped + stats.Errors
		if cfg.ProgressFn != nil {
			cfg.ProgressFn(current, stats.Total, label, detail)
		}
		if cfg.TaskID > 0 {
			_ = cfg.DB.Tasks.SetProgress(cfg.TaskID, current, stats.Total, label, detail)
		}
	}
	label := "indexing " + cfg.Project
	progress(label, "")

	// Per-file loop. Cancellation checked between files (mid-file work
	// would just waste a partially-summarized result).
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if err := indexOne(ctx, cfg, c, stats); err != nil {
			stats.Errors++
		}
		progress(label, c.RelPath)
	}

	// Stale-file deletion: remove indexed_files rows for files no longer
	// present on disk. Chunks cascade via ON DELETE CASCADE on file_id.
	// Build the set of rel-paths we walked this run.
	//
	// Safety guard: skip stale-deletion entirely if the walk yielded zero
	// candidates. An empty present-set would wipe every indexed_files row
	// for the project — irrecoverable without re-indexing. Real causes
	// (over-aggressive --exclude, wrong path, transient FS issue) shouldn't
	// destroy the existing index.
	present := make([]string, 0, len(candidates))
	for _, c := range candidates {
		present = append(present, c.RelPath)
	}
	if len(present) == 0 {
		log.Printf("warn: learn project=%s walked 0 candidates — skipping stale-deletion to protect existing index", cfg.Project)
	} else if deleted, err := cfg.DB.IndexedFiles.DeleteMissing(cfg.Project, present); err != nil {
		log.Printf("warn: learn stale-deletion project=%s: %v", cfg.Project, err)
	} else if deleted > 0 {
		log.Printf("learn: removed %d stale file(s) from %s index", deleted, cfg.Project)
	}

	// Recount file_count after the batch — saves N redundant updates
	// during the loop.
	if err := cfg.DB.Projects.RecountFiles(cfg.Project); err != nil {
		log.Printf("warn: learn recount project=%s: %v", cfg.Project, err)
	}

	// Generate a project description from accumulated summaries. Best
	// effort — a failure here doesn't fail the run, the user can ask
	// `learn describe` to retry.
	if desc, err := generateProjectDescription(ctx, cfg); err != nil {
		log.Printf("warn: learn project-description project=%s: %v", cfg.Project, err)
	} else if desc != "" {
		if err := cfg.DB.Projects.SetDescription(cfg.Project, desc); err != nil {
			log.Printf("warn: learn set-description project=%s: %v", cfg.Project, err)
		}
	}

	stats.Duration = time.Since(start)
	progress(fmt.Sprintf("done · %s", cfg.Project),
		fmt.Sprintf("%d indexed · %d skipped · %d errors · %s",
			stats.Indexed, stats.Skipped, stats.Errors, stats.Duration.Round(time.Second)))
	agent.RunHooks(cfg.DB, "learn_indexed", cfg.Project, []string{
		"CAIRO_PROJECT=" + cfg.Project,
		"CAIRO_ROOT=" + cfg.Root,
		"CAIRO_INDEXED=" + strconv.Itoa(stats.Indexed),
		"CAIRO_SKIPPED=" + strconv.Itoa(stats.Skipped),
		"CAIRO_ERRORS=" + strconv.Itoa(stats.Errors),
		"CAIRO_DURATION_SEC=" + strconv.FormatFloat(stats.Duration.Seconds(), 'f', 2, 64),
	})
	return stats, nil
}

// indexOne handles a single candidate. SHA-checks first to skip unchanged
// files; otherwise reads, summarizes, embeds, and upserts.
// When cfg.ForceReembed is true the SHA check is bypassed so every file is
// re-indexed even if its content hasn't changed — required after an
// embed_model_code change to migrate all chunks to the new vector space.
func indexOne(ctx context.Context, cfg Config, c Candidate, stats *Stats) error {
	sha, err := SHA256(c.AbsPath)
	if err != nil {
		return err
	}
	if !cfg.ForceReembed {
		prev, _ := cfg.DB.IndexedFiles.GetSHA(cfg.Project, c.RelPath)
		if prev != "" && prev == sha {
			stats.Skipped++
			return nil
		}
	}

	data, err := os.ReadFile(c.AbsPath)
	if err != nil {
		return err
	}
	content := string(data)

	summary, err := summarizeFile(ctx, cfg, c, content)
	if err != nil {
		return fmt.Errorf("summarize %s: %w", c.RelPath, err)
	}

	// Embed the augmented summary so retrieval matches on filename + project
	// context, not just summary prose.
	augmented := fmt.Sprintf("project=%s file=%s · %s", cfg.Project, c.RelPath, summary)
	vec, err := cfg.LLM.Embed(ctx, cfg.EmbedModel, augmented)
	if err != nil {
		return fmt.Errorf("embed %s: %w", c.RelPath, err)
	}

	// Write file row without SHA so a chunk failure forces a retry next run
	// rather than leaving orphaned chunks forever.
	fileID, err := cfg.DB.IndexedFiles.Upsert(&db.IndexedFile{
		Project:    cfg.Project,
		RelPath:    c.RelPath,
		FileType:   c.Type,
		Bytes:      c.Bytes,
		SHA256:     "",
		Summary:    summary,
		Embedding:  vec,
		EmbedModel: cfg.EmbedModel,
	})
	if err != nil {
		return err
	}

	// Chunk-level indexing: all-or-nothing per file. On failure the SHA
	// stays empty so the next indexing run retries the file.
	if err := indexChunks(ctx, cfg, c, content, fileID); err != nil {
		log.Printf("warn: learn chunk-indexing rel_path=%s: %v — file will retry next run", c.RelPath, err)
		return err
	}

	// Chunks complete — commit SHA so the next run skips this unchanged file.
	if _, err := cfg.DB.IndexedFiles.Upsert(&db.IndexedFile{
		Project:    cfg.Project,
		RelPath:    c.RelPath,
		FileType:   c.Type,
		Bytes:      c.Bytes,
		SHA256:     sha,
		Summary:    summary,
		Embedding:  vec,
		EmbedModel: cfg.EmbedModel,
	}); err != nil {
		log.Printf("warn: learn commit-sha rel_path=%s: %v", c.RelPath, err)
	}

	stats.Indexed++
	return nil
}

// indexChunks extracts semantic chunks from content, embeds each one using
// AugmentedText, and writes rows to indexed_chunks. Replaces any existing
// chunks for the file. Uses regex-based extraction for Go and other supported
// types; falls through to whole-file for unknown types.
func indexChunks(ctx context.Context, cfg Config, c Candidate, content string, fileID int64) error {
	chunks := ChunkContent(content, c.Type)
	if len(chunks) == 0 {
		return nil
	}

	// Delete stale chunks before re-inserting so a re-index stays clean.
	if err := cfg.DB.Chunks.DeleteForFile(fileID); err != nil {
		return fmt.Errorf("delete chunks %s: %w", c.RelPath, err)
	}

	for _, chunk := range chunks {
		augmented := AugmentedText(cfg.Project, c.RelPath, chunk)
		vec, err := cfg.LLM.Embed(ctx, cfg.EmbedModel, augmented)
		if err != nil {
			return fmt.Errorf("embed chunk %s:%d: %w", c.RelPath, chunk.StartLine, err)
		}
		if _, err := cfg.DB.Chunks.Upsert(
			fileID, chunk.Label, chunk.Name, chunk.Content,
			chunk.StartLine, chunk.Length, vec, cfg.EmbedModel,
		); err != nil {
			return fmt.Errorf("upsert chunk %s:%d: %w", c.RelPath, chunk.StartLine, err)
		}
		// Check context between chunks to allow cancellation on large files.
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// summarizeFile sends the file content to the summary model under
// structured-output mode (JSON schema) and returns the parsed summary.
// Caps the input at 16KB so a 250KB source file doesn't blow context —
// the first 16KB is plenty for a summary.
func summarizeFile(ctx context.Context, cfg Config, c Candidate, content string) (string, error) {
	const maxInputChars = 16 * 1024
	if len(content) > maxInputChars {
		content = content[:maxInputChars] + "\n\n[... truncated for summarization]"
	}
	header := fmt.Sprintf("File: %s (%s, %d bytes)\n\n", c.RelPath, c.Type, c.Bytes)

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	var raw strings.Builder
	_, _, _, err := cfg.LLM.StreamOnce(callCtx, cfg.SummaryModel,
		[]llm.Message{
			{Role: "system", Content: summaryPrompt},
			{Role: "user", Content: header + content},
		},
		nil, llm.ChatOptions{Format: fileSummarySchema}, llm.ChatCallbacks{
			Content: func(token string) { raw.WriteString(token) },
		})
	if err != nil {
		return "", err
	}

	out := parseFileSummary(raw.String())
	out = strings.TrimSpace(out)
	if len(out) > maxFileSummaryChars {
		out = out[:maxFileSummaryChars] + "…"
	}
	if out == "" {
		return "", fmt.Errorf("empty summary (raw=%q)", trimForLog(raw.String()))
	}
	return out, nil
}

// parseFileSummary tries the schema-constrained JSON shape first and falls
// back to treating the entire response as the summary string. Models that
// honor the schema take the fast path; ones that drift fall through.
func parseFileSummary(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var resp fileSummaryResponse
	if err := json.Unmarshal([]byte(raw), &resp); err == nil && resp.Summary != "" {
		return resp.Summary
	}
	return raw
}

// trimForLog shortens a string for safe inclusion in error messages.
func trimForLog(s string) string {
	const max = 200
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// generateProjectDescription asks the summary model to write a short
// "what is this project" blurb from the per-file summaries. Schema-
// constrained so the model can't return a multi-page architecture doc.
// Best effort — returns "" on any failure; caller falls back gracefully.
func generateProjectDescription(ctx context.Context, cfg Config) (string, error) {
	files, err := cfg.DB.IndexedFiles.ForProject(cfg.Project)
	if err != nil || len(files) == 0 {
		return "", err
	}

	cap := 200
	if len(files) < cap {
		cap = len(files)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Project: %s\nRoot: %s\nFile count: %d\n\nSelected file summaries:\n\n",
		cfg.Project, cfg.Root, len(files))
	for _, f := range files[:cap] {
		fmt.Fprintf(&b, "%s — %s\n", f.RelPath, f.Summary)
	}

	callCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var raw strings.Builder
	_, _, _, err = cfg.LLM.StreamOnce(callCtx, cfg.SummaryModel,
		[]llm.Message{
			{Role: "system", Content: projectDescPrompt},
			{Role: "user", Content: b.String()},
		},
		nil, llm.ChatOptions{Format: projectDescSchema}, llm.ChatCallbacks{
			Content: func(tok string) { raw.WriteString(tok) },
		})
	if err != nil {
		return "", err
	}

	out := parseProjectDescription(raw.String())
	out = strings.TrimSpace(out)
	if len(out) > maxProjectDescChars {
		out = out[:maxProjectDescChars] + "…"
	}
	return out, nil
}

func parseProjectDescription(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var resp projectDescResponse
	if err := json.Unmarshal([]byte(raw), &resp); err == nil && resp.Description != "" {
		return resp.Description
	}
	return raw
}
