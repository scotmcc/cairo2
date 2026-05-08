# v0.3.0 — Job orchestration with worktree-based dispatch

Status (as of 2026-04-29): all five chunks shipped and merged on master.

- Chunk 1: schema + DB query helpers — `internal/store/schema.go`, `internal/store/worktrees.go`
- Chunk 2: `worktree.Manager` package — `internal/worktree/`
- Chunk 3: orchestrator CWD plumbing + `worktree` tool + `job briefing` param — `e5f3dc5`, `fbf292d`, `328be73`
- Chunk 4a: two-pane diff panel (Ctrl+D), `OpenDiffPanel`, approve/reject keybindings — `cb4a591`, `0c40775`, `8d37fd8`
- Chunk 4b: reject sets `status=rejected`, `merge_job` approve/reject flow — `1ded27a`, `b3fe33a`
- Chunk 5: drop auto-resolve, surface all rebase failures as conflict — `d588b1f`

The system is operationally wired. See `docs/ai/workflows/dispatch-job.md` for the step-by-step dispatch sequence. The `worktree_id` linking step (Step 4 in that guide) is a known usability gap: `job(update)` does not accept `worktree_id`, so linking requires a direct SQL UPDATE.

This doc captures the architectural decisions made for cairo's v0.3.0 job-orchestration system. The system replaces today's pattern of agents editing the live tree directly with a structured flow where Selene manages user intent, orchestrators manage worktrees, and sub-agents execute tasks within those worktrees.

The decisions below are settled — do not re-litigate when implementing. If a decision needs to change, edit this doc first and explain why, then change the code.

---

## Hierarchy

Three layers, modeled on how a head of dev manages a team:

- **Selene** = Chief of Staff. Talks to the user. Owns *jobs*. Stays available for conversation; does not write code herself.
- **Orchestrator** = team lead. Owns a *worktree*. Breaks the job's briefing into tasks. Dispatches sub-agents in a fixed pipeline.
- **Sub-agents** (researcher, planner, coder, reviewer) = devs on the team. Do the actual work. Run sequentially within an orchestrator's pipeline.

The orchestrator pipeline within one job:

1. Researcher — what files need to be touched, can this be done, blockers, caveats. Produces a research document.
2. Planner — given the research and the briefing, produce a checklist of small actionable tasks.
3. Coder → Reviewer cycle, one task at a time. If reviewer rejects, coder fixes and review repeats. **Cap: 3 iterations.** If the cap is reached, set job status to `failed` with the reviewer's last rejection in the `error` column; Selene narrates the failure and asks Scot what to do. The cap is configurable via config key `job_max_review_iterations` (default 3) — note that this key is not yet implemented; wire it at build time.
4. Orchestrator spot-checks the result, builds a summary, returns control to Selene.

Selene reviews the summary, flags anything Scot needs to know, surfaces the diff for review.

---

## User-request decomposition

A user request may be one job or many. Selene decides. The rule:

> Split when the user has obviously split (e.g. "add ctrl+L log viewer AND fix the indexer bug" → two jobs). On ambiguity, default to one job.

**This is a performance heuristic, not a correctness invariant.** Worktree isolation plus rebase-before-merge is the actual safety net. If Selene splits wrong and two parallel jobs collide, the rebase-before-merge of the second job surfaces the conflict and the existing merge flow handles it (auto-resolve once, then `conflict` status if it fails). The split heuristic exists to avoid wasted tokens on jobs that are very likely to conflict — not because the machinery requires it.

Sequential dependencies (UI depends on business depends on data) live *within* one job's pipeline, not across jobs.

A user request itself is not a DB row — it's conversational state. Jobs reference the user message that spawned them via `parent_message_id`; Selene queries siblings by message ID when she needs to report on a request as a whole.

---

## Schema

v0.3.0 extends the existing `jobs` table additively (no renames, no drops) and introduces a new `worktrees` table. The two-table split is intentional: a `job` is the conceptual unit of work (briefing, status, summary, diff stats); a `worktree` is the mechanical artifact (path, branch). Old-style jobs from the existing orchestration system simply have `worktree_id IS NULL`.

Per-task tracking is deferred (orchestrator runtime memory for v0.3.0).

### Existing `jobs` table (already deployed)

The `jobs` table exists in `internal/store/schema.go` (lines 96–107) with columns: `id`, `title`, `description`, `status`, `orchestrator_role`, `session_id`, `result`, `created_at`, `started_at`, `completed_at`. v0.3.0 adds the following columns via `ALTER TABLE`:

```sql
ALTER TABLE jobs ADD COLUMN worktree_id        INTEGER REFERENCES worktrees(id) ON DELETE SET NULL;
ALTER TABLE jobs ADD COLUMN briefing           TEXT;
ALTER TABLE jobs ADD COLUMN parent_message_id  INTEGER;
ALTER TABLE jobs ADD COLUMN summary            TEXT;
ALTER TABLE jobs ADD COLUMN diff_files         INTEGER;
ALTER TABLE jobs ADD COLUMN diff_insertions    INTEGER;
ALTER TABLE jobs ADD COLUMN diff_deletions     INTEGER;
ALTER TABLE jobs ADD COLUMN reviewed_at        INTEGER;
ALTER TABLE jobs ADD COLUMN error              TEXT;
```

### New `worktrees` table

```sql
CREATE TABLE worktrees (
    id              INTEGER PRIMARY KEY,
    path            TEXT    NOT NULL,
    branch          TEXT    NOT NULL,
    parent_branch   TEXT    NOT NULL,
    push_pending    INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL DEFAULT (unixepoch())
);
```

Jobs reference their worktree via `worktree_id` FK. The predicate `worktree_id IS NOT NULL` implicitly distinguishes v0.3.0 jobs (with worktrees) from old-style jobs (without).

### Status enum

The existing `status` values are unchanged. v0.3.0 adds `awaiting_review | merged | rejected | cancelled | conflict` alongside whatever the existing system uses. Old jobs (no worktree) and new jobs (with worktree) coexist; the predicate `worktree_id IS NOT NULL` distinguishes them without a separate discriminator column.

`pending | running | awaiting_review | merged | rejected | failed | conflict | cancelled`

- **pending** — created, not yet started
- **running** — orchestrator is executing the pipeline
- **awaiting_review** — orchestrator finished, diff exists, user needs to act
- **merged** — Selene merged it (covers both real merges and empty-diff acknowledgments; disambiguate via `diff_files = 0`)
- **rejected** — work completed, user rejected via the panel
- **failed** — orchestrator errored mid-run (includes review-loop cap exhaustion)
- **conflict** — pre-merge rebase failed; auto-resolve attempted once and didn't work
- **cancelled** — work did not complete; user requested stop. Distinct from `rejected` (which means work completed but Scot didn't want it).

### Pruning

When Selene removes a worktree, `DELETE FROM worktrees WHERE id = X`. The job's `worktree_id` cascades to NULL via `ON DELETE SET NULL`. Query "jobs whose tree still exists on disk" as `WHERE worktree_id IS NOT NULL`. Query the worktree directory directly as `SELECT * FROM worktrees`.

### Deliberately not in this schema

- **Per-task tracking** (which sub-agent did what) — orchestrator runtime memory; add `job_tasks` in v0.3.x if it becomes queryable.
- **Approval policy / multi-user fields** — single-user today.
- **Diff blob caching** — `git diff` is fast enough.
- **`pushed_at`** — push *timestamp* is still deferred; nothing queries it. This is distinct from `worktrees.push_pending` (the boolean which tracks whether a push is outstanding). `push_pending` is queryable state; `pushed_at` is a timestamp we don't need yet.
- **A separate `worktree_jobs` table or `kind` discriminator column** — the FK pattern (`worktree_id IS NOT NULL`) is sufficient and doesn't require a new column or table.

---

## Briefing format

The `briefing` column is TEXT, but Selene fills a structured 5-section markdown template. All 5 sections are required (sections can be empty but must be present — keeps the orchestrator's mental model uniform). Parsed by section header.

```
## Goal
One sentence. What outcome the user wants.

## Context
Why this matters / what was discussed / relevant constraints from the
conversation. 2-5 sentences.

## Files & landmarks (if known)
Paths, line numbers, function names that anchor the work. Selene may
leave this empty if she doesn't know — researcher will fill it.

## Acceptance
What "done" looks like. Concrete, testable. ("ctrl+L opens a panel
that shows last 1000 lines of cairo.log.") Not "code is clean."

## Out of scope
What NOT to touch. Especially valuable when there's adjacent work
that's tempting but separate.
```

---

## Worktree creation

Always create a worktree per job (decision B from the conversation). No "research-only skip the worktree" optimization — uniform path is more important than the marginal cost saved.

When spawning a job: insert a `worktrees` row first (capturing `path`, `branch`, `parent_branch`), then insert/update the `jobs` row with the resulting `worktree_id`. The worktree row is the authoritative record of the on-disk artifact; the job row holds the conceptual state.

`parent_branch` stores the **symbolic branch name** of the parent repo's current HEAD at job-creation time (e.g., `master`, `feature/foo`), captured via `git symbolic-ref --short HEAD`. Storing the branch name (not a commit hash) is what makes the rebase-before-merge step below catch parallel work — if `parent_branch` were a snapshot commit, the rebase would target a dead point and miss collisions.

### Hardening (best effort, not a sandbox)

There is no clean way to *prevent* an agent with bash access from escaping its worktree. We do what we can and accept the residual risk:

- `chdir` the agent into the worktree at spawn time.
- Set CWD env vars so subprocesses inherit the working directory.
- Brief the orchestrator explicitly: *"You work here, do not touch other files."*

This is the same shape of trust as a junior dev with commit access. We don't try to detect escape — false-positives on the user's unrelated edits would be worse than no detection. The merge-time diff of the worktree is the auditable surface; anything written outside the worktree is simply not in that diff and is lost when Selene merges. Trust + diff audit, consistent with the framing above.

---

## Diff panel UI (Ctrl-D)

Two-pane layout.

- **Left (narrow):** list of jobs in non-terminal state — `pending`, `running`, `awaiting_review`, `conflict`. Most recent first. Each row: `#43 awaiting_review · 5f +200/-30 · "Add diff viewer hotkey"`.
- **Right (wide):** detail for the selected job — briefing at top, diff body below (rendered via Glamour with a `diff` code-fence wrapper for syntax highlighting), action keys at bottom.

### Selene-driven entry

When Selene says *"press Ctrl-D to see diff #43,"* the panel opens with #43 pre-selected. Implementation: `tui.OpenDiffPanel(jobID)` callable from the agent loop.

### Actions

- **`a`** — approve. Emits an event Selene picks up; she narrates the merge in chat and does the actual git work.
- **`r`** — reject. Emits an event; Selene asks the keep/remove question in chat.
- **`Esc`** — close panel without action.

The user never directly invokes git from the panel. The panel records a decision; Selene executes. This makes everything narratable and recoverable.

### Status changes mid-view

If a job's status changes while the user has it open (Selene merged it), update the status indicator in place. Do not auto-close; do not auto-switch. Let the user dismiss when they're done reading.

---

## Merge flow

Selene executes the merge after the user approves. All git work runs in the user's repo, narrated step by step in chat.

### Approve

1. `git -C <worktree> rebase <parent_branch>` — catch up to current HEAD. If conflict, attempt one-shot auto-resolve. If still conflicted, set status to `conflict` and surface to user. Stop.
2. `git merge --squash <branch>` then `git commit -m "<job summary>"` — one commit per job lands on parent_branch. No researcher/planner/coder/reviewer chatter in master history.
3. `git push` — push the squashed commit to remote.
4. `git worktree remove <worktrees.path>`, then `DELETE FROM worktrees WHERE id = X` (cascades `worktree_id` to NULL on the job row).
5. Set status to `merged`, `reviewed_at` to now.
6. Narrate each step in chat as it happens.

### Reject

1. Set status to `rejected`, `reviewed_at` to now.
2. Ask user in chat: *"Keep the worktree for inspection, or remove it?"*
3. If remove: `git worktree remove`, then `DELETE FROM worktrees WHERE id = X`. If keep: leave it.

### Push failure handling

Local merge stays. Set status to `merged`. On successful push, set `worktrees.push_pending = 0`. On failed push, set `worktrees.push_pending = 1` and surface immediately ("merged locally, couldn't push: <reason>, want me to retry?"). No auto-retry. Don't undo the local merge. `push_pending` makes pending pushes queryable so Selene can list them proactively ("3 jobs merged but not pushed — retry now?"). Selene's "list pending pushes" query: `SELECT j.id, w.branch FROM jobs j JOIN worktrees w ON j.worktree_id = w.id WHERE w.push_pending = 1`.

### Send-back with feedback

Deferred to v0.3.x. v0.3.0 ships approve and reject only.

---

## Cancellation

Two signals. Selene picks based on user intent:

- **`cancel`** (soft) — orchestrator finishes its current pipeline stage, then bails before starting the next. "Stop when you can." Stage boundaries only: between researcher → planner → coder → reviewer transitions. Mid-stage cancellation would mean killing an in-flight sub-agent — messy, not worth it.
- **`kill`** (hard) — existing `agent kill` behavior; SIGKILL the process tree. "Stop now." For when the user wants immediate termination regardless of stage.

**Status.** Soft cancel (and kill) lands the job in `cancelled`. This is distinct from `rejected`: `rejected` means work completed but Scot didn't want it; `cancelled` means work didn't complete.

**Worktree disposition.** Same as reject: ask the user "keep for inspection, or remove?"

---

## Audit trail

The `jobs` table is the primary audit surface, with `worktrees` joined where relevant:

- **What was asked:** `jobs.briefing` (Selene's structured brief to the orchestrator).
- **What happened:** `jobs.summary` (orchestrator's spot-check report).
- **What changed:** `jobs.diff_files / diff_insertions / diff_deletions` plus `worktrees.path` / `worktrees.branch` for the on-disk artifact.
- **What the user decided:** `jobs.status` + `jobs.reviewed_at`.
- **Provenance:** `jobs.session_id` + `jobs.parent_message_id` link back to the conversation.

If we later want richer audit (per-task breakdown, tool-call logs), add `job_tasks` and `job_tool_calls` tables in v0.3.x. Don't pre-build.

---

## What's deliberately deferred to v0.3.x

- Per-task tracking in the schema.
- Reactive replanning mid-job (Selene revising her plan when an orchestrator returns surprises).
- Send-back with feedback in the reject flow.
- Auto-resolve strategies more sophisticated than one-shot.
- Conflict-resolution sub-agent.
- Multi-orchestrator dependency tracking (sequential orchestrators where B branches from A's merged work).

---

## Implementation order

All five chunks are shipped (2026-04-29):

1. Schema migration + Go query helpers. **Done** — `internal/store/schema.go`, `internal/store/worktrees.go`.
2. Worktree creation + hardening. **Done** — `internal/worktree/` package with `Manager.Create/Remove/Validate`, unit-tested.
3. Orchestrator dispatch path (CWD plumbing, `worktree` tool, `job briefing` param, post-loop writeback). **Done** — `e5f3dc5`, `fbf292d`, `328be73`.
4. Diff panel UI (two-pane Ctrl+D, approve/reject keybindings, `merge_job` flow). **Done** — `cb4a591`, `0c40775`, `8d37fd8`, `1ded27a`, `b3fe33a`.
5. Merge flow — drop auto-resolve, surface all rebase failures as conflict. **Done** — `d588b1f`.

Known usability gap (v0.3.x): `job(update)` does not accept `worktree_id`, requiring a direct SQL UPDATE to link a worktree to a job after creation. Tracked separately — see punch list in `.internal/research/2026-04-29-pipeline-ux-report.md`.
