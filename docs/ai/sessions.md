> **See also:** [concepts/sessions-and-steering.md](../concepts/sessions-and-steering.md) is the authoritative reference for this topic. This file documents the AI-facing perspective — what persists, how to query sessions, and how steering works.

# Sessions

A session is one conversation: a row in `sessions` (id, name, cwd, role, timestamps) with a stream of `messages` rows attached.

You don't create sessions through tools. The harness creates one when cairo starts; the user manages them via `/new`, the sessions panel (Ctrl+B), or `cairo -new`.

---

## What persists across sessions

- Memories, skills, prompt parts, custom tools, hooks, config, soul.
- Summaries (cross-session global scope) — search via `memory_tool(action="search")`.
- Facts — search via `memory_tool(action="search")` (unified with memories and summaries).
- Dream records — query directly via `bash sqlite3 ~/.cairo/cairo.db "SELECT summary FROM dreams ORDER BY ended_at DESC LIMIT 5"`.

What does NOT cross sessions automatically: the raw `messages` from a prior session. You see prior sessions only through summaries/facts/memories.

---

## Managing sessions

Sessions are managed via the `db_access` skill with `bash sqlite3`. For example:

```
bash(command="sqlite3 ~/.cairo/cairo.db 'SELECT id, name, role, cwd, created_at FROM sessions ORDER BY created_at DESC LIMIT 20'")
```

To delete a session (cascades through messages, summaries, facts, jobs, tasks):
```
bash(command="sqlite3 ~/.cairo/cairo.db 'DELETE FROM sessions WHERE id = <id>'")
```

This does NOT touch identity tables (memories, skills, etc.). Use this when the user wants to wipe conversation noise without losing identity.

---

## Summaries are your memory of past sessions

When you wonder *"have we talked about this before?"* — `memory_tool(action="search", query="...")`. This searches across memories, facts, and summaries from any session. The current session's recent summaries are also auto-injected as `## Conversation context`.

The summarizer runs in the background after each turn if `summary_threshold` (default 4) unsummarized messages have accumulated. It uses `summary_model` (default ministral-8b — small and fast).

---

## Steering and follow-ups

You don't manage these — the harness does. But you should understand them:

- **Steering** — user types while you're streaming. The message queues; the agent loop merges it after the current inner-loop iteration. You'll see it on the next outer pass.
- **Follow-ups** — system-queued instructions (e.g. `/init` queues a "now store what you learned" follow-up). Same merge point.

If you suddenly see a user message mid-flow with new instructions, that's steering. Acknowledge briefly and adjust.

---

## Threads (background work) ≠ sessions

Background tasks run as subprocess sessions of their own (each with a session_id), but they are addressed through `task` and `agent` tools. From your live session, you see their results as task artifacts and inbox notes — not as your session's own messages.

---

## What's not here yet

- No branching/forking of sessions. One linear thread per session.
- No merging two sessions. (You'd have to recreate notable content as memories/notes.)
