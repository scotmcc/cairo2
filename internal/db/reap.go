package db

import (
	"fmt"
	"log"
	"os"
	"syscall"
)

// isProcessAlive reports whether a Unix process with the given PID exists.
// Uses the classic signal(0) probe: a successful send proves the process
// is still owned by someone; ESRCH means it's gone. Does not distinguish
// "our task subprocess" from "a recycled PID" — false positives delay
// cleanup by one startup cycle but don't cause wrong state.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 is a no-op that still triggers permission + existence checks.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// ReapOrphanedTasks sweeps tasks stuck in status='running' whose subprocess
// is no longer alive, marking them failed so dependents and the UI aren't
// stuck waiting forever. Called once on DB open — the subprocesses spawned
// by agent_spawn detach, so a cairo crash leaves no cleanup path otherwise.
// Tasks with no recorded pid are also reaped — they were never properly
// started (e.g. cairo crashed mid-spawn) and cannot recover.
func (db *DB) ReapOrphanedTasks() (int, error) {
	rows, err := db.sql.Query(
		`SELECT id, COALESCE(pid, 0) FROM tasks WHERE status = 'running'`)
	if err != nil {
		return 0, err
	}
	type rec struct {
		id  int64
		pid int
	}
	var stale []rec
	for rows.Next() {
		var id int64
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			rows.Close()
			return 0, err
		}
		// pid=0 means NULL in DB (no pid recorded) — always stale.
		// pid>0: check whether the process is still alive.
		if pid == 0 || !isProcessAlive(pid) {
			stale = append(stale, rec{id, pid})
		}
	}
	rows.Close()

	for _, r := range stale {
		var msg string
		if r.pid == 0 {
			msg = "reaped: process not found at startup"
		} else {
			msg = fmt.Sprintf("reaped: subprocess (pid %d) no longer alive on cairo startup", r.pid)
		}
		// reported_at = NULL is explicit here: the drainBackgroundInbox mechanism
		// surfaces tasks where reported_at IS NULL, so reaped tasks must stay
		// unreported so the parent session is notified on the next turn.
		if _, err := db.sql.Exec(
			`UPDATE tasks SET status='failed', result=?, completed_at=unixepoch(), reported_at=NULL WHERE id=?`,
			msg, r.id,
		); err != nil {
			return len(stale), err
		}
		// Trigger job rollup so the parent job reflects terminal state.
		if task, gerr := db.Tasks.Get(r.id); gerr == nil {
			if rerr := db.Jobs.ResolveAndUpdateJobStatus(task.JobID); rerr != nil {
				log.Printf("reap: reconcile job %d: %v", task.JobID, rerr)
			}
		}
	}
	if len(stale) > 0 {
		log.Printf("reap: marked %d orphaned task(s) as failed", len(stale))
	}
	return len(stale), nil
}
