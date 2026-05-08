package db

import (
	"testing"
)

// TestConsiderActivationsTriggerSource verifies that each known trigger
// source value round-trips through Insert and is reflected on the row.
func TestConsiderActivationsTriggerSource(t *testing.T) {
	d := openTest(t)

	sess, err := d.Sessions.Create("test", "", "thinking_partner")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	sources := []string{"tui", "cli", "api", "tool"}
	ids := make([]int64, 0, len(sources))
	for _, src := range sources {
		id, err := d.ConsiderActivations.Insert(sess.ID, "Joy", 0.5, "thought", "question?", 12, src)
		if err != nil {
			t.Fatalf("insert %s: %v", src, err)
		}
		ids = append(ids, id)
	}

	// Read back and assert.
	for i, id := range ids {
		var got string
		err := d.sql.QueryRow(`SELECT trigger_source FROM consider_activations WHERE id = ?`, id).Scan(&got)
		if err != nil {
			t.Fatalf("scan %d: %v", id, err)
		}
		if got != sources[i] {
			t.Errorf("row %d: trigger_source = %q, want %q", id, got, sources[i])
		}
	}

	// Empty triggerSource defaults to "tui".
	id, err := d.ConsiderActivations.Insert(sess.ID, "Joy", 0.5, "t", "q?", 1, "")
	if err != nil {
		t.Fatalf("insert default: %v", err)
	}
	var got string
	if err := d.sql.QueryRow(`SELECT trigger_source FROM consider_activations WHERE id = ?`, id).Scan(&got); err != nil {
		t.Fatalf("scan default: %v", err)
	}
	if got != "tui" {
		t.Errorf("empty triggerSource: got %q, want tui", got)
	}
}
