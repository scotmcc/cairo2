# Dispatch a background job

Use this when work is non-trivial: needs research + planning + implementation, touches more than one file, or would take more than a few careful minutes. When in doubt, orchestrate.

**Do yourself:** typo fix, one-liner, obvious rename, explaining code, spot-checking a diff.
**Orchestrate:** anything else.

---

## Prerequisites

- The `orchestrate` skill (read it once with `skill(action="read", name="orchestrate")` if you want a reminder on when to orchestrate).
- A clear goal. If you don't have one, ask the user before proceeding.

---

## Step 1 — Write a briefing

The briefing is a self-contained document the orchestrator reads cold with zero memory of your conversation. Include all five sections. Short but complete.

```
## Goal
One sentence. What outcome should be true when this is done?

## Context
Why this matters. What the user said. What you already know about the relevant code.
2-5 sentences max.

## Files & landmarks
File paths, function names, line numbers you already know are involved.
Leave empty if unknown — the researcher will find them.

## Acceptance
Concrete, testable definition of done.
Bad: "the code is clean"
Good: "ctrl+L opens a panel that shows the last 1000 lines of cairo.log"

## Out of scope
What must NOT be touched. Especially important when there is adjacent tempting work.
```

A weak briefing produces a blocked or wrong result. Be specific.

---

## Step 2 — Create the job

```
job(action="create", title="<short name>", description="<one-line summary>", briefing="<full briefing text>")
```

The result returns `job created (id: N): <title>`. Record the job ID.

---

## Step 3 — Create the worktree

```
worktree(action="create", job_id=<job_id>, briefing="<same briefing text>")
```

The result returns `worktree created (id: W, branch: job/<job_id>, parent: master): <path> linked to job <job_id>`. Record `worktree_id` W. The job→worktree link is set automatically; do not run a separate UPDATE.

The briefing's `## Goal` section is used to derive the branch slug. Using the same text here as in step 2 is intentional.

---

## Step 4 — Create the orchestrator task

```
task(action="create", job_id=<job_id>, title="orchestrate", description="<same briefing text>", assigned_role="orchestrator")
```

The result returns `task created (id: T)`. Record the task ID.

---

## Step 5 — Spawn it

```
agent(action="spawn", id=<task_id>)
```

Returns immediately. The orchestrator runs in the background.

---

## What the orchestrator does (internal pipeline)

You do not manage this — it is what the orchestrator role does inside the worktree on your behalf. Documented here so you know what to look for when monitoring or diagnosing.

The orchestrator follows a researcher → coder pattern:

1. **Researcher task** — the orchestrator calls `task(action="create", assigned_role="researcher")` and spawns it. The researcher reads the relevant code, writes a plan, and records findings in its result.
2. **Coder task** — once the researcher task is `done`, the orchestrator calls `task(action="create", assigned_role="coder", depends_on=[researcher_task_id])` and spawns it. The coder implements the plan from the researcher's result.
3. **Review loop** — the orchestrator may create a reviewer task to validate the coder's work. If review fails, the orchestrator can re-spawn the coder (up to `job_max_review_iterations` attempts).
4. **Writeback** — when all tasks are done, the orchestrator commits its changes on the worktree branch and sets job status to `awaiting_review`.

Typical call shape inside an orchestrator turn:

```
task(action="create", job_id=N, title="research", description="<goal>", assigned_role="researcher")
agent(action="spawn", id=<researcher_task_id>)
agent(action="wait", id=<researcher_task_id>)          ← blocks until research is done
task(action="create", job_id=N, title="implement", description="<plan from research>", assigned_role="coder", depends_on=[<researcher_task_id>])
agent(action="spawn", id=<coder_task_id>)
agent(action="wait", id=<coder_task_id>)               ← blocks until implementation is done
```

If any sub-task fails with a `BLOCKED:` result, the fix is in the briefing or role config — not in taking over the work.

---

## Step 6 — Tell the user and stay available

Confirm the job is running. Give them the job ID and task ID. You are immediately available for other work.

Example: "Job #N is running in the background (task #T). I'll let you know when it's ready for review."

---

## Monitoring

While the job runs:

```
task(action="list", job_id=<job_id>)      — see all tasks and statuses
agent(action="log", id=<task_id>)         — read live output
agent(action="wait", id=<task_id>)        — block until done (up to 300s by default)
job(action="list")                        — see all jobs and statuses
```

The background summarizer and TUI progress bar also surface task progress automatically.

---

## When it finishes

Check the job status:

```
job(action="list")
```

**Status `awaiting_review`** — the orchestrator finished. The job has diff stats (`diff_files`, `diff_insertions`, `diff_deletions`). Proceed to review.

**Status `failed`** — read the error:
```
bash(command="sqlite3 ~/.cairo/cairo.db 'SELECT error FROM jobs WHERE id = N'")
```
If the error starts with `BLOCKED:`, the fix is in the briefing, the role's prompt, or the role's model config — never in taking over the work yourself. Correct the cause and re-create the orchestrator task.

**Status `running` for too long** — check the log with `agent(action="log")`, kill if hung with `agent(action="kill")`.

---

## Review

1. Read the orchestrator task result: `agent(action="log", id=<task_id>)` or via `task(action="list")`.
2. Verify a commit was made on the worktree branch:
   `bash(command="git -C <worktree_path> log --oneline <parent_branch>..HEAD")`
   If this is empty, the orchestrator edited files but never committed — the merge will be empty. Re-dispatch with a clearer briefing or fix the orchestrator role prompt before trying again.
3. Spot-check 1-2 key changed files in the worktree path.
4. Offer the user two paths to approve/reject:
   - **TUI:** "Press Ctrl+D to review the diff for job #N. Hit `a` to approve or `r` to reject."
   - **Chat:** "Want me to merge it?" — if yes, call `merge_job(action="approve", job_id=N)` directly.
5. Summarize what changed and any caveats.

---

## Approve / reject (merge_job)

You can invoke `merge_job` two ways:

- **Directly** — when the user approves/rejects in chat ("merge it", "go ahead", "no, scrap it"), call `merge_job(action="approve|reject", job_id=N)` immediately.
- **Diff-panel hand-off** — when the user presses `a`/`r` in the Ctrl+D diff panel, the TUI auto-submits a prompt telling you to run merge_job. You'll see a system line like `[review] approving job #N — handing off to Selene...` followed by the prompt. Run merge_job as instructed.

Either path runs the same merge_job tool. The UI path is just a way for the user to trigger it without typing.

### Approve

```
merge_job(action="approve", job_id=N)
```

This handles the full sequence: rebase onto parent_branch, squash-merge, push, worktree removal, status → `merged`. It emits `step:` tool-update events as it progresses — narrate each step briefly in chat.

Outcomes:
- **Success** — "job #N merged: branch X squash-committed to Y and pushed; worktree removed"
- **Push failure** — result contains "merged locally, couldn't push" — local merge succeeded, `push_pending=1` set. Tell the user; do NOT auto-retry.
- **Conflict** (IsError=true) — result contains "conflict:" — rebase onto parent_branch failed. Worktree is preserved at the listed path; job status is now `conflict`. No auto-resolution is attempted. Tell the user what happened and where the worktree is.

### Reject

```
merge_job(action="reject", job_id=N)
```

Sets status=`rejected`, keeps the worktree. After calling it, ask: "Keep the worktree for inspection, or remove it?" If they want it removed:

```
worktree(action="remove", worktree_id=W)
```

---

## Common errors

| Error | Cause | Fix |
|-------|-------|-----|
| `job N has no worktree (worktree_id is null)` | `worktree(create)` was not called with the correct `job_id`, or was never called | Re-run `worktree(create, job_id=N, ...)` to create and link a worktree, then retry the spawn |
| `error: job has no associated worktree — cannot approve old-style jobs with merge_job` | `merge_job(approve)` called on a job with `worktree_id IS NULL` | This is an old-style job; you cannot use merge_job on it |
| `BLOCKED: briefing incomplete — ...` | Orchestrator couldn't find required information in the briefing | Fix the briefing (add file paths, clearer goal, acceptance criteria) and re-create the orchestrator task |
| `BLOCKED: step N review failed twice` | Coder+reviewer cycle exhausted | Re-examine the plan step; it may be too large or the acceptance criteria unclear |
| `conflict: rebase of X onto Y failed` | Parallel work merged to parent_branch since the worktree was created | Inspect the conflict in the worktree; resolve manually, or abandon the job with `merge_job(reject)` |
| `task N: dependency N is not done` | `agent(spawn)` called before a dependency task completed | Run `task(action="ready", job_id=...)` to see which tasks are unblocked, spawn those first |

---

## Checking push-pending jobs

Query worktrees with outstanding pushes:

```
bash(command="sqlite3 ~/.cairo/cairo.db 'SELECT j.id, j.title, w.branch FROM jobs j JOIN worktrees w ON j.worktree_id = w.id WHERE w.push_pending = 1'")
```

When you see any, surface them proactively: "N job(s) merged locally but not pushed — want me to retry the push?"

---

## Recovery sequences

### Null worktree

**Symptom:** You called `agent(action="spawn", id=<task_id>)` and got:

```
job N has no worktree (worktree_id is null); cannot resolve CWD for task T.
To fix:
  1. worktree(action="create", job_id=N, briefing="<text>")
  2. Retry your task action
The worktree tool auto-links to the job — no manual UPDATE is needed.
```

**Diagnosis:** `worktree(action="create")` was never called for this job, or was called with the wrong `job_id`. The job's `worktree_id` column is NULL.

**Recovery:**

```
worktree(action="create", job_id=<N>, briefing="<copy the original briefing>")
```

Then retry the spawn:

```
agent(action="spawn", id=<task_id>)
```

**Verify:** The spawn returns a task ID rather than an error.

Note: `worktree(action="create")` auto-links to the job. Do not follow it with `job(action="update", worktree_id=...)` — the `job` tool does not accept `worktree_id` as a parameter.

---

### Push failure

**Symptom:** `merge_job(action="approve", job_id=N)` returns IsError=**false** but content contains:

```
merged locally, couldn't push: <git error>; push_pending=1 on worktree W; job #N status=merged
```

**Diagnosis:** The squash-merge commit succeeded locally but `git push` failed. The commit is already on the local branch; the worktree is preserved with `push_pending=1` set. Job status is `merged`.

Check the `<git error>` portion for root cause:
- `"error: failed to push some refs to origin"` — fast-forward refused; remote has diverged
- `"fatal: could not read Username"` — authentication not configured
- `"fatal: 'origin' does not appear to be a git repository"` — no remote configured

**Recovery:**

There is no tool action to retry a push. Push manually from the repository root:

```
bash(command="git push")
```

If the remote has diverged (non-fast-forward), check whether remote changes need to be incorporated before pushing. Do not force-push without human confirmation.

Once push succeeds, clean up the worktree:

```
worktree(action="remove", worktree_id=W)
```

**Verify:** `bash(command="git log --oneline origin/<branch>")` shows the squash commit. The worktree is gone.

---

### Merge conflict

**Symptom:** `merge_job(action="approve", job_id=N)` returns IsError=**true** with content:

```
conflict: rebase of <branch> onto <parent_branch> failed (<git error>); worktree preserved at <path> for inspection; job status set to conflict
```

**Diagnosis:** The rebase of the worktree branch onto the parent branch hit a conflict. The tool aborted the rebase — no auto-resolve is attempted. Job status is now `conflict`. Worktree is preserved at `<path>`.

Inspect the state:

```
bash(command="git -C <path> status")
```

**Recovery:** Stop and tell the user. Do not attempt to resolve the conflict yourself. The exact message to the user:

> merge_job returned a rebase conflict on job #N. The worktree is at `<path>`. Please resolve the conflict manually: `git -C <path> status` to see conflicted files, edit them, then `git -C <path> add <files> && git -C <path> rebase --continue`. Once resolved, I can retry the merge.

Once the user confirms resolution, retry:

```
merge_job(action="approve", job_id=N)
```

If the user decides to abandon the job instead:

```
merge_job(action="reject", job_id=N)
worktree(action="remove", worktree_id=W)
```

**Verify:** `merge_job(action="approve")` returns the success string (`"job #N merged: branch X squash-committed to Y and pushed; worktree removed"`), or the user confirms they want the job rejected.

Note: `job(action="reconcile")` will not change a job from `conflict` status — reconcile skips decided states.

---

### Rebase fail (non-conflict)

**Symptom:** Same error surface as merge conflict (`conflict: rebase of ... failed`), but the `<git error>` text indicates a non-content cause.

**Diagnosis:** Read the error text carefully to identify root cause:

| Error substring | Root cause |
|---|---|
| `"Your local changes to ... would be overwritten"` | Dirty working tree — uncommitted edits in the worktree |
| `"Merge conflict in"` | Content conflict — use the merge conflict sequence above |
| `"Needed a single revision"` | Non-linear parent — parent branch was force-pushed or deleted |
| Other | Environmental / git state issue |

All rebase failures land in `conflict` job status regardless of cause. Inspect:

```
bash(command="git -C <path> status")
bash(command="git -C <path> log --oneline <parent_branch>")
```

**Recovery:**

For a dirty working tree (uncommitted edits):

```
bash(command="git -C <path> add -A && git -C <path> commit -m 'fix: commit uncommitted work before merge'")
```

Then retry:

```
merge_job(action="approve", job_id=N)
```

For a non-linear parent (force-pushed or deleted), assess whether the commits can be rebased onto the new parent tip. If yes, do it manually and retry. If the parent branch is gone or the commit history is irrecoverable, abandon:

```
merge_job(action="reject", job_id=N)
worktree(action="remove", worktree_id=W)
```

Then re-dispatch the job from the briefing.

For other environmental failures, run `bash(command="git -C <path> rebase --abort")` to clear any partial state (the tool may have already done this), then diagnose and fix the git configuration before retrying.

**Verify:** `merge_job(action="approve")` succeeds, or the job is cleanly rejected and worktree removed.

---

## What NOT to do during recovery

- Do not call `job(action="update", worktree_id=...)` — the `job` tool does not accept `worktree_id`.
- Do not call `job(action="reconcile")` on a job in `conflict`, `merged`, `rejected`, or `awaiting_review` status — reconcile skips decided states.
- Do not attempt to resolve merge conflicts automatically — always hand off to the user.
- Do not force-push without explicit human approval.
