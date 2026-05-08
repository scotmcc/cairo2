package tools

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// mergeJobTool implements the "merge_job" built-in. It handles approve/reject
// for jobs in awaiting_review status. All git work runs in the user's repo and
// is narrated step-by-step via EventToolUpdate events on the bus.
//
// Architecture note: merge_job is a Go tool, not bash from Selene. The merge
// sequence is deterministic and safety-critical — keeping it in Go (not in
// Selene's reasoning) ensures consistent behaviour regardless of model output.
//
// Invocation: Selene calls this after seeing "User approved/rejected job #N"
// in her system-prompt context (injected by tui_handlers.go via enqueueUIEvent).
type mergeJobTool struct {
	manager *worktree.Manager
	db      *sqliteopen.DB
}

// MergeJob constructs a merge_job tool.
func MergeJob(m *worktree.Manager, database *sqliteopen.DB) agent.Tool {
	return mergeJobTool{manager: m, db: database}
}

func (mergeJobTool) Name() string { return "merge_job" }
func (mergeJobTool) Description() string {
	return `Complete the approve or reject flow for a job in awaiting_review status.

Actions:
- approve: rebase worktree branch onto parent_branch, squash-merge into parent_branch,
  push to remote, remove the worktree, set job status=merged. On rebase conflict,
  attempts one-shot auto-resolve (checkout --ours); if still conflicted sets
  status=conflict and preserves the worktree for inspection. On push failure,
  keeps the worktree, sets push_pending=1, and sets status=merged (local merge preserved).
  Args: job_id (required).
- reject: set job status=rejected, preserve the worktree for inspection.
  Args: job_id (required). To remove the worktree call worktree(action=remove) separately.`
}

func (mergeJobTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": prop("string", "Operation: approve | reject."),
			"job_id": prop("integer", "Job ID — required for both actions."),
		},
		"required": []string{"action", "job_id"},
	}
}

func (t mergeJobTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "merge_job", "", 3); refused {
		return r
	}
	switch strArg(args, "action") {
	case "approve":
		return t.doApprove(args, ctx)
	case "reject":
		return t.doReject(args, ctx)
	case "":
		return agent.ToolResult{Content: "error: action is required (approve|reject)", IsError: true}
	default:
		return agent.ToolResult{
			Content: fmt.Sprintf("error: unknown action %q — valid: approve|reject", strArg(args, "action")),
			IsError: true,
		}
	}
}

func (t mergeJobTool) doApprove(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	jobID := int64(intArg(args, "job_id", 0))
	if jobID == 0 {
		return agent.ToolResult{Content: "error: job_id is required for approve", IsError: true}
	}

	database := t.db
	if ctx != nil && ctx.DB != nil {
		database = ctx.DB
	}

	job, err := database.Jobs.Get(jobID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: load job %d: %v", jobID, err), IsError: true}
	}
	if job.WorktreeID == nil {
		return agent.ToolResult{
			Content: "error: job has no associated worktree — cannot approve old-style jobs with merge_job",
			IsError: true,
		}
	}

	wt, err := database.Worktrees.Get(*job.WorktreeID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: load worktree %d: %v", *job.WorktreeID, err), IsError: true}
	}

	repoRoot := t.manager.RepoRoot()
	publish := func(step string) {
		if ctx != nil && ctx.Bus != nil {
			ctx.Bus.Publish(agent.Event{
				Type:    agent.EventToolUpdate,
				Payload: agent.PayloadToolUpdate{Name: "merge_job", Output: "step: " + step},
			})
		}
	}

	publish("verify-commits")
	if r, ok := t.verifyCommits(jobID, wt); !ok {
		return r
	}

	publish("rebase")
	if r, ok := t.rebaseOnto(jobID, wt, database); !ok {
		return r
	}

	publish("squash-merge")
	if r, ok := t.squashMerge(wt, repoRoot); !ok {
		return r
	}

	publish("commit")
	if r, ok := t.commitSquash(job, repoRoot); !ok {
		return r
	}

	publish("push")
	return t.pushAndFinalize(jobID, *job.WorktreeID, wt, repoRoot, database, publish)
}

// verifyCommits checks that the worktree branch has at least one commit ahead of parent.
func (t mergeJobTool) verifyCommits(jobID int64, wt *jobs.Worktree) (agent.ToolResult, bool) {
	commitLog, _ := runGitOutput(wt.Path, "log", "--oneline", wt.ParentBranch+".."+wt.Branch)
	if strings.TrimSpace(commitLog) == "" {
		return agent.ToolResult{
			Content: fmt.Sprintf(
				"error: job #%d has no commits on branch %s ahead of %s — the orchestrator edited files but never ran `git add && git commit` in the worktree. Inspect the worktree at %s; re-dispatch with a fixed briefing or role prompt rather than committing manually.",
				jobID, wt.Branch, wt.ParentBranch, wt.Path,
			),
			IsError: true,
		}, false
	}
	return agent.ToolResult{}, true
}

// rebaseOnto rebases the worktree branch onto its parent branch.
// On failure it aborts the rebase, sets job status=conflict, and returns an error result.
func (t mergeJobTool) rebaseOnto(jobID int64, wt *jobs.Worktree, database *sqliteopen.DB) (agent.ToolResult, bool) {
	if err := runGitIn(wt.Path, "rebase", wt.ParentBranch); err != nil {
		_ = runGitIn(wt.Path, "rebase", "--abort")
		_ = database.Jobs.SetStatus(jobID, jobs.StatusConflict)
		return agent.ToolResult{
			Content: fmt.Sprintf(
				"conflict: rebase of %s onto %s failed (%v); worktree preserved at %s for inspection; job status set to conflict",
				wt.Branch, wt.ParentBranch, err, wt.Path,
			),
			IsError: true,
		}, false
	}
	return agent.ToolResult{}, true
}

// squashMerge runs git merge --squash in the repo root.
func (t mergeJobTool) squashMerge(wt *jobs.Worktree, repoRoot string) (agent.ToolResult, bool) {
	if err := runGitIn(repoRoot, "merge", "--squash", wt.Branch); err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("error: git merge --squash %s: %v", wt.Branch, err),
			IsError: true,
		}, false
	}
	return agent.ToolResult{}, true
}

// commitSquash commits the staged squash using the job summary (or title) as the message.
func (t mergeJobTool) commitSquash(job *jobs.Job, repoRoot string) (agent.ToolResult, bool) {
	commitMsg := job.Title
	if job.Summary != nil && *job.Summary != "" {
		commitMsg = *job.Summary
	}
	cmd := exec.Command("git", "-C", repoRoot, "commit", "-m", commitMsg)
	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("error: git commit: %v — %s", err, strings.TrimSpace(string(out))),
			IsError: true,
		}, false
	}
	return agent.ToolResult{}, true
}

// pushAndFinalize pushes to remote, then removes the worktree and sets job status=merged.
// On push failure it keeps the worktree, sets push_pending=1, and still marks the job merged.
func (t mergeJobTool) pushAndFinalize(jobID int64, worktreeID int64, wt *jobs.Worktree, repoRoot string, database *sqliteopen.DB, publish func(string)) agent.ToolResult {
	var pushErr error
	pushCmd := exec.Command("git", "-C", repoRoot, "push")
	var pushOut bytes.Buffer
	pushCmd.Stdout = &pushOut
	pushCmd.Stderr = &pushOut
	if err := pushCmd.Run(); err != nil {
		pushErr = fmt.Errorf("%v — %s", err, strings.TrimSpace(pushOut.String()))
	}

	if pushErr != nil {
		publish("push-failed-keeping-worktree")
		_ = database.Worktrees.SetPushPending(worktreeID, true)
		if err := database.Jobs.SetMerged(jobID); err != nil {
			return agent.ToolResult{
				Content: fmt.Sprintf("merged locally, couldn't push: %v; also failed to set status: %v", pushErr, err),
				IsError: true,
			}
		}
		return agent.ToolResult{
			Content: fmt.Sprintf(
				"merged locally, couldn't push: %v; push_pending=1 on worktree %d; job #%d status=merged",
				pushErr, worktreeID, jobID,
			),
		}
	}

	publish("remove-worktree")
	if err := t.manager.Remove(worktreeID); err != nil {
		_ = database.Jobs.SetMerged(jobID)
		return agent.ToolResult{
			Content: fmt.Sprintf(
				"job #%d merged and pushed, but worktree removal failed: %v — clean up with worktree(action=remove)",
				jobID, err,
			),
		}
	}

	if err := database.Jobs.SetMerged(jobID); err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("merged and worktree removed, but failed to set status: %v", err),
			IsError: true,
		}
	}

	publish("done")
	return agent.ToolResult{
		Content: fmt.Sprintf(
			"job #%d merged: branch %s squash-committed to %s and pushed; worktree removed",
			jobID, wt.Branch, wt.ParentBranch,
		),
		Details: map[string]any{
			"job_id":        jobID,
			"branch":        wt.Branch,
			"parent_branch": wt.ParentBranch,
			"status":        jobs.StatusMerged,
		},
	}
}

func (t mergeJobTool) doReject(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	jobID := int64(intArg(args, "job_id", 0))
	if jobID == 0 {
		return agent.ToolResult{Content: "error: job_id is required for reject", IsError: true}
	}

	database := t.db
	if ctx != nil && ctx.DB != nil {
		database = ctx.DB
	}

	// Verify the job exists before stamping.
	job, err := database.Jobs.Get(jobID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: load job %d: %v", jobID, err), IsError: true}
	}

	if err := database.Jobs.SetRejected(jobID); err != nil {
		return agent.ToolResult{
			Content: fmt.Sprintf("error: set rejected for job %d: %v", jobID, err),
			IsError: true,
		}
	}

	worktreeNote := "no worktree associated"
	if job.WorktreeID != nil {
		worktreeNote = fmt.Sprintf(
			"worktree %d preserved for inspection — call worktree(action=remove, worktree_id=%d) to clean up",
			*job.WorktreeID, *job.WorktreeID,
		)
	}

	return agent.ToolResult{
		Content: fmt.Sprintf("job #%d rejected; %s", jobID, worktreeNote),
		Details: map[string]any{
			"job_id": jobID,
			"status": jobs.StatusRejected,
		},
	}
}

// runGitIn runs a git command in dir and returns an error that includes stderr.
func runGitIn(dir string, args ...string) error {
	_, err := runGitOutput(dir, args...)
	return err
}

// runGitOutput runs a git command in dir and returns stdout.
func runGitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w — %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out.String(), nil
}
