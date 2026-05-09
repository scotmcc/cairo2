package registryserver

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS agents (
  agent_id      TEXT PRIMARY KEY,
  hostname      TEXT NOT NULL,
  owner         TEXT NOT NULL,
  tailnet_node  TEXT NOT NULL,
  version       TEXT NOT NULL,
  registered_at INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  status        TEXT NOT NULL,
  ws_connected  INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_agents_owner_host ON agents(owner, hostname);
CREATE TABLE IF NOT EXISTS commands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  operator   TEXT NOT NULL,
  command    TEXT NOT NULL,
  created_at INTEGER NOT NULL
)`

// ErrRevoked indicates an attempt to register or use an agent whose status is 'revoked'.
var ErrRevoked = errors.New("agent revoked")

// Agent is a row from the agents table.
type Agent struct {
	AgentID      string `json:"agent_id"`
	Hostname     string `json:"hostname"`
	Owner        string `json:"owner"`
	TailnetNode  string `json:"tailnet_node"`
	Version      string `json:"version"`
	RegisteredAt int64  `json:"registered_at"`
	LastSeenAt   int64  `json:"last_seen_at"`
	Status       string `json:"status"`
	WsConnected  int    `json:"ws_connected"`
}

// Ledger wraps the sqlite DB for agent lifecycle operations.
type Ledger struct {
	db *sql.DB
}

// OpenLedger opens (or creates) the sqlite DB at path and applies the schema.
func OpenLedger(path string) (*Ledger, error) {
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := execSchema(db, schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Ledger{db: db}, nil
}

// Register upserts an agent. When requestedAgentID is non-empty and (agent_id, owner)
// matches an existing row, that row is updated in place and the same agent_id is
// returned (stable identity across hostname changes). Otherwise falls through to the
// existing (owner, hostname, tailnet_node) lookup-or-insert.
func (l *Ledger) Register(ctx context.Context, requestedAgentID, owner, hostname, tailnetNode, version string) (agentID string, registeredAt int64, err error) {
	tx, err := l.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().Unix()

	if requestedAgentID != "" {
		var existingAt int64
		var existingStatus string
		lookupErr := tx.QueryRowContext(ctx,
			`SELECT registered_at, status FROM agents WHERE agent_id = ? AND owner = ?`,
			requestedAgentID, owner,
		).Scan(&existingAt, &existingStatus)
		if lookupErr == nil {
			if existingStatus == "revoked" {
				_ = tx.Rollback()
				return "", 0, ErrRevoked
			}
			_, err = tx.ExecContext(ctx,
				`UPDATE agents SET hostname = ?, tailnet_node = ?, version = ?, last_seen_at = ?, status = 'active' WHERE agent_id = ?`,
				hostname, tailnetNode, version, now, requestedAgentID,
			)
			if err != nil {
				return "", 0, err
			}
			if err = tx.Commit(); err != nil {
				return "", 0, err
			}
			return requestedAgentID, existingAt, nil
		}
		if lookupErr != sql.ErrNoRows {
			return "", 0, lookupErr
		}
		log.Printf("register: agent_id=%s not honored (not found or owner mismatch); falling back to (owner,hostname,tailnet_node) lookup", requestedAgentID)
	}

	var existingID string
	var existingAt int64
	var existingStatus string
	err = tx.QueryRowContext(ctx,
		`SELECT agent_id, registered_at, status FROM agents WHERE owner = ? AND hostname = ? AND tailnet_node = ?`,
		owner, hostname, tailnetNode,
	).Scan(&existingID, &existingAt, &existingStatus)

	if err == nil {
		if existingStatus == "revoked" {
			_ = tx.Rollback()
			return "", 0, ErrRevoked
		}
		// Row found — update last_seen_at, status, version.
		_, err = tx.ExecContext(ctx,
			`UPDATE agents SET last_seen_at = ?, status = 'active', version = ? WHERE owner = ? AND hostname = ? AND tailnet_node = ?`,
			now, version, owner, hostname, tailnetNode,
		)
		if err != nil {
			return "", 0, err
		}
		if err = tx.Commit(); err != nil {
			return "", 0, err
		}
		return existingID, existingAt, nil
	}
	if err != sql.ErrNoRows {
		return "", 0, err
	}

	// No existing row — insert new agent.
	newID := uuid.New().String()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO agents (agent_id, hostname, owner, tailnet_node, version, registered_at, last_seen_at, status, ws_connected)
		 VALUES (?, ?, ?, ?, ?, ?, ?, 'active', 0)`,
		newID, hostname, owner, tailnetNode, version, now, now,
	)
	if err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return newID, now, nil
}

// Sweep marks active agents with last_seen_at older than 90s as stale.
// Returns the number of rows updated.
func (l *Ledger) Sweep(ctx context.Context) (int64, error) {
	threshold := time.Now().Add(-90 * time.Second).Unix()
	result, err := l.db.ExecContext(ctx,
		`UPDATE agents SET status = 'stale' WHERE status = 'active' AND last_seen_at < ? AND ws_connected = 0`,
		threshold,
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// List returns all agents ordered by last_seen_at descending.
func (l *Ledger) List(ctx context.Context) ([]Agent, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT agent_id, hostname, owner, tailnet_node, version, registered_at, last_seen_at, status, ws_connected
		 FROM agents ORDER BY last_seen_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.AgentID, &a.Hostname, &a.Owner, &a.TailnetNode, &a.Version,
			&a.RegisteredAt, &a.LastSeenAt, &a.Status, &a.WsConnected); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// Counts is a snapshot of agent counts for /healthz.
type Counts struct {
	Total       int64 `json:"total"`
	Active      int64 `json:"active"`
	Stale       int64 `json:"stale"`
	WsConnected int64 `json:"ws_connected"`
}

// CountAgents returns total / active / stale / ws_connected counts in a single query.
func (l *Ledger) CountAgents(ctx context.Context) (Counts, error) {
	var c Counts
	err := l.db.QueryRowContext(ctx,
		`SELECT
		   COUNT(*),
		   COALESCE(SUM(CASE WHEN status = 'active' THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(CASE WHEN status = 'stale'  THEN 1 ELSE 0 END), 0),
		   COALESCE(SUM(ws_connected), 0)
		 FROM agents`,
	).Scan(&c.Total, &c.Active, &c.Stale, &c.WsConnected)
	return c, err
}

// Touch updates last_seen_at to now for the given agent_id.
// Returns sql.ErrNoRows if the agent does not exist.
func (l *Ledger) Touch(ctx context.Context, agentID string) error {
	result, err := l.db.ExecContext(ctx,
		`UPDATE agents SET last_seen_at = ? WHERE agent_id = ?`,
		time.Now().Unix(), agentID,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetWsConnected sets ws_connected to 1 (connected) or 0 (disconnected).
// Returns sql.ErrNoRows if the agent does not exist.
func (l *Ledger) SetWsConnected(ctx context.Context, agentID string, connected bool) error {
	v := 0
	if connected {
		v = 1
	}
	result, err := l.db.ExecContext(ctx,
		`UPDATE agents SET ws_connected = ? WHERE agent_id = ?`,
		v, agentID,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListByOwner returns agents belonging to owner, ordered by last_seen_at descending.
// Returns a non-nil empty slice when no rows match.
func (l *Ledger) ListByOwner(ctx context.Context, owner string) ([]Agent, error) {
	rows, err := l.db.QueryContext(ctx,
		`SELECT agent_id, hostname, owner, tailnet_node, version, registered_at, last_seen_at, status, ws_connected
		 FROM agents WHERE owner = ? ORDER BY last_seen_at DESC`,
		owner,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	agents := []Agent{}
	for rows.Next() {
		var a Agent
		if err := rows.Scan(&a.AgentID, &a.Hostname, &a.Owner, &a.TailnetNode, &a.Version,
			&a.RegisteredAt, &a.LastSeenAt, &a.Status, &a.WsConnected); err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// GetByOwner returns the agent with agentID owned by owner.
// Returns (nil, nil) when not found; non-nil error only on db failure.
func (l *Ledger) GetByOwner(ctx context.Context, agentID, owner string) (*Agent, error) {
	var a Agent
	err := l.db.QueryRowContext(ctx,
		`SELECT agent_id, hostname, owner, tailnet_node, version, registered_at, last_seen_at, status, ws_connected
		 FROM agents WHERE agent_id = ? AND owner = ?`,
		agentID, owner,
	).Scan(&a.AgentID, &a.Hostname, &a.Owner, &a.TailnetNode, &a.Version,
		&a.RegisteredAt, &a.LastSeenAt, &a.Status, &a.WsConnected)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// Revoke sets status='revoked' for agentID scoped to owner.
// Returns sql.ErrNoRows if the agent does not exist or owner mismatch.
func (l *Ledger) Revoke(ctx context.Context, agentID, owner string) error {
	result, err := l.db.ExecContext(ctx,
		`UPDATE agents SET status = 'revoked' WHERE agent_id = ? AND owner = ?`,
		agentID, owner,
	)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetStatus returns the status string for agentID.
// Returns sql.ErrNoRows if the agent does not exist.
func (l *Ledger) GetStatus(ctx context.Context, agentID string) (string, error) {
	var status string
	err := l.db.QueryRowContext(ctx,
		`SELECT status FROM agents WHERE agent_id = ?`,
		agentID,
	).Scan(&status)
	return status, err
}

// InsertCommand persists a broadcast command to the commands table.
func (l *Ledger) InsertCommand(ctx context.Context, operator, command string) error {
	_, err := l.db.ExecContext(ctx,
		`INSERT INTO commands (operator, command, created_at) VALUES (?, ?, ?)`,
		operator, command, time.Now().Unix(),
	)
	return err
}

// Close closes the underlying database connection.
func (l *Ledger) Close() error {
	return l.db.Close()
}

func execSchema(db *sql.DB, s string) error {
	for _, stmt := range strings.Split(s, ";") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
