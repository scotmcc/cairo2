package db

import (
	"os"
	"path/filepath"
)

// DataDirName is the name of the cairo data directory within the user's home.
const DataDirName = ".cairo"

// DefaultDataDir returns the default cairo data directory (~/.cairo).
// Resolution order: CAIRO_DATA_DIR env var → ~/.cairo.
// The --data-dir CLI flag is handled in main.go and calls db.Open directly
// with the resolved path, bypassing this function.
func DefaultDataDir() string {
	if d := os.Getenv("CAIRO_DATA_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.Getenv("HOME")
	}
	return filepath.Join(home, DataDirName)
}

// busyTimeoutMs is the SQLite busy_timeout value in milliseconds.
const busyTimeoutMs = 15000

// Role name constants — match the seeded role names in seed.go.
const (
	RoleThinkingPartner = "thinking_partner"
	RoleOrchestrator    = "orchestrator"
	RoleCoder           = "coder"
	RolePlanner         = "planner"
	RoleReviewer        = "reviewer"
	RoleDream           = "dream"
	RoleResearcher      = "researcher"
)

// Job and task status constants.
// Legacy values (used by both jobs and tasks) are listed first.
// v0.3.0 job-only values follow — they are valid for jobs but not tasks.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusBlocked   = "blocked"
	StatusCancelled = "cancelled"

	// v0.3.0 job-only status values.
	StatusAwaitingReview = "awaiting_review"
	StatusMerged         = "merged"
	StatusRejected       = "rejected"
	StatusConflict       = "conflict"
)
