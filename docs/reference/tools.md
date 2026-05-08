# Built-in tools

The model has access to a fixed set of tools built into the binary. This is the full list (18 distinct built-in tools). Many are "consolidated" — one tool with an `action` argument that dispatches to a family of operations. A few are single-purpose.

For DB-direct operations (listing roles, sessions, prompt parts, dream records, config), use the `db_access` skill with `bash sqlite3 ~/.cairo/cairo.db`. File-drop hook support via `~/.cairo/hooks.d/` is planned but not yet implemented — use `db_access` to manage hooks directly in the DB.

See [Custom tools](../development/custom-tools.md) for how the model can extend this set at runtime.

---

## Filesystem

Four tools for reading, writing, and editing files. File exploration and pattern search use `bash` with standard shell tools (`grep`, `find`, `ls`).

### `read`
Read a file's contents.
- `path` (string, required)
- `offset` (integer, optional) — line number to start reading from (1-based)
- `limit` (integer, optional, default 500) — maximum lines to return

Files exceeding 1 MB are truncated with a notice. If the file has more lines than `limit`, output ends with a truncation notice and a hint to use `offset`/`limit` to read more.

### `write`
Write a full file, creating directories as needed. Overwrites.
- `path` (string, required)
- `content` (string, required)

### `edit`
String-replacement edit: find `old_string`, replace with `new_string`. Errors if `old_string` isn't unique in the file.
- `path` (string, required)
- `old_string` (string, required)
- `new_string` (string, required)

### `bash`
Run a shell command. Combined stdout+stderr returned.
- `command` (string, required)
- `timeout` (integer, optional) — seconds, default 30, max 120

---

## Memory

### `memory_tool`
Consolidated memory interface. Searches across memories, facts, and summaries from a single call.
Actions: `add | search | delete | pin | unpin`
- `add`: `content` (required), `importance` (optional 0–1, default 0.5), `tags` (optional comma-separated), `force` (optional boolean) — store a new memory with a fresh embedding. Near-duplicate detection runs before write (cosine similarity > `memory_dedup_threshold` config, default 0.85); pass `force=true` to bypass.
- `search`: `query` (required), `scope` (optional: `memories,facts,summaries` or `all`, default `all`), `mode` (optional: `semantic`, `exact`, `hybrid` — default `hybrid`), `limit` (optional, default 10) — semantic + FTS5 search across memories, facts, and summaries (deduplicated, each result tagged with its source).
- `delete`: `id` (required, integer) — soft-delete the memory with the given ID. The row is retained for audit (via `deleted_at` timestamp) but excluded from all future reads and searches. Only permitted for roles: `thinking_partner`, `dream`, `orchestrator`.
- `pin`: `id` (required, integer) — set `pinned_at` on the memory. Pinned memories survive nightly auto-dump and are never the merged-away source in a dream-pass deduplication. Only permitted for roles: `thinking_partner`, `dream`, `orchestrator`.
- `unpin`: `id` (required, integer) — clear `pinned_at`. Only permitted for roles: `thinking_partner`, `dream`, `orchestrator`.

Write, delete, and pin actions (`add`, `delete`, `pin`, `unpin`) are restricted to roles `thinking_partner`, `dream`, and `orchestrator`. For listing all memories or direct row inspection: use the `db_access` skill with `bash sqlite3`.

---

## Skills

### `skill`
Actions: `list | read | create | update | delete | search`
- `list`: no args — returns all skill names + descriptions
- `read`: `name` (required) — returns the skill's full content
- `create`: `name` (required), `description` (required), `content` (required), `tags` (optional)
- `update`: `name` (required), + any field to change
- `delete`: `name` (required)
- `search`: `query` (required), `limit` (optional, default 5), `mode` (optional: `semantic` default, `exact`, `hybrid`)

Skills are reusable instructions — runnable "prompts" the being can dispatch to itself. See [Skills](../getting-started/skills.md).

---

## Jobs and tasks

### `job`
Actions: `create | list | update | delete`
- `create`: `title` (required), `description` (optional), `orchestrator_role` (optional, default `orchestrator`)
- `list`: no args
- `update`: `id` (required), `status` (optional), `result` (optional)
- `delete`: `id` (required)

### `task`
Actions: `create | list | update | delete | ready | artifacts`
- `create`: `job_id` (required), `title` (required), `description` (optional), `assigned_role` (optional, default `coder`), `depends_on` (optional, array of task ids)
- `list`: `job_id` (optional) — filter to one job's tasks
- `update`: `id` (required), `status` (optional), `result` (optional)
- `delete`: `id` (required)
- `ready`: `job_id` (optional) — returns tasks whose deps are done and status is pending
- `artifacts`: `id` (required) — returns the task's `task_artifacts` rows

### `agent`
Actions: `spawn | wait | log | kill` — controls background agents (parallel threads).
- `spawn`: `id` (required) — spawn a subprocess for task `id`. Atomic dependency check.
- `wait`: `id` (required), `timeout` (optional seconds, default 300, max 3600) — block until task terminal
- `log`: `id` (required), `tail` (optional) — read the task's captured stdout/stderr
- `kill`: `id` (required) — send SIGTERM to a running task's process. Marks the task `cancelled`.

See [Background work](../development/background-work.md) for workflows.

---

## Worktrees

### `worktree`
Manage per-job git worktrees. A worktree isolates each job's file edits to its own branch so parallel jobs do not interfere. Create one before spawning the orchestrator task; remove it after the job is merged or rejected.
Actions: `create | remove | path`
- `create`: `job_id` (required), `briefing` (required) — create a worktree for a job. Captures the current branch as `parent_branch` via `git symbolic-ref --short HEAD`. Returns `worktree_id` and `path`.
- `remove`: `worktree_id` (required) — remove a worktree and its DB row.
- `path`: `worktree_id` (required) — look up the on-disk path for a worktree.

Requires discipline mode `full`.

### `merge_job`
Complete the approve or reject flow for a job in `awaiting_review` status. The merge sequence is deterministic and safety-critical — implemented in Go rather than delegated to the model's reasoning.
Actions: `approve | reject`
- `approve`: `job_id` (required) — rebase the worktree branch onto `parent_branch`, squash-merge into `parent_branch`, push to remote, remove the worktree, set job `status=merged`. On rebase conflict, aborts and sets `status=conflict`, preserving the worktree for inspection. On push failure, sets `push_pending=1` and preserves the worktree; job is still marked `merged` locally.
- `reject`: `job_id` (required) — set job `status=rejected`, preserve the worktree for inspection. To remove the worktree call `worktree(action=remove)` separately.

Requires discipline mode `full`. Note: `merge_job` is registered outside `Default()` and is not included in any seeded role allowlist — add it to a role's tools via `db_access` if needed.

---

## Identity

### `soul`
Actions: `get | set`
- `get`: no args — returns the current soul_prompt
- `set`: `content` (required, max 300 chars) — replace the soul

For sessions, config, roles, prompt parts, and hooks: use the `db_access` skill with `bash sqlite3 ~/.cairo/cairo.db`. See [Config keys](config-keys.md) for the key reference and [Hooks](../ai/hooks.md) for hook events and env vars.

---

## Web

### `search`
Search the web via SearXNG (requires `searxng_url` config).

### `fetch`
Fetch a URL and return its content.

---

## Codebase indexing

### `learn`
Build and query the per-project map of summarized files. The sole codebase-search path — `code_search` was removed in v0.2.1.

Actions: `add | search | list | describe | forget | status`

- `add`: walk a directory, summarize each file via the summary model, embed, store. Spawns a background subprocess — progress appears as a bar above the input and as a row in the threads panel. Args: `path` (required), `project` (optional — defaults to basename of `path`), `summary_model` (optional override).
- `search`: semantic search over a project's file summaries. Returns `[score] rel_path (type, bytes)\n  summary` for each result. Args: `project` (required), `query` (required), `limit` (optional, default 10).
- `list`: list known projects with file counts and last-updated time.
- `describe`: get a project's auto-generated description plus metadata. Args: `project` (required).
- `forget`: delete a project and all its indexed files. Args: `project` (required).
- `status`: show in-flight `learn add` task progress. Args: `project` (optional filter).

Re-running `add` on the same project is incremental: SHA-256 hash comparison skips unchanged files.


---

## Voice and interaction

### `say`
Speak text aloud via Kokoro TTS, played locally with `afplay`. No-op when `kokoro_url` is empty. Currently macOS-only — uses `afplay` via Kokoro TTS. Linux/Windows fallback not implemented.
- `text` (string, required) — what to say; brief and conversational
- `voice` (string, optional) — voice blend; falls back to `kokoro_voice` config, then to default `af_heart(8)+af_nicole(2)`
- `speed` (number, optional, default `1.0`) — playback speed, clamped to 0.5–2.0

Audio playback runs in a background goroutine; the tool returns immediately.

### `choose`
Present a multiple-choice prompt and block until the user selects one. Returns the selected option as the tool result.
- `title` (string, required) — the question
- `options` (array of strings, required) — 2–8 choices

Errors out (`requires an interactive session`) when called from headless or `-background` mode — there's no overlay to render. The TUI displays the choice as a panel; `Esc` cancels the agent's turn rather than returning a default.

---

## Self-inspection

### `tool_list_builtin`
List every built-in tool name.
- No args. Auto-populated from the registry — stays in sync as tools are added.

---

## Per-role availability

Different roles see different tools. Seeded defaults as of v0.3.0:

| Tool | thinking_partner | orchestrator | planner | coder | reviewer | dream | researcher |
|---|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| read | ✓ | ✓ | ✓ | ✓ | ✓ |   | ✓ |
| write | ✓ |   |   | ✓ |   |   |   |
| edit | ✓ |   |   | ✓ |   |   |   |
| bash | ✓ | ✓ | ✓ | ✓ | ✓ |   | ✓ |
| memory_tool | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ |
| skill | ✓ |   | ✓ |   |   |   |   |
| job | ✓ | ✓ |   |   |   |   |   |
| task | ✓ | ✓ |   | ✓ | ✓ |   |   |
| agent | ✓ | ✓ |   |   |   |   |   |
| soul | ✓ |   |   |   |   |   |   |
| search | ✓ | ✓ | ✓ |   |   |   | ✓ |
| fetch | ✓ | ✓ | ✓ |   |   |   | ✓ |
| learn | ✓ |   | ✓ | ✓ | ✓ |   | ✓ |
| say | ✓ |   |   |   |   |   |   |
| choose | ✓ |   |   |   |   |   |   |
| worktree | ✓ |   |   |   |   |   |   |
| merge_job | — | — | — | — | — | — | — |

Custom tools (DB-stored scripts) are always loaded regardless of role, as they are authored by the being for its own use. See [Roles](../concepts/roles.md).

---

## Removed tools

### `grep`, `find`, `ls` (removed in v0.3.0)

Redundant with `bash`. Use `bash(command="grep ...")`, `bash(command="find ...")`, `bash(command="ls ...")`.

### `memory`, `summary_search`, `fact_search`, `fact_promote`, `fact_list`, `summary_rewrite`, `dream_search` (removed in v0.3.0)

Superseded by `memory_tool` (add + unified semantic search across memories, facts, and summaries). For maintenance operations (promote, rewrite, list), use the `db_access` skill with `bash sqlite3`.

### `note`, `custom_tool`, `hook`, `session`, `role`, `prompt_part`, `config` (removed in v0.3.0)

Replaced by the `db_access` skill (bash sqlite3 discipline). Direct DB queries are more flexible and eliminate the token cost of wrapping every DB operation in a dedicated tool description.

### `code_search` (removed in v0.2.1)

Removed in v0.2.1. Use `learn` for codebase questions. Historic: this tool searched chunk-level raw-content embeddings stored by `cairo index`; superseded by `learn`'s file-summary embeddings which are project-namespaced and retrieval-quality.

---

## Known rough edges

- **Tool output cap is global, not per-tool.** Every tool result is truncated to `tool_output_limit` bytes (default 65536 / 64KB) before reaching the model, with a notice appended. Per-tool caps (read, bash, grep, fetch) still apply on top.
- **No streaming tool progress.** Tools run to completion and return one result. Long-running tools can't show partial output to the model (only to the event bus as `EventToolUpdate`, which is a UI signal).
- **`agent(action="wait")` polls.** 2-second poll interval; up to 1 hour max. Fine for the current use case; not ideal if many concurrent waits stack up.
