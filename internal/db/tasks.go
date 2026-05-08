package db

import (
	"database/sql"
	"fmt"
	"time"
)

type Task struct {
	ID           int64
	JobID        int64
	Title        string
	Description  string
	Status       string // pending | running | done | failed | blocked
	AssignedRole string
	DependsOn    string // JSON array of task IDs
	Result       string
	PID          *int
	StartToken   string
	LogPath      string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time

	// Progress fields. Only populated by RunningWithProgress; zero on
	// regular Get/ForJob fetches. Tools update these via SetProgress to
	// drive the global progress bar above the input.
	ProgressCurrent int
	ProgressTotal   int
	ProgressLabel   string
	ProgressDetail  string
}

type TaskQ struct{ db *sql.DB }

func (q *TaskQ) Create(jobID int64, title, description, assignedRole, dependsOn string) (*Task, error) {
	// Parse and validate depends_on before inserting.
	deps, err := parseDeps(dependsOn)
	if err != nil {
		return nil, fmt.Errorf("invalid depends_on: %w", err)
	}

	// Check each declared dependency belongs to the same job, exists, and that
	// adding these edges would not introduce a cycle. We don't have a real task
	// ID yet (it's auto-assigned on insert), so we use a sentinel value of -1
	// to represent the task-to-be-created and build a prospective graph.
	if len(deps) > 0 {
		existing, err := q.ForJob(jobID)
		if err != nil {
			return nil, fmt.Errorf("cycle check: %w", err)
		}
		existingIDs := make(map[int64]bool, len(existing))
		for _, t := range existing {
			existingIDs[t.ID] = true
		}
		for _, depID := range deps {
			if !existingIDs[depID] {
				return nil, fmt.Errorf("dependency task %d not found in job %d", depID, jobID)
			}
		}

		// Build adjacency list for the prospective graph (sentinel -1 → deps).
		adj := make(map[int64][]int64, len(existing)+1)
		for _, t := range existing {
			existingDeps, _ := parseDeps(t.DependsOn)
			adj[t.ID] = existingDeps
		}
		adj[-1] = deps

		if err := checkForCycles(-1, adj); err != nil {
			return nil, err
		}
	}

	res, err := q.db.Exec(
		`INSERT INTO tasks(job_id, title, description, assigned_role, depends_on) VALUES(?, ?, ?, ?, ?)`,
		jobID, title, description, assignedRole, dependsOn)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

func (q *TaskQ) Get(id int64) (*Task, error) {
	row := q.db.QueryRow(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token
		 FROM tasks WHERE id = ?`, id)
	return scanTask(row)
}

func (q *TaskQ) ForJob(jobID int64) ([]*Task, error) {
	rows, err := q.db.Query(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token
		 FROM tasks WHERE job_id = ? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (q *TaskQ) SetStatus(id int64, status string) error {
	// Enforce dependency ordering: a task can only run when all its deps are done.
	if status == StatusRunning {
		if err := q.checkDeps(id); err != nil {
			return err
		}
	}

	switch status {
	case StatusRunning:
		_, err := q.db.Exec(
			`UPDATE tasks SET status = ?, started_at = unixepoch() WHERE id = ?`, status, id)
		return err
	case StatusDone, StatusFailed:
		_, err := q.db.Exec(
			`UPDATE tasks SET status = ?, completed_at = unixepoch() WHERE id = ?`, status, id)
		return err
	default:
		_, err := q.db.Exec(`UPDATE tasks SET status = ? WHERE id = ?`, status, id)
		return err
	}
}

// ClaimForSpawn atomically transitions a task from pending/blocked to running
// when all its dependencies are already done. Prevents the TOCTOU race where
// two concurrent agent_spawn callers both pass a stale Tasks.Get read and
// then both write status='running' via SetStatus, launching two subprocesses
// against the same task (and clobbering each other's log file).
//
// On failure, runs a diagnostic query to return a useful reason —
// "already running", "dep X not done", or "not found".
func (q *TaskQ) ClaimForSpawn(id int64) (*Task, error) {
	res, err := q.db.Exec(
		`UPDATE tasks SET status='running', started_at=unixepoch()
		 WHERE id = ?
		   AND status IN ('pending','blocked')
		   AND NOT EXISTS (
		       SELECT 1 FROM json_each(depends_on) je
		       WHERE (SELECT status FROM tasks WHERE id = je.value) != 'done'
		   )`, id)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return q.Get(id)
	}

	// Claim failed — diagnose.
	task, gerr := q.Get(id)
	if gerr != nil {
		return nil, fmt.Errorf("task %d not found: %w", id, gerr)
	}
	if task.Status != StatusPending && task.Status != StatusBlocked {
		return nil, fmt.Errorf("task %d is already %s", id, task.Status)
	}
	if err := q.checkDeps(id); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("task %d: claim failed", id)
}

func (q *TaskQ) SetResult(id int64, result string) error {
	_, err := q.db.Exec(`UPDATE tasks SET result = ? WHERE id = ?`, result, id)
	return err
}

func (q *TaskQ) SetStatusAndResult(id int64, status, result string) error {
	_, err := q.db.Exec(
		`UPDATE tasks SET status=?, result=?, completed_at=unixepoch() WHERE id=?`,
		status, result, id)
	return err
}

func (q *TaskQ) SetPID(id int64, pid int) error {
	_, err := q.db.Exec(`UPDATE tasks SET pid = ? WHERE id = ?`, pid, id)
	return err
}

func (q *TaskQ) SetPIDAndToken(id int64, pid int, token string) error {
	_, err := q.db.Exec(`UPDATE tasks SET pid = ?, start_token = ? WHERE id = ?`, pid, token, id)
	return err
}

func (q *TaskQ) SetLogPath(id int64, path string) error {
	_, err := q.db.Exec(`UPDATE tasks SET log_path = ? WHERE id = ?`, path, id)
	return err
}

func (q *TaskQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	return err
}

// Running returns all tasks currently in status='running', including their
// PID and started_at so the watchdog can check process liveness and age.
func (q *TaskQ) Running() ([]*Task, error) {
	rows, err := q.db.Query(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token
		 FROM tasks WHERE status = 'running'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// SweepOrphans runs once per parent-process startup (not in subprocess workers —
// see main.go insertion point). It marks any task that is recorded as running
// but whose process is no longer alive as failed with result "orphaned (cairo restarted)".
func (q *TaskQ) SweepOrphans() (int, error) {
	running, err := q.Running()
	if err != nil {
		return 0, err
	}
	swept := 0
	for _, t := range running {
		if !IsTaskAlive(*t) {
			if err := q.SetStatusAndResult(t.ID, "failed", "orphaned (cairo restarted)"); err != nil {
				return swept, err
			}
			swept++
		}
	}
	return swept, nil
}

// CountRunning returns the number of tasks currently in status='running'.
// Used by the TUI's status bar to show a live pulse of Selene's parallel
// attention threads.
func (q *TaskQ) CountRunning() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE status = 'running'`).Scan(&n)
	return n, err
}

// RecentAll returns the most recent tasks across all jobs, ordered newest
// first by created_at. Used by the TUI's threads panel to render the
// being's parallel-attention surface — what has run lately, what's running
// now. Pass limit to cap.
func (q *TaskQ) RecentAll(limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := q.db.Query(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token
		 FROM tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UnreportedCompleted returns tasks that have reached a terminal status
// (done, failed) but whose completion hasn't yet been surfaced to the parent
// session as a background-activity note. Caller should format them into a
// "[background] ..." inbox message and then call MarkReported.
func (q *TaskQ) UnreportedCompleted() ([]*Task, error) {
	rows, err := q.db.Query(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token
		 FROM tasks
		 WHERE status IN ('done','failed') AND reported_at IS NULL
		 ORDER BY completed_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkReported stamps reported_at so the given tasks won't show up in the
// inbox again. Takes a slice of IDs to batch the update in one statement.
func (q *TaskQ) MarkReported(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := q.db.Exec(
		"UPDATE tasks SET reported_at = unixepoch() WHERE id IN ("+buildPlaceholders(len(ids))+")",
		int64SliceToAny(ids)...,
	)
	return err
}

// ReadyTasks returns all pending/blocked tasks in a job whose dependencies are
// all done — i.e. tasks the orchestrator can start right now.
func (q *TaskQ) ReadyTasks(jobID int64) ([]*Task, error) {
	all, err := q.ForJob(jobID)
	if err != nil {
		return nil, err
	}

	// index status by id
	statusOf := make(map[int64]string, len(all))
	for _, t := range all {
		statusOf[t.ID] = t.Status
	}

	var ready []*Task
	for _, t := range all {
		if t.Status != StatusPending && t.Status != StatusBlocked {
			continue
		}
		deps, err := parseDeps(t.DependsOn)
		if err != nil {
			continue
		}
		allDone := true
		for _, depID := range deps {
			if statusOf[depID] != StatusDone {
				allDone = false
				break
			}
		}
		if allDone {
			ready = append(ready, t)
		}
	}
	return ready, nil
}

// checkDeps returns an error if any dependency of task id is not yet done.
func (q *TaskQ) checkDeps(id int64) error {
	task, err := q.Get(id)
	if err != nil {
		return err
	}
	deps, err := parseDeps(task.DependsOn)
	if err != nil {
		return fmt.Errorf("task %d: invalid depends_on: %w", id, err)
	}
	for _, depID := range deps {
		dep, err := q.Get(depID)
		if err != nil {
			return fmt.Errorf("task %d: dependency %d not found", id, depID)
		}
		if dep.Status != StatusDone {
			return fmt.Errorf("task %d cannot run: depends on task %d (%q) which is %s",
				id, depID, dep.Title, dep.Status)
		}
	}
	return nil
}

// SetProgress updates the in-flight progress fields for a task. Pass total=0
// for indeterminate progress (the bar will pulse instead of filling). Label
// is the short headline ("indexing cairo"); detail is the per-step caption
// ("internal/agent/loop.go"). Cheap call — designed to fire often (every
// file in a long walk) without DB pressure.
func (q *TaskQ) SetProgress(id int64, current, total int, label, detail string) error {
	_, err := q.db.Exec(
		`UPDATE tasks
		 SET progress_current = ?, progress_total = ?,
		     progress_label   = ?, progress_detail = ?
		 WHERE id = ?`,
		current, total, label, detail, id)
	return err
}

// RunningWithProgress returns running tasks that have something worth
// rendering — non-empty label or non-zero total. Sort by started_at so the
// oldest in-flight task lists first (mirrors how the threads panel orders).
func (q *TaskQ) RunningWithProgress() ([]*Task, error) {
	rows, err := q.db.Query(
		`SELECT id, job_id, title, description, status, assigned_role, depends_on, result,
		        pid, COALESCE(log_path,''), created_at, started_at, completed_at, start_token,
		        progress_current, progress_total, progress_label, progress_detail
		 FROM tasks
		 WHERE status = 'running'
		   AND (progress_label != '' OR progress_total > 0)
		 ORDER BY COALESCE(started_at, created_at) ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Task
	for rows.Next() {
		t, err := scanTaskWithProgress(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// scanTaskWithProgress mirrors scanTask but pulls the four progress columns
// at the end. Kept separate from scanTask so existing callers don't pay the
// extra column cost.
func scanTaskWithProgress(row scanner) (*Task, error) {
	var t Task
	var createdAt int64
	var startedAt, completedAt sql.NullInt64
	var pid sql.NullInt64
	err := row.Scan(
		&t.ID, &t.JobID, &t.Title, &t.Description, &t.Status,
		&t.AssignedRole, &t.DependsOn, &t.Result,
		&pid, &t.LogPath,
		&createdAt, &startedAt, &completedAt, &t.StartToken,
		&t.ProgressCurrent, &t.ProgressTotal, &t.ProgressLabel, &t.ProgressDetail,
	)
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		p := int(pid.Int64)
		t.PID = &p
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	if startedAt.Valid {
		ts := time.Unix(startedAt.Int64, 0)
		t.StartedAt = &ts
	}
	if completedAt.Valid {
		ts := time.Unix(completedAt.Int64, 0)
		t.CompletedAt = &ts
	}
	return &t, nil
}

func scanTask(row scanner) (*Task, error) {
	var t Task
	var createdAt int64
	var startedAt, completedAt sql.NullInt64
	var pid sql.NullInt64
	err := row.Scan(
		&t.ID, &t.JobID, &t.Title, &t.Description, &t.Status,
		&t.AssignedRole, &t.DependsOn, &t.Result,
		&pid, &t.LogPath,
		&createdAt, &startedAt, &completedAt, &t.StartToken,
	)
	if err != nil {
		return nil, err
	}
	if pid.Valid {
		p := int(pid.Int64)
		t.PID = &p
	}
	t.CreatedAt = time.Unix(createdAt, 0)
	if startedAt.Valid {
		ts := time.Unix(startedAt.Int64, 0)
		t.StartedAt = &ts
	}
	if completedAt.Valid {
		ts := time.Unix(completedAt.Int64, 0)
		t.CompletedAt = &ts
	}
	return &t, nil
}
