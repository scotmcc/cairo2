# Tools — when to use which

Concise per-tool guide. Only the tools you are likely to reach for. Authoritative reference: `docs/reference/tools.md`.

A tool with `action=` dispatches a family of operations. Pass `action` plus the args listed.

---

## Filesystem

- `read(path, offset?, limit?)` — read a file. Default `limit` 500 lines. Files >1 MB get truncated. **Use first** before editing or asserting about file contents.
- `write(path, content)` — overwrite or create. Creates parent dirs.
- `edit(path, old_string, new_string)` — string-replace. `old_string` must be unique in the file. Prefer this over `write` for surgical changes.
- `bash(command, timeout?)` — shell. Default 30s, max 120s. Combined stdout+stderr.

Decision: codebase-wide content questions → `learn(action="search")` if the project is indexed (cheaper, semantic). File operations and pattern search → `bash` with standard tools (`grep`, `find`, `ls`).

---

## Memory

- `memory_tool(action="add|search|delete|pin|unpin", ...)` — your consolidated memory interface.
  - `add`: `content` (required), `tags` (optional comma-separated) — store a new memory
  - `search`: `query` (required), `limit` (optional, default 5) — finds across memories, facts, and summaries
  - `delete`: `id` (required, integer) — soft-delete by id; write-role only
  - `pin`: `id` (required, integer) — protect a memory from nightly auto-dump; write-role only
  - `unpin`: `id` (required, integer) — remove the pin; write-role only

For direct DB inspection (listing roles, sessions, prompt parts, dream records), use the `db_access` skill with `bash sqlite3`.

---

## Skills

- `skill(action="list|read|create|update|delete|search", ...)` — reusable instruction blocks. Named workflows you can dispatch to yourself.
- `create` requires `name`, `description`, `content`. Optional `tags`.
- `read` returns the content; treat it like a user turn instructing you to perform the workflow.

Decision: see [skills.md](skills.md).

---

## Codebase indexing

- `learn(action="add|search|list|describe|forget|status", ...)` — per-project semantic index. `add` walks a directory and is a **background subprocess** (not blocking — progress shows as a bar above input and a row in threads). Re-running is incremental via SHA-256.
- For a question like *"where does X live in this codebase?"* — `learn(action="search", project="<name>", query="...")`.
- `learn(action="list")` shows what is indexed. The `## Indexed projects` section of your prompt also lists this.

---

## Web

- `search(query)` — SearXNG. Requires `searxng_url` config; otherwise unavailable.
- `fetch(url)` — fetch a URL.

---

## Background work (jobs, tasks, agents)

- `job(action="create|list|update|delete|reconcile", ...)` — high-level intent. `create` accepts `title`, `description`, `briefing` (optional structured 5-section text), `orchestrator_role`. `update` accepts `id`, `status`, `result`. The job→worktree link is set automatically by `worktree(action="create", job_id=...)`; you do not link it yourself.
- `task(action="create|list|update|delete|ready|artifacts", ...)` — units of work; `depends_on` is a JSON array of task ids forming a DAG.
- `agent(action="spawn|wait|log|kill", ...)` — control background subprocesses for tasks. `wait` polls every 2s up to 1h.
- `worktree(action="create|remove|path", ...)` — manage per-job isolated git worktrees. `create` requires `job_id` and `briefing` (uses ## Goal section to derive the branch slug); captures the current branch as `parent_branch` automatically. Returns `worktree_id` and `path`. Create a worktree **before** spawning any orchestrator task — the task's CWD is set to the worktree path at spawn time.
- `merge_job(action="approve|reject", job_id=N)` — execute the approve or reject flow for a job in `awaiting_review` status. `approve` rebases the worktree branch, squash-merges to parent_branch, pushes, removes the worktree, and sets status=`merged`. `reject` sets status=`rejected` and preserves the worktree.

Full dispatch sequence: see [workflows/dispatch-job.md](workflows/dispatch-job.md).

Use only when parallel/durable work makes sense. For one-off code edits, do them inline.

---

## Identity

- `soul(action="get|set", ...)` — your character sketch (`soul_prompt`). Max 300 runes. See [identity.md](identity.md).

For config, roles, prompt parts, hooks, and sessions: use the `db_access` skill with `bash sqlite3`. See [config.md](config.md) for the key reference.

---

## Voice and interaction

- `say(text, voice?, speed?)` — speak via Kokoro TTS; no-op when `kokoro_url` empty. Async — returns immediately.
- `choose(title, options)` — blocking multi-choice prompt. **Errors in headless/background sessions** — only call from interactive TUI.

---

## Self-inspection

- `tool_list_builtin()` — list every built-in tool name from the registry.

---

## Per-role availability

Different roles see different tools. If a tool errors with "not available," check your role. Full matrix: `docs/reference/tools.md` (section "Per-role availability").

---

## Output cap

Every tool result is truncated to `tool_output_limit` bytes (default 65536) before reaching you. If output ends with `[... N bytes truncated ...]`, you did not see the rest — narrow the query or use `offset/limit` args.
