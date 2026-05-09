package registryserver

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestRegisterIdempotency(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	id1, at1, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
	if err != nil {
		t.Fatalf("first register: %v", err)
	}

	time.Sleep(time.Millisecond)

	id2, at2, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
	if err != nil {
		t.Fatalf("second register: %v", err)
	}

	if id1 != id2 {
		t.Errorf("agent_id changed: %q → %q", id1, id2)
	}
	if at1 != at2 {
		t.Errorf("registered_at changed: %d → %d", at1, at2)
	}

	var lastSeen int64
	if err := l.db.QueryRowContext(ctx, `SELECT last_seen_at FROM agents WHERE agent_id = ?`, id1).Scan(&lastSeen); err != nil {
		t.Fatalf("query last_seen_at: %v", err)
	}
	if lastSeen < at1 {
		t.Errorf("last_seen_at %d < registered_at %d, expected update", lastSeen, at1)
	}
}

func TestSweepStateTransition(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	agentID, _, err := l.Register(ctx, "", "bob", "server", "server.ts.net", "v1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	var status string
	if err := l.db.QueryRowContext(ctx, `SELECT status FROM agents WHERE agent_id = ?`, agentID).Scan(&status); err != nil {
		t.Fatalf("query status: %v", err)
	}
	if status != "active" {
		t.Errorf("expected active after register, got %q", status)
	}

	// Backdate last_seen_at past the 90s threshold.
	staleAt := time.Now().Add(-120 * time.Second).Unix()
	if _, err := l.db.ExecContext(ctx, `UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`, staleAt, agentID); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	n, err := l.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if n != 1 {
		t.Errorf("expected sweep to mark 1 row stale, got %d", n)
	}

	if err := l.db.QueryRowContext(ctx, `SELECT status FROM agents WHERE agent_id = ?`, agentID).Scan(&status); err != nil {
		t.Fatalf("query status after sweep: %v", err)
	}
	if status != "stale" {
		t.Errorf("expected stale after sweep, got %q", status)
	}

	// Re-register should flip back to active.
	_, _, err = l.Register(ctx, "", "bob", "server", "server.ts.net", "v1")
	if err != nil {
		t.Fatalf("re-register: %v", err)
	}

	if err := l.db.QueryRowContext(ctx, `SELECT status FROM agents WHERE agent_id = ?`, agentID).Scan(&status); err != nil {
		t.Fatalf("query status after re-register: %v", err)
	}
	if status != "active" {
		t.Errorf("expected active after re-register, got %q", status)
	}

	var lastSeen int64
	if err := l.db.QueryRowContext(ctx, `SELECT last_seen_at FROM agents WHERE agent_id = ?`, agentID).Scan(&lastSeen); err != nil {
		t.Fatalf("query last_seen_at: %v", err)
	}
	if lastSeen <= staleAt {
		t.Errorf("last_seen_at %d not updated after re-register (staleAt was %d)", lastSeen, staleAt)
	}
}

func TestCountAgents(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()

	c, err := l.CountAgents(ctx)
	if err != nil {
		t.Fatalf("count empty: %v", err)
	}
	if c.Total != 0 || c.Active != 0 || c.Stale != 0 || c.WsConnected != 0 {
		t.Errorf("empty counts: got %+v, want zeros", c)
	}

	id1, _, _ := l.Register(ctx, "", "alice", "a", "a.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "bob", "b", "b.ts.net", "v1")
	_ = l.SetWsConnected(ctx, id1, true)

	c, err = l.CountAgents(ctx)
	if err != nil {
		t.Fatalf("count two: %v", err)
	}
	if c.Total != 2 || c.Active != 2 || c.Stale != 0 || c.WsConnected != 1 {
		t.Errorf("two-agent counts: got %+v, want total=2 active=2 stale=0 ws=1", c)
	}

	// Mark one stale by direct UPDATE (faster than waiting on the sweeper threshold).
	_, _ = l.db.ExecContext(ctx, `UPDATE agents SET status='stale' WHERE owner='bob'`)

	c, err = l.CountAgents(ctx)
	if err != nil {
		t.Fatalf("count post-stale: %v", err)
	}
	if c.Total != 2 || c.Active != 1 || c.Stale != 1 {
		t.Errorf("post-stale counts: got %+v, want total=2 active=1 stale=1", c)
	}
}

func TestListByOwner(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	idA1, _, _ := l.Register(ctx, "", "alice", "a1", "a1.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "alice", "a2", "a2.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "bob", "b1", "b1.ts.net", "v1")

	aliceAgents, err := l.ListByOwner(ctx, "alice")
	if err != nil {
		t.Fatalf("ListByOwner alice: %v", err)
	}
	if len(aliceAgents) != 2 {
		t.Errorf("expected 2 alice agents, got %d", len(aliceAgents))
	}
	for _, a := range aliceAgents {
		if a.Owner != "alice" {
			t.Errorf("unexpected owner %q in alice's list", a.Owner)
		}
	}

	bobAgents, err := l.ListByOwner(ctx, "bob")
	if err != nil {
		t.Fatalf("ListByOwner bob: %v", err)
	}
	if len(bobAgents) != 1 {
		t.Errorf("expected 1 bob agent, got %d", len(bobAgents))
	}

	carolAgents, err := l.ListByOwner(ctx, "carol")
	if err != nil {
		t.Fatalf("ListByOwner carol: %v", err)
	}
	if len(carolAgents) != 0 {
		t.Errorf("expected 0 carol agents, got %d", len(carolAgents))
	}
	_ = idA1
}

func TestGetByOwner(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	aliceID, _, _ := l.Register(ctx, "", "alice", "a-host", "a.ts.net", "v1")
	_, _, _ = l.Register(ctx, "", "bob", "b-host", "b.ts.net", "v1")

	t.Run("hit", func(t *testing.T) {
		a, err := l.GetByOwner(ctx, aliceID, "alice")
		if err != nil {
			t.Fatalf("GetByOwner: %v", err)
		}
		if a == nil {
			t.Fatal("expected agent, got nil")
		}
		if a.AgentID != aliceID {
			t.Errorf("expected %s, got %s", aliceID, a.AgentID)
		}
	})

	t.Run("miss-by-id", func(t *testing.T) {
		a, err := l.GetByOwner(ctx, "00000000-0000-0000-0000-000000000000", "alice")
		if err != nil {
			t.Fatalf("GetByOwner: %v", err)
		}
		if a != nil {
			t.Errorf("expected nil for unknown id, got %+v", a)
		}
	})

	t.Run("miss-by-owner-mismatch", func(t *testing.T) {
		a, err := l.GetByOwner(ctx, aliceID, "bob")
		if err != nil {
			t.Fatalf("GetByOwner: %v", err)
		}
		if a != nil {
			t.Errorf("expected nil for owner mismatch, got %+v", a)
		}
	})
}

func TestRegister_StableAgentID(t *testing.T) {
	t.Run("honor matching agent_id", func(t *testing.T) {
		l, err := OpenLedger(t.TempDir() + "/test.db")
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		defer l.Close()

		ctx := context.Background()
		id1, at1, err := l.Register(ctx, "", "alice", "lap1", "lap1.ts.net", "v1")
		if err != nil {
			t.Fatalf("first register: %v", err)
		}

		// Re-register with same agent_id but different hostname.
		id2, at2, err := l.Register(ctx, id1, "alice", "renamed-host", "lap1.ts.net", "v1")
		if err != nil {
			t.Fatalf("second register: %v", err)
		}
		if id2 != id1 {
			t.Errorf("expected same agent_id %q, got %q", id1, id2)
		}
		if at2 != at1 {
			t.Errorf("expected registered_at unchanged %d, got %d", at1, at2)
		}

		// Verify hostname was updated and only one row exists.
		var hostname string
		var count int
		if err := l.db.QueryRowContext(ctx, `SELECT hostname FROM agents WHERE agent_id = ?`, id1).Scan(&hostname); err != nil {
			t.Fatalf("query hostname: %v", err)
		}
		if hostname != "renamed-host" {
			t.Errorf("expected hostname=renamed-host, got %q", hostname)
		}
		if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 row, got %d", count)
		}
	})

	t.Run("owner mismatch falls through", func(t *testing.T) {
		l, err := OpenLedger(t.TempDir() + "/test.db")
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		defer l.Close()

		ctx := context.Background()
		id1, _, err := l.Register(ctx, "", "alice", "lap1", "lap1.ts.net", "v1")
		if err != nil {
			t.Fatalf("alice register: %v", err)
		}

		// Bob attempts to claim alice's agent_id — owner mismatch, falls through to new row.
		id2, _, err := l.Register(ctx, id1, "bob", "lap1", "lap1.ts.net", "v1")
		if err != nil {
			t.Fatalf("bob register: %v", err)
		}
		if id2 == id1 {
			t.Errorf("expected new agent_id for bob, got same as alice: %q", id1)
		}

		// Two rows should exist: alice's original + bob's new row.
		var count int
		if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 2 {
			t.Errorf("expected 2 rows, got %d", count)
		}
	})

	t.Run("empty agent_id uses legacy path", func(t *testing.T) {
		l, err := OpenLedger(t.TempDir() + "/test.db")
		if err != nil {
			t.Fatalf("open ledger: %v", err)
		}
		defer l.Close()

		ctx := context.Background()
		id1, at1, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
		if err != nil {
			t.Fatalf("first register: %v", err)
		}

		// Passing empty requestedAgentID should behave identically to the old signature.
		id2, at2, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
		if err != nil {
			t.Fatalf("second register: %v", err)
		}
		if id1 != id2 {
			t.Errorf("expected same agent_id, got %q → %q", id1, id2)
		}
		if at1 != at2 {
			t.Errorf("expected same registered_at, got %d → %d", at1, at2)
		}

		var count int
		if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM agents`).Scan(&count); err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 1 {
			t.Errorf("expected 1 row, got %d", count)
		}
	})
}

func TestRevokeStatusChange(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	agentID, _, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	if err := l.Revoke(ctx, agentID, "alice"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	status, err := l.GetStatus(ctx, agentID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "revoked" {
		t.Errorf("expected status=revoked, got %q", status)
	}

	if err := l.Revoke(ctx, "missing-id", "alice"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows for unknown id, got %v", err)
	}

	if err := l.Revoke(ctx, agentID, "bob"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows for owner mismatch, got %v", err)
	}
}

func TestRevokedAgentRejectsReRegister(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()

	t.Run("requestedAgentID path", func(t *testing.T) {
		agentID, _, err := l.Register(ctx, "", "alice", "lap1", "lap1.ts.net", "v1")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := l.Revoke(ctx, agentID, "alice"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		_, _, err = l.Register(ctx, agentID, "alice", "lap1", "lap1.ts.net", "v1")
		if !errors.Is(err, ErrRevoked) {
			t.Errorf("expected ErrRevoked on re-register via agent_id, got %v", err)
		}
	})

	t.Run("legacy path", func(t *testing.T) {
		agentID, _, err := l.Register(ctx, "", "bob", "srv", "srv.ts.net", "v1")
		if err != nil {
			t.Fatalf("register: %v", err)
		}
		if err := l.Revoke(ctx, agentID, "bob"); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		_, _, err = l.Register(ctx, "", "bob", "srv", "srv.ts.net", "v1")
		if !errors.Is(err, ErrRevoked) {
			t.Errorf("expected ErrRevoked on re-register via legacy path, got %v", err)
		}
	})
}

func TestGetStatus(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	agentID, _, err := l.Register(ctx, "", "alice", "box", "box.ts.net", "v1")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	status, err := l.GetStatus(ctx, agentID)
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if status != "active" {
		t.Errorf("expected active, got %q", status)
	}

	_, err = l.GetStatus(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("expected sql.ErrNoRows for unknown agent, got %v", err)
	}
}

func TestInsertCommand(t *testing.T) {
	l, err := OpenLedger(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer l.Close()

	ctx := context.Background()
	if err := l.InsertCommand(ctx, "alice", "echo hello"); err != nil {
		t.Fatalf("insert command: %v", err)
	}
	if err := l.InsertCommand(ctx, "alice", "echo world"); err != nil {
		t.Fatalf("insert command 2: %v", err)
	}

	var count int
	if err := l.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM commands`).Scan(&count); err != nil {
		t.Fatalf("count commands: %v", err)
	}
	if count < 2 {
		t.Errorf("expected at least 2 rows, got %d", count)
	}

	var operator, command string
	var createdAt int64
	if err := l.db.QueryRowContext(ctx,
		`SELECT operator, command, created_at FROM commands ORDER BY id LIMIT 1`,
	).Scan(&operator, &command, &createdAt); err != nil {
		t.Fatalf("query first command: %v", err)
	}
	if operator != "alice" || command != "echo hello" {
		t.Errorf("first row mismatch: operator=%q command=%q", operator, command)
	}
	if createdAt <= 0 {
		t.Errorf("created_at not set: %d", createdAt)
	}
}
