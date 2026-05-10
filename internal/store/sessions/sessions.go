package sessions

import (
	"database/sql"
	"time"
)

type Session struct {
	ID             int64
	Name           string
	CWD            string
	Role           string
	DisciplineMode int // 1=readonly, 2=scoped, 3=full
	CreatedAt      time.Time
	LastActive     time.Time
}

type SessionQ struct{ db *sql.DB }

func NewSessionQ(db *sql.DB) *SessionQ { return &SessionQ{db: db} }

func (q *SessionQ) Create(name, cwd, role string) (*Session, error) {
	res, err := q.db.Exec(
		`INSERT INTO sessions(name, cwd, role, discipline_mode) VALUES(?, ?, ?, 3)`,
		nullStr(name), cwd, role,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

func (q *SessionQ) Get(id int64) (*Session, error) {
	// COALESCE(name,'') guards against legacy NULLs; a migration ensures no new
	// NULLs are inserted, but old rows may still have NULL until re-migrated.
	row := q.db.QueryRow(
		`SELECT id, COALESCE(name,''), cwd, role, COALESCE(discipline_mode,3), created_at, last_active FROM sessions WHERE id = ?`, id)
	return scanSession(row)
}

func (q *SessionQ) Latest() (*Session, error) {
	row := q.db.QueryRow(
		`SELECT id, COALESCE(name,''), cwd, role, COALESCE(discipline_mode,3), created_at, last_active FROM sessions ORDER BY last_active DESC LIMIT 1`)
	s, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// LatestByRole returns the most recently-active session whose role matches
// the given value, or nil if none exists. Used by resolveSession to avoid
// resuming an orchestrator-or-other-background session when the user
// launched cairo for a different role.
func (q *SessionQ) LatestByRole(role string) (*Session, error) {
	row := q.db.QueryRow(
		`SELECT id, COALESCE(name,''), cwd, role, COALESCE(discipline_mode,3), created_at, last_active FROM sessions WHERE role = ? ORDER BY last_active DESC LIMIT 1`,
		role)
	s, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return s, err
}

// SinceUnix returns the IDs of all sessions whose last_active timestamp is
// at or after sinceUnix (a Unix epoch integer). Pass 0 to get all sessions.
// Used by the dream-pass writer role to find the session window to scan.
func (q *SessionQ) SinceUnix(sinceUnix int64) ([]int64, error) {
	rows, err := q.db.Query(
		`SELECT id FROM sessions WHERE last_active >= ? ORDER BY id ASC`, sinceUnix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (q *SessionQ) List() ([]*Session, error) {
	rows, err := q.db.Query(
		`SELECT id, COALESCE(name,''), cwd, role, COALESCE(discipline_mode,3), created_at, last_active FROM sessions ORDER BY last_active DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Session
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetDisciplineMode updates the stored discipline mode for a session.
// Mode values: 1=readonly, 2=scoped, 3=full.
func (q *SessionQ) SetDisciplineMode(id int64, mode int) error {
	_, err := q.db.Exec(`UPDATE sessions SET discipline_mode = ? WHERE id = ?`, mode, id)
	return err
}

func (q *SessionQ) Touch(id int64) error {
	_, err := q.db.Exec(`UPDATE sessions SET last_active = unixepoch() WHERE id = ?`, id)
	return err
}

func (q *SessionQ) Rename(id int64, name string) error {
	_, err := q.db.Exec(`UPDATE sessions SET name = ? WHERE id = ?`, name, id)
	return err
}

// Count returns the total number of sessions.
func (q *SessionQ) Count() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&n)
	return n, err
}

// Delete removes a session and — via ON DELETE CASCADE declared on every
// table that references sessions(id) — sweeps its messages, summaries,
// facts, jobs, and transitively the jobs' tasks and task_artifacts.
// Requires PRAGMA foreign_keys=on (set in Open()) to take effect.
func (q *SessionQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSession(row scanner) (*Session, error) {
	var s Session
	var createdAt, lastActive int64
	err := row.Scan(&s.ID, &s.Name, &s.CWD, &s.Role, &s.DisciplineMode, &createdAt, &lastActive)
	if err != nil {
		return nil, err
	}
	if s.DisciplineMode == 0 {
		s.DisciplineMode = 3 // default to full for legacy rows
	}
	s.CreatedAt = time.Unix(createdAt, 0)
	s.LastActive = time.Unix(lastActive, 0)
	return &s, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
