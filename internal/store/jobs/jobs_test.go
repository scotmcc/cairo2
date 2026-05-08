package jobs_test

import (
	"testing"

	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// Test_Jobs_V030Fields verifies that the v0.3.0 ALTER TABLE columns land
// correctly and that the extended Job struct round-trips through the DB.
func Test_Jobs_V030Fields(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Create a job using the existing (unmodified) Create helper.
	j, err := db.Jobs.Create("test job", "description", identity.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if j.WorktreeID != nil || j.Briefing != nil || j.Summary != nil || j.Error != nil {
		t.Fatalf("new job should have nil v0.3.0 fields, got: worktree_id=%v briefing=%v", j.WorktreeID, j.Briefing)
	}

	// Write v0.3.0 fields.
	briefing := "## Goal\nDo something.\n\n## Context\nTest context.\n\n## Files & landmarks\nNone.\n\n## Acceptance\nTest passes.\n\n## Out of scope\nEverything else."
	summary := "completed successfully"
	errText := ""
	var diffFiles, diffInsertions, diffDeletions int64 = 3, 42, 7
	if err := db.Jobs.SetV030Fields(j.ID, nil, &briefing, &summary, &errText, &diffFiles, &diffInsertions, &diffDeletions); err != nil {
		t.Fatalf("SetV030Fields: %v", err)
	}

	// Reload and verify.
	j2, err := db.Jobs.Get(j.ID)
	if err != nil {
		t.Fatalf("Get after SetV030Fields: %v", err)
	}
	if j2.Briefing == nil || *j2.Briefing != briefing {
		t.Errorf("Briefing mismatch: got %v", j2.Briefing)
	}
	if j2.Summary == nil || *j2.Summary != summary {
		t.Errorf("Summary mismatch: got %v", j2.Summary)
	}
	if j2.DiffFiles == nil || *j2.DiffFiles != diffFiles {
		t.Errorf("DiffFiles: got %v, want %d", j2.DiffFiles, diffFiles)
	}
	if j2.DiffInsertions == nil || *j2.DiffInsertions != diffInsertions {
		t.Errorf("DiffInsertions: got %v, want %d", j2.DiffInsertions, diffInsertions)
	}
	if j2.DiffDeletions == nil || *j2.DiffDeletions != diffDeletions {
		t.Errorf("DiffDeletions: got %v, want %d", j2.DiffDeletions, diffDeletions)
	}
}

// Test_Jobs_MigrationPreservesData ensures the ALTER TABLE migration doesn't
// drop existing rows — a populated DB must survive migration intact.
func Test_Jobs_MigrationPreservesData(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Insert several jobs with only the original columns.
	titles := []string{"alpha", "beta", "gamma"}
	for _, title := range titles {
		if _, err := db.Jobs.Create(title, "desc", identity.RoleOrchestrator, nil); err != nil {
			t.Fatalf("Create %q: %v", title, err)
		}
	}

	jobs, err := db.Jobs.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != len(titles) {
		t.Fatalf("expected %d jobs, got %d", len(titles), len(jobs))
	}

	// All v0.3.0 fields must be nil on legacy rows.
	for _, j := range jobs {
		if j.WorktreeID != nil {
			t.Errorf("job %d: WorktreeID should be nil on legacy row", j.ID)
		}
		if j.ReviewedAt != nil {
			t.Errorf("job %d: ReviewedAt should be nil on legacy row", j.ID)
		}
	}
}

// Test_Jobs_ListJobsByStatus verifies filtering by status list.
func Test_Jobs_ListJobsByStatus(t *testing.T) {
	db := testdb.OpenTestDB(t)

	j1, _ := db.Jobs.Create("pending job", "", identity.RoleOrchestrator, nil)
	j2, _ := db.Jobs.Create("running job", "", identity.RoleOrchestrator, nil)
	j3, _ := db.Jobs.Create("done job", "", identity.RoleOrchestrator, nil)
	_ = db.Jobs.SetStatus(j2.ID, jobs.StatusRunning)
	_ = db.Jobs.SetStatus(j3.ID, jobs.StatusDone)

	results, err := db.Jobs.ListJobsByStatus([]string{jobs.StatusPending, jobs.StatusRunning})
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	got := make(map[int64]bool)
	for _, j := range results {
		got[j.ID] = true
	}
	if !got[j1.ID] || !got[j2.ID] {
		t.Errorf("expected j1 and j2 in results, got IDs: %v", got)
	}
	if got[j3.ID] {
		t.Errorf("done job should not appear in pending|running filter")
	}
}

// Test_Jobs_ListJobsWithLiveWorktrees verifies the worktree_id IS NOT NULL predicate.
func Test_Jobs_ListJobsWithLiveWorktrees(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// A legacy job (no worktree).
	_, _ = db.Jobs.Create("old style job", "", identity.RoleOrchestrator, nil)

	// A v0.3.0 job linked to a worktree.
	wt, err := db.Worktrees.Create("/tmp/worktrees/test", "feature/test", "master")
	if err != nil {
		t.Fatalf("Worktrees.Create: %v", err)
	}
	j2, _ := db.Jobs.Create("v0.3.0 job", "", identity.RoleOrchestrator, nil)
	if err := db.Jobs.SetV030Fields(j2.ID, &wt.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("SetV030Fields: %v", err)
	}

	live, err := db.Jobs.ListJobsWithLiveWorktrees()
	if err != nil {
		t.Fatalf("ListJobsWithLiveWorktrees: %v", err)
	}
	if len(live) != 1 || live[0].ID != j2.ID {
		t.Errorf("expected only j2 with live worktree, got %v", live)
	}
}

// Test_Jobs_SetReviewed verifies the reviewed_at timestamp and status update.
func Test_Jobs_SetReviewed(t *testing.T) {
	db := testdb.OpenTestDB(t)

	j, _ := db.Jobs.Create("review test", "", identity.RoleOrchestrator, nil)
	if err := db.Jobs.SetReviewed(j.ID, jobs.StatusAwaitingReview); err != nil {
		t.Fatalf("SetReviewed: %v", err)
	}
	j2, err := db.Jobs.Get(j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if j2.Status != jobs.StatusAwaitingReview {
		t.Errorf("status: got %q, want %q", j2.Status, jobs.StatusAwaitingReview)
	}
	if j2.ReviewedAt == nil {
		t.Error("ReviewedAt should be set after SetReviewed")
	}
}

// Test_Jobs_ListActiveJobs verifies the ListActiveJobs query: worktree JOIN,
// status filter, legacy job exclusion, and sort order.
func Test_Jobs_ListActiveJobs(t *testing.T) {
	db := testdb.OpenTestDB(t)

	// Create a shared worktree for the v0.3.0 jobs.
	wt, err := db.Worktrees.Create("/tmp/worktrees/active-test", "feature/active", "master")
	if err != nil {
		t.Fatalf("Worktrees.Create: %v", err)
	}

	// Three v0.3.0 jobs with the worktree attached, in different statuses.
	jAwaiting, _ := db.Jobs.Create("awaiting job", "", identity.RoleOrchestrator, nil)
	_ = db.Jobs.SetV030Fields(jAwaiting.ID, &wt.ID, nil, nil, nil, nil, nil, nil)
	_ = db.Jobs.SetStatus(jAwaiting.ID, jobs.StatusAwaitingReview)

	jRunning, _ := db.Jobs.Create("running job", "", identity.RoleOrchestrator, nil)
	_ = db.Jobs.SetV030Fields(jRunning.ID, &wt.ID, nil, nil, nil, nil, nil, nil)
	_ = db.Jobs.SetStatus(jRunning.ID, jobs.StatusRunning)

	jMerged, _ := db.Jobs.Create("merged job", "", identity.RoleOrchestrator, nil)
	_ = db.Jobs.SetV030Fields(jMerged.ID, &wt.ID, nil, nil, nil, nil, nil, nil)
	_ = db.Jobs.SetStatus(jMerged.ID, jobs.StatusMerged)

	// A legacy job (no worktree_id) — must not appear.
	_, _ = db.Jobs.Create("legacy job", "", identity.RoleOrchestrator, nil)

	results, err := db.Jobs.ListActiveJobs()
	if err != nil {
		t.Fatalf("ListActiveJobs: %v", err)
	}

	// Expect exactly jAwaiting and jRunning; jMerged and legacy are excluded.
	if len(results) != 2 {
		t.Fatalf("expected 2 active jobs, got %d", len(results))
	}

	got := make(map[int64]*jobs.ActiveJob)
	for _, aj := range results {
		got[aj.ID] = aj
	}
	if _, ok := got[jAwaiting.ID]; !ok {
		t.Errorf("jAwaiting (%d) not in results", jAwaiting.ID)
	}
	if _, ok := got[jRunning.ID]; !ok {
		t.Errorf("jRunning (%d) not in results", jRunning.ID)
	}
	if _, ok := got[jMerged.ID]; ok {
		t.Errorf("jMerged should be excluded (terminal status)")
	}

	// Branch metadata must be populated.
	for _, aj := range results {
		if aj.Branch != "feature/active" {
			t.Errorf("job %d: Branch = %q, want %q", aj.ID, aj.Branch, "feature/active")
		}
		if aj.ParentBranch != "master" {
			t.Errorf("job %d: ParentBranch = %q, want %q", aj.ID, aj.ParentBranch, "master")
		}
		if aj.WorktreePath != "/tmp/worktrees/active-test" {
			t.Errorf("job %d: WorktreePath = %q, want %q", aj.ID, aj.WorktreePath, "/tmp/worktrees/active-test")
		}
	}

	// Sort: newer job (jRunning, created after jAwaiting) should appear first.
	if results[0].ID != jRunning.ID {
		t.Errorf("expected jRunning first (newer), got ID %d", results[0].ID)
	}
}

// Test_ValidJobStatus covers all accepted and rejected status values.
func Test_ValidJobStatus(t *testing.T) {
	valid := []string{
		jobs.StatusPending, jobs.StatusRunning, jobs.StatusDone, jobs.StatusFailed,
		jobs.StatusBlocked, jobs.StatusCancelled,
		jobs.StatusAwaitingReview, jobs.StatusMerged, jobs.StatusRejected, jobs.StatusConflict,
	}
	for _, s := range valid {
		if !jobs.ValidJobStatus(s) {
			t.Errorf("jobs.ValidJobStatus(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "unknown", "PENDING", "awaiting review"}
	for _, s := range invalid {
		if jobs.ValidJobStatus(s) {
			t.Errorf("jobs.ValidJobStatus(%q) = true, want false", s)
		}
	}
}
