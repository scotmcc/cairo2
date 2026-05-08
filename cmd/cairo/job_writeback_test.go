package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/worktree"
)

func createTestWorktreeJob(t *testing.T) (*sqliteopen.DB, *worktree.Manager, *jobs.Job, int64) {
	t.Helper()
	repoRoot := initTestRepo(t)
	database := openTestDB(t)
	mgr := worktree.NewManager(repoRoot, database)

	job, err := database.Jobs.Create("test job", "test description", "orchestrator", nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	const briefing = "## Goal\nTest writeback.\n## Context\nTest.\n## Files & landmarks\n\n## Acceptance\n\n## Out of scope\n"
	worktreeID, err := mgr.Create(job.ID, briefing, "master")
	if err != nil {
		t.Fatalf("Manager.Create: %v", err)
	}

	if err := database.Jobs.SetV030Fields(job.ID, &worktreeID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("SetV030Fields: %v", err)
	}

	job, err = database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("re-read job: %v", err)
	}

	return database, mgr, job, worktreeID
}

func commitFileToWorktree(t *testing.T, worktreePath, filename, content string) {
	t.Helper()
	mustGitIn := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v in %s: %v — %s", args, dir, err, out)
		}
	}

	filePath := filepath.Join(worktreePath, filename)
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatalf("write file %s: %v", filename, err)
	}
	mustGitIn(worktreePath, "add", filename)
	mustGitIn(worktreePath, "-c", "user.email=test@example.com", "-c", "user.name=Test",
		"commit", "-m", fmt.Sprintf("add %s", filename))
}

func Test_PostLoopJobWriteback_SuccessPath(t *testing.T) {
	database, _, job, worktreeID := createTestWorktreeJob(t)

	wt, err := database.Worktrees.Get(worktreeID)
	if err != nil {
		t.Fatalf("get worktree: %v", err)
	}

	content := "line1\nline2\nline3\nline4\nline5\n"
	commitFileToWorktree(t, wt.Path, "feature.go", content)

	result := "Implemented feature.go with 5 lines\nDetails: added feature logic\nAll tests pass."
	postLoopJobWriteback(database, job.ID, result, nil)

	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}

	if updated.Status != jobs.StatusAwaitingReview {
		t.Errorf("status = %q, want %q", updated.Status, jobs.StatusAwaitingReview)
	}
	if updated.DiffFiles == nil || *updated.DiffFiles != 1 {
		t.Errorf("diff_files = %v, want 1", updated.DiffFiles)
	}
	if updated.DiffInsertions == nil || *updated.DiffInsertions != 5 {
		t.Errorf("diff_insertions = %v, want 5", updated.DiffInsertions)
	}
	if updated.DiffDeletions == nil || *updated.DiffDeletions != 0 {
		t.Errorf("diff_deletions = %v, want 0", updated.DiffDeletions)
	}
	if updated.Summary == nil || *updated.Summary == "" {
		t.Error("summary should be non-empty")
	} else {
		wantSummary := "Implemented feature.go with 5 lines"
		if *updated.Summary != wantSummary {
			t.Errorf("summary = %q, want %q", *updated.Summary, wantSummary)
		}
	}
}

func Test_PostLoopJobWriteback_FailurePath(t *testing.T) {
	database, _, job, _ := createTestWorktreeJob(t)

	runErr := fmt.Errorf("agent hit turn cap after 18 turns")
	postLoopJobWriteback(database, job.ID, "error: agent hit turn cap after 18 turns", runErr)

	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}

	if updated.Status != jobs.StatusFailed {
		t.Errorf("status = %q, want %q", updated.Status, jobs.StatusFailed)
	}
	if updated.Error == nil || *updated.Error == "" {
		t.Error("error field should be non-empty on failure path")
	}
	if updated.DiffFiles != nil {
		t.Errorf("diff_files should be nil on failure path, got %v", *updated.DiffFiles)
	}
}

func Test_PostLoopJobWriteback_BlockedResult(t *testing.T) {
	database, _, job, _ := createTestWorktreeJob(t)

	blockedResult := "BLOCKED: step 2 — researcher returned no files to modify"
	postLoopJobWriteback(database, job.ID, blockedResult, nil)

	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}

	if updated.Status != jobs.StatusFailed {
		t.Errorf("status = %q, want %q (BLOCKED should be treated as failure)", updated.Status, jobs.StatusFailed)
	}
	if updated.Error == nil || *updated.Error == "" {
		t.Error("error field should be non-empty for BLOCKED result")
	}
	if updated.Error != nil && len(*updated.Error) > 0 {
		if (*updated.Error)[0:7] != "BLOCKED" {
			t.Errorf("error field should start with 'BLOCKED', got: %q", *updated.Error)
		}
	}
}

func Test_PostLoopJobWriteback_NonWorktreeJobNoOp(t *testing.T) {
	database := openTestDB(t)

	job, err := database.Jobs.Create("plain job", "no worktree", "orchestrator", nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	postLoopJobWriteback(database, job.ID, "some result", nil)

	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}

	if updated.Status != jobs.StatusPending {
		t.Errorf("status = %q for non-worktree job, want %q (no-op expected)", updated.Status, jobs.StatusPending)
	}
}

func Test_JobMaxReviewIterationsSeeded(t *testing.T) {
	database := openTestDB(t)

	val, err := database.Config.Get(config.KeyJobMaxReviewIterations)
	if err != nil {
		t.Fatalf("Config.Get(%q): %v", config.KeyJobMaxReviewIterations, err)
	}
	if val != "3" {
		t.Errorf("job_max_review_iterations = %q, want %q", val, "3")
	}
}

func Test_ResolveAndUpdateJobStatus_SkipsAwaitingReview(t *testing.T) {
	database := openTestDB(t)

	job, err := database.Jobs.Create("test job", "desc", "orchestrator", nil)
	if err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := database.Jobs.SetStatus(job.ID, jobs.StatusAwaitingReview); err != nil {
		t.Fatalf("SetStatus awaiting_review: %v", err)
	}

	task, err := database.Tasks.Create(job.ID, "task", "desc", "coder", "[]")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := database.Tasks.SetStatusAndResult(task.ID, jobs.StatusDone, "done"); err != nil {
		t.Fatalf("set task done: %v", err)
	}

	if err := database.Jobs.ResolveAndUpdateJobStatus(job.ID); err != nil {
		t.Fatalf("ResolveAndUpdateJobStatus: %v", err)
	}

	updated, err := database.Jobs.Get(job.ID)
	if err != nil {
		t.Fatalf("get updated job: %v", err)
	}

	if updated.Status != jobs.StatusAwaitingReview {
		t.Errorf("status = %q after ResolveAndUpdateJobStatus, want %q (must not overwrite decided state)",
			updated.Status, jobs.StatusAwaitingReview)
	}
}

func Test_ParseShortstat(t *testing.T) {
	cases := []struct {
		line    string
		f, i, d int64
	}{
		{" 2 files changed, 5 insertions(+), 2 deletions(-)", 2, 5, 2},
		{" 1 file changed, 5 insertions(+)", 1, 5, 0},
		{" 1 file changed, 2 deletions(-)", 1, 0, 2},
		{" 3 files changed, 10 insertions(+), 1 deletion(-)", 3, 10, 1},
		{"", 0, 0, 0},
	}
	for _, tc := range cases {
		f, i, d := parseShortstat(tc.line)
		if f != tc.f || i != tc.i || d != tc.d {
			t.Errorf("parseShortstat(%q) = (%d,%d,%d), want (%d,%d,%d)",
				tc.line, f, i, d, tc.f, tc.i, tc.d)
		}
	}
}

func Test_FirstLine(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"first line\nsecond line", "first line"},
		{"\nfirst non-empty\n", "first non-empty"},
		{"", ""},
		{"  trimmed  \nsecond", "trimmed"},
	}
	for _, tc := range cases {
		got := firstLine(tc.input)
		if got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
