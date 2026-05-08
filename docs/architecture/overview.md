# Architecture overview

Cairo is a Go binary, a SQLite database, and an Ollama server. Those three pieces, with a few library dependencies for the TUI and embeddings. No daemon, no sync service, no cloud anything.

This doc is the picture that makes the subsystem docs easier to read.

---

## The three pieces

```
  ┌─────────────────────────────────────────────────────┐
  │                                                     │
  │    Ollama  ←──── HTTP ────→  cairo (Go binary)      │
  │  (LLM host)                     ↕                   │
  │                             SQLite DB               │
  │                          ~/.cairo/cairo.db           │
  │                                                     │
  └─────────────────────────────────────────────────────┘
```

**Ollama** runs models locally. Cairo talks to it over HTTP at `http://localhost:11434` by default (configurable via `config.ollama_url`). Two endpoints are used: `/api/chat` for streaming generation and `/api/embeddings` for vector embeddings.

**The Go binary** is stateless. It opens the DB, loads roles/tools/prompts from it, talks to Ollama, and writes results back. Restarting the binary loses nothing.

**The SQLite database** is the being. Identity, memories, sessions, history, tools, skills, jobs, tasks, hooks, dreams, learn project index — all in 19 tables. See [Database](database.md) for the schema.

---

## Subsystem map

Inside the Go binary, responsibility breaks down like this:

```
cmd/cairo/               CLI entrypoint + subcommands (export, import, diff)
  main.go                flag parsing, mode dispatch, session resolution
  bundle.go              portable identity: .cairo tar format

internal/store/             SQLite ownership — schema, queries, migrations
  db.go, schema.go       Open(), schema + migration application
  constants.go           DataDirName, role/status constants
  config_keys.go         compile-time config key constants (KeyModel, KeyEmbedModel, …)
  seed.go                defaults on first run
  helpers.go             shared utilities
  embed_search.go        SearchTopK[T Embeddable] — generic cosine × decay search
  dag.go                 cycle detection for task dependency graphs
  *.go (per table)       CRUD per entity: memories, sessions, tasks, jobs, hooks,
                         dreams, summaries, facts, …
  reap.go                startup sweep of orphaned running tasks

internal/llm/            Ollama interop
  client.go              HTTP client, 10-min timeout, Ping
  chat.go                StreamOnce — one request, streaming response
  embed.go               Embed — text → []float32
  modelinfo.go           FetchModelInfo — context length lookup; ErrModelNotFound sentinel
  types.go               Message, ToolCall, ToolDef shapes

internal/agent/          the agent itself
  agent.go               Agent struct, agentQueues + pendingAnnotations sub-structs
  loop.go                runLoop — the outer+inner turn loop; executeToolCall pure fn
  prompt.go              BuildSystemPrompt — decomposed into appendUserSteering,
                         appendBaseParts, appendSoul, appendUserContext,
                         appendRoleAddendum, appendToolAddenda,
                         appendIndexedProjects, appendSummaries,
                         appendMemories, appendFacts, appendTemporalContext
  hooks.go               RunHooks — fires shell commands for lifecycle events
                         (session_start, session_end, pre_tool, post_tool)
  history.go             repairIncompleteTurn — pure fn returning (history, note, didRepair)
  db_interfaces.go       DB interface types consumed by the agent layer
  events.go              typed event bus (Bus)
  summarizer.go          post-turn compression: summaries + facts
  types.go               Tool interface, ToolContext, ToolResult

internal/commands/       shared slash-command registry
  registry.go            CommandEnv interface + Registry
  commands.go            built-in command registrations

internal/learn/          learn feature: per-project file indexer
  walk.go                Walk() — directory traversal with gitignore + builtin excludes
  indexer.go             Run() — per-file summarize+embed loop; project description gen
  spawn.go               SpawnBackground() — creates job/task rows, launches subprocess
  spawn_unix.go          detached sysattr (shared with internal/tools/spawn_unix.go)

internal/hostedit/       editor routing
  hostedit.go            Detect() — TERM_PROGRAM/env inspection; Open() — routes to
                         VS Code (code -r -g), WaveTerm (wsh editor), JetBrains
                         (idea/goland/webstorm), or $EDITOR fallback

internal/providers/      environment context providers (wsh, VS Code, git, shell)
                         GetContext(cwd) injects env facts into BuildSystemPrompt

internal/tuisetup/       TUI initialization helpers (separate from internal/tui)

internal/tools/          the tools the model can call. Entity families
                         are consolidated into action-dispatched tools —
                         memory_tool(action="add"|"search"), skill(action=...),
                         job/task/agent for orchestration, soul for identity.
                         15 distinct built-in tools as of v0.3.0; config, role,
                         note, hook, and other thin DB wrappers were removed
                         in favor of the db_access skill (bash sqlite3).
  registry.go            Default() returns built-in tools; custom-tool loader
  *.go (per tool)        read, write, edit, bash, memory_tool, skill,
                         orchestration (job+task), spawn (agent),
                         dbtools (tool_list_builtin),
                         learn, say, choose, search, fetch, soul
  spawn_unix.go          detached subprocess for background agents

internal/cli/            line-oriented chat interface
  cli.go                 Run, RunOnce, slash commands
  renderer.go            event-bus subscriber → stdout
  background.go          renderer variant for task logs

internal/tui/            Bubble Tea terminal UI
  tui.go                 Run, Update dispatcher, OSC leak filter, smart-paste
  tui_model.go           model struct, newModel
  tui_view.go            View, render* helpers
  tui_transcript.go      append* functions, appendTurnSummary, transcript management
  tui_events.go          handleEvent, listenEvents
  tui_handlers.go        handleKey, handleTick, and other input handlers
  tui_command_env.go     CommandEnv implementation for the TUI
  activity.go            activityTracker type — activity states, awaiting/stale logic
  panels.go              panel framework (positions, toggle dispatch, O(1) index)
  panel_*.go             11 panels: help, memory, prompt, threads, files,
                         sessions, inspector (Ctrl+Y), diff (Ctrl+D), config (Ctrl+G),
                         log (Ctrl+L), quote (Ctrl+R / quote-reply)
  tool_toasts.go         ephemeral per-tool-call toast rows
  tool_family.go         toolFamily enum and color/icon helpers
  progress.go            global in-flight progress bars for background tasks
  prefixes.go            PrefixExpander struct — !shell, @file, @paste handling
  style.go               color palette, lipgloss styles
```

---

## Data flow: one turn

```
  ┌────────────────────┐
  │ user types a line  │
  └──────────┬─────────┘
             ▼
     ┌─────────────┐     drainBackgroundInbox()
     │  Agent.     │ ──▶ persist [background] note if any
     │  Prompt()   │
     └──────┬──────┘
            │
            ▼
     ┌──────────────┐
     │ persist user │
     │   message    │
     └──────┬───────┘
            │
            ▼      ┌────────────────────────────────────────────────┐
     ┌─────────────│ BuildSystemPrompt():                          │
     │ runLoop()   │   appendUserSteering + appendBaseParts +      │
     │             │   appendSoul + appendUserContext +            │
     │             │   appendRoleAddendum + appendToolAddenda +    │
     │             │   appendIndexedProjects + appendSummaries +   │
     │             │   appendMemories + appendFacts +              │
     │             │   appendTemporalContext                       │
     │             └────────────────────────────────────────────────┘
     │
     │     ┌──────────────────────────────────┐
     │     │  LLM.StreamOnce() → Ollama       │
     │     │  tokens stream back via event    │
     │     │  bus → UI renders live           │
     │     └──────────────────────────────────┘
     │
     │     if response contains tool_calls:
     │        for each call:
     │          RunHooks(pre_tool)
     │          executeToolCall(...)
     │          RunHooks(post_tool)
     │          persist assistant tool-call msg + tool result
     │        re-stream with updated history
     │        (inner loop)
     │
     │     when stream ends without tool_calls:
     │        persist final assistant text
     │
     ▼
     (outer loop: drain steering, drain follow-up, or done)
            │
            ▼
     ┌─────────────┐
     │  background │
     │  summarizer │  (goroutine, fires after turn ends)
     │             │  reads messages, writes summaries+facts
     └─────────────┘
```

---

## The event bus

Agent execution publishes typed events on a `Bus`. Anyone can subscribe. This is how the same `runLoop` drives three different UIs (line CLI, TUI, background log renderer) without coupling.

Events:

- `AgentStart` / `AgentEnd` — bracketing a whole `Prompt()` call
- `TurnStart` / `TurnEnd` — per-turn within a `Prompt()` (outer loop iterations)
- `Tokens` / `Thinking` — streaming chunks from the model
- `ToolStart` / `ToolEnd` — tool call lifecycle
- `ToolUpdate` — progress during a long-running tool
- `Error` — anything that went wrong

Subscribers are non-blocking. A slow subscriber will lose events rather than stalling the agent. See [Agent loop](agent-loop.md) for the tradeoff and [ROADMAP](../../ROADMAP.md) for how this is planned to evolve.

---

## Two deployment shapes

Cairo is a single binary, but it runs in two distinct modes:

**Interactive / single-message mode** — what happens when you type `cairo`, `cairo -new`, `cairo -tui`, or `cairo "one-shot question"`. You're the user, the binary is the agent, one process, one session.

**Background task mode** — what happens when you type `cairo -task 42 -background`. The binary is a background worker subprocess, spawned by the `agent(action="spawn")` tool from an interactive session. It has no stdin, its output goes to a log file, and it writes its result back to the `tasks` table when done.

Both modes share all the same code. They diverge only in `main.go`'s flag dispatch.

See [Background work](../development/background-work.md) for the job-and-task model.

---

## What's outside this picture

- **No separate database per user, per project, per role.** One `cairo.db` per install, at `~/.cairo/cairo.db`. The path is overridable via `CAIRO_DATA_DIR` env var or `--data-dir` flag. Multi-user is a future feature; solo-dev tooling is the current target.
- **No message queue, no job scheduler.** Background tasks are `exec.Cmd` subprocesses, coordinated through the DB.
- **No secret management.** Cairo doesn't store credentials. If a custom tool needs an API key, the environment is how to plumb it in, and `safe_env_extras` is the explicit whitelist (see [Custom tools](../development/custom-tools.md)).

Simplicity is the architecture's main feature. One binary, one file, one LLM host.
