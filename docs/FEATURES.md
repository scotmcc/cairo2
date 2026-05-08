# Cairo Features

Cairo (**C**ollaborative **A**I **R**hizomatic **O**rchestrator) is a local-first coding assistant built on three pillars: **one being, not a team of agents**; **the database is the identity**; and **rhizomatic connections** — any component can reach any other through a shared `cairo.db`.

---

## 1. One Being, Many Threads

Cairo rejects the "multi-agent crew" pattern. There's no orchestrator handing off to separate coder/reviewer identities with isolated memories and histories. Instead: **one unified being that can run parallel threads of attention**. All modes share a single `memories` table, a single `config` store, and a single soul. What one thread learns, the whole being knows.

---

## 2. SQLite as Identity

Everything lives in a single `~/.cairo/cairo.db` file — tables covering identity, memory, sessions, history, tools, skills, hooks, dreams, jobs, worktrees, and the learn project index. The Go binary is completely stateless: delete the binary, reinstall it, and the being is unchanged.

- **The soul is a config row** — editable at any time via `soul(action="set")`
- **Template substitution** — every config key available as `{{key}}` in prompts, skills, and tool addenda
- **Prompt composition from rows** — the system prompt is assembled fresh each turn from composable `prompt_parts` fragments triggered by role or tool
- **Inspectable identity** — query any attribute of the being with a `SELECT`

---

## 3. Role-Based Mode Switching

A session runs in a specific role that determines three things: which Ollama model is used, which tools are available (via allowlists), and what framing text goes into the system prompt. Roles are overlays, not separate identities — same soul, different focus.

**Seven built-in roles:**

| Role | Purpose | Default model |
|---|---|---|
| `thinking_partner` | Interactive collaboration (default) | Generalist |
| `orchestrator` | Coordinate jobs and track progress | Generalist |
| `planner` | Research and design before implementation | Read-heavy, think enabled |
| `coder` | Write and edit code, run tests | Coding-tuned |
| `reviewer` | Verify output, run tests | Small/fast |
| `dream` | Headless maintenance — consolidate memories and facts | (global default) |
| `researcher` | Gather facts — read context, return structured findings | Read-heavy, think enabled |

Roles are rows in the DB; add your own by inserting a role row plus a matching `prompt_parts` entry. Each role can use a different Ollama model. **New in v0.3.0:** per-role model defaults seeded in DB; planner and researcher roles have think enabled by default; hardcoded fallback model removed — missing role model is now a startup error.

---

## 4. Rich Memory Model

Four distinct persistence layers, each with a different job:

**Memories** — Stable, curated identity knowledge injected into every prompt (default top 15). Add and search via `memory_tool`. Retrieval score = `cosine * decayImportance(importance)` — importance decays slowly over 180 days with a 60% floor.

**Summaries** — Paragraph-sized distillations of conversation turns, written automatically by a background summarizer after threshold accumulation. Global scope: summaries from one session are findable from any other via `memory_tool(action="search")`.

**Facts** — Atomic observations extracted during summarization. Immutable records ("user's name is Scot", "project uses Go 1.25"). `memory_tool(action="search")` searches across memories, facts, and summaries in one call.

**Search modes** available on memories, facts, and summaries via `memory_tool(action="search")`: semantic (embedding cosine similarity). For keyword search, use `bash sqlite3` with the `db_access` skill.

**New in v0.3.0:**
- `weight` column tracks retrieval frequency (bumps on hit, nightly decay). Separate from `importance` — not used in retrieval scoring.
- `last_retrieved_at` column stamped on each search hit.
- Pre-write dedup: duplicate memory content rejected at write time (override with `force=true`).
- `delete` action added to `memory_tool` (soft-delete via `deleted_at`).
- Write-partition by role: orchestration-chain roles (orchestrator, planner, coder, reviewer, researcher) cannot write to memories reserved for the identity layer.
- Importance-guidance addendum seeded into `memory_tool` prompt_parts.

---

## 5. Self-Maintenance — Dream Mode

`cairo dream` runs a headless maintenance cycle in the `dream` role. The being reviews and consolidates all memories, facts, and summaries — essentially "sleeping" to sort through what it's learned. Before any work starts, Cairo takes a DB snapshot via `VACUUM INTO`, so a bad consolidation can be rolled back with `cairo import`. Intended for scheduled runs (cron, launchd) or manual cleanup after long sessions.

**New in v0.3.0 — nightly lifecycle pass (runs before each dream agent prompt):**
- **Weight decay:** memories not retrieved in the past 24 hours lose 0.001 weight.
- **Auto-dump:** memories whose weight reaches 0.0 are soft-deleted (`deleted_at` set).
- **Auto-promote:** memories whose weight reaches >= 1.0 are promoted to `importance=1.0` (one-way bridge from retrieval frequency to permanent salience).

---

## 6. Portable Identity — `.cairo` Bundles

The entire being is exportable as a single file: a gzipped tar archive containing a `manifest.json` and SQLite snapshot.

- **`cairo export <bundle.cairo>`** — exports identity (memories, skills, roles, prompt parts, custom tools, config). Conversation history excluded by default for privacy.
- **`cairo import <bundle.cairo>`** — replaces the current DB after auto-backing it up.
- **`cairo diff <bundle.cairo>`** — compares a bundle to local state without touching anything.

Bundles are inspection-friendly: standard `tar`, `gunzip`, and `sqlite3` can poke at them. Moving Cairo to a new machine is literally `cp cairo.db`.

---

## 7. Job Orchestration Pipeline (new in v0.3.0)

Cairo can dispatch background coding jobs into isolated git worktrees and merge results back through a human-gated approval flow.

**Three-level model:**
- **Job** — high-level intent (e.g., "refactor memory subsystem"); carries a `parent_branch`, a briefing, and a `worktree_id`
- **Task** — one chunk of work assigned to a role with optional DAG dependencies (`deps` JSON array)
- **Agent** — background subprocess spawned for a task, writing results to the DB and its own log file

**Worktree isolation:** `worktree(action="create")` creates a git worktree at `.claude/worktrees/<name>` branched from `parent_branch`. Jobs run in that worktree; the main working tree stays clean. `worktree(action="remove")` tears it down after merge or abandonment.

**CWD plumbing:** background task subprocesses resolve their working directory from the job's worktree. `resolveTaskCWD` fails fast if a job has no worktree — no silent fallback to the main repo.

**Approve/reject merge flow:** when a job completes, it enters `awaiting_review` status. The TUI diff panel (Ctrl+D) shows a two-pane view — job list on the left, git diff on the right.
- `a` (approve): rebases the worktree branch onto `parent_branch` and fast-forward merges. If rebase fails (conflict), status stays `awaiting_review`; conflicts surface to the user rather than auto-resolving.
- `r` (reject): sets the job to `rejected` status, preserving the worktree for inspection.

**Configurable review limit:** `job_max_review_iterations` config key caps how many review cycles a job can go through before being force-closed.

**Tools:**
- `job(action=create|list|update|delete|reconcile)`
- `task(action=create|list|update|delete|ready|artifacts)`
- `agent(action=spawn|wait|log)` — background subprocess spawned by the task system
- `worktree(action=create|remove|path)` — git worktree lifecycle
- `merge_job(action=approve|reject)` — human-gated merge

---

## 8. Custom Tools — The AI Writes Its Own Capabilities

The being can create tools at runtime by registering a bash script or Python snippet in the database. On the next turn, the new tool appears in the model's registry alongside built-in tools — same interface, same JSON-schema parameters.

Custom tools get a restricted execution environment: only `PATH`, `HOME`, `TMPDIR`, `SHELL`, `CAIRO_ARG_*` variables, and optionally whitelisted extra env vars from `safe_env_extras`. No wholesale environment inheritance means secrets don't leak.

Tools persist in the DB, survive restarts, move with bundles, and come with an optional `prompt_addendum` that teaches the model when to use them.

---

## 9. Skills — Reusable Workflow Templates

A skill is a named block of instructional prose stored in the DB. Unlike tools (which execute code), skills are pure content: when invoked, Cairo pastes the skill text into a user turn and the model responds to it like any other instruction.

Seeded skills include `/init` (guided setup — learns project + preferences), `/init codebase` (codebase exploration only), `orchestrate` (briefing and launching background orchestrators), and `db_access` (safe `sqlite3` discipline for direct DB queries).

**Slash commands** are TUI-layer actions dispatched from the command drawer (type `/` on empty input): `/init`, `/config`, `/new` (drain current session via SummarizeAll, then restart), `/learn [path]` (index a directory), `/deepen` (foreground turn: searches memories, summaries, indexed projects, and facts about the current context and reports what it knows), `/reload` (re-exec cairo in-place), `/export`, `/help`, `/clear`, `/quit`.

Skills support template substitution (`{{key}}` → config value) and semantic/exact/hybrid search for discovery.

---

## 10. Semantic Code Indexing — `learn`

Cairo builds a per-project semantic index of source files. `learn(action="add")` walks a directory, summarizes and embeds each file, and stores results in `projects` / `indexed_files` tables.

**New in v0.3.0 — RAG chunked indexing:**
- Symbol-level chunking for Go (functions and type declarations), Ruby/Java (methods), Python (defs), Markdown (headings), and generic paragraph-based fallback.
- `MaxChunkTokens` cap prevents oversized embeddings — large symbols are split further.
- Stale file deletion: files removed from disk are pruned from the index on the next `add` run.
- `learn(action="search")` finds chunks by semantic similarity; results include relative path, line range, and auto-generated summary.
- **Fetch auto-ingest:** `fetch` tool auto-persists fetched pages and queues a learn ingest, so browsed URLs become searchable.

Re-running on an unchanged project is incremental (SHA-256 file hashes). Background subprocess with progress bar. Six actions: `add`, `search`, `list`, `describe`, `forget`, `status`.

**Embedding model split (new):** `learn` uses a dedicated `embed_model_code` config key for code chunk embeddings, separate from the prose `embed_model` used for memories, facts, and summaries. When `embed_model_code` is unset, `learn` falls back to `embed_model`. Use `cairo learn --reembed` to migrate all indexed projects after changing `embed_model_code`.

---

## 11. Compaction and Summarization

A background summarizer accumulates conversation turns and periodically writes paragraph summaries + extracted facts to the DB.

- **Token-pressure trigger (new in v0.3.0):** `summary_token_threshold` config key; when the estimated context window usage exceeds it, the summarizer fires regardless of turn count. Prevents context overflow on long sessions.
- **Named-entity preservation:** the summarizer prompt explicitly instructs the model to retain proper nouns, project names, file paths, and user-stated constraints in summaries — they are no longer silently dropped during compaction.
- Summarizer status indicator visible in the threads panel (Ctrl+T).

---

## 12. Discipline Mode (new in v0.3.0-rc)

Sessions can run in a tiered tool-access mode that limits what the model can call:

| Mode | Level | Allowed |
|---|---|---|
| `readonly` | 1 | `read`, `memory_tool` (search), `learn` (search/list), `skill` (list/read), `search`, `fetch`, `tool_list_builtin` |
| `scoped` | 2 | Readonly + `write`, `edit`, `say`, `choose` |
| `full` | 3 | All tools per the role allowlist (current default) |

Discipline mode is stored per-session and enforced inside each tool's `Execute` method. Action-dispatched tools (memory_tool, learn, skill) enforce per-action tiers internally.

---

## 13. Prompt Engineering

System prompt assembly in `BuildSystemPrompt` follows a fixed order: steering → base parts → soul → user context → role → tools → projects → summaries → memories → facts → temporal stamp.

**New in v0.3.0:**
- `tool_error_recovery` prompt part: guides the model on how to recover from tool errors using the `IsError` protocol.
- `IsError` protocol: `ToolResult.IsError=true` propagates through to the LLM message, signaling recoverable vs. fatal errors distinctly.
- XML handoff format for researcher → orchestrator structured findings.
- Orchestrator `BLOCKED` list: explicit list of things the orchestrator must not do itself.
- Researcher loop guard: prompt constraint preventing infinite search loops.
- `thinking_partner` constraints + output format section added to role prompt.
- `tool_refusal_handling` clause added (v0.3.0).
- Coder "think before tool" one-liner added.

---

## 14. Terminal UI with Panels

`cairo` (default) launches a Bubble Tea terminal interface with a scrollable transcript, slash-command drawer, and eleven pluggable panels:

| Panel | Toggle | Shows |
|---|---|---|
| help | `?` | Hotkeys and commands |
| memory | `Ctrl+E` | Memory spotlight |
| prompt | `Ctrl+P` | System prompt preview (fullscreen rail+detail) with live token meter |
| threads | `Ctrl+T` | Jobs/tasks tree + summarizer status indicator |
| files | `Ctrl+O` | File picker |
| sessions | `Ctrl+B` | Session browser |
| inspector | `Ctrl+Y` | Model, context window, counts |
| diff | `Ctrl+D` | Two-pane: job list + git diff; approve/reject keybindings |
| config | `Ctrl+G` | Browse and edit config keys |
| log | `Ctrl+L` | Log viewer |
| quote-reply | `Ctrl+R` | Quote a prior message and start a reply |

**Input features:**
- `!command` — run shell and use output as turn
- `@file` — inject file contents
- **Smart paste** — pastes > 800 chars or > 6 lines are diverted to a temp file and replaced with an `@paste:N` token
- `Ctrl+Enter` — dispatch input as background task (non-blocking)

**Tool toasts** — ephemeral rows above the input showing active tool calls: icon, name, arg preview, elapsed time while active; ✓/✗, duration, result size on completion. Successful toasts linger 3 s, error toasts 6 s. End-of-turn summary line in transcript: `[3 tools · 2.4 KB · memory, read×2]`.

**Global progress bars** — one bar per background task calling `SetProgress`. Used by `learn add` and other long-running subprocesses. Up to 3 visible; `+N more in flight` overflow.

**Activity states** in status bar during streaming:
- `⋯ awaiting model` — cold start
- `❋ thinking · N think` — reasoning tokens arriving
- `⤓ processing tool result` — post-tool re-prefill gap
- `⚠ silent Xs` — model silent for > 30 seconds
- `● Selene` — streaming content tokens

**Other TUI features (new in v0.3.0):**
- Live token meter in status bar (chars/4 approximation)
- Startup-time hotkey registration guard — duplicate or bare (non-ctrl) hotkeys fail at boot
- Loop detection toast — warns when the same tool appears ≥ 5 times in a 90-second window
- Session history rendered on resume (prior turns re-displayed on startup)
- Mid-turn steering — typing while the model streams works naturally

---

## 15. Session Management and End-of-Session Feedback

Sessions are scoped conversations tied to a working directory and role. They survive process restarts — Cairo resumes the most recent session for the current `cwd` by default, or you can specify one by ID.

**End-of-session feedback loop (new in v0.3.0):** when a session ends (shutdown or `/new`), Cairo fires a background async prompt asking the model to reflect on what it learned from the session — communication preferences, friction points, things that worked. The response is written as a feedback memory. Controlled by `session_feedback_enabled` (default `true`) and `session_feedback_min_messages` (default 6).

---

## 16. Hooks — Lifecycle Shell Commands

Store shell commands in the DB that fire automatically on agent lifecycle events. Errors don't abort the turn. Environment variables convey event context.

**Events:** `pre_tool | post_tool | session_start | session_end | pre_turn | post_turn | dream_completed | learn_indexed | task_completed | fact_promoted | summarizer_ran`

**Environment variables:** `CAIRO_EVENT` (always set), `CAIRO_CONTEXT_JSON` (structured payload, always set), `CAIRO_TOOL_NAME` (`pre_tool`/`post_tool` only), `CAIRO_TOOL_ARGS_JSON` (`pre_tool` only), `CAIRO_TOOL_RESULT` (`post_tool` only).

---

## 17. Voice Interaction (Optional)

The `say` tool uses Kokoro TTS to speak text aloud via local `afplay` (macOS). Configurable per-call with voice blends (`voice1(weight)+voice2(weight)`), speed control (0.5×–2.0×), and a default voice preset. Non-blocking — audio plays in a background goroutine while the tool returns immediately. Requires `kokoro_url` config key to be set.

---

## 18. Web Tools

- **`search`** — web search via a configured search backend (e.g., SearXNG instance)
- **`fetch`** — HTTP fetch with optional LLM-powered extraction; auto-persists fetched content and queues a learn ingest so browsed pages become semantically searchable

---

## 19. Event Bus Architecture

A typed, non-blocking event bus (`Bus`) decouples agent execution from UI rendering. Events include turn start/end, streaming tokens, tool start/end, tool progress updates, and errors. The same `runLoop` drives three different renderers (line CLI, TUI, background log) without coupling to any of them. Slow-subscriber drop counter logs when events are dropped rather than silently discarding them.

---

## 20. Two Deployment Modes, One Binary

- **Interactive** — `cairo`, `cairo -new`, `cairo -tui`: full conversation with line CLI or rich TUI
- **Background task** — `cairo -task <id> -background`: headless subprocess worker spawned by the `agent` tool, writing results to the DB and exiting
- **Dream** — `cairo dream`: headless maintenance run

Both modes share all code paths. Single-message mode (`cairo "question"`) sends one prompt and exits.

---

## 21. Consider — Pre-Turn Inner Dialogue

Before the assistant generates its main reply, cairo can run a parallel set of named "aspect" voices over the user's input. The feature is **disabled by default**.

**How it works:**

1. On each turn, all enabled aspects are called in parallel against the user's message. Each aspect is a small model call that returns a JSON object with two fields: `alignment` (0.0–1.0, how strongly the input expresses that aspect's traits) and a short first-person `thought`.
2. The thoughts from all enabled aspects are summarized by a configurable summarizer model.
3. The summary is injected into the system prompt as an "internal voices" preamble before the main reply runs.

The model sees a synthesized inner-dialogue fragment — not raw aspect outputs — and uses it as additional framing when composing its response.

**Aspects:**

Eight aspects are seeded by default and enabled when Consider is active: Joy, Heart, Trust, Curiosity, Sadness, Frustration, Fear, and Shadow. Each aspect has a name and a list of traits that define its voice. Aspects are managed from the Consider section of the config panel (`Ctrl+G`): add, edit, enable/disable, and delete aspects there.

**Controls (all in the config panel, Consider section):**

| Setting | What it does |
|---|---|
| Master on/off toggle | Enables or disables consider entirely for the session |
| Per-aspect model | Which Ollama model runs each aspect call (default: global model) |
| Summarizer model | Which model condenses the aspect thoughts before injection |
| Prompt template | The prompt sent to each aspect model — supports `{name}` and `{traits}` substitution |
| Aspect list | Per-aspect enabled/disabled toggle, name, and trait list |

**Config keys:** `consider.enabled`, `consider.model`, `consider.summary_model`, `consider.template`. Aspect definitions live in the `consider_aspects` table (name, traits, enabled flag).

**Why off by default — a note from the lead dev:**

The lead dev has found that giving the agent some emotional management tends to produce responses that feel like they come from someone who *cares* about the code — which ultimately achieves better results: more careful refactors, more honest disagreement, more attention to edge cases. The flip side: it can be jarring in a corporate setting, and some users find it distracting. So consider is **off by default** and opt-in. Enable it if you want a more personable collaborator; leave it off for a strictly mechanical one. Enter at your own risk. :)

---

## 22. HTTP Server (`cairo serve`)

`cairo serve` starts an HTTP server — default port 1337, overridable with `--port N` or the `server_port` config key.

**Endpoint families:**

| Endpoint | Notes |
|---|---|
| `POST /api/chat` | Native chat API |
| `GET /v1/models` | OpenAI-compatible model list |
| `POST /v1/chat/completions` | OpenAI-compatible completions; streaming via SSE |
| `POST /rpc` | JSON-RPC 2.0 |
| `GET /rpc/stream/{id}` | SSE stream for an in-progress RPC call |
| `GET /healthz` | Unauthenticated health check |

**Auth:** optional bearer-token authentication via `--auth` flag. Token is read from the `server_token` config key if present; generated and saved there if absent. Use `cairo token` to pre-generate and store a token without starting the server.

**Session bridge:** the HTTP server shares the agent loop with the interactive session. Requests are serialized — concurrent HTTP requests queue (depth 32) rather than run in parallel, keeping agent state consistent.

**Use cases:** remote access, VS Code extension backend, OpenAI-compatible clients (Cursor, Continue, etc.).

---

## Architecture Summary

```
Ollama (LLM host) ←── HTTP ──→ cairo (Go binary)
                                ↕
                          SQLite DB (~/.cairo/cairo.db)
```

No daemon, no sync service, no cloud dependency. One binary, one database file, one local LLM server. The Ollama interface is deliberately narrow (five functions: `New`, `Ping`, `StreamOnce`, `Embed`, `FetchModelInfo`), making it theoretically portable to other backends with minor adjustments.
