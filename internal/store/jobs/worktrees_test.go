package jobs_test

import (
	"testing"

	"github.com/scotmcc/cairo2/internal/store/identity"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// Test_Worktrees_CRUD verifies the basic create/get/update/delete round-trip.
func Test_Worktrees_CRUD(t *testing.T) {
	db := testdb.OpenTestDB(t)

	wt, err := db.Worktrees.Create("/tmp/worktrees/branch-a", "feature/branch-a", "master")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if wt.ID == 0 {
		t.Fatal("expected non-zero ID after Create")
	}
	if wt.Path != "/tmp/worktrees/branch-a" {
		t.Errorf("Path: got %q", wt.Path)
	}
	if wt.Branch != "feature/branch-a" {
		t.Errorf("Branch: got %q", wt.Branch)
	}
	if wt.ParentBranch != "master" {
		t.Errorf("ParentBranch: got %q", wt.ParentBranch)
	}
	if wt.PushPending {
		t.Error("PushPending should default to false")
	}

	// Update push_pending via the targeted helper.
	if err := db.Worktrees.SetPushPending(wt.ID, true); err != nil {
		t.Fatalf("SetPushPending: %v", err)
	}
	wt2, err := db.Worktrees.Get(wt.ID)
	if err != nil {
		t.Fatalf("Get after SetPushPending: %v", err)
	}
	if !wt2.PushPending {
		t.Error("PushPending should be true after SetPushPending(true)")
	}

	// Full update via Update.
	wt2.Path = "/tmp/worktrees/renamed"
	wt2.PushPending = false
	if err := db.Worktrees.Update(wt2); err != nil {
		t.Fatalf("Update: %v", err)
	}
	wt3, _ := db.Worktrees.Get(wt.ID)
	if wt3.Path != "/tmp/worktrees/renamed" {
		t.Errorf("Path after Update: got %q", wt3.Path)
	}
	if wt3.PushPending {
		t.Error("PushPending should be false after Update")
	}

	// Delete.
	if err := db.Worktrees.Delete(wt.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = db.Worktrees.Get(wt.ID)
	if err == nil {
		t.Error("Get after Delete should return an error")
	}
}

// Test_Worktrees_List verifies listing and the push-pending filter.
func Test_Worktrees_List(t *testing.T) {
	db := testdb.OpenTestDB(t)

	wt1, _ := db.Worktrees.Create("/tmp/a", "branch-a", "master")
	wt2, _ := db.Worktrees.Create("/tmp/b", "branch-b", "master")
	wt3, _ := db.Worktrees.Create("/tmp/c", "branch-c", "master")

	_ = db.Worktrees.SetPushPending(wt1.ID, true)
	_ = db.Worktrees.SetPushPending(wt3.ID, true)

	all, err := db.Worktrees.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("List: expected 3, got %d", len(all))
	}

	pending, err := db.Worktrees.ListWithPendingPush()
	if err != nil {
		t.Fatalf("ListWithPendingPush: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("ListWithPendingPush: expected 2, got %d", len(pending))
	}
	ids := map[int64]bool{pending[0].ID: true, pending[1].ID: true}
	if !ids[wt1.ID] || !ids[wt3.ID] {
		t.Errorf("expected wt1 and wt3 in pending list, got %v", ids)
	}
	if ids[wt2.ID] {
		t.Error("wt2 should not appear in pending push list")
	}
}

// Test_Worktrees_DeleteCascadesToJob verifies that deleting a worktree row
// clears jobs.worktree_id via ON DELETE SET NULL without removing the job.
func Test_Worktrees_DeleteCascadesToJob(t *testing.T) {
	db := testdb.OpenTestDB(t)

	wt, err := db.Worktrees.Create("/tmp/cascade-test", "feature/cascade", "master")
	if err != nil {
		t.Fatalf("Create worktree: %v", err)
	}

	j, err := db.Jobs.Create("cascade job", "", identity.RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}
	if err := db.Jobs.SetV030Fields(j.ID, &wt.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("SetV030Fields: %v", err)
	}

	// Confirm the FK is wired.
	linked, err := db.Jobs.Get(j.ID)
	if err != nil {
		t.Fatalf("Get job: %v", err)
	}
	if linked.WorktreeID == nil || *linked.WorktreeID != wt.ID {
		t.Fatalf("WorktreeID not set correctly, got %v", linked.WorktreeID)
	}

	// Delete the worktree — job must survive with worktree_id = NULL.
	if err := db.Worktrees.Delete(wt.ID); err != nil {
		t.Fatalf("Delete worktree: %v", err)
	}

	after, err := db.Jobs.Get(j.ID)
	if err != nil {
		t.Fatalf("Get job after worktree delete: %v", err)
	}
	if after.WorktreeID != nil {
		t.Errorf("worktree_id should be NULL after worktree deletion, got %v", after.WorktreeID)
	}
	if after.Title != j.Title {
		t.Errorf("job title changed unexpectedly: got %q", after.Title)
	}
}

// Test_Worktrees_JobFKLiveFilter confirms ListJobsWithLiveWorktrees updates
// correctly after a worktree is deleted.
func Test_Worktrees_JobFKLiveFilter(t *testing.T) {
	db := testdb.OpenTestDB(t)

	wt, _ := db.Worktrees.Create("/tmp/live-filter", "feature/x", "master")
	j, _ := db.Jobs.Create("live filter job", "", identity.RoleOrchestrator, nil)
	_ = db.Jobs.SetV030Fields(j.ID, &wt.ID, nil, nil, nil, nil, nil, nil)

	live, _ := db.Jobs.ListJobsWithLiveWorktrees()
	if len(live) != 1 {
		t.Fatalf("expected 1 live job before delete, got %d", len(live))
	}

	_ = db.Worktrees.Delete(wt.ID)

	live2, _ := db.Jobs.ListJobsWithLiveWorktrees()
	if len(live2) != 0 {
		t.Errorf("expected 0 live jobs after worktree delete, got %d", len(live2))
	}
}
