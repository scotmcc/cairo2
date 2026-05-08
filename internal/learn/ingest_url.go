package learn

// ingest_url.go — in-process path for indexing a pre-fetched web page.
// Unlike Run (which walks a directory), IngestURL takes content that has
// already been fetched and runs summarize+embed+upsert directly.

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/scotmcc/cairo2/internal/db"
)

// WebProject is the project name used for all auto-ingested web pages.
const WebProject = "_web"

// IngestURL summarizes and embeds a pre-fetched web page into the _web project.
// It reuses the same summarize→embed→upsert pipeline as indexOne, treating the
// URL as the rel_path identifier. Skips silently if the content hash matches the
// previously stored version (idempotent on repeated fetches of the same page).
//
// cfg.DB and cfg.LLM must be non-nil. cfg.SummaryModel and cfg.EmbedModel must
// be set — the caller is responsible for reading these from DB config keys.
func IngestURL(ctx context.Context, cfg Config, rawURL, content string) error {
	if cfg.DB == nil || cfg.LLM == nil {
		return fmt.Errorf("learn.IngestURL: DB and LLM are required")
	}

	// Ensure the _web project row exists before writing files (FK constraint).
	if err := cfg.DB.Projects.Upsert(WebProject, WebProject, "Automatically ingested web pages"); err != nil {
		return fmt.Errorf("upsert _web project: %w", err)
	}

	// Compute content hash for idempotency.
	h := sha256.Sum256([]byte(content))
	contentSHA := fmt.Sprintf("%x", h)
	prev, _ := cfg.DB.IndexedFiles.GetSHA(WebProject, rawURL)
	if prev == contentSHA {
		return nil // unchanged since last fetch
	}

	c := Candidate{
		RelPath: rawURL,
		AbsPath: rawURL,
		Type:    "web",
		Bytes:   len(content),
	}
	summary, err := summarizeFile(ctx, cfg, c, content)
	if err != nil {
		return fmt.Errorf("summarize %s: %w", rawURL, err)
	}

	augmented := fmt.Sprintf("project=%s url=%s · %s", WebProject, rawURL, summary)
	vec, err := cfg.LLM.Embed(ctx, cfg.EmbedModel, augmented)
	if err != nil {
		return fmt.Errorf("embed %s: %w", rawURL, err)
	}

	if _, err := cfg.DB.IndexedFiles.Upsert(&db.IndexedFile{
		Project:    WebProject,
		RelPath:    rawURL,
		FileType:   "web",
		Bytes:      len(content),
		SHA256:     contentSHA,
		Summary:    summary,
		Embedding:  vec,
		EmbedModel: cfg.EmbedModel,
	}); err != nil {
		return fmt.Errorf("upsert indexed file: %w", err)
	}
	_ = cfg.DB.Projects.RecountFiles(WebProject)
	return nil
}
