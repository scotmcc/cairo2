// Package audit provides the append-only audit log gate.
//
// Phase 4.3: real implementation — SQLite-backed, append-only, with
// DELETE/UPDATE trigger enforcement. The cairo agent continues to use
// the NopSink (no access to the registry DB); cross-process audit
// ingestion is deferred to a later phase.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Event is a single audit record.
type Event struct {
	Timestamp time.Time         `json:"timestamp"`
	Actor     string            `json:"actor"`
	Gate      string            `json:"gate"`
	Action    string            `json:"action"`
	Target    string            `json:"target"`
	Decision  string            `json:"decision"`
	Reason    string            `json:"reason,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Sink writes audit events to a backing store.
type Sink interface {
	Write(ctx context.Context, e Event) error
}

// Reader queries audit events from a backing store.
type Reader interface {
	Query(ctx context.Context, f QueryFilter) ([]Event, error)
}

// QueryFilter selects events. Zero values mean "no constraint".
type QueryFilter struct {
	Actor  string
	Gate   string
	Action string
	Since  time.Time
	Until  time.Time
	Limit  int // 0 = default 100; cap at 1000
}

// NopSink discards all events. Used by the cairo agent and tests.
type NopSink struct{}

func (NopSink) Write(_ context.Context, _ Event) error { return nil }

// SQLiteSink writes to the audit_events table and supports Query.
type SQLiteSink struct{ db *sql.DB }

// NewSQLiteSink returns a SQLiteSink backed by the given *sql.DB.
// The DB must already have the audit_events table (created by the
// registry schema).
func NewSQLiteSink(db *sql.DB) *SQLiteSink { return &SQLiteSink{db: db} }

// Write inserts one audit event row. The caller must not modify e after calling Write.
func (s *SQLiteSink) Write(ctx context.Context, e Event) error {
	md, _ := json.Marshal(e.Metadata)
	if len(md) == 0 || string(md) == "null" {
		md = []byte("{}")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO audit_events
		 (timestamp, actor, gate, action, target, decision, reason, metadata)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Timestamp.Unix(), e.Actor, e.Gate, e.Action, e.Target,
		e.Decision, e.Reason, string(md),
	)
	return err
}

// Query returns events matching f, newest first. Limit defaults to 100; capped at 1000.
func (s *SQLiteSink) Query(ctx context.Context, f QueryFilter) ([]Event, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	where := []string{}
	args := []any{}

	if f.Actor != "" {
		where = append(where, "actor = ?")
		args = append(args, f.Actor)
	}
	if f.Gate != "" {
		where = append(where, "gate = ?")
		args = append(args, f.Gate)
	}
	if f.Action != "" {
		where = append(where, "action = ?")
		args = append(args, f.Action)
	}
	if !f.Since.IsZero() {
		where = append(where, "timestamp >= ?")
		args = append(args, f.Since.Unix())
	}
	if !f.Until.IsZero() {
		where = append(where, "timestamp <= ?")
		args = append(args, f.Until.Unix())
	}

	q := `SELECT id, timestamp, actor, gate, action, target, decision, reason, metadata
	      FROM audit_events`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY timestamp DESC, id DESC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []Event
	for rows.Next() {
		var e Event
		var ts int64
		var mdStr string
		var id int64
		if err := rows.Scan(&id, &ts, &e.Actor, &e.Gate, &e.Action, &e.Target, &e.Decision, &e.Reason, &mdStr); err != nil {
			return nil, err
		}
		e.Timestamp = time.Unix(ts, 0).UTC()
		if mdStr != "" && mdStr != "{}" {
			_ = json.Unmarshal([]byte(mdStr), &e.Metadata)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// Package-level default sink — set by the registry at boot, NopSink otherwise.
var (
	sinkMu      sync.RWMutex
	defaultSink Sink = NopSink{}
)

// SetDefaultSink replaces the package-level sink. Concurrency-safe.
func SetDefaultSink(s Sink) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	defaultSink = s
}

// Log records an audit event. Audit failures are logged to stderr and
// never returned to the caller — audit must never block a gate decision.
func Log(ctx context.Context, e Event) {
	sinkMu.RLock()
	s := defaultSink
	sinkMu.RUnlock()
	if err := s.Write(ctx, e); err != nil {
		log.Printf("audit: write failed: %v", err)
	}
}
