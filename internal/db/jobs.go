package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Job represents a unit of work in the cairo job system.
// v0.3.0 fields (WorktreeID and below) are nullable; old-style jobs simply
// have WorktreeID = nil, which distinguishes them from v0.3.0 orchestrated jobs.
type Job struct {
	ID               int64
	Title            string
	Description      string
	Status           string // see StatusPending, StatusRunning, etc. and v0.3.0 variants
	OrchestratorRole string
	SessionID        *int64
	Result           string
	CreatedAt        time.Time
	StartedAt        *time.Time
	CompletedAt      *time.Time

	// v0.3.0 fields — all nullable; nil means not set.
	WorktreeID      *int64
	Briefing        *string
	ParentMessageID *int64
	Summary         *string
	DiffFiles       *int64
	DiffInsertions  *int64
	DiffDeletions   *int64
	ReviewedAt      *time.Time
	Error           *string
}

type JobQ struct{ db *sql.DB }

func (q *JobQ) Create(title, description, orchestratorRole string, sessionID *int64) (*Job, error) {
	res, err := q.db.Exec(
		`INSERT INTO jobs(title, description, orchestrator_role, session_id) VALUES(?, ?, ?, ?)`,
		title, description, orchestratorRole, sessionID)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

// CreateWithBriefing creates a v0.3.0 job with optional briefing and
// parent_message_id set at insert time. Use this in preference to Create when
// the caller has briefing text available, so the fields are populated
// atomically with the row rather than requiring a follow-up SetV030Fields call.
func (q *JobQ) CreateWithBriefing(title, description, orchestratorRole string, sessionID *int64, briefing *string, parentMessageID *int64) (*Job, error) {
	res, err := q.db.Exec(
		`INSERT INTO jobs(title, description, orchestrator_role, session_id, briefing, parent_message_id)
		 VALUES(?, ?, ?, ?, ?, ?)`,
		title, description, orchestratorRole, sessionID, briefing, parentMessageID)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return q.Get(id)
}

func (q *JobQ) Get(id int64) (*Job, error) {
	row := q.db.QueryRow(jobSelectCols+` FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

func (q *JobQ) List() ([]*Job, error) {
	rows, err := q.db.Query(jobSelectCols + ` FROM jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListJobsByStatus returns all jobs whose status is in the provided list.
// The statuses slice may contain any combination of existing and v0.3.0 values.
func (q *JobQ) ListJobsByStatus(statuses []string) ([]*Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(statuses))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	rows, err := q.db.Query(
		jobSelectCols+` FROM jobs WHERE status IN (`+placeholders+`) ORDER BY created_at DESC`,
		args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ActiveJob is a Job with branch metadata joined from the worktrees table.
// Used by the diff panel to render the job list and run git diff without a
// second DB round-trip.
type ActiveJob struct {
	Job
	Branch       string // worktrees.branch
	ParentBranch string // worktrees.parent_branch
	WorktreePath string // worktrees.path
}

// jobSelectColsAliased is jobSelectCols with the jobs table alias j. for use
// in JOIN queries where bare column names would be ambiguous (e.g. id, created_at
// appear in both jobs and worktrees).
const jobSelectColsAliased = `SELECT j.id, j.title, j.description, j.status, j.orchestrator_role,
	j.session_id, j.result, j.created_at, j.started_at, j.completed_at,
	j.worktree_id, j.briefing, j.parent_message_id, j.summary,
	j.diff_files, j.diff_insertions, j.diff_deletions, j.reviewed_at, j.error`

// ListActiveJobs returns jobs that have a live worktree and are in a
// non-terminal, user-actionable status (pending, running, awaiting_review,
// or conflict). Results are ordered newest first.
func (q *JobQ) ListActiveJobs() ([]*ActiveJob, error) {
	rows, err := q.db.Query(
		jobSelectColsAliased + `, w.branch, w.parent_branch, w.path
		FROM jobs j
		JOIN worktrees w ON j.worktree_id = w.id
		WHERE j.worktree_id IS NOT NULL
		  AND j.status IN ('pending', 'running', 'awaiting_review', 'conflict')
		ORDER BY j.created_at DESC, j.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ActiveJob
	for rows.Next() {
		aj, err := scanActiveJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, aj)
	}
	return out, rows.Err()
}

// scanActiveJob scans a row that includes the standard job columns followed
// by w.branch, w.parent_branch, w.path from a worktrees JOIN.
func scanActiveJob(row *sql.Rows) (*ActiveJob, error) {
	var aj ActiveJob
	var createdAt int64
	var sessionID, startedAt, completedAt sql.NullInt64
	var worktreeID, parentMessageID sql.NullInt64
	var briefing, summary, errText sql.NullString
	var diffFiles, diffInsertions, diffDeletions sql.NullInt64
	var reviewedAt sql.NullInt64

	err := row.Scan(
		&aj.ID, &aj.Title, &aj.Description, &aj.Status,
		&aj.OrchestratorRole, &sessionID, &aj.Result,
		&createdAt, &startedAt, &completedAt,
		&worktreeID, &briefing, &parentMessageID, &summary,
		&diffFiles, &diffInsertions, &diffDeletions, &reviewedAt, &errText,
		&aj.Branch, &aj.ParentBranch, &aj.WorktreePath,
	)
	if err != nil {
		return nil, err
	}

	aj.CreatedAt = time.Unix(createdAt, 0)
	if sessionID.Valid {
		aj.SessionID = &sessionID.Int64
	}
	if startedAt.Valid {
		t := time.Unix(startedAt.Int64, 0)
		aj.StartedAt = &t
	}
	if completedAt.Valid {
		t := time.Unix(completedAt.Int64, 0)
		aj.CompletedAt = &t
	}
	if worktreeID.Valid {
		aj.WorktreeID = &worktreeID.Int64
	}
	if briefing.Valid {
		aj.Briefing = &briefing.String
	}
	if parentMessageID.Valid {
		aj.ParentMessageID = &parentMessageID.Int64
	}
	if summary.Valid {
		aj.Summary = &summary.String
	}
	if diffFiles.Valid {
		aj.DiffFiles = &diffFiles.Int64
	}
	if diffInsertions.Valid {
		aj.DiffInsertions = &diffInsertions.Int64
	}
	if diffDeletions.Valid {
		aj.DiffDeletions = &diffDeletions.Int64
	}
	if reviewedAt.Valid {
		t := time.Unix(reviewedAt.Int64, 0)
		aj.ReviewedAt = &t
	}
	if errText.Valid {
		aj.Error = &errText.String
	}
	return &aj, nil
}

// ListJobsWithLiveWorktrees returns jobs that currently have an associated
// worktree row (worktree_id IS NOT NULL). These are v0.3.0 jobs whose
// on-disk worktree has not yet been removed.
func (q *JobQ) ListJobsWithLiveWorktrees() ([]*Job, error) {
	rows, err := q.db.Query(
		jobSelectCols + ` FROM jobs WHERE worktree_id IS NOT NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (q *JobQ) SetStatus(id int64, status string) error {
	switch status {
	case StatusRunning:
		_, err := q.db.Exec(
			`UPDATE jobs SET status = ?, started_at = unixepoch() WHERE id = ?`, status, id)
		return err
	case StatusDone, StatusFailed, StatusCancelled:
		_, err := q.db.Exec(
			`UPDATE jobs SET status = ?, completed_at = unixepoch() WHERE id = ?`, status, id)
		return err
	default:
		_, err := q.db.Exec(`UPDATE jobs SET status = ? WHERE id = ?`, status, id)
		return err
	}
}

func (q *JobQ) SetResult(id int64, result string) error {
	_, err := q.db.Exec(`UPDATE jobs SET result = ? WHERE id = ?`, result, id)
	return err
}

func (q *JobQ) Delete(id int64) error {
	_, err := q.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	return err
}

// ResolveAndUpdateJobStatus checks whether all tasks for jobID have reached
// terminal states and, if so, updates the job's status accordingly. It is a
// no-op when the job is already in a terminal state or when any task is still
// in-flight.
//
// Terminal resolution rules:
//   - Any failed task → job becomes "failed"
//   - All tasks done (or mix of done/cancelled) → job becomes "done"
//
// ResolveAndUpdateJobStatus is called automatically after each task completes
// (success or failure) and after the startup reap sweep, so the job status
// stays consistent without the orchestrator having to update it manually.
func (q *JobQ) ResolveAndUpdateJobStatus(jobID int64) error {
	// Skip if the job is already in a terminal state.
	job, err := q.Get(jobID)
	if err != nil {
		return err
	}
	switch job.Status {
	case StatusDone, StatusFailed, StatusCancelled,
		StatusAwaitingReview, StatusMerged, StatusRejected, StatusConflict:
		// Skip: already in a terminal or decided state.
		// v0.3.0 decided states (awaiting_review, merged, rejected, conflict)
		// are set by postLoopJobWriteback / merge flow and must not be
		// overwritten by the task-count reconcile.
		return nil
	}

	// Count tasks by status in a single query.
	rows, err := q.db.Query(
		`SELECT status, COUNT(*) FROM tasks WHERE job_id = ? GROUP BY status`, jobID)
	if err != nil {
		return err
	}
	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return err
		}
		counts[status] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return nil // no tasks yet
	}

	// Check whether any task is still in a non-terminal state.
	inFlight := counts[StatusPending] + counts[StatusRunning] + counts[StatusBlocked]
	if inFlight > 0 {
		return nil // job still running
	}

	// All tasks are terminal — derive job outcome.
	newStatus := StatusDone
	if counts[StatusFailed] > 0 {
		newStatus = StatusFailed
	}
	return q.SetStatus(jobID, newStatus)
}

// CountRunning returns the number of jobs currently in status='running'.
// Used by the TUI's status bar to show active background job count.
func (q *JobQ) CountRunning() (int, error) {
	var n int
	err := q.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status = 'running'`).Scan(&n)
	return n, err
}

// jobSelectCols is the canonical column list for job SELECTs. Keep in sync
// with scanJob — the column order must match exactly.
const jobSelectCols = `SELECT id, title, description, status, orchestrator_role,
	session_id, result, created_at, started_at, completed_at,
	worktree_id, briefing, parent_message_id, summary,
	diff_files, diff_insertions, diff_deletions, reviewed_at, error`

func scanJob(row scanner) (*Job, error) {
	var j Job
	var createdAt int64
	var sessionID, startedAt, completedAt sql.NullInt64
	// v0.3.0 nullable columns
	var worktreeID, parentMessageID sql.NullInt64
	var briefing, summary, errText sql.NullString
	var diffFiles, diffInsertions, diffDeletions sql.NullInt64
	var reviewedAt sql.NullInt64

	err := row.Scan(
		&j.ID, &j.Title, &j.Description, &j.Status,
		&j.OrchestratorRole, &sessionID, &j.Result,
		&createdAt, &startedAt, &completedAt,
		&worktreeID, &briefing, &parentMessageID, &summary,
		&diffFiles, &diffInsertions, &diffDeletions, &reviewedAt, &errText,
	)
	if err != nil {
		return nil, err
	}

	j.CreatedAt = time.Unix(createdAt, 0)
	if sessionID.Valid {
		j.SessionID = &sessionID.Int64
	}
	if startedAt.Valid {
		t := time.Unix(startedAt.Int64, 0)
		j.StartedAt = &t
	}
	if completedAt.Valid {
		t := time.Unix(completedAt.Int64, 0)
		j.CompletedAt = &t
	}

	// v0.3.0 fields
	if worktreeID.Valid {
		j.WorktreeID = &worktreeID.Int64
	}
	if briefing.Valid {
		j.Briefing = &briefing.String
	}
	if parentMessageID.Valid {
		j.ParentMessageID = &parentMessageID.Int64
	}
	if summary.Valid {
		j.Summary = &summary.String
	}
	if diffFiles.Valid {
		j.DiffFiles = &diffFiles.Int64
	}
	if diffInsertions.Valid {
		j.DiffInsertions = &diffInsertions.Int64
	}
	if diffDeletions.Valid {
		j.DiffDeletions = &diffDeletions.Int64
	}
	if reviewedAt.Valid {
		t := time.Unix(reviewedAt.Int64, 0)
		j.ReviewedAt = &t
	}
	if errText.Valid {
		j.Error = &errText.String
	}
	return &j, nil
}

// SetV030Fields updates the v0.3.0 extended fields on a job in a single round-trip.
// Any nil pointer is left unchanged (the SQL uses COALESCE to preserve existing values).
func (q *JobQ) SetV030Fields(id int64, worktreeID *int64, briefing, summary, errText *string, diffFiles, diffInsertions, diffDeletions *int64) error {
	_, err := q.db.Exec(`UPDATE jobs SET
		worktree_id      = COALESCE(?, worktree_id),
		briefing         = COALESCE(?, briefing),
		summary          = COALESCE(?, summary),
		error            = COALESCE(?, error),
		diff_files       = COALESCE(?, diff_files),
		diff_insertions  = COALESCE(?, diff_insertions),
		diff_deletions   = COALESCE(?, diff_deletions)
	WHERE id = ?`,
		worktreeID, briefing, summary, errText,
		diffFiles, diffInsertions, diffDeletions, id)
	return err
}

// SetReviewed stamps reviewed_at = now and sets status for a job being
// approved or rejected. It is a thin helper so callers don't hard-code the
// timestamp logic.
func (q *JobQ) SetReviewed(id int64, status string) error {
	_, err := q.db.Exec(
		`UPDATE jobs SET status = ?, reviewed_at = unixepoch() WHERE id = ?`, status, id)
	return err
}

// SetMerged stamps status=merged and reviewed_at=now in a single round-trip.
// Use this after a successful approve flow (git rebase + squash + push + worktree remove).
func (q *JobQ) SetMerged(id int64) error {
	return q.SetReviewed(id, StatusMerged)
}

// SetRejected stamps status=rejected and reviewed_at=now in a single round-trip.
// Use this when the user rejects a job via the diff panel.
func (q *JobQ) SetRejected(id int64) error {
	return q.SetReviewed(id, StatusRejected)
}

// SetParentMessage links a job to the user message that spawned it.
func (q *JobQ) SetParentMessage(id, messageID int64) error {
	_, err := q.db.Exec(`UPDATE jobs SET parent_message_id = ? WHERE id = ?`, messageID, id)
	return err
}

// ValidJobStatus reports whether s is a recognised job status value (union of
// legacy and v0.3.0 values).
func ValidJobStatus(s string) bool {
	switch s {
	case StatusPending, StatusRunning, StatusDone, StatusFailed, StatusBlocked, StatusCancelled,
		StatusAwaitingReview, StatusMerged, StatusRejected, StatusConflict:
		return true
	}
	return false
}

// jobStatusErr formats an "unknown status" error consistently.
func jobStatusErr(s string) error {
	return fmt.Errorf("unknown job status %q", s)
}
