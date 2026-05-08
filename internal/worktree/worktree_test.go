package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/db"
)

// initRepo creates a temporary git repository with an initial commit and
// returns its absolute path. Tests use this instead of the live cairo repo.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	mustGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}

	mustGit("init", "-b", "master")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test")
	// Initial commit so HEAD is valid (git worktree add requires a clean HEAD).
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("test repo\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit("add", "README.md")
	mustGit("commit", "-m", "init")
	return dir
}

// openTestDB opens a temporary cairo DB.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// newTestManager creates a Manager wired to a fresh temp git repo and temp DB.
func newTestManager(t *testing.T) (*Manager, string) {
	t.Helper()
	repoRoot := initRepo(t)
	database := openTestDB(t)
	return NewManager(repoRoot, database), repoRoot
}

// ---- slug helpers ----

func Test_goalSlug_normal(t *testing.T) {
	briefing := `## Goal
Add ctrl+L log viewer panel.

## Context
Some context here.
`
	got := goalSlug(briefing)
	// "add-ctrl-l-log-viewer" is 21 chars; cap is 20, so last char is trimmed.
	if got != "add-ctrl-l-log-viewe" {
		t.Errorf("goalSlug: got %q", got)
	}
}

func Test_goalSlug_noGoalSection(t *testing.T) {
	briefing := "## Context\nSome stuff.\n"
	got := goalSlug(briefing)
	if got != "" {
		t.Errorf("goalSlug with no ## Goal: expected empty, got %q", got)
	}
}

func Test_goalSlug_cap20(t *testing.T) {
	briefing := "## Goal\nThis is a very long sentence that exceeds the cap.\n"
	got := goalSlug(briefing)
	if len(got) > 20 {
		t.Errorf("goalSlug: result longer than 20: %q (%d)", got, len(got))
	}
}

func Test_slugify_basic(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Hello World", "hello-world"},
		{"  extra  spaces  ", "extra-spaces"},
		{"foo--bar", "foo-bar"},
		{"123 test!", "123-test"},
	}
	for _, c := range cases {
		got := slugify(c.in, 40)
		if got != c.want {
			t.Errorf("slugify(%q): got %q, want %q", c.in, got, c.want)
		}
	}
}

func Test_branchName_withBriefing(t *testing.T) {
	// "fix-the-indexer-bug" is 19 chars — fits within 20.
	briefing := "## Goal\nFix the indexer bug.\n"
	got := branchName(7, briefing)
	if got != "job/7-fix-the-indexer-bug" {
		t.Errorf("branchName: got %q", got)
	}
}

func Test_branchName_fallback(t *testing.T) {
	got := branchName(42, "no goal section here")
	if got != "job/42" {
		t.Errorf("branchName fallback: got %q", got)
	}
}

// ---- Create / Remove ----

func Test_Create_makesWorktreeOnDisk(t *testing.T) {
	m, repoRoot := newTestManager(t)

	// First we need a job row.
	database := m.database
	j, err := database.Jobs.Create("test job", "", db.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	briefing := "## Goal\nAdd a test feature.\n"
	wtID, err := m.Create(j.ID, briefing, "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// DB row must exist.
	wt, err := database.Worktrees.Get(wtID)
	if err != nil {
		t.Fatalf("Worktrees.Get: %v", err)
	}

	// Path must be under .claude/worktrees/ in the repo.
	expectedPrefix := filepath.Join(repoRoot, ".claude", "worktrees")
	if !strings.HasPrefix(wt.Path, expectedPrefix) {
		t.Errorf("worktree path %q does not start with %q", wt.Path, expectedPrefix)
	}

	// On-disk directory must exist.
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("worktree directory not found on disk: %v", err)
	}

	// Branch name format: job/<jobID>-<slug>.
	if !strings.HasPrefix(wt.Branch, "job/") {
		t.Errorf("branch %q does not start with job/", wt.Branch)
	}

	// Marker file must be present.
	marker := filepath.Join(wt.Path, "CAIRO_WORKTREE")
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("CAIRO_WORKTREE marker not found: %v", err)
	}

	// ParentBranch recorded correctly.
	if wt.ParentBranch != "master" {
		t.Errorf("ParentBranch: got %q", wt.ParentBranch)
	}
}

func Test_Create_autoLinksJobWorktreeID(t *testing.T) {
	m, _ := newTestManager(t)
	database := m.database

	j, err := database.Jobs.Create("auto-link test", "", db.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	_, err = m.Create(j.ID, "## Goal\nTest auto-link.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := database.Jobs.Get(j.ID)
	if err != nil {
		t.Fatalf("Jobs.Get: %v", err)
	}

	if updated.WorktreeID == nil {
		t.Error("expected job.WorktreeID to be set after Create, got nil")
	}
}

func Test_Remove_cleansUpDiskAndDB(t *testing.T) {
	m, _ := newTestManager(t)
	database := m.database

	j, err := database.Jobs.Create("remove test", "", db.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	wtID, err := m.Create(j.ID, "## Goal\nDo something.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wt, _ := database.Worktrees.Get(wtID)
	diskPath := wt.Path

	// Remove it.
	if err := m.Remove(wtID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// On-disk directory must be gone.
	if _, err := os.Stat(diskPath); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after Remove: %v", err)
	}

	// DB row must be gone.
	if _, err := database.Worktrees.Get(wtID); err == nil {
		t.Error("DB row still present after Remove")
	}
}

func Test_WorktreePath_returnsPath(t *testing.T) {
	m, _ := newTestManager(t)
	database := m.database

	j, _ := database.Jobs.Create("path test", "", db.RoleOrchestrator, nil)
	wtID, err := m.Create(j.ID, "## Goal\nTest path.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	path, err := m.WorktreePath(wtID)
	if err != nil {
		t.Fatalf("WorktreePath: %v", err)
	}
	wt, _ := database.Worktrees.Get(wtID)
	if path != wt.Path {
		t.Errorf("WorktreePath: got %q, want %q", path, wt.Path)
	}
}

// ---- Validate ----

func Test_Validate_noEscapeWhenClean(t *testing.T) {
	m, _ := newTestManager(t)
	database := m.database

	j, _ := database.Jobs.Create("validate clean", "", db.RoleOrchestrator, nil)
	wtID, err := m.Create(j.ID, "## Goal\nTest clean state.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Clean working tree — no escaped files expected.
	escaped, files, err := m.Validate(wtID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if escaped {
		t.Errorf("expected no escape on clean repo, got files: %v", files)
	}
}

func Test_Validate_detectsEscapedFile(t *testing.T) {
	m, repoRoot := newTestManager(t)
	database := m.database

	j, _ := database.Jobs.Create("validate escape", "", db.RoleOrchestrator, nil)
	wtID, err := m.Create(j.ID, "## Goal\nTest escape detection.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Write a file in the main checkout (outside the worktree dir) — simulates
	// an agent that escaped its sandbox.
	escapedFile := filepath.Join(repoRoot, "escaped.txt")
	if err := os.WriteFile(escapedFile, []byte("oops\n"), 0644); err != nil {
		t.Fatalf("write escaped file: %v", err)
	}

	escaped, files, err := m.Validate(wtID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if !escaped {
		t.Error("expected escape to be detected")
	}
	found := false
	for _, f := range files {
		if strings.Contains(f, "escaped.txt") {
			found = true
		}
	}
	if !found {
		t.Errorf("escaped.txt not in files list: %v", files)
	}
}

func Test_Validate_ignoresFilesInsideWorktree(t *testing.T) {
	m, _ := newTestManager(t)
	database := m.database

	j, _ := database.Jobs.Create("validate inside", "", db.RoleOrchestrator, nil)
	wtID, err := m.Create(j.ID, "## Goal\nTest inside detection.\n", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	wt, _ := database.Worktrees.Get(wtID)

	// Write a file inside the worktree — should NOT be flagged.
	insideFile := filepath.Join(wt.Path, "inside.go")
	if err := os.WriteFile(insideFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("write inside file: %v", err)
	}

	escaped, files, err := m.Validate(wtID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if escaped {
		t.Errorf("files inside worktree should not be flagged: %v", files)
	}
}
