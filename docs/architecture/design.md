# Cairo architecture redesign

**Authors:** Selene (this session), 2026-05-07.
**Status:** Proposal — for Scot to review and decide. No code changes yet.
**Inputs:** `~/cairo2/BRIEFING.md` and the six files in `~/cairo2/research/`.

---

## 1. Executive summary — the .NET analogy mapped to Go

Your **classlibs** are Go packages under `internal/`. Your **programs** are directories under `cmd/`, each with a `main.go` that compiles to one binary. Your **DI** is constructor injection done by hand in a small `App` struct in `cmd/cairo/`, with interfaces declared at the consumer (where `IFoo` would live in C#) — no `IServiceCollection`, no Wire, no Fx. Your **csproj** equivalent is a single `go.mod` at the repo root for the entire monorepo. Your **WebAPI** lives at `internal/server/` and exposes one HTTP surface that both the Node web-agent and the .NET cairo-ui consume; nothing reads SQLite directly across that wall.

In one sentence: **classlibs → `internal/<domain>/`; programs → `cmd/<binary>/`; DI → an `App` struct built by `newApp(ctx, opts)`; module → one `go.mod`.**

---

## 2. Proposed `cmd/` surface

Three binaries, one repo, one module. (Source: `research/registry-merge-plan.md` §5; `research/main-go-audit.md` §"Surface selection".)

```
cmd/
  cairo/              — the user-facing binary: TUI, line CLI, one-shot, serve, dream, learn, export, import, diff, token, config, task
  cairo-registry/     — the fleet-control-plane server (formerly ~/cairo-registry/cmd/cairo-registry)
  cairo-ctl/          — operator CLI for inspecting the registry (formerly ~/cairo-registry/cmd/cairo-ctl)
```

Why three and not more:

- **`cairo`** stays the everything-binary. Splitting it (e.g. `cairo-tui` vs. `cairo-serve` vs. `cairo-agent`) would force users to install/learn multiple binaries for what is conceptually one product. Today `cairo serve` reuses the same DB, the same agent, the same tools — same code path with a different surface. Keep it that way.
- **`cairo-registry`** is genuinely a different deployment shape: it runs as a long-lived server on a fleet host, has its own SQLite ledger, listens on a different port, and most cairo installs do NOT run it. Separate binary; separate systemd unit.
- **`cairo-ctl`** is the registry's operator CLI. Pairing it with the registry binary keeps "I need to inspect the fleet" one-stop. Could be folded into `cairo-registry` as a subcommand later; harmless to keep separate.

Not adding:
- `cairo-agent` headless variant — `cairo --background` already does this; the headless surface is a flag, not a binary.
- `cairo-worker` for the `--task` worker — same reason; it's a re-entry into the same binary with a different mode.

---

## 3. Proposed `internal/` package map

The big move is splitting `internal/db/`. Everything else is mostly preserved with light renames. (Source: `research/internal-db-audit.md`; `research/registry-merge-plan.md` §5.)

```
internal/
  ─── core kernel ─────────────────────────────────────────────
  agent/              — agent loop, prompt assembly, summarizer (unchanged scope)
    consider/         — inner-dialogue sub-agent (unchanged)
  llm/                — Ollama/OpenAI client (unchanged)
  tools/              — tool registry + ~15 implementations (unchanged)

  ─── store layer (the split) ─────────────────────────────────
  store/
    schema/           — schema.go DDL + migrations + seed (one place; immune to feature merges below)
    sqliteopen/       — Open/OpenAt/WithTx + premigration backup + the composite *DB
    config/           — ConfigQ + KeyXxx constants (the typed KV)
    sessions/         — SessionQ + MessageQ (sessions and their messages)
    identity/         — RoleQ + PromptQ + SkillQ + HookQ + ConsiderAspectQ + ConsiderActivationQ + StateQ
                        + state_ritual (export/import bundles)
    memory/           — MemoryQ + FactQ + SummaryQ + DreamQ + DreamLogQ + curator + mmr
    jobs/             — JobQ + TaskQ + WorktreeQ + TaskArtifactQ + reap + proc_unix/windows
    index/            — IndexedFileQ + ChunkQ + ProjectQ + embed_search (the Learn-RAG store)
    registrations/    — RegistrationQ (registry-side state; lives here for now)

  ─── presentation surfaces ───────────────────────────────────
  tui/                — Bubble Tea TUI (unchanged)
  tuisetup/           — TUI init / wiring (unchanged; the lipgloss-init blank import lives here)
  cli/                — line CLI (Run, RunOnce, RunVSCode), subcommand handlers (learn, config helpers)
  commands/           — slash command registry (unchanged)
  server/             — HTTP server (one Bridge, OpenAI-compat, JSON-RPC, SSE; gains the new endpoints below)

  ─── integration / glue ──────────────────────────────────────
  hostedit/           — file-open routing (unchanged)
  learn/              — codebase indexing pipeline; thin layer over store/index (unchanged)
  protocol/           — wire types for registry (and any future on-the-wire schemas) — SINGLE SOURCE OF TRUTH
  providers/          — environment context injection (unchanged)
  registry/           — registry CLIENT (consolidated from registry/ and registry-client/; imports protocol)
  registryserver/     — registry SERVER (NEW — merged from ~/cairo-registry/internal/registry/)
  worktree/           — worktree sandbox manager (unchanged)
  version/            — build-time version string (unchanged)
```

### Three classlib tiers, .NET-style

For Scot specifically — the `internal/` layout has three layers, like a clean .NET solution:

1. **Store layer** (`internal/store/...`) — the data classlibs. No HTTP, no LLM, no UI. Pure persistence. Roughly: `Cairo.Data.Memory`, `Cairo.Data.Sessions`, `Cairo.Data.Jobs`. These are leaf libraries.
2. **Domain layer** (`internal/agent/`, `internal/tools/`, `internal/llm/`, `internal/learn/`, `internal/registry/`, `internal/registryserver/`) — the business logic. Imports the store layer. Roughly: `Cairo.Agent`, `Cairo.Tools`, `Cairo.Llm`. Doesn't know about the TUI or the HTTP server.
3. **Presentation layer** (`internal/tui/`, `internal/cli/`, `internal/server/`) — the surfaces. Imports the domain layer. Roughly: `Cairo.Web`, `Cairo.Console`, `Cairo.Tui`.

`cmd/cairo/` is the composition root (analogous to `Program.cs` in a .NET 8+ web app) — it picks which surfaces to spin up.

---

## 4. Module structure recommendation: one `go.mod`

**Decision: single `go.mod` at the repo root. No `go.work`.**

(Source: `research/go-project-layout-patterns.md` §3 and §5.)

### Why one module is right for Cairo

- Cairo is a **product**, not a federation of libraries. The whole tree ships together as one rpm/deb release. Tailscale (~500K LOC, ~30 binaries), CockroachDB (~3M LOC), and Kubernetes (~5M LOC) all land on a single `go.mod` for the same reason.
- Cross-binary refactoring becomes one commit instead of "bump pseudoversion in module A, update consumer's go.mod in module B, retest". The protocol-drift problem you just hit between `cairo` and `cairo-registry` is exactly the cost of having two modules. Merging them removes the drift.
- The Go linker drops what each binary doesn't use, so a binary that doesn't import the TUI doesn't pay for it. There's no runtime tax for living in the same module.
- One `go.mod` means one `go.sum`, one `go test ./...`, one `go build ./cmd/...`. Simpler for CI, simpler for new contributors.

### When `go.work` would have been right (and isn't here)

`go.work` is for **separately-published** modules that you want to evolve in lockstep without publishing a pseudoversion to test changes. Cairo doesn't publish any module. Every package in the tree is consumed only by `cmd/cairo/` or `cmd/cairo-registry/` or `cmd/cairo-ctl/`. There is nothing to publish, so there is nothing for `go.work` to coordinate.

### `internal/` vs. `pkg/`

Use `internal/` for everything. **Don't introduce `pkg/`.**

- `internal/foo` is **compiler-enforced private** to this module. It's the only access-control mechanism Go has, and it's the right default for an application.
- `pkg/foo` has zero special meaning; it's purely a folder-naming convention that adds an `pkg/` prefix to every import path for no benefit. Tailscale, Caddy, and the Charm projects all skip `pkg/`.

(Source: `research/go-project-layout-patterns.md` §4.)

---

## 5. DI pattern recommendation: manual injection + `App` struct

**Decision: stay with manual constructor injection. No Wire, no Fx. Refactor `cmd/cairo/main.go` into an `App` struct.**

(Source: `research/go-di-options.md` §5–§6; `research/main-go-audit.md`.)

### .NET → Go translation

In .NET you'd write:

```csharp
services.AddSingleton<DB>(sp => DB.Open(dataPath));
services.AddSingleton<ILlmClient>(sp => new OllamaClient(url, key));
services.AddSingleton<Agent>();
// ... 20 more lines
var host = builder.Build();
```

In Go, you write:

```go
func newApp(ctx context.Context, opts Options) (*App, func(), error) {
    app := &App{Cfg: opts, BgWg: &sync.WaitGroup{}}
    cleanup := func() { app.BgWg.Wait(); if app.DB != nil { app.DB.Close() } }

    if err := app.openDB(); err != nil           { return nil, cleanup, err }
    if err := app.connectLLM(ctx); err != nil    { return nil, cleanup, err }
    if err := app.resolveSession(); err != nil   { return nil, cleanup, err }
    if err := app.buildTools(); err != nil       { return nil, cleanup, err }
    if err := app.buildAgent(); err != nil       { return nil, cleanup, err }
    if err := app.startRegistry(ctx); err != nil { return nil, cleanup, err }
    return app, cleanup, nil
}
```

Each method on `App` is short (10–30 lines), takes nothing, mutates one or two fields. The compiler verifies the dependency graph at build time — if you call `app.buildAgent()` before `app.connectLLM()`, the agent-build code fails to compile because `app.LLM` is nil-typed at the use site (you'd be reading a nil interface, but more typically the missing dep shows up as an obvious runtime panic in tests, caught on first run).

### Why no framework

`cmd/cairo/main.go` is **429 lines today**, but most of those lines are not wiring — they're signal handling, subcommand dispatch, flag parsing, surface selection. The actual `New()`-call portion is roughly 30 lines. Wire would generate code that's not meaningfully shorter, at the cost of a build-time tool dependency. Fx is reflection-based and meant for service fleets — wrong shape for a single product binary.

### What this changes in `main.go`

Concrete refactor (target structure for `cmd/cairo/`):

```
cmd/cairo/
  main.go            ~80 lines: signal handler, subcommand dispatch, surface dispatch
  app.go             ~150 lines: type App struct + newApp + openDB/connectLLM/etc.
  surfaces.go        ~120 lines: runTUI, runCLI, runVSCode, runOneShot — each takes *App
  cmd_export.go      one subcommand
  cmd_import.go
  cmd_diff.go
  cmd_dream.go
  cmd_learn.go
  cmd_serve.go       — uses *App; constructs server.New(app.Agent, app.DB, ...)
  cmd_token.go
  cmd_config.go
  cmd_task.go        — currently `--task` flag; promote to subcommand
  wizard.go          first-run wizard
```

`main.go` stops being a 429-line liability and becomes a small dispatcher. Each `cmd_*.go` and surface is independently testable. Manual DI stays, painless.

---

## 6. Web surface contract

(Source: `research/server-api-surface.md`.)

### Endpoints to add to `internal/server/`

Replace every direct-SQLite read in `web-agent/server/src/cairoDb.ts` with an HTTP endpoint:

| Method | Path | Purpose |
|---|---|---|
| GET    | `/api/health`                              | Public health (cairo version, uptime, db path). Keep `/healthz` for liveness probes. |
| GET    | `/api/config/snapshot`                     | All config keys + roles + consider_aspects (replaces `cairoDb.snapshot`). |
| PUT    | `/api/config/{key}`                        | Set a single config value (replaces `cairoDb.setConfig`). |
| GET    | `/api/sessions`                            | List sessions with insight (replaces `cairoDb.list_sessions`). |
| GET    | `/api/sessions/{id}`                       | Single session detail. |
| PATCH  | `/api/sessions/{id}`                       | Rename session (replaces `cairoDb.rename_session`). |
| DELETE | `/api/sessions/{id}`                       | Delete session (replaces `cairoDb.delete_session`). |
| GET    | `/api/sessions/{id}/messages?limit&before` | Paginated message history. |
| PATCH  | `/api/roles/{name}`                        | Update role.model or role.think (replaces `cairoDb.set_role`). |
| PUT    | `/api/consider/aspects/{name}`             | Upsert an aspect. |
| PATCH  | `/api/consider/aspects/{name}`             | Toggle enabled. |
| DELETE | `/api/consider/aspects/{name}`             | Delete an aspect. |
| GET    | `/api/metrics`                             | Snapshot counts for cairo-ui dashboard (already drafted in `cairo-ui/docs/cairo-api-requests/metrics.md`). |
| GET    | `/api/events`                              | SSE: agent-loop events (`tool_call`, `summarizer_run`, `task_completed`). For observers. |

**Streaming.** Keep SSE for token streaming and event broadcast. **Do not** introduce WebSockets at the cairo level today. The web-agent's React client and cairo-ui's Blazor client both cope with SSE trivially. WS is only worth it if a future feature requires full-duplex on the same socket; we'll know when we hit that.

### What web-agent drops

- The entire `PYTHON_BRIDGE` const in `cairoDb.ts` (~280 lines) — replaced by HTTP calls to the cairo binary.
- The `python3` runtime dependency.
- All schema introspection (`PRAGMA table_info`, `sqlite_master` checks).

What web-agent keeps:
- Its own session store (`SessionStore`) — that's web-agent's per-tab/per-browser state, not cairo's.
- Its `CairoRunner` (which spawns cairo as a child process) — that's the orchestration shape; the contract is unchanged.
- Its own `/api/...` surface for the React frontend. The frontend doesn't change.

The web-agent goes from "Node server that reads cairo's SQLite" to "Node server that calls cairo's HTTP API". Schema coupling is broken.

### What cairo-ui gets

cairo-ui's `HttpCairoClient` is currently a stub that throws `NotImplementedException`. After this redesign, it wires up against:
- `/rpc cairo.send` and `cairo.send.stream` for chat (already exist).
- `/api/metrics` for dashboard cards (new, drafted in `cairo-ui/docs/cairo-api-requests/metrics.md`).
- Bearer auth via `cairo serve --auth` (already supported).

cairo-ui stays in its own repo. The contract is HTTP-to-a-port, and that's the right boundary for a separate-language, separate-team-velocity component.

---

## 7. Migration path

Phased — every phase compiles, tests, and ships. No "big bang" step. Each phase has a goal, the concrete moves, and a success test.

### Phase 0 — preflight (prep only, no code change)

**Goal:** lock in the proposal, write the plan to repo state.
**Concrete:** copy `~/cairo2/design.md` and `~/cairo2/research/*.md` into `~/cairo/.internal/notes/architecture/redesign-2026-05/` (or wherever Scot wants — they need to ride the repo, per global instructions about state living in the repo).
**Success test:** files are committed; another machine reads them.

### Phase 1 — Registry merge (the easy obvious win)

**Goal:** one repo, one source of truth for registry types, one client package.
**Concrete:** follow the four-step plan in `research/registry-merge-plan.md` §4.
- Step A: copy `cairo-registry` source into `cairo/cmd/cairo-registry/`, `cairo/cmd/cairo-ctl/`, `cairo/internal/registryserver/`. Update import paths.
- Step B: update `scripts/build.sh`, packaging, smoke test.
- Step C: consolidate `internal/registry/` and `internal/registry-client/` into one package; rename to `internal/registry/`.
- Step D (deferred to after release soak): archive the old `~/cairo-registry` repo.

**Success test:** `bash scripts/build.sh` produces all three binaries; `bash scripts/smoke/registry-client.sh` passes; `internal/protocol/registry.go` is the only definition of `RegisterRequest`/`Frame` in the tree (`grep -rn "type RegisterRequest"` returns one hit).

**Effort:** half a day.

### Phase 2 — `cmd/cairo/` decomposition (no functional change)

**Goal:** turn `main.go` from 429 lines into a small dispatcher + an `App` struct.
**Concrete:**
1. Create `cmd/cairo/app.go` with `type App struct{...}` and `newApp(ctx, opts) (*App, func(), error)`.
2. Move the linear init pipeline (DB open → LLM connect → session resolve → tools build → agent build → registry start) into methods on `App`.
3. Create `cmd/cairo/surfaces.go` with `runTUI`, `runCLI`, `runVSCode`, `runOneShot`. Each takes `*App`.
4. Move each subcommand into `cmd/cairo/cmd_*.go` (one file each).
5. Promote `--task` flag to a real `cairo task <id>` subcommand.
6. `main.go` becomes ~80 lines: signal handler + subcommand switch + surface dispatch.

**Success test:** all existing behaviors work identically — TUI launches, `cairo serve --auth` works, `cairo learn`, `cairo dream`, `cairo export`/`import`, registry registration, background tasks. Smoke tests pass. Diff is large but mechanical (cut-and-paste of existing code into new files; tiny shape changes around the `App` accessor).

**Effort:** one day.

### Phase 3 — Add the missing HTTP endpoints (no client changes yet)

**Goal:** internal/server/ exposes the full surface that web-agent and cairo-ui need. Both clients still use their old paths; the new endpoints are additive.
**Concrete:**
1. Add handlers to `internal/server/` for the 14 endpoints in §6. Each handler is ~30–60 lines.
2. Each handler delegates to `*db.DB` query methods that already exist (or trivial wrappers).
3. Add tests per handler (~10 endpoints × small smoke = manageable).
4. Document the surface in `docs/reference/http-api.md`.

**Success test:** `curl localhost:11434/api/sessions` returns sessions JSON when `cairo serve --port 11434 --auth=false` is running. `curl /api/config/snapshot` returns config+roles+aspects. All new endpoints have a test.

**Effort:** 2–3 days. The work is mechanical and parallelizable.

### Phase 4 — Web-agent migration to HTTP (drops the SQLite reads)

**Goal:** `cairoDb.ts` Python bridge deletes; web-agent calls cairo over HTTP.
**Concrete:**
1. Replace `cairoDb.ts` with a TypeScript HTTP client against `internal/server/`'s endpoints.
2. Web-agent gains a `CAIRO_HTTP_URL` config (defaults to localhost:port-of-spawned-cairo).
3. Delete the `PYTHON_BRIDGE` constant. Delete the `python3` runtime dep from packaging.
4. End-to-end: open the web UI, log in, see sessions list, edit a config key, edit a role, create/delete a consider aspect — all paths exercise HTTP.

**Success test:** web-agent works without Python installed on the host. Smoke: `python3 --version` removed; web-agent still functions for config/snapshot/sessions.

**Effort:** 1–2 days.

### Phase 5 — `internal/db/` split (the heavy lift)

**Goal:** the data layer is no longer one 12K-line "everything" package. Each sub-store is independently maintained.
**Concrete:** in this order (each step compiles and tests):
1. Create `internal/store/sqliteopen/` housing `*DB`, `Open`, `OpenAt`, `WithTx`. Move from `internal/db/db.go`.
2. Create `internal/store/schema/` housing `schema.go`, `migrations`, and `seed.go`.
3. Create `internal/store/memory/` and move `memories.go`, `memories_test.go`, `mmr.go`, plus `facts`, `summaries`, `dreams`, `dream_log`, `curator`. This is the largest single chunk — start here because it's the most decoupled.
4. Create `internal/store/jobs/` and move `jobs.go`, `tasks.go`, `worktrees.go`, `artifacts.go`, `reap.go`, `proc_unix.go`, `proc_windows.go`, hooks.
5. Create `internal/store/index/` and move `learn.go`, `chunks.go`, `embed_search.go`, projects, indexed_files.
6. Create `internal/store/identity/` and move roles, prompts, skills, hooks, consider_aspects, consider_activations, state, state_ritual.
7. Create `internal/store/sessions/`, `internal/store/config/`, `internal/store/registrations/` for the remaining stores.
8. The composite `*db.DB` becomes a thin aggregator under `internal/store/sqliteopen/` that vends the sub-stores. Update every import site.

**Tactic:** do this with one sub-store per PR. Each PR is a mechanical move (`git mv` plus import-path rewrites). The tests for each sub-store come along.

**Success test:** every PR keeps `go test ./...` green. No `internal/db/` symbol is referenced outside `internal/store/...` after the split. `wc -l internal/db/*.go` returns zero (the directory is gone, replaced by `internal/store/`).

**Effort:** 1–2 days per sub-store × 8 sub-stores = ~2 weeks of calendar time, distributed.

### Phase 6 — cairo-ui wires up `HttpCairoClient`

**Goal:** cairo-ui's `MockCairoClient` is no longer the default in dev; the real client speaks to a running cairo.
**Concrete:** out-of-tree (cairo-ui repo). Implement `HttpCairoClient.SendAsync` against `/rpc cairo.send`, `SendStreamAsync` against `cairo.send.stream` + SSE consumption, `GetMetricsAsync` against `/api/metrics`. The cairo-ui team owns this; cairo's contribution is the stable HTTP surface from Phase 3.

**Success test:** cairo-ui's dashboard renders real numbers when pointed at a running `cairo serve --auth`.

**Effort:** cairo-ui side; not blocking cairo's plan.

### Phase ordering rationale

- **1 first** because it's the highest pain-per-effort ratio (drift between two repos is ongoing risk; the merge fixes it in half a day).
- **2 next** because every later phase touches `main.go`/`cmd/cairo/` and they're easier with the App struct in place.
- **3 before 4** because adding endpoints additively is risk-free; flipping clients to use them is the second step.
- **5 last** because the db split is the largest mechanical move and should not block phase 1–4 user-visible improvements. By the time we get there, `internal/server/` and `cmd/cairo/` are already shaped right, and the split is purely "move files into folders".

---

## 8. What stays the same

- **The TUI.** Bubble Tea panels, transcript, toasts, progress bars — no change. `internal/tui/` is fine as-is.
- **The agent loop.** `internal/agent/agent.go` and `prompt.go` keep their behavior. Re-homed module path stays `internal/agent/`.
- **The LLM client.** `internal/llm/` is a narrow 5-function interface; it's already the right size. No change.
- **The SQLite schema.** No DDL changes. The migration counter (PRAGMA user_version) keeps its meaning. `schema.go` simply moves into `internal/store/schema/`.
- **The on-disk layout.** `~/.cairo/cairo.db`, `~/.cairo-registry/registry.db`, packaging install paths — all unchanged.
- **The wire protocol** between cairo and cairo-registry. `RegisterRequest`, `RegisterResponse`, `Frame` keep their shapes. After the merge they live in one place; that's the only change.
- **All slash commands** in `internal/commands/`. Same registry, same dispatch.
- **The tool registry.** `internal/tools/` and the ~15 built-ins keep their shape. `tools.Default(...)` keeps its signature.
- **`cairo serve --auth` / `cairo serve --tsnet`.** Same flags, same behavior. Just gains additional endpoints from §6.
- **The web-agent's React frontend.** No UX changes. Only its server-side TypeScript talks to cairo differently.
- **The cairo-ui Blazor app.** Stays in its own repo. The contract from cairo's side is HTTP-to-a-port; that boundary is preserved by design.
- **Packaging.** `.deb` / `.rpm` build process via `scripts/packaging/build-packages.sh`. Adds two binaries (cairo-registry, cairo-ctl) but the layout otherwise is unchanged.
- **VS Code extension.** Unchanged.
- **The single `go.mod` decision** — already in place; we are explicitly NOT moving to a workspace.

---

## 9. Open questions to surface for Scot

These are flagged for explicit decision; the proposal above takes a position but the cost of changing later is low if you'd rather pick differently.

1. **`internal/store/` vs. `internal/db/sub/`.** I proposed creating a new top-level `internal/store/`. An alternative is keeping `internal/db/` as the parent and putting sub-stores at `internal/db/memory/`, `internal/db/jobs/`, etc. The "store" rename signals the broader role (it's not just SQL anymore — it's the persistence abstraction). I lean store; happy to keep `db` if you prefer continuity.
2. **`cmd/cairo-ctl` vs. `cairo-registry ctl` subcommand.** Today they're separate binaries. We could fold `cairo-ctl` into `cairo-registry ctl ...` later. Not blocking.
3. **`/rpc` vs. clean REST.** I proposed adding REST endpoints alongside the existing `/rpc`. We could deprecate `/rpc` once cairo-ui's `HttpCairoClient` is built, but it has live consumers (cairo-ui's drafted contract uses it for chat) — so retire only after both clients are migrated. Not urgent.
4. **`/api/events` SSE shape.** Proposed shape is `{type, payload}`. Worth aligning with the existing registry `Frame` type for consistency, or letting them diverge because the audiences differ. Defer until Phase 3 implementation.

---

*End of proposal. Six research files in `~/cairo2/research/` provide the supporting evidence. The migration phases above are independently shippable — Phase 1 alone is worth doing this week regardless of decisions on later phases.*
