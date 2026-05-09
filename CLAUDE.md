# cairo2 — CLAUDE.md

Local-first AI coding harness in Go. Single SQLite DB at `~/.cairo/cairo.db`. Bubble Tea TUI with an Ollama-backed agent loop. Module path `github.com/scotmcc/cairo2`. Pre-milestone-1 development; no stable release yet.

This file is the project layer. Global conventions (file hygiene, repo cleanliness, collaboration style) live in `~/.claude/CLAUDE.md`. Contributor onboarding lives in `docs/development/contributing.md`.

---

## Build & run

Use the scripts — not bare `go build` — so builds are consistent with what packaging produces.

```sh
# Build all three binaries (output → ./bin/)
bash scripts/build.sh

# Build and install cairo, cairo-registry, cairo-ctl to /usr/local/bin/
bash scripts/install.sh

# Run all tests
make test          # go test ./...

# Build and run from ./bin/cairo
make run

# Start the TUI (Bubble Tea full-screen)
./bin/cairo -tui

# Bare invocation drops to the line CLI (`>` REPL) — same as cairo
./bin/cairo

# Subcommands
./bin/cairo export [--full] <out.cairo>  # export identity to portable bundle
./bin/cairo import [--force] <bundle>    # replace local identity from bundle
./bin/cairo diff <bundle>                # compare bundle against local identity
./bin/cairo dream                        # headless maintenance: memories, facts, summaries
./bin/cairo learn [path]                 # index a codebase (background subprocess when called as a tool)
./bin/cairo serve [--port N] [--auth] [--register URL] [--tsnet]  # HTTP server
./bin/cairo token                        # generate bearer token for serve --auth
./bin/cairo config get <key>             # read a config key
./bin/cairo config set <key> <value>     # write a config key

# cairo-registry — fleet registry + enterprise gateway (Phase 2.1)
./bin/cairo-registry  # stub until Phase 2.1

# cairo-ctl — operator CLI (Phase 2.1+)
./bin/cairo-ctl  # stub until Phase 2.1
```

Binaries land at `./bin/` (cairo, cairo-registry, cairo-ctl). SQLite DB lives at `~/.cairo/cairo.db` — created on first run.
`CAIRO_DATA_DIR` env var overrides the data directory.

---

## Scripts

All scripts in `scripts/` are self-contained and safe to re-run.

| Script | What it does |
|---|---|
| `scripts/build.sh` | Build all three binaries → `./bin/`. **Use this instead of bare `go build`.** |
| `scripts/install.sh` | Calls build.sh, then installs cairo, cairo-registry, cairo-ctl to `/usr/local/bin/`. |
| `scripts/install-deps.sh` | One-shot bootstrap for a fresh machine. Installs Go, Node, sqlite, packaging tools, then builds cairo. Flags: `--skip-cairo`, `--skip-system`. |
| `scripts/build-extension.sh` | Build the VS Code extension → `vscode-extension/.vscode-extension/cairo-vscode-<ver>.vsix`. Actions: `build`, `package`, `clean`, `all` (default). |
| `scripts/install-extension.sh` | Install the .vsix into local VS Code. Auto-builds first if no .vsix found. |
| `scripts/install-hooks.sh` | Copy `.githooks/pre-commit` → `.git/hooks/pre-commit`. Run once after cloning. |
| `scripts/reset-userdata.sh` | Wipe user-generated data from `~/.cairo/cairo.db` (sessions, messages, memories, etc.) while preserving config, roles, aspects, skills, tools. Flags: `--yes`, `--dry-run`, `--db PATH`. |
| `scripts/build-web-agent.sh` | Build the web agent (npm install, TypeScript compile, webpack) → `web-agent/dist/`. |
| `scripts/cairo-web.sh` | Runtime launcher: starts the web-agent Node server + manages the Cairo child process. Env vars: `CAIRO_WEB_HOST`, `CAIRO_WEB_PORT`, `CAIRO_WEB_TOKEN`, `CAIRO_CLI_PATH`, `CAIRO_WORKSPACE_ROOTS`. |
| `scripts/install-web-agent.sh` | Install web-agent to `/usr/share/cairo/web-agent/`, configure systemd user unit `cairo-web.service`, enable autostart. |
| `scripts/packaging/build-packages.sh` | Build `.deb` and `.rpm` packages → `build/packages/`. Flags: `--deb`, `--rpm`, `--version VERSION`, `--skip-extension`, `--skip-web-agent`, `--skip-tests`. |
| `scripts/packaging/pre-package.sh` | Pre-package gate: runs lint/tests before packaging. Called by `build-packages.sh` unless `--skip-tests`. |
| `scripts/smoke/registry-client.sh` | End-to-end smoke: builds cairo + cairo-registry, runs full registration + WS liveness + stale sweep + stable-agent-id-on-restart flow. Requires `jq`, `python3`. Exit 0 = PASS. |

**Makefile targets** (thin wrappers — prefer scripts for anything beyond quick local runs):

```sh
make build         # build all three binaries to ./bin/
make install       # build + install all three binaries to /usr/local/bin
make lint          # go vet ./...
make run           # build + run
make test          # go test ./...
make clean         # rm ./bin
```

---

## Package map

```
cmd/cairo/           — binary entrypoint
cmd/cairo-registry/  — fleet registry server (stub; Phase 2.1)
cmd/cairo-ctl/       — operator CLI (stub; Phase 2.1+)
internal/agent/      — agent loop, system prompt assembly (prompt.go), summarizer
internal/cli/        — subcommand handlers (learn, config, ...)
internal/commands/   — slash command registry and dispatchers
internal/store/          — store layer (8 sub-packages; split from internal/db/ in Phase 1.3)
  internal/store/schema/     — numbered migrations (schema.go); apply via sqliteopen
  internal/store/sqliteopen/  — wiring root: opens DB, runs migrations, constructs all Q types
  internal/store/config/      — config key/value store
  internal/store/sessions/     — session + message store
  internal/store/identity/     — identity, roles, aspects, prompts
  internal/store/memory/       — memory store (scoring doctrine — see Landmines)
  internal/store/jobs/         — job queue and task store
  internal/store/index/        — codebase index (chunks, embeddings)
internal/hostedit/   — file-open editor routing
internal/learn/      — codebase indexing: walker, indexer, background spawn
internal/llm/        — Ollama client (narrow 5-function interface)
internal/protocol/   — shared wire types (registry register/heartbeat payloads)
internal/providers/  — environment context injection (git, shell, vscode, wsh)
internal/registry/   — registry client: LivenessStream (WebSocket) + Register/Stream (formerly registry-client)
internal/server/     — HTTP server, Bridge (SSE event relay), tsnet listener
internal/tools/      — tool registry and ~15 built-in tool implementations (post-v063 trim)
internal/tui/        — Bubble Tea TUI: panels, transcript, toasts, progress bars
internal/tuisetup/   — TUI initialization and wiring
internal/version/    — version string (set by ldflags at build time)
internal/worktree/   — worktree sandbox management

# Milestone 2+ placeholders (obligations, not dead dirs):
internal/access/       — (Phase 2+)
internal/audit/        — (Phase 2+)
internal/authn/        — (Phase 2+)
internal/connectors/   — (Phase 2+)
internal/guardrails/   — (Phase 2+)
internal/knowledge/    — (Phase 2+)
internal/modelmanager/ — (Phase 2+)
internal/registryserver/ — (Phase 2+)
internal/services/     — (Phase 2+)
internal/telemetry/    — (Phase 2+)

vscode-extension/    — VS Code extension (TypeScript): DB inspector panels, cairo integration
web-agent/           — Browser UI + Node.js server for remote Cairo access over LAN/Tailscale
  web-agent/server/  — Fastify + WebSocket server: CairoRunner (child proc mgmt), cairoDb (SQLite read), sessionStore, workspaces
  web-agent/web/     — React frontend: App.tsx (chat UI), ConfigPanel, SessionsPanel
  web-agent/shared/  — Shared protocol types (protocol.ts) between server and web
```

---

## Landmines

**Worktree directory.** `.claude/worktrees/` accumulates sandbox worktrees from agent runs. They are git worktrees — use `git worktree prune`, not `rm -rf`. Do not read them as source.

**`code_search` retired.** `code_search` was removed from `Default()` and all role allowlists in v0.2.1. `learn` is the sole codebase-search path. The `code_index` table, `cairo index`, and `cairo re-embed` CLI were removed in v0.3.0 (v064 migration).

**Schema vs seed.** `internal/store/schema/` has numbered migrations (v001+); `internal/store/sqliteopen/` runs them at open time. Seed data is in `internal/store/sqliteopen/seed.go`. New columns or tables typically need both: a migration to backfill existing DBs and a seed entry for new ones. Audit recent migrations for parity before adding either.

**System prompt assembly.** `BuildSystemPrompt` in `internal/agent/prompt.go` assembles the prompt in a specific order (steering → base parts → soul → user context → role → tools → projects → summaries → memories → facts → temporal stamp). Order matters; do not reorder sections without understanding why each is positioned.

**`cairo learn` is a background subprocess.** When invoked as a tool from inside the agent loop, it spawns a child process. Do not expect inline blocking behavior.

**Memory scoring doctrine.** `memories.importance` and `memories.weight` measure different things and must NOT be combined multiplicatively at retrieval. `importance` = inherent salience, set at write, decays slowly via `decayImportance()` at query time — this is the retrieval-relevance modifier. `weight` = lifecycle signal, bumps on retrieval, decays nightly in dream — affects only auto-promote (→ importance=1.0) and auto-dump (→ deleted_at). The retrieval score in `internal/store/memory/` stays `cosine * decayImportance(importance)`. The auto-promote path (Phase 3 of dream-agent roadmap) is the one-way bridge from weight to importance. Decided 2026-04-28; reasoning: an auto-promoted memory that's currently cold (high importance, low weight) must not be crushed in retrieval — that defeats the purpose of promotion.

**store/ import invariant.** `internal/store/sqliteopen` is the wiring root — it imports all sub-packages and constructs their Q types. Sub-packages import only `store/config/` and `database/sql`. No sub-package may import sqliteopen or another sub-package directly. Violating this creates an import cycle that breaks the build.

**Placeholder directories are obligations.** `internal/access/`, `internal/audit/`, `internal/authn/`, `internal/connectors/`, etc. are Milestone 2+ targets. They exist to document the obligation. Do not delete them; they will grow into real packages. Check `docs/architecture/decisions.md` for D5–D11 before touching them.

**CAIRO_CLI_PATH default in web-agent.** The web-agent server defaults `CAIRO_CLI_PATH` to `'cairo'`. On a dev machine where `/usr/local/bin/cairo` is the legacy cairo binary, the web-agent will use the wrong binary. Set `CAIRO_CLI_PATH=$(pwd)/bin/cairo` in your shell environment when developing against cairo2.

---

## Where to look for things

- `~/.claude/CLAUDE.md` — global conventions, collaboration style, model selection
- `docs/architecture/` — deep-dive design docs
- `docs/reference/tools.md` — tool catalog
- `docs/reference/config-keys.md` — runtime config keys
- `.internal/notes/` — working drafts (gitignored; do not pollute)

---

## Working notes

Working notes, plans, and scratch go in `.internal/notes/` or auto-memory (`~/.claude/projects/<slug>/memory/`). Repo root is for shipped artifacts only. No `PLAN.md`, `TODO.md`, or `DESIGN.md` at root.
