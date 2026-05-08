package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// initTestRepo creates a temporary git repository with an initial commit.
func initTestRepo(t *testing.T) string {
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
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("test repo\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit("add", "README.md")
	mustGit("commit", "-m", "init")
	return dir
}

func openTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	database, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("sqliteopen.OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func Test_ResolveTaskCWD_WorktreePath(t *testing.T) {
	repoRoot := initTestRepo(t)
	database := openTestDB(t)
	mgr := worktree.NewManager(repoRoot, database)

	job, err := database.Jobs.Create("test job", "test description", "orchestrator", nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	const briefing = "## Goal\nVerify CWD plumbing.\n## Context\nTest.\n## Files & landmarks\n\n## Acceptance\n\n## Out of scope\n"
	worktreeID, err := mgr.Create(job.ID, briefing, "master")
	if err != nil {
		t.Fatalf("Manager.Create: %v", err)
	}

	task, err := database.Tasks.Create(job.ID, "pwd check", "echo the cwd", "coder", "[]")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	cwd, err := resolveTaskCWD(database, task)
	if err != nil {
		t.Fatalf("resolveTaskCWD: %v", err)
	}

	wt, err := database.Worktrees.Get(worktreeID)
	if err != nil {
		t.Fatalf("Worktrees.Get: %v", err)
	}

	if cwd != wt.Path {
		t.Errorf("resolveTaskCWD = %q, want worktree path %q", cwd, wt.Path)
	}

	processCWD, _ := os.Getwd()
	if cwd == processCWD {
		t.Errorf("resolveTaskCWD returned process CWD %q — worktree CWD plumbing is broken", processCWD)
	}

	if _, err := os.Stat(cwd); os.IsNotExist(err) {
		t.Errorf("worktree path %q does not exist on disk", cwd)
	}

	if !strings.Contains(cwd, ".claude/worktrees/") {
		t.Errorf("worktree path %q does not contain .claude/worktrees/", cwd)
	}
}

func Test_ResolveTaskCWD_NullWorktreeError(t *testing.T) {
	database := openTestDB(t)

	job, err := database.Jobs.Create("no-worktree job", "description", "orchestrator", nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	task, err := database.Tasks.Create(job.ID, "task", "description", "coder", "[]")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = resolveTaskCWD(database, task)
	if err == nil {
		t.Fatal("resolveTaskCWD: expected error for job with null worktree_id, got nil")
	}
	if !strings.Contains(err.Error(), "no worktree") {
		t.Errorf("error message should mention 'no worktree', got: %v", err)
	}
	if !strings.Contains(err.Error(), "worktree(action=\"create\"") {
		t.Errorf("error message should contain prescriptive fix, got: %v", err)
	}
	if strings.Contains(err.Error(), "UPDATE jobs") {
		t.Errorf("error message must not contain 'UPDATE jobs' (regression guard), got: %s", err.Error())
	}
}

func Test_ResolveTaskCWD_NoJobError(t *testing.T) {
	database := openTestDB(t)
	task := &jobs.Task{ID: 99, JobID: 0}

	_, err := resolveTaskCWD(database, task)
	if err == nil {
		t.Fatal("resolveTaskCWD: expected error for task with JobID == 0, got nil")
	}
	if !strings.Contains(err.Error(), "no job_id") {
		t.Errorf("error message should mention 'no job_id', got: %v", err)
	}
}
