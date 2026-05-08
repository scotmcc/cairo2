package agent

import (
	"context"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// TestConsiderInput_NoAspectsShortCircuits verifies that ConsiderInput returns
// an empty result and writes nothing when no consider_aspects are enabled —
// the guard inside consider.RunWithResultForced (consider.go:188). This is
// the path background workers and CLI bare-query callers ultimately rely on
// when consider feature is off; the lighter property here is enough to prove
// the canonical entry point doesn't write activation rows in that mode.
func TestConsiderInput_NoAspectsShortCircuits(t *testing.T) {
	d := openConsiderTestDB(t)

	// Disable all aspects so RunWithResultForced returns empty result.
	if _, err := d.SQL().Exec(`UPDATE consider_aspects SET enabled = 0`); err != nil {
		t.Fatalf("disable aspects: %v", err)
	}

	sess, err := d.Sessions.Create("test", "", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	userMsg, err := d.Messages.AddWithInnerVoice(sess.ID, "user", "hi", "", "", "", "")
	if err != nil {
		t.Fatalf("add user msg: %v", err)
	}

	// LLM client is nil — won't be hit because aspect list is empty.
	result, innerVoice, err := ConsiderInput(context.Background(), d, nil, nil, sess.ID, "thinking_partner", userMsg.ID, "hi", "tui")
	if err != nil {
		t.Fatalf("ConsiderInput: %v", err)
	}
	if innerVoice != "" {
		t.Errorf("innerVoice = %q, want empty", innerVoice)
	}
	if result.Summary != "" {
		t.Errorf("Summary = %q, want empty", result.Summary)
	}

	// Confirm zero activation rows landed.
	var n int
	if err := d.SQL().QueryRow(`SELECT COUNT(*) FROM consider_activations WHERE session_id = ?`, sess.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("activation count = %d, want 0", n)
	}
}

// openConsiderTestDB opens a test DB. Uses OpenAt directly because the agent
// package can't import the db package's test helper.
func openConsiderTestDB(t *testing.T) *sqliteopen.DB {
	t.Helper()
	path := t.TempDir() + "/test.db"
	d, err := sqliteopen.OpenAt(path)
	if err != nil {
		t.Fatalf("OpenAt: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}
