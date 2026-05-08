# Schema — your DB at a glance

You don't write SQL. You operate on the DB through tools. This file maps the **mental model**: which tool reaches which table, what columns matter when you reason about state.

Authoritative reference: `docs/architecture/database.md`.

---

## Tables and how to reach them

| Table | How to access | What it holds |
|---|---|---|
| `config` | `db_access` skill (`bash sqlite3`); `soul` tool for `soul_prompt` row | Identity + runtime settings (key/value). |
| `prompt_parts` | `db_access` skill | Composable system-prompt fragments. `trigger` = `NULL` (always) or `role:<name>` / `tool:<name>`. |
| `memories` | `memory_tool(action="add\|search")` | Stable identity knowledge. Top `memory_limit` injected every turn. |
| `notes` | `db_access` skill | Scratch — drafts, plans. Not auto-injected. (The `note` tool was removed in v0.3.0.) |
| `skills` | `skill` | Reusable instructions. See [skills.md](skills.md). |
| `summaries` | `memory_tool(action="search")` (unified search); `db_access` for direct access | Compressed conversation history. Auto-written by summarizer. **Cross-session** scope. |
| `facts` | `memory_tool(action="search")` (unified search); `db_access` for direct access | Atomic observations extracted during summarization. Immutable. |
| `dreams` | `db_access` skill (`db.Dreams`) | Narrative metadata for each dream-pass: one row per date (`date TEXT UNIQUE`). Columns: `id`, `created_at`, `date`, `narrative_path`, `themes`, `mood`, `state_daily_ref`, `last_edited_at`. No embedding — dreams are excluded from semantic search by construction. |
| `dream_log` | `db_access` skill (`db.DreamLog`) | Mutation audit trail written by the dream-pass. One row per action (merge, write, delete). Columns: `id`, `dream_id`, `created_at`, `action`, `target_table`, `target_ids` (JSON), `note`. |
| `hooks` | `db_access` skill; `~/.cairo/hooks.d/` file-drop planned (not yet implemented — see ROADMAP.md) | Lifecycle shell commands. See [hooks.md](hooks.md). |
| `custom_tools` | `db_access` skill | Tools you wrote yourself. |
| `roles` | `db_access` skill | Mode overlays — model + tool allowlist + base prompt key. |
| `sessions` / `messages` | `db_access` skill (delete cascades to messages, summaries, facts) | Conversations. Messages are persisted by the agent loop, not by you directly. |
| `jobs` | `job` tool | Orchestration units of work. v0.3.0 jobs have `worktree_id IS NOT NULL`. |
| `tasks` | `task` tool | Steps within a job; DAG via `depends_on`. |
| `worktrees` | `worktree` tool; `merge_job` removes on approve | On-disk git worktree artifacts. `DELETE FROM worktrees` cascades `worktree_id` to NULL on linked jobs. |
| `projects` / `indexed_files` | `learn` | Per-project semantic file map. |

---

## Tables you read but don't edit

- `messages` — written by the agent loop. Each turn writes user, assistant tool-call request, tool result(s), final assistant text.
- `task_artifacts` — written by background tasks. Read via `task(action="artifacts", id=...)`.
- `code_index` — legacy (was used by the removed `code_search` tool). Use `learn` instead.

## Job/worktree relationship

`jobs.worktree_id` is a nullable FK to `worktrees.id`. `worktree(action="create")` sets it automatically when called with a `job_id` — no separate update step is needed. The result reads `worktree created (id: W, ...) linked to job N`.

When a worktree row is deleted, `ON DELETE SET NULL` clears `jobs.worktree_id` automatically. The job row is preserved.

`worktree_id IS NOT NULL` distinguishes v0.3.0 orchestrated jobs (with worktrees) from old-style jobs (without).

---

## Embedding rules

Every embeddable table (`memories`, `notes`, `skills`, `summaries`, `facts`, `indexed_files`) carries an `embed_model TEXT` column. Search **skips** rows whose `embed_model` doesn't match the current query model — prevents nonsense scores from dimension mismatch. If embeddings look sparse, the user may have changed `embed_model`; cairo prints a startup warning listing affected tables. Re-index projects with `cairo learn`; memories and facts can be re-embedded via `cairo dream` or direct DB work.

---

## Importance and time decay

`memories`, `notes`, `facts` carry `importance REAL` (default 0.5). Search ranking:

```
score = cosine_similarity × decayed_importance
decayed_importance = importance × max(1.0 − days/180 × 0.4, 0.6)
```

Updating a memory resets the clock (via `updated_at`). Facts use `created_at` since they are immutable.

`memories` also carries `weight REAL` (default 0.5) — a separate lifecycle signal bumped on each retrieval and decayed nightly. Weight drives auto-promote (weight ≥ 1.0 → importance=1.0) and auto-dump (weight ≤ 0 → soft-deleted). **Weight is not included in retrieval scoring** — retrieval uses `cosine × decayed_importance` only. When `pinned_at IS NOT NULL`, auto-dump is suppressed regardless of weight. See [memory-and-facts.md](memory-and-facts.md) for the full doctrine.

---

## Memory soft delete

`memory_tool(action="delete", id=<int>)` soft-deletes a memory by ID — sets `deleted_at` so the row is excluded from all reads and searches thereafter. The row is retained for audit. Only `thinking_partner`, `dream`, and `orchestrator` roles may delete. For direct SQL access use `db_access` with `bash sqlite3`; a SQL `DELETE` is a hard delete with no undo path.

---

## Cascade on session delete

Deleting a session row (`DELETE FROM sessions WHERE id = ...`) cascades to `messages`, `summaries`, `facts`, `jobs` (and `tasks`/`task_artifacts` via `jobs`). The `dreams` table has no `session_id` FK — dream records are not session-scoped and are not affected by session deletes. It does **not** touch `memories`, `skills`, `roles`, `prompt_parts`, `custom_tools`, `hooks`, or `config`. Use this when you want a clean slate of conversation history without losing identity.

---

## FTS5 mirror tables

`memories_fts`, `notes_fts`, `skills_fts` are virtual full-text indexes maintained by triggers on the source tables. They power `mode="exact"` and `mode="hybrid"` search.

---

## What you don't get

- No row-level diff/version log.
- No down-migrations. Schema-altering changes only.
- No per-row ACL. Anything in the DB is reachable by any tool that owns the table.
