package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/cli"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// runTask is the background worker path: load task, create session in task's role,
// run the task description through the agent, store result, mark done.
func runTask(database *sqliteopen.DB, taskID int64, model string, background bool) error {
	task, err := database.Tasks.Get(taskID)
	if err != nil {
		return fmt.Errorf("get task: %w", err)
	}

	ollamaURL := resolveOllamaURL(database)
	embedModel, _ := database.Config.Get("embed_model")

	llmClient := llm.New(ollamaURL, resolveLLMAPIKey(database))
	if err := llmClient.Ping(); err != nil {
		database.Tasks.SetStatusAndResult(taskID, "failed", fmt.Sprintf("llm server unreachable: %v", err))
		return err
	}

	cwd, err := resolveTaskCWD(database, task)
	if err != nil {
		return fmt.Errorf("resolve task CWD: %w", err)
	}

	session, err := database.Sessions.Create(
		fmt.Sprintf("task-%d", taskID),
		cwd,
		task.AssignedRole,
	)
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	resolvedModel, err := sqliteopen.ResolveModelWithExplicit(database, model, task.AssignedRole, "qwen3.6:35b-a3b-mlx-bf16")
	if err != nil {
		return fmt.Errorf("resolve model: %w", err)
	}

	builtins := tools.Default(database, llmClient, embedModel, nil)
	repoRoot, _ := os.Getwd()
	wm := worktree.NewManager(repoRoot, database)
	builtins = append(builtins, tools.Worktree(wm))
	builtins = append(builtins, tools.MergeJob(wm, database))
	allowed, _ := database.Roles.AllowedTools(task.AssignedRole)
	if len(allowed) > 0 {
		builtins = tools.FilterByAllowlist(builtins, allowed)
	}
	custom, _ := tools.LoadCustom(database)
	custom = tools.FilterByAllowlist(custom, allowed)
	allTools := append(builtins, custom...)

	maxTurns := 0
	if task.AssignedRole == identity.RoleOrchestrator {
		maxIter := 3
		if s, _ := database.Config.Get(config.KeyJobMaxReviewIterations); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				maxIter = n
			}
		}
		maxTurns = maxIter * 6
	}

	a, err := agent.New(agent.Config{
		DB:           database,
		LLM:          llmClient,
		Model:        resolvedModel,
		Session:      session,
		Tools:        allTools,
		IsBackground: true,
		MaxTurns:     maxTurns,
	})
	if err != nil {
		return fmt.Errorf("create agent: %w", err)
	}
	defer a.Close()

	var stopRenderer func()
	if background && task.LogPath != "" {
		logFile, err := cli.OpenTaskLog(task.LogPath)
		if err == nil {
			defer logFile.Close()
			stopRenderer = cli.BackgroundRenderer(a.Bus(), logFile)
		}
	}
	if stopRenderer == nil {
		stopRenderer = cli.BackgroundRenderer(a.Bus(), os.Stdout)
	}
	defer stopRenderer()

	artifactCh, stopArtifacts := a.Bus().Subscribe()
	defer stopArtifacts()
	go collectArtifacts(artifactCh, database, taskID)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runErr := a.Prompt(ctx, task.Description)

	result := a.LastAssistantText()
	if result == "" && runErr != nil {
		result = fmt.Sprintf("error: %v", runErr)
	}

	status := "done"
	if runErr != nil {
		status = "failed"
	}
	database.Tasks.SetStatusAndResult(taskID, status, result)
	agent.RunHooks(database, "task_completed", task.Title, []string{
		"CAIRO_TASK_ID=" + fmt.Sprintf("%d", taskID),
		"CAIRO_TASK_TITLE=" + task.Title,
		"CAIRO_TASK_STATUS=" + status,
		"CAIRO_TASK_ROLE=" + task.AssignedRole,
		"CAIRO_TASK_RESULT=" + agent.CapHookEnv(result),
	})

	if task.JobID != 0 {
		postLoopJobWriteback(database, task.JobID, result, runErr)
	}

	if rerr := database.Jobs.ResolveAndUpdateJobStatus(task.JobID); rerr != nil {
		log.Printf("task %d: reconcile job %d: %v", taskID, task.JobID, rerr)
	}

	return runErr
}

// resolveTaskCWD determines the working directory for a spawned task.
// D2: never silently fall back to os.Getwd().
func resolveTaskCWD(database *sqliteopen.DB, task *jobs.Task) (string, error) {
	if task.JobID == 0 {
		return "", fmt.Errorf("task %d has no job_id; cannot resolve CWD without a job/worktree", task.ID)
	}
	job, err := database.Jobs.Get(task.JobID)
	if err != nil {
		return "", fmt.Errorf("get job %d: %w", task.JobID, err)
	}
	if job.WorktreeID == nil {
		return "", fmt.Errorf(
			"job %d has no worktree (worktree_id is null); cannot resolve CWD for task %d.\n"+
				"To fix, run these tool calls in order:\n"+
				"  1. worktree(action=\"create\", job_id=%d, briefing=\"<original briefing text>\")\n"+
				"     → returns worktree_id; the job link is set automatically — no UPDATE needed\n"+
				"  2. agent(action=\"spawn\", id=%d)\n"+
				"     → retry the spawn\n"+
				"See docs/ai/workflows/dispatch-job.md — Recovery: Null worktree for the full sequence.",
			task.JobID, task.ID, task.JobID, task.ID,
		)
	}
	wt, err := database.Worktrees.Get(*job.WorktreeID)
	if err != nil {
		return "", fmt.Errorf("get worktree %d for job %d: %w", *job.WorktreeID, task.JobID, err)
	}
	return wt.Path, nil
}

// collectArtifacts observes the bus and records write/edit/bash results
// as task artifacts.
func collectArtifacts(ch <-chan agent.Event, database *sqliteopen.DB, taskID int64) {
	var pendingName string
	var pendingPath string

	for event := range ch {
		switch event.Type {
		case agent.EventToolStart:
			p := event.Payload.(agent.PayloadToolStart)
			pendingName = p.Name
			pendingPath = ""
			if p.Name == "write" || p.Name == "edit" {
				if path, ok := p.Args["path"].(string); ok {
					pendingPath = path
				}
			}

		case agent.EventToolEnd:
			p := event.Payload.(agent.PayloadToolEnd)
			if p.IsError {
				pendingName = ""
				continue
			}
			switch pendingName {
			case "write", "edit":
				if pendingPath != "" {
					if err := database.TaskArtifacts.Add(taskID, "file", pendingPath, "", pendingName); err != nil {
						log.Printf("task %d: store artifact: %v", taskID, err)
					}
				}
			case "bash":
				if p.Result != "" {
					if err := database.TaskArtifacts.Add(taskID, "output", "", p.Result, "bash"); err != nil {
						log.Printf("task %d: store artifact: %v", taskID, err)
					}
				}
			}
			pendingName = ""
			pendingPath = ""
		}
	}
}
