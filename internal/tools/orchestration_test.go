package tools

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

func openOrchTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	database, err := sqliteopen.OpenAt(filepath.Join(t.TempDir(), "orch_test.db"))
	if err != nil {
		t.Fatalf("sqliteopen.OpenAt: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func TestJobTool_doCreate_NoBriefing(t *testing.T) {
	database := openOrchTestDB(t)
	tool := jobTool{db: database}
	res := tool.doCreate(map[string]any{
		"title":       "test job",
		"description": "no briefing path",
	}, nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "job created (id:") {
		t.Errorf("expected 'job created' prefix, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "next:") {
		t.Errorf("no-briefing path should not emit 'next:' nudge, got: %s", res.Content)
	}
}

func TestJobTool_doCreate_WithBriefing(t *testing.T) {
	database := openOrchTestDB(t)
	tool := jobTool{db: database}
	res := tool.doCreate(map[string]any{
		"title":       "test job with briefing",
		"description": "briefing path",
		"briefing":    "## Goal\nTest the nudge.\n",
	}, nil)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if !strings.HasPrefix(res.Content, "job created (id:") {
		t.Errorf("expected 'job created' prefix, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "To dispatch") {
		t.Errorf("briefing path should emit dispatch advisory, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "worktree(action=\"create\"") {
		t.Errorf("nudge should mention worktree create action, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "job_id=") {
		t.Errorf("nudge should mention job_id= in worktree create call, got: %s", res.Content)
	}
	if strings.Contains(res.Content, "UPDATE jobs") {
		t.Errorf("nudge must not contain 'UPDATE jobs' (regression guard), got: %s", res.Content)
	}
}
