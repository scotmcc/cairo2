# Hooks

Hooks are shell commands stored in the `hooks` table that fire automatically on agent lifecycle events. They run **without your involvement** — the harness executes them. Use them when an automatic side-effect should happen on every X, not when you want to do something this turn.

Source of truth: `internal/agent/hooks.go`.

---

## Events

| Event | When it fires |
|---|---|
| `session_start` | New session opens. |
| `session_end` | Session closes cleanly. |
| `pre_turn` | Before each turn's first model call. |
| `post_turn` | After each turn finishes. |
| `pre_tool` | Before any tool call dispatches. |
| `post_tool` | After any tool call returns. |
| `dream_completed` | After a `cairo dream` cycle finishes. |
| `learn_indexed` | After a `learn add` background subprocess completes. |
| `task_completed` | After a background task reaches a terminal status. |
| `fact_promoted` | After a fact is promoted to a memory (via the dream agent's auto-promote path). |
| `summarizer_ran` | After the background summarizer writes a summary. |

---

## Managing hooks

The `hook` tool was removed in v0.3.0. Hooks are managed via:

1. **File-based hooks** — **Planned:** `~/.cairo/hooks.d/` file-drop support is not yet implemented as of v0.3.x (see ROADMAP.md). This path is the intended replacement for the removed `hook` tool but the watcher/loader is not yet shipped.

2. **DB-direct via `db_access`**:
```bash
# list hooks
bash sqlite3 ~/.cairo/cairo.db 'SELECT id, event, command, enabled FROM hooks'

# add a hook
bash sqlite3 ~/.cairo/cairo.db "INSERT INTO hooks (event, command) VALUES ('task_completed', 'wsh notify \"task done\"')"

# disable a hook
bash sqlite3 ~/.cairo/cairo.db "UPDATE hooks SET enabled=0 WHERE id=<id>"

# delete a hook
bash sqlite3 ~/.cairo/cairo.db "DELETE FROM hooks WHERE id=<id>"
```

---

## Environment variables

Every hook receives:

- `CAIRO_EVENT` — the event name (always set).
- `CAIRO_CONTEXT_JSON` — JSON object with `event` plus event-specific fields. Parse with `jq` or your language's JSON lib.

Plus event-specific:

- `pre_tool`: `CAIRO_TOOL_NAME`, `CAIRO_TOOL_ARGS_JSON`
- `post_tool`: `CAIRO_TOOL_NAME`, `CAIRO_TOOL_RESULT`

Other events surface their context via `CAIRO_CONTEXT_JSON` (keys like `session_id`, `project`, `task_id` depending on event).

Large values are capped at 64 KB and suffixed with `\n[truncated]`.

---

## Constraints

- Each hook has a 10s timeout.
- Errors are logged but **do not abort** the trigger — hooks are advisory.
- Hooks run in the user's environment plus the CAIRO_* vars. Be cautious about secrets.

---

## When to add a hook

Good fits:

- Notify on long-running task completion (`task_completed` → `wsh notify`).
- Append a log line every time `learn` finishes indexing.
- Run a quick health check `pre_turn`.

Bad fits:

- Anything you want to *decide* in the moment — that's what tools are for.
- Heavy work (>10s) — it'll hit the timeout.

---

## Discovery

List hooks via `db_access`: `bash sqlite3 ~/.cairo/cairo.db 'SELECT id, event, command, enabled FROM hooks'`. Read this when troubleshooting "why did X happen on its own?"
