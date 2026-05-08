// Package worktree implements the mechanical layer for cairo's v0.3.0
// job-orchestration system. It creates and removes git worktrees on disk and
// keeps the corresponding worktrees DB rows in sync.
//
// # Path convention
//
// Worktrees live at:
//
//	<repo-root>/.claude/worktrees/job-<jobID>-<short-hash>/
//
// This mirrors the existing agent sandbox convention
// (.claude/worktrees/agent-<id>/) so all sandboxes live in the same directory
// and the existing git worktree prune guidance applies uniformly.
//
// # Branch naming
//
// Branches are named:
//
//	job/<jobID>-<slug>
//
// where <slug> is a 20-char-cap kebab-case derivative of the first sentence
// of the briefing's ## Goal section. Non-alphanumeric characters are stripped;
// spaces become hyphens; result is lowercased. If briefing parsing fails the
// branch falls back to just job/<jobID>.
package worktree

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/scotmcc/cairo2/internal/db"
)

// Manager owns worktree operations for a specific repository.
// The repoRoot must be the absolute path to the main git checkout (not a
// worktree itself). Use NewManager to construct one.
type Manager struct {
	repoRoot string
	database *db.DB
}

// NewManager constructs a Manager for the repository rooted at repoRoot.
// repoRoot must be the absolute path of the main checkout.
func NewManager(repoRoot string, database *db.DB) *Manager {
	return &Manager{repoRoot: repoRoot, database: database}
}

// RepoRoot returns the absolute path to the repository root this Manager is
// wired to. Used by callers that need to run git commands in the same repo.
func (m *Manager) RepoRoot() string { return m.repoRoot }

// Create creates a new git worktree for a job and inserts a worktrees DB row.
//
// jobID is the jobs.id the worktree belongs to. briefing is the structured
// briefing text (the ## Goal section's first sentence is used for the branch
// slug). parentBranch is the branch the worktree should start from.
//
// Returns the new worktrees row ID on success.
func (m *Manager) Create(jobID int64, briefing string, parentBranch string) (int64, error) {
	branch := branchName(jobID, briefing)
	path := m.worktreePath(jobID)

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("worktree: mkdir parent: %w", err)
	}

	if err := m.runGit("worktree", "add", path, "-b", branch, parentBranch); err != nil {
		return 0, fmt.Errorf("worktree: git worktree add: %w", err)
	}

	wt, err := m.database.Worktrees.Create(path, branch, parentBranch)
	if err != nil {
		// Best effort: clean up the on-disk worktree if the DB insert failed.
		_ = m.runGit("worktree", "remove", "--force", path)
		return 0, fmt.Errorf("worktree: db insert: %w", err)
	}

	if err := m.database.Jobs.SetV030Fields(jobID, &wt.ID, nil, nil, nil, nil, nil, nil); err != nil {
		_ = m.database.Worktrees.Delete(wt.ID)
		_ = m.runGit("worktree", "remove", "--force", path)
		return 0, fmt.Errorf("worktree: link to job %d: %w", jobID, err)
	}

	if err := writeMarker(path, jobID, branch); err != nil {
		// Non-fatal: marker is informational only.
		_ = err
	}

	return wt.ID, nil
}

// Remove removes the on-disk git worktree and deletes the DB row.
// The FK ON DELETE SET NULL on jobs.worktree_id clears the job's link
// automatically, so the job record is preserved.
func (m *Manager) Remove(worktreeID int64) error {
	wt, err := m.database.Worktrees.Get(worktreeID)
	if err != nil {
		return fmt.Errorf("worktree: get row %d: %w", worktreeID, err)
	}

	if err := m.runGit("worktree", "remove", "--force", wt.Path); err != nil {
		return fmt.Errorf("worktree: git worktree remove: %w", err)
	}

	if err := m.database.Worktrees.Delete(worktreeID); err != nil {
		return fmt.Errorf("worktree: db delete %d: %w", worktreeID, err)
	}
	return nil
}

// WorktreePath returns the on-disk path for the given worktree ID.
func (m *Manager) WorktreePath(worktreeID int64) (string, error) {
	wt, err := m.database.Worktrees.Get(worktreeID)
	if err != nil {
		return "", fmt.Errorf("worktree: get row %d: %w", worktreeID, err)
	}
	return wt.Path, nil
}

// Validate inspects the main checkout's git status for files modified outside
// the worktree's directory tree. This is a best-effort audit — it will
// false-positive on the user's unrelated local edits. The result is
// informational; the real audit trail is the merge-time diff.
//
// Returns (escaped, escapedFiles, err) where escaped is true when at least one
// modified file lies outside worktreeID's path prefix.
func (m *Manager) Validate(worktreeID int64) (bool, []string, error) {
	wt, err := m.database.Worktrees.Get(worktreeID)
	if err != nil {
		return false, nil, fmt.Errorf("worktree: get row %d: %w", worktreeID, err)
	}

	// Relative path from repo root to the worktree directory.
	relPath, err := filepath.Rel(m.repoRoot, wt.Path)
	if err != nil {
		return false, nil, fmt.Errorf("worktree: rel path: %w", err)
	}
	// Normalise to forward slashes and ensure a trailing separator so prefix
	// matching doesn't incorrectly match a sibling directory.
	relPath = filepath.ToSlash(relPath) + "/"

	out, err := m.runGitOutput("status", "--porcelain", "-uall")
	if err != nil {
		return false, nil, fmt.Errorf("worktree: git status: %w", err)
	}

	var escaped []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		// Porcelain format: XY SP<path>. File path starts at column 3.
		filePath := strings.TrimSpace(line[3:])
		if filePath == "" {
			continue
		}
		// Unquote git's C-style quoting for paths with special chars.
		filePath = unquoteGitPath(filePath)
		if !strings.HasPrefix(filePath, relPath) {
			escaped = append(escaped, filePath)
		}
	}

	return len(escaped) > 0, escaped, nil
}

// worktreePath builds the on-disk path for a new job worktree. It appends a
// 4-byte random suffix to avoid the rare collision when two jobs with the same
// ID digit are created concurrently (or when the old directory was not pruned).
func (m *Manager) worktreePath(jobID int64) string {
	suffix := shortHash()
	name := fmt.Sprintf("job-%d-%s", jobID, suffix)
	return filepath.Join(m.repoRoot, ".claude", "worktrees", name)
}

// runGit executes a git command in the repo root directory. stdout and stderr
// are combined in any returned error message.
func (m *Manager) runGit(args ...string) error {
	_, err := m.runGitOutput(args...)
	return err
}

func (m *Manager) runGitOutput(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = m.repoRoot
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w — stderr: %s", strings.Join(args, " "), err, stderr.String())
	}
	return out.String(), nil
}

// branchName derives a branch name from a job ID and its briefing.
// Format: job/<jobID>-<slug> where slug comes from the ## Goal section.
// Falls back to job/<jobID> if the briefing has no parseable Goal section.
func branchName(jobID int64, briefing string) string {
	slug := goalSlug(briefing)
	if slug == "" {
		return fmt.Sprintf("job/%d", jobID)
	}
	return fmt.Sprintf("job/%d-%s", jobID, slug)
}

// goalSlug extracts the first sentence of the ## Goal section from a structured
// briefing, then converts it to a 20-char-max kebab-case slug.
func goalSlug(briefing string) string {
	const header = "## Goal"
	idx := strings.Index(briefing, header)
	if idx == -1 {
		return ""
	}
	rest := strings.TrimSpace(briefing[idx+len(header):])
	// Take everything up to the next ## section or a blank line, whichever comes first.
	firstLine := rest
	for _, sep := range []string{"\n##", "\n\n"} {
		if i := strings.Index(rest, sep); i != -1 && i < len(firstLine) {
			firstLine = rest[:i]
		}
	}
	// Extract the first sentence (up to the first period or end of line).
	sentence := strings.TrimSpace(firstLine)
	if i := strings.Index(sentence, "."); i != -1 {
		sentence = sentence[:i]
	}
	return slugify(sentence, 20)
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// slugify converts s to a lowercase kebab-case identifier capped at maxLen chars.
func slugify(s string, maxLen int) string {
	s = strings.ToLower(s)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > maxLen {
		s = s[:maxLen]
		s = strings.TrimRight(s, "-")
	}
	return s
}

// shortHash returns a 4-byte random hex string for uniqueness in path names.
func shortHash() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use a timestamp-derived suffix. Should never happen in practice.
		return "0000"
	}
	return hex.EncodeToString(b)
}

// writeMarker writes a small CAIRO_WORKTREE file into the worktree directory
// so agents know they are inside a cairo job sandbox.
func writeMarker(worktreePath string, jobID int64, branch string) error {
	content := fmt.Sprintf(
		"This directory is a cairo job worktree.\nJob ID: %d\nBranch: %s\nAgents work here only — do not modify files outside this directory.\n",
		jobID, branch,
	)
	return os.WriteFile(filepath.Join(worktreePath, "CAIRO_WORKTREE"), []byte(content), 0644)
}

// unquoteGitPath removes git's C-style quoting from a path if present.
// Git wraps paths in double-quotes and escapes special bytes when
// core.quotepath is on (the default). For our purposes, stripping the
// surrounding quotes is sufficient — we only need the prefix for matching.
func unquoteGitPath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		return p[1 : len(p)-1]
	}
	return p
}
