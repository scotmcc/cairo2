package db

import (
	"database/sql"
	"time"
)

// Hook represents a shell command bound to a lifecycle event.
type Hook struct {
	ID        int64
	Event     string
	Matcher   string // empty = match all targets
	Command   string
	IsEnabled bool
	CreatedAt time.Time
}

// HookQ provides query methods for the hooks table.
type HookQ struct{ db *sql.DB }

// Add creates a new hook for the given event.
func (q *HookQ) Add(event, command string) (*Hook, error) {
	res, err := q.db.Exec(
		`INSERT INTO hooks(event, command) VALUES(?, ?)`,
		event, command)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.get(id)
}

func (q *HookQ) get(id int64) (*Hook, error) {
	row := q.db.QueryRow(
		`SELECT id, event, matcher, command, enabled, created_at FROM hooks WHERE id = ?`, id)
	return scanHook(row)
}

// List returns all hooks ordered by id.
func (q *HookQ) List() ([]*Hook, error) {
	rows, err := q.db.Query(
		`SELECT id, event, matcher, command, enabled, created_at FROM hooks ORDER BY id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Hook
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Delete removes a hook by id.
func (q *HookQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM hooks WHERE id = ?`, id)
	return err
}

// SetEnabled enables or disables a hook.
func (q *HookQ) SetEnabled(id int64, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := q.db.Exec(`UPDATE hooks SET enabled = ? WHERE id = ?`, v, id)
	return err
}

// ForEvent returns all enabled hooks for the given event.
func (q *HookQ) ForEvent(event string) ([]*Hook, error) {
	rows, err := q.db.Query(
		`SELECT id, event, matcher, command, enabled, created_at
		 FROM hooks WHERE event = ? AND enabled = 1 ORDER BY id ASC`, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Hook
	for rows.Next() {
		h, err := scanHook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func scanHook(row scanner) (*Hook, error) {
	var h Hook
	var createdAt int64
	var enabled int
	err := row.Scan(&h.ID, &h.Event, &h.Matcher, &h.Command, &enabled, &createdAt)
	if err != nil {
		return nil, err
	}
	h.IsEnabled = enabled != 0
	h.CreatedAt = time.Unix(createdAt, 0)
	return &h, nil
}
