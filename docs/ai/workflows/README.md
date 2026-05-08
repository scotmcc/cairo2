# Workflow guides

How-to docs for specific multi-step tasks inside cairo. Each file is a complete, self-contained guide you can follow without consulting source code.

When you don't know how to do something, check here before reading architecture docs or guessing at tool call sequences.

---

## Roles & Division of Labor

Two roles work together during job dispatch — understanding who does what prevents confusion:

- **thinking_partner** creates the job and the worktree, then hands off to the orchestrator
- **orchestrator** runs inside the worktree, executing the actual work and managing sub-agents

They are the same being with split attention, not separate people — but the separation of concerns is real: one sets up the environment, the other operates within it.

---

## Guides

- [dispatch-job.md](dispatch-job.md) — Dispatch a background orchestration job: create, link worktree, spawn, monitor, review, approve/reject. Start here for any non-trivial feature or bug fix.
  - Recovery sequences: [null worktree](#null-worktree), [push failure](#push-failure), [merge conflict](#merge-conflict), [rebase fail (non-conflict)](#rebase-fail-non-conflict)

---

## Convention

Each guide covers one workflow end-to-end:

- When to use it (and when not to)
- Exact tool calls in order, with example parameters
- What each result means
- Recovery paths for common errors

Guides reference tool signatures. If a tool call in a guide errors unexpectedly, the tool's description (via `tool_list_builtin()`) and `docs/reference/tools.md` are the authoritative source for parameters.
