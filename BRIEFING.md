# Cairo Architecture Redesign — Session Briefing

**Written by:** Selene (Claude Sonnet 4.6), 2026-05-07  
**For:** Next Claude Code session starting in ~/cairo2  
**Purpose:** Deep research + design proposal for reorganizing the Cairo project before it becomes unmanageable

---

## What You Are Being Asked To Do

Produce a concrete Go-idiomatic architecture proposal for reorganizing the Cairo project. This is not a "list tradeoffs" document — it is a **decision document with a recommended structure**, the reasoning behind it, and a migration path. The audience is Scot, who comes from .NET (classlibs, DI, Blazor, Console, WebAPI) and needs the Go mental model explained in terms he already knows, then shown what it means specifically for Cairo.

You have permission to spawn as many research subagents as you need. The output goes in `~/cairo2/`. Work methodically. Take your time.

---

## Background: What Cairo Has Grown Into

Cairo started as a local-first AI coding harness — a Go binary with a Bubble Tea TUI and an Ollama-backed agent loop. It has grown into at least six distinct surfaces:

1. **TUI surface** — Bubble Tea interactive terminal UI (`internal/tui/`, `internal/tuisetup/`)
2. **AI Agent runtime** — the loop, system prompt, tool dispatch, summarizer (`internal/agent/`)
3. **Memory surface** — memory/fact/dream subsystems, scoring, FTS5 search (`internal/db/` memory queries + `internal/agent/`)
4. **Tool surface** — ~15 built-in tools + custom tool loading (`internal/tools/`)
5. **HTTP server surface** — OpenAI-compat API, JSON-RPC, SSE streaming, auth (`internal/server/`)
6. **Web surface** — Node.js/React browser UI, spawns cairo as child process (`web-agent/`)
7. **VS Code surface** — JSONL event stream mode, VS Code extension (`internal/cli/vscode.go`, `vscode-extension/`)
8. **Registry/fleet surface** — control plane, heartbeat, WS liveness (`internal/registry/`, `internal/registry-client/`)
9. **Packaging surface** — RPM/deb, systemd units, install scripts (`scripts/packaging/`)

**The problem:** These surfaces are growing independently. The code that serves them is getting tangled. `internal/db/` is doing triple duty (data layer + config store + memory engine). `cmd/cairo/main.go` is 400+ lines of initialization that touches every surface. The web-agent reads cairo's SQLite schema directly because the HTTP API doesn't expose what it needs. The registry protocol types are copy-pasted between two repos with a documented "sync ritual" that has already drifted.

**Scot's mental model (from .NET):** He would reach for classlibs for shared logic, a Console project for the CLI, a WebAPI project for the HTTP surface, Blazor for the UI, and DI containers to wire them together. He knows this is the right instinct. He needs you to translate it into Go.

---

## The Three Repos Today

| Repo | Stack | LOC | Status |
|---|---|---|---|
| `~/cairo` | Go + TypeScript | ~66K Go + ~3.5K TS | Main; all surfaces |
| `~/cairo-registry` | Go | ~1,836 | Separate; protocol copy-paste problem |
| `~/cairo-ui` | .NET 10, Blazor Server | ~4,200 | Separate; intentionally loose coupling |

**Decision already made (by Scot this session):** Merge `cairo-registry` into the `cairo` repo. The briefing should take this as a given and incorporate it into the proposed structure.

**Not being changed:** `cairo-ui` stays separate. .NET + Go in one CI is not worth it. The coupling is HTTP-to-a-port by design.

---

## The Core Question

**In Go, how would you design this application from scratch?**

Scot asked: if you were a Go expert starting fresh, knowing what this needs to be — multiple surfaces, a shared agent core, separate deployable binaries — how would you organize it? What are the "classlibs"? What are the "programs"? What are the "shared utilities"?

The answer needs to cover:

### 1. Module structure: one module or many?

Go has two options for multi-binary repos:
- **One `go.mod`** — all binaries in `cmd/`, all shared code in `internal/`. Simple. Standard. The Go team's preference for most projects.
- **Go workspace (`go.work`)** — multiple `go.mod` files in one tree, linked by a workspace file. Used when you need separate versioning per module or when modules are independently publishable.

For Cairo specifically: which is right, and why?

### 2. Internal package organization: what are the boundaries?

In .NET terms: which packages are classlibs (shared, no UI concern, injectable) and which are presentation layers (TUI, HTTP handler, VS Code adapter)?

Current `internal/` packages are:
```
agent/          — agent loop, system prompt, summarizer, consider sub-agent
agent/consider/ — inner-dialogue sub-agent
cli/            — non-TUI CLI modes: background, vscode, learn
commands/       — slash command registry
db/             — SQLite schema, all migrations, ALL query helpers (very large)
hostedit/       — file-open routing
learn/          — codebase indexing
llm/            — Ollama/OpenAI client
protocol/       — registry wire types (recently added)
providers/      — environment context injection (git, shell, vscode, wsh)
registry/       — WebSocket liveness client
registry-client/ — HTTP register + stream client
server/         — HTTP server, Bridge, tsnet listener
tools/          — tool registry + ~15 implementations
tui/            — Bubble Tea TUI panels
tuisetup/       — TUI init/wiring
version/        — version string
worktree/       — worktree sandbox management
```

Questions to answer:
- Is `internal/db/` too large? Should memory, config, and schema migrate to separate packages?
- Is `internal/agent/` doing too much (loop + prompt assembly + summarizer)?
- Should there be a `internal/core/` or `internal/runtime/` that is the pure agent kernel — no UI, no HTTP, just: given an LLM client + tool set + DB, run the loop?
- What is the right home for the registry after the merge?

### 3. The DI question: how does Go do dependency injection?

.NET has `IServiceCollection`, `AddSingleton`, `AddScoped`, constructor injection everywhere. Go doesn't have a DI container by default. The Go idioms are:

- **Manual constructor injection** (most common) — `func NewAgent(db *DB, llm LLMClient, tools []Tool) *Agent`
- **Interface-based polymorphism** — define interfaces, accept interfaces, return concrete types
- **Wire** (Google's codegen tool) — generates wiring code from provider functions, zero runtime reflection
- **Fx** (Uber's DI framework) — runtime reflection-based, similar feel to .NET DI

For Cairo specifically: `cmd/cairo/main.go` does manual wiring today (400+ lines). Is that sustainable? What's the right answer?

### 4. The `cmd/` surface question: what binaries should exist?

Currently:
- `cmd/cairo` — everything (TUI, agent, HTTP server, VS Code mode, learn, config, export/import)

After registry merge:
- `cmd/cairo` — as above
- `cmd/cairo-registry` — fleet registry server
- `cmd/cairo-ctl` — operator CLI

Should there be more? Less? Should `cairo serve` stay inside the main binary or become its own binary? Is there a case for a `cairo-agent` binary (headless, no TUI) vs. `cairo` (with TUI)?

### 5. The web surface question: HTTP API formalization

Web-agent currently reads cairo's SQLite schema directly. Cairo's HTTP server (`internal/server/`) exposes:
- `POST /api/chat`
- `GET /api/events` (SSE)
- `POST /v1/chat/completions` (OpenAI-compat)
- `POST /rpc` (JSON-RPC)

It does NOT expose:
- `GET /api/sessions`
- `GET /api/sessions/:id/messages`
- `GET /api/config`
- `GET /api/health` (except via the registry path)

If these were added, web-agent could drop its direct SQLite reads. Should they be added? Should the streaming path upgrade from SSE to WebSocket? What does this mean for the API's stability contract?

### 6. The TypeScript/Node question

`web-agent/` is TypeScript+React inside the Go repo. This is already working. The question is: what's the right contract between them? Options:
- Keep as child-process spawn (current) — simple, works, but schema coupling via direct SQLite reads
- Switch web-agent to use cairo's HTTP API — clean contract, loses the schema coupling
- Expose a dedicated WebSocket endpoint from cairo that web-agent consumes — better streaming story

---

## What the Output Should Be

Produce these files in `~/cairo2/`:

### `design.md` — The main proposal
Structure:
1. **Executive summary** (Scot's .NET analogy mapped to Go — 1 paragraph)
2. **Proposed `cmd/` surface** — what binaries exist and why
3. **Proposed `internal/` package map** — the new "classlibs" with clear responsibilities
4. **Module structure recommendation** — one `go.mod` or `go.work`, with reasoning
5. **DI pattern recommendation** — what changes in `main.go` wiring
6. **Web surface contract** — what HTTP endpoints cairo should expose, what web-agent drops
7. **Migration path** — how to get from here to there without breaking everything at once (phased)
8. **What stays the same** — things that don't need to change

### `research/` — Supporting research (one file per topic)
Spawn subagents for each of these. Each produces a file in `~/cairo2/research/`:

- `internal-db-audit.md` — How large is `internal/db/`? What does it actually do? Is it three things in a trenchcoat?
- `main-go-audit.md` — What does `cmd/cairo/main.go` do in its 400+ lines? What could be extracted?
- `go-project-layout-patterns.md` — Survey of how real Go projects (Kubernetes, Tailscale, CockroachDB, etc.) organize multi-binary repos. What's idiomatic at Cairo's scale?
- `go-di-options.md` — Manual injection vs. Wire vs. Fx — tradeoffs, what Cairo's scale warrants
- `server-api-surface.md` — What does cairo's HTTP server currently expose? What's missing that web-agent and cairo-ui need?
- `registry-merge-plan.md` — Concrete steps to merge cairo-registry into cairo (import paths, go.mod changes, protocol consolidation)

---

## Research Agents: How to Dispatch Them

Use the Agent tool with `run_in_background: true`. Dispatch all research agents in parallel. Each agent:
- Is READ-ONLY (except writing its output file)
- Gets a scoped question and a specific output path
- Does NOT try to fix anything — research only

Working directories to read:
- `~/cairo` — main repo (already merged web-agent, packaging, etc.)
- `~/cairo-registry` — registry repo (to be merged)
- `~/cairo-ui` — UI repo (stays separate, but read for context)

---

## Constraints and Preferences

- **Scot does not know Go deeply.** Explain Go idioms by analogy to .NET where useful. Don't assume he knows what `go.work` is or why `internal/` has special semantics.
- **Don't propose rewriting.** This is a reorganization, not a rewrite. The logic stays; the file layout changes.
- **Don't propose frameworks.** Cairo does not need Fx or Wire. If the answer is "manual injection is fine at this scale," say so clearly and explain why.
- **Respect what's working.** The TUI, the agent loop, the HTTP server, the packaging — these work. The proposal should change the *organization*, not the *behavior*.
- **The web-agent's Node/TypeScript is not going away.** It lives inside the cairo repo. The question is the contract between it and the Go binary, not whether it should exist.
- **Prioritize the migration path.** The most important part of the proposal is "how do we get there" — not "what does the end state look like." End state is useless without a safe path.

---

## Known Pain Points to Address

1. **`internal/db/` is too large** — schema, migrations, config store, memory engine, fact store, session store, all queries. This is not a single responsibility.
2. **`cmd/cairo/main.go` is 400+ lines** — does initialization, wiring, signal handling, surface selection (TUI vs. vscode vs. background vs. serve). Hard to test, hard to extend.
3. **Protocol copy-paste** — `internal/protocol/registry.go` in cairo vs. equivalent types in cairo-registry. After the merge this gets fixed. The proposal should show where the single source of truth lives.
4. **`warnEmbedModelMismatch` queried the dropped `notes` table** — just fixed, but symptom of: code that knows about tables by name rather than through a typed query layer.
5. **Two registry client packages** — `internal/registry/` (LivenessStream only) and `internal/registry-client/` (Register + Stream). Artifact of a merge conflict. Should become one.
6. **Web-agent reads SQLite directly** — `cairoDb.ts` knows cairo's schema. Schema-coupled with no contract. Should be replaced by HTTP API calls.

---

## Key Files to Read First

When you start the session, read these before dispatching agents:

```
~/cairo/CLAUDE.md                          — project instructions (recently updated)
~/cairo/cmd/cairo/main.go                  — main entrypoint
~/cairo/internal/db/schema.go              — migrations (understand scale)
~/cairo/internal/agent/agent.go            — agent loop
~/cairo/internal/server/server.go          — HTTP server
~/cairo/web-agent/server/src/cairoDb.ts    — the SQLite direct reads
~/cairo/web-agent/server/src/createApp.ts  — web-agent's own API surface
~/cairo-registry/cmd/cairo-registry/main.go — registry server
~/cairo-registry/internal/                  — registry packages
~/cairo2/BRIEFING.md                        — this file
```

---

## Deliverable Standard

The design.md is done when:
- A .NET developer can read it and understand what each piece is without knowing Go
- A Go developer can read it and implement it without making architectural decisions
- The migration path is phased — no single step breaks the whole system
- Every "we should" is backed by evidence from the research files

Good luck. Take your time. The goal is clarity, not speed.
