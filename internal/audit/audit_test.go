package audit

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testSchema = `
CREATE TABLE IF NOT EXISTS audit_events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp   INTEGER NOT NULL,
  actor       TEXT    NOT NULL,
  gate        TEXT    NOT NULL,
  action      TEXT    NOT NULL,
  target      TEXT    NOT NULL,
  decision    TEXT    NOT NULL,
  reason      TEXT    NOT NULL DEFAULT '',
  metadata    TEXT    NOT NULL DEFAULT '{}'
);
CREATE TRIGGER IF NOT EXISTS audit_events_no_update
  BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only: UPDATE rejected'); END;
CREATE TRIGGER IF NOT EXISTS audit_events_no_delete
  BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only: DELETE rejected'); END`

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range splitSchema(testSchema) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec schema stmt %q: %v", stmt, err)
		}
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// splitSchema splits SQL on semicolons, aware of BEGIN...END blocks.
func splitSchema(s string) []string {
	var stmts []string
	depth := 0
	start := 0
	upper := strings.ToUpper(s)
	for i := 0; i < len(s); i++ {
		if i+5 <= len(s) && upper[i:i+5] == "BEGIN" {
			if (i == 0 || !isAlnumByte(s[i-1])) && (i+5 >= len(s) || !isAlnumByte(s[i+5])) {
				depth++
			}
		}
		if i+3 <= len(s) && upper[i:i+3] == "END" {
			if (i == 0 || !isAlnumByte(s[i-1])) && (i+3 >= len(s) || !isAlnumByte(s[i+3])) {
				if depth > 0 {
					depth--
				}
			}
		}
		if s[i] == ';' && depth == 0 {
			stmt := strings.TrimSpace(s[start:i])
			start = i + 1
			if stmt != "" {
				stmts = append(stmts, stmt)
			}
		}
	}
	if stmt := strings.TrimSpace(s[start:]); stmt != "" {
		stmts = append(stmts, stmt)
	}
	return stmts
}

func isAlnumByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func TestNopSink_WriteAlwaysNil(t *testing.T) {
	var s NopSink
	e := Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "agent.list",
		Target:    "agents",
		Decision:  "granted",
	}
	if err := s.Write(context.Background(), e); err != nil {
		t.Errorf("NopSink.Write returned non-nil error: %v", err)
	}
}

func TestSQLiteSink_AppendAndQuery(t *testing.T) {
	db := openTestDB(t)
	sink := NewSQLiteSink(db)
	ctx := context.Background()

	now := time.Now().UTC()
	events := []Event{
		{Timestamp: now.Add(-2 * time.Second), Actor: "alice", Gate: "access", Action: "agent.list", Target: "agents", Decision: "granted"},
		{Timestamp: now.Add(-1 * time.Second), Actor: "bob", Gate: "access", Action: "agent.get", Target: "abc-123", Decision: "denied", Reason: "not owner"},
		{Timestamp: now, Actor: "alice", Gate: "access", Action: "broadcast", Target: "broadcast", Decision: "denied", Reason: "super-admin required"},
	}
	for _, e := range events {
		if err := sink.Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got, err := sink.Query(ctx, QueryFilter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 events, got %d", len(got))
	}
	// Newest first.
	if got[0].Actor != "alice" || got[0].Action != "broadcast" {
		t.Errorf("expected alice/broadcast first, got %s/%s", got[0].Actor, got[0].Action)
	}
	if got[2].Actor != "alice" || got[2].Action != "agent.list" {
		t.Errorf("expected alice/agent.list last, got %s/%s", got[2].Actor, got[2].Action)
	}
}

func TestSQLiteSink_QueryFilters(t *testing.T) {
	db := openTestDB(t)
	sink := NewSQLiteSink(db)
	ctx := context.Background()

	// Use a fixed epoch-aligned base to avoid sub-second rounding surprises.
	base := time.Unix(time.Now().Unix(), 0).UTC()
	events := []Event{
		{Timestamp: base.Add(-10 * time.Second), Actor: "alice", Gate: "access", Action: "agent.list", Target: "agents", Decision: "granted"},
		{Timestamp: base.Add(-4 * time.Second), Actor: "bob", Gate: "admin", Action: "broadcast", Target: "broadcast", Decision: "denied"},
		{Timestamp: base.Add(-2 * time.Second), Actor: "alice", Gate: "admin", Action: "broadcast", Target: "broadcast", Decision: "denied"},
	}
	for _, e := range events {
		if err := sink.Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	t.Run("actor filter", func(t *testing.T) {
		got, err := sink.Query(ctx, QueryFilter{Actor: "alice"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 alice events, got %d", len(got))
		}
	})

	t.Run("gate filter", func(t *testing.T) {
		got, err := sink.Query(ctx, QueryFilter{Gate: "admin"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 admin-gate events, got %d", len(got))
		}
	})

	t.Run("action filter", func(t *testing.T) {
		got, err := sink.Query(ctx, QueryFilter{Action: "agent.list"})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 agent.list event, got %d", len(got))
		}
	})

	t.Run("since filter", func(t *testing.T) {
		// since base-5s: includes base-4s and base-2s (2 events).
		got, err := sink.Query(ctx, QueryFilter{Since: base.Add(-5 * time.Second)})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("expected 2 events since -5s, got %d", len(got))
		}
	})

	t.Run("until filter", func(t *testing.T) {
		// until base-5s: includes only base-10s (1 event).
		got, err := sink.Query(ctx, QueryFilter{Until: base.Add(-5 * time.Second)})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 event until -5s, got %d", len(got))
		}
	})

	t.Run("limit", func(t *testing.T) {
		got, err := sink.Query(ctx, QueryFilter{Limit: 1})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("expected 1 event with limit=1, got %d", len(got))
		}
	})

	t.Run("default limit 100", func(t *testing.T) {
		got, err := sink.Query(ctx, QueryFilter{Limit: 0})
		if err != nil {
			t.Fatalf("Query: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("expected 3 events with default limit, got %d", len(got))
		}
	})
}

func TestSQLiteSink_AppendOnly_NoUpdate(t *testing.T) {
	db := openTestDB(t)
	sink := NewSQLiteSink(db)
	ctx := context.Background()

	e := Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "agent.list",
		Target:    "agents",
		Decision:  "granted",
	}
	if err := sink.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	_, err := db.ExecContext(ctx, `UPDATE audit_events SET actor = 'mallory'`)
	if err == nil {
		t.Fatal("expected error from UPDATE trigger, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("expected 'append-only' in error, got: %v", err)
	}
}

func TestSQLiteSink_AppendOnly_NoDelete(t *testing.T) {
	db := openTestDB(t)
	sink := NewSQLiteSink(db)
	ctx := context.Background()

	e := Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "agent.list",
		Target:    "agents",
		Decision:  "granted",
	}
	if err := sink.Write(ctx, e); err != nil {
		t.Fatalf("Write: %v", err)
	}

	_, err := db.ExecContext(ctx, `DELETE FROM audit_events`)
	if err == nil {
		t.Fatal("expected error from DELETE trigger, got nil")
	}
	if !strings.Contains(err.Error(), "append-only") {
		t.Errorf("expected 'append-only' in error, got: %v", err)
	}
}

func TestSetDefaultSink_IsConcurrencySafe(t *testing.T) {
	// Swap the default sink concurrently to verify no data races.
	var wg sync.WaitGroup
	ctx := context.Background()
	db := openTestDB(t)
	sqlite := NewSQLiteSink(db)
	nop := NopSink{}

	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			SetDefaultSink(sqlite)
		}()
		go func() {
			defer wg.Done()
			Log(ctx, Event{
				Timestamp: time.Now(),
				Actor:     "test",
				Gate:      "access",
				Action:    "test.action",
				Target:    "test",
				Decision:  "granted",
			})
		}()
	}
	wg.Wait()
	// Restore NopSink.
	SetDefaultSink(nop)
}

func TestLog_AuditFailureDoesNotPanic(t *testing.T) {
	original := defaultSink
	defer SetDefaultSink(original)

	SetDefaultSink(&errorSink{})

	// Log must not panic even when the sink errors.
	Log(context.Background(), Event{
		Timestamp: time.Now(),
		Actor:     "alice",
		Gate:      "access",
		Action:    "agent.list",
		Target:    "agents",
		Decision:  "granted",
	})
}

type errorSink struct{}

func (errorSink) Write(_ context.Context, _ Event) error {
	return &testError{msg: "simulated sink failure"}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
