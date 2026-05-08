package jobs

import (
	"database/sql"
	"time"
)

// Worktree is the mechanical artifact backing a v0.3.0 orchestrated job.
// The conceptual state (briefing, status, summary) lives on the Job; the
// on-disk artifact (path, branch) lives here. Deleting a Worktree row
// cascades via ON DELETE SET NULL to clear jobs.worktree_id on any linked
// job, so the job record is preserved after the worktree is pruned.
type Worktree struct {
	ID           int64
	Path         string
	Branch       string
	ParentBranch string
	PushPending  bool
	CreatedAt    time.Time
}

// WorktreeQ holds query methods for the worktrees table.
type WorktreeQ struct{ db *sql.DB }

func NewWorktreeQ(db *sql.DB) *WorktreeQ { return &WorktreeQ{db: db} }

// CreateWorktree inserts a new worktree row and returns the created record.
func (q *WorktreeQ) Create(path, branch, parentBranch string) (*Worktree, error) {
	res, err := q.db.Exec(
		`INSERT INTO worktrees(path, branch, parent_branch) VALUES(?, ?, ?)`,
		path, branch, parentBranch)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

// Get returns the worktree with the given id, or sql.ErrNoRows if absent.
func (q *WorktreeQ) Get(id int64) (*Worktree, error) {
	row := q.db.QueryRow(
		`SELECT id, path, branch, parent_branch, push_pending, created_at
		 FROM worktrees WHERE id = ?`, id)
	return scanWorktree(row)
}

// Update persists all mutable fields of w back to the database.
// The caller is responsible for setting w.PushPending before calling.
func (q *WorktreeQ) Update(w *Worktree) error {
	pushPending := 0
	if w.PushPending {
		pushPending = 1
	}
	_, err := q.db.Exec(
		`UPDATE worktrees SET path = ?, branch = ?, parent_branch = ?, push_pending = ?
		 WHERE id = ?`,
		w.Path, w.Branch, w.ParentBranch, pushPending, w.ID)
	return err
}

// SetPushPending updates only the push_pending flag for the given worktree.
func (q *WorktreeQ) SetPushPending(id int64, pending bool) error {
	v := 0
	if pending {
		v = 1
	}
	_, err := q.db.Exec(`UPDATE worktrees SET push_pending = ? WHERE id = ?`, v, id)
	return err
}

// Delete removes the worktree row. The FK ON DELETE SET NULL on jobs.worktree_id
// automatically clears the link on any job that referenced this worktree.
func (q *WorktreeQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM worktrees WHERE id = ?`, id)
	return err
}

// List returns all worktree rows, most recently created first.
func (q *WorktreeQ) List() ([]*Worktree, error) {
	rows, err := q.db.Query(
		`SELECT id, path, branch, parent_branch, push_pending, created_at
		 FROM worktrees ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWorktrees(rows)
}

// ListWithPendingPush returns worktrees where push_pending = 1.
// Used by Selene's "list pending pushes" proactive reminder.
func (q *WorktreeQ) ListWithPendingPush() ([]*Worktree, error) {
	rows, err := q.db.Query(
		`SELECT id, path, branch, parent_branch, push_pending, created_at
		 FROM worktrees WHERE push_pending = 1 ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWorktrees(rows)
}

func scanWorktree(row scanner) (*Worktree, error) {
	var w Worktree
	var createdAt int64
	var pushPending int
	err := row.Scan(&w.ID, &w.Path, &w.Branch, &w.ParentBranch, &pushPending, &createdAt)
	if err != nil {
		return nil, err
	}
	w.PushPending = pushPending != 0
	w.CreatedAt = time.Unix(createdAt, 0)
	return &w, nil
}

func scanWorktrees(rows *sql.Rows) ([]*Worktree, error) {
	var out []*Worktree
	for rows.Next() {
		w, err := scanWorktree(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
