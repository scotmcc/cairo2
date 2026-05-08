package tools

import (
	"bytes"
	"fmt"
	"os/exec"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// worktreeTool exposes worktree.Manager operations (create/remove/path) to the
// agent via the "worktree" tool. Selene calls this before spawning an
// orchestrator task so that the task runs inside an isolated git worktree.
type worktreeTool struct{ manager *worktree.Manager }

// Worktree constructs a worktree tool wrapping the given Manager.
func Worktree(m *worktree.Manager) agent.Tool { return worktreeTool{manager: m} }

func (worktreeTool) Name() string { return "worktree" }
func (worktreeTool) Description() string {
	return `Manage per-job git worktrees. A worktree isolates each job's file edits to its
own branch so parallel jobs do not interfere. Create one before spawning the
orchestrator task; remove it after the job is merged or rejected.

Actions:
- create: create a worktree for a job. Args: job_id (required), briefing (required).
  Captures the current branch as parent_branch via 'git symbolic-ref --short HEAD'.
  Returns worktree_id and path.
- remove: remove a worktree and its DB row. Args: worktree_id (required).
- path:   look up the on-disk path for a worktree. Args: worktree_id (required).`
}

func (worktreeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action":      propEnum("Operation to perform.", []string{"create", "remove", "path"}),
			"job_id":      prop("integer", "Job ID — required for create."),
			"briefing":    prop("string", "Structured briefing text — required for create. Used to derive branch slug from ## Goal section."),
			"worktree_id": prop("integer", "Worktree ID — required for remove and path."),
		},
		"required": []string{"action"},
	}
}

func (t worktreeTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "worktree", "", 3); refused {
		return r
	}
	switch strArg(args, "action") {
	case "create":
		return t.doCreate(args)
	case "remove":
		return t.doRemove(args)
	case "path":
		return t.doPath(args)
	case "":
		return agent.ToolResult{Content: "error: action is required (create|remove|path)", IsError: true}
	default:
		return agent.ToolResult{
			Content: fmt.Sprintf("error: unknown action %q — valid: create|remove|path", strArg(args, "action")),
			IsError: true,
		}
	}
}

func (t worktreeTool) doCreate(args map[string]any) agent.ToolResult {
	jobID := int64(intArg(args, "job_id", 0))
	briefing := strArg(args, "briefing")
	if jobID == 0 || briefing == "" {
		return agent.ToolResult{Content: "error: job_id and briefing are required for create", IsError: true}
	}

	// D1: capture parent_branch as the symbolic branch name at call time.
	parentBranch, err := symbolicRef(t.manager.RepoRoot())
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: resolve parent branch: %v", err), IsError: true}
	}

	worktreeID, err := t.manager.Create(jobID, briefing, parentBranch)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}

	wtPath, err := t.manager.WorktreePath(worktreeID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: get path for new worktree %d: %v", worktreeID, err), IsError: true}
	}

	return agent.ToolResult{
		Content: fmt.Sprintf("worktree created (id: %d, branch: %s, parent: %s): %s\nlinked to job %d", worktreeID, "job/"+fmt.Sprintf("%d", jobID), parentBranch, wtPath, jobID),
		Details: map[string]any{"worktree_id": worktreeID, "path": wtPath, "parent_branch": parentBranch},
	}
}

func (t worktreeTool) doRemove(args map[string]any) agent.ToolResult {
	wtID := int64(intArg(args, "worktree_id", 0))
	if wtID == 0 {
		return agent.ToolResult{Content: "error: worktree_id is required for remove", IsError: true}
	}
	if err := t.manager.Remove(wtID); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("worktree %d removed", wtID)}
}

func (t worktreeTool) doPath(args map[string]any) agent.ToolResult {
	wtID := int64(intArg(args, "worktree_id", 0))
	if wtID == 0 {
		return agent.ToolResult{Content: "error: worktree_id is required for path", IsError: true}
	}
	p, err := t.manager.WorktreePath(wtID)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: p, Details: map[string]any{"path": p}}
}

// symbolicRef returns the symbolic branch name of HEAD in the given repo
// (e.g. "master", "feature/foo") via 'git symbolic-ref --short HEAD'.
func symbolicRef(repoRoot string) (string, error) {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoRoot
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git symbolic-ref: %w — %s", err, stderr.String())
	}
	branch := string(bytes.TrimSpace(out.Bytes()))
	if branch == "" {
		return "", fmt.Errorf("git symbolic-ref: empty output (detached HEAD?)")
	}
	return branch, nil
}
