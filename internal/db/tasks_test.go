package db

import (
	"testing"
)

func TestSweepOrphans_bogus_pid(t *testing.T) {
	db := openTest(t)

	job, err := db.Jobs.Create("sweep test job", "desc", RoleOrchestrator, nil)
	if err != nil {
		t.Fatalf("Jobs.Create: %v", err)
	}

	task, err := db.Tasks.Create(job.ID, "sweep test task", "desc", "coder", "")
	if err != nil {
		t.Fatalf("Tasks.Create: %v", err)
	}

	// Force status to running; default after Create is pending.
	if err := db.Tasks.SetStatusAndResult(task.ID, "running", ""); err != nil {
		t.Fatalf("SetStatusAndResult: %v", err)
	}
	if err := db.Tasks.SetPIDAndToken(task.ID, 9999999, "fake-token"); err != nil {
		t.Fatalf("SetPIDAndToken: %v", err)
	}

	n, err := db.Tasks.SweepOrphans()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 swept, got %d", n)
	}

	updated, err := db.Tasks.Get(task.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if updated.Status != "failed" {
		t.Errorf("status: got %q, want %q", updated.Status, "failed")
	}
	if updated.Result != "orphaned (cairo restarted)" {
		t.Errorf("result: got %q, want %q", updated.Result, "orphaned (cairo restarted)")
	}
	if updated.CompletedAt == nil {
		t.Error("completed_at not set after sweep")
	}
}
