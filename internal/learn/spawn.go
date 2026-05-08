package learn

// spawn.go — shared helper that creates the job/task rows and launches
// `cairo learn -task=N -background ...` as a detached subprocess. Used by
// both the `learn` agent tool (when Selene calls it) and the `/learn`
// slash command (when the user invokes it directly).

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/scotmcc/cairo2/internal/db"
)

// SpawnRequest carries everything SpawnBackground needs to fire off an
// indexing run as a subprocess. SummaryModel may be empty to fall back to
// the global config.summary_model.
type SpawnRequest struct {
	DB           *db.DB
	Project      string
	Root         string // must be absolute
	SummaryModel string // optional override
}

// SpawnResult is what SpawnBackground returns on success.
type SpawnResult struct {
	TaskID  int64
	JobID   int64
	PID     int
	LogPath string
}

// SpawnBackground creates a job + task pair, marks the task running, and
// launches a `cairo learn -task=N` subprocess that updates the task's
// progress fields as it walks the project. The subprocess detaches so the
// parent (TUI or AI session) can keep going.
//
// SysProc setup (detach, suid drop, etc.) is delegated to a function pointer
// rather than imported directly — that lets this file stay free of Unix-vs-
// Windows build constraints. Pass nil to use os/exec defaults.
func SpawnBackground(req SpawnRequest, sysProc func() *syscall.SysProcAttr) (*SpawnResult, error) {
	if req.DB == nil {
		return nil, fmt.Errorf("learn.Spawn: DB is required")
	}
	if req.Project == "" || req.Root == "" {
		return nil, fmt.Errorf("learn.Spawn: project and root are required")
	}

	// Pass nil session — learn jobs are user-initiated and not bound to
	// the conversational session; threads panel still picks them up.
	job, err := req.DB.Jobs.Create(fmt.Sprintf("learn %s", req.Project), "", "", nil)
	if err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	task, err := req.DB.Tasks.Create(job.ID,
		fmt.Sprintf("index %s", req.Project),
		fmt.Sprintf("learn add path=%s project=%s", req.Root, req.Project),
		"learn", "")
	if err != nil {
		return nil, fmt.Errorf("create task: %w", err)
	}
	if err := req.DB.Tasks.SetStatus(task.ID, db.StatusRunning); err != nil {
		return nil, fmt.Errorf("set running: %w", err)
	}

	logDir := filepath.Join(db.DefaultDataDir(), "logs")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("task_%d.log", task.ID))
	_ = req.DB.Tasks.SetLogPath(task.ID, logPath)
	logFile, err := os.Create(logPath)
	if err != nil {
		req.DB.Tasks.SetStatusAndResult(task.ID, db.StatusFailed, fmt.Sprintf("create log: %v", err))
		return nil, err
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "cairo"
	}
	args := []string{
		"learn",
		"-task", fmt.Sprintf("%d", task.ID),
		"-background",
		"-path", req.Root,
		"-project", req.Project,
	}
	if req.SummaryModel != "" {
		args = append(args, "-summary-model", req.SummaryModel)
	}

	cmd := exec.Command(exe, args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if sysProc != nil {
		cmd.SysProcAttr = sysProc()
	}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		req.DB.Tasks.SetStatusAndResult(task.ID, db.StatusFailed, fmt.Sprintf("spawn failed: %v", err))
		return nil, fmt.Errorf("spawn: %w", err)
	}
	logFile.Close()
	token, err := db.ReadStartToken(cmd.Process.Pid)
	if err != nil {
		log.Printf("warn: ReadStartToken pid=%d: %v (continuing with empty token; sweep will fall back to PID-only liveness)", cmd.Process.Pid, err)
	}
	if err := req.DB.Tasks.SetPIDAndToken(task.ID, cmd.Process.Pid, token); err != nil {
		log.Printf("warn: SetPIDAndToken task %d: %v", task.ID, err)
	}
	cmd.Process.Release()

	return &SpawnResult{
		TaskID:  task.ID,
		JobID:   job.ID,
		PID:     cmd.Process.Pid,
		LogPath: logPath,
	}, nil
}
