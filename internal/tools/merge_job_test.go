package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// ---- test helpers ----

// initTestRepo creates a temporary git repo with one commit on 'master',
// then creates a bare clone as 'origin'. Returns (repoRoot, bareRoot).
// Tests that need a push target use bareRoot as the remote.
func initTestRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()

	mustGit := func(gitDir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = gitDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v — %s", args, gitDir, err, out)
		}
	}

	mustGit(dir, "init", "-b", "master")
	mustGit(dir, "config", "user.email", "test@example.com")
	mustGit(dir, "config", "user.name", "Test")
	readme := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readme, []byte("test repo\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGit(dir, "add", "README.md")
	mustGit(dir, "commit", "-m", "init")

	// Create a bare repo as origin.
	bareDir := t.TempDir()
	mustGit(bareDir, "init", "--bare", "-b", "master")
	// Push master to bare and set the upstream tracking branch.
	mustGit(dir, "remote", "add", "origin", bareDir)
	mustGit(dir, "push", "--set-upstream", "origin", "master")

	return dir, bareDir
}

// openMergeJobTestDB opens a temp cairo DB (package-local, avoids collision with registry_test.go's openTestDB).
func openMergeJobTestDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.OpenAt(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// setupJobAndWorktree creates a job + worktree in the test repo and returns them.
func setupJobAndWorktree(t *testing.T, database *db.DB, repoRoot string) (*db.Job, *db.Worktree, *worktree.Manager) {
	t.Helper()
	m := worktree.NewManager(repoRoot, database)
	job, err := database.Jobs.Create("test job", "test description", db.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	briefing := "## Goal\nAdd a test feature.\n"
	wtID, err := m.Create(job.ID, briefing, "master")
	if err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	wt, err := database.Worktrees.Get(wtID)
	if err != nil {
		t.Fatalf("get worktree: %v", err)
	}
	// Reload job with worktree_id set.
	job, err = database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	return job, wt, m
}

// addCommitToWorktree writes a file in the worktree and commits it.
func addCommitToWorktree(t *testing.T, wtPath, filename, content string) {
	t.Helper()
	fpath := filepath.Join(wtPath, filename)
	if err := os.WriteFile(fpath, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
	mustGitIn := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}
	mustGitIn(wtPath, "add", filename)
	mustGitIn(wtPath, "commit", "-m", "add "+filename)
}

// newMergeJobCtx builds a ToolContext for tests (bus only; no DB override — tool uses its own).
func newMergeJobCtx() *agent.ToolContext {
	return &agent.ToolContext{
		Bus:            &agent.Bus{},
		DisciplineMode: agent.DisciplineFull,
	}
}

// ---- tests ----

func TestMergeJob_Approve_HappyPath(t *testing.T) {
	repoRoot, _ := initTestRepo(t)
	database := openMergeJobTestDB(t)

	job, wt, manager := setupJobAndWorktree(t, database, repoRoot)
	addCommitToWorktree(t, wt.Path, "feature.go", "package main\n")

	tool := MergeJob(manager, database)
	ctx := newMergeJobCtx()
	ctx.DB = database

	result := tool.Execute(map[string]any{
		"action": "approve",
		"job_id": float64(job.ID),
	}, ctx)

	if result.IsError {
		t.Fatalf("approve failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "merged") {
		t.Errorf("expected 'merged' in result, got: %s", result.Content)
	}

	// Assert: job.Status == "merged", reviewed_at set.
	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updated.Status != db.StatusMerged {
		t.Errorf("job status: want merged, got %s", updated.Status)
	}
	if updated.ReviewedAt == nil {
		t.Error("expected reviewed_at to be set")
	}

	// Assert: worktree directory gone.
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory still exists after merge: %v", err)
	}

	// Assert: worktrees DB row gone (worktree_id on job cascades to NULL).
	if _, err := database.Worktrees.Get(wt.ID); err == nil {
		t.Error("worktrees DB row still present after merge")
	}

	// Assert: squash commit landed on master.
	cmd := exec.Command("git", "-C", repoRoot, "log", "--oneline", "master")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected at least 2 commits on master (init + squash), got: %v", lines)
	}
}

func TestMergeJob_Approve_ConflictSetsStatusConflict(t *testing.T) {
	repoRoot, _ := initTestRepo(t)
	database := openMergeJobTestDB(t)

	job, wt, manager := setupJobAndWorktree(t, database, repoRoot)

	mustGitIn := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v — %s", args, dir, err, out)
		}
	}

	// Arrange a commit on the worktree branch so the branch has history.
	// We commit feature.go so it becomes a tracked file.
	addCommitToWorktree(t, wt.Path, "feature.go", "package main\n")

	// Modify a tracked file WITHOUT staging it. git rebase refuses to run
	// when tracked files are modified ("Cannot rebase: Your working tree is
	// dirty"). This causes runGitIn(rebase) to fail. doApprove aborts the
	// rebase and sets status=conflict — no auto-resolve in this design.
	dirtyFile := filepath.Join(wt.Path, "feature.go")
	if err := os.WriteFile(dirtyFile, []byte("package main // dirty\n"), 0644); err != nil {
		t.Fatalf("modify tracked file to make worktree dirty: %v", err)
	}

	// Add a commit on master AFTER the worktree branched so the rebase has
	// something to do (otherwise rebase is a no-op and succeeds trivially).
	masterFile := filepath.Join(repoRoot, "master_change.txt")
	if err := os.WriteFile(masterFile, []byte("master change\n"), 0644); err != nil {
		t.Fatalf("write master_change.txt: %v", err)
	}
	mustGitIn(repoRoot, "add", "master_change.txt")
	mustGitIn(repoRoot, "commit", "-m", "advance master")

	tool := MergeJob(manager, database)
	ctx := newMergeJobCtx()
	ctx.DB = database

	result := tool.Execute(map[string]any{
		"action": "approve",
		"job_id": float64(job.ID),
	}, ctx)

	if !result.IsError {
		t.Fatalf("expected conflict error, got success: %s", result.Content)
	}
	if !strings.Contains(result.Content, "conflict") {
		t.Errorf("expected 'conflict' in error result, got: %s", result.Content)
	}

	// Assert: job.Status == "conflict".
	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updated.Status != db.StatusConflict {
		t.Errorf("job status: want conflict, got %s", updated.Status)
	}

	// Assert: worktree directory still exists (preserved for inspection).
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("worktree should be preserved after conflict: %v", err)
	}
}

func TestMergeJob_Approve_PushFailure(t *testing.T) {
	// Create repo WITHOUT a valid remote (no 'origin' configured).
	repoRoot := t.TempDir()
	mustGitIn := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v — %s", args, dir, err, out)
		}
	}
	mustGitIn(repoRoot, "init", "-b", "master")
	mustGitIn(repoRoot, "config", "user.email", "test@example.com")
	mustGitIn(repoRoot, "config", "user.name", "Test")
	readme := filepath.Join(repoRoot, "README.md")
	if err := os.WriteFile(readme, []byte("test repo\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	mustGitIn(repoRoot, "add", "README.md")
	mustGitIn(repoRoot, "commit", "-m", "init")
	// NO remote configured → push will fail.

	database := openMergeJobTestDB(t)
	job, wt, manager := setupJobAndWorktree(t, database, repoRoot)
	addCommitToWorktree(t, wt.Path, "feature.go", "package main\n")

	tool := MergeJob(manager, database)
	ctx := newMergeJobCtx()
	ctx.DB = database

	result := tool.Execute(map[string]any{
		"action": "approve",
		"job_id": float64(job.ID),
	}, ctx)

	// Push failure is NOT a hard error — result should indicate partial success.
	if result.IsError {
		t.Fatalf("push failure should not be IsError=true, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "couldn't push") {
		t.Errorf("expected 'couldn't push' in result, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "push_pending=1") {
		t.Errorf("expected 'push_pending=1' in result, got: %s", result.Content)
	}

	// Assert: job.Status == "merged" (local merge preserved).
	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updated.Status != db.StatusMerged {
		t.Errorf("job status: want merged, got %s", updated.Status)
	}

	// Assert: push_pending=1 on worktree row (worktree kept).
	wtUpdated, err := database.Worktrees.Get(wt.ID)
	if err != nil {
		t.Fatalf("get worktree (should still exist after push failure): %v", err)
	}
	if !wtUpdated.PushPending {
		t.Error("expected push_pending=true on worktree after push failure")
	}

	// Assert: squash commit IS on master (local merge preserved).
	cmd := exec.Command("git", "-C", repoRoot, "log", "--oneline", "master")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Errorf("expected squash commit on master, got: %v", lines)
	}
}

func TestMergeJob_Reject_SetsStatusKeepsWorktree(t *testing.T) {
	repoRoot, _ := initTestRepo(t)
	database := openMergeJobTestDB(t)

	job, wt, manager := setupJobAndWorktree(t, database, repoRoot)

	tool := MergeJob(manager, database)
	ctx := newMergeJobCtx()
	ctx.DB = database

	result := tool.Execute(map[string]any{
		"action": "reject",
		"job_id": float64(job.ID),
	}, ctx)

	if result.IsError {
		t.Fatalf("reject failed: %s", result.Content)
	}
	if !strings.Contains(result.Content, "rejected") {
		t.Errorf("expected 'rejected' in result, got: %s", result.Content)
	}

	// Assert: job.Status == "rejected", reviewed_at set.
	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if updated.Status != db.StatusRejected {
		t.Errorf("job status: want rejected, got %s", updated.Status)
	}
	if updated.ReviewedAt == nil {
		t.Error("expected reviewed_at to be set")
	}

	// Assert: worktree still exists on disk (kept).
	if _, err := os.Stat(wt.Path); err != nil {
		t.Errorf("worktree should be preserved after reject: %v", err)
	}

	// Assert: worktrees DB row still present.
	if _, err := database.Worktrees.Get(wt.ID); err != nil {
		t.Errorf("worktrees DB row should be preserved after reject: %v", err)
	}
}

func TestMergeJob_Approve_NoWorktree_ReturnsError(t *testing.T) {
	repoRoot, _ := initTestRepo(t)
	database := openMergeJobTestDB(t)
	manager := worktree.NewManager(repoRoot, database)

	// Create a job WITHOUT a worktree (old-style).
	job, err := database.Jobs.Create("old job", "", db.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	tool := MergeJob(manager, database)
	ctx := newMergeJobCtx()
	ctx.DB = database

	result := tool.Execute(map[string]any{
		"action": "approve",
		"job_id": float64(job.ID),
	}, ctx)

	if !result.IsError {
		t.Fatalf("expected error for old-style job, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "worktree") {
		t.Errorf("expected 'worktree' in error, got: %s", result.Content)
	}
}

func TestMergeJob_MissingAction_ReturnsError(t *testing.T) {
	repoRoot, _ := initTestRepo(t)
	database := openMergeJobTestDB(t)
	manager := worktree.NewManager(repoRoot, database)

	tool := MergeJob(manager, database)
	result := tool.Execute(map[string]any{"job_id": float64(1)}, nil)
	if !result.IsError {
		t.Error("expected error for missing action")
	}
}
