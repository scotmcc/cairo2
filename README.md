# Cairo

Private. Context-Aware. Enterprise-Ready.

Cairo is an AI coding agent and enterprise intelligence platform. The agent binary (`cairo`) is the brain — deployable standalone on any developer's box, or enrolled in the enterprise fleet via `cairo serve --tsnet`. The enterprise stack layers security, routing, knowledge, and governance on top without changing the agent.

---

## Ground rules for cairo2 (read this before writing code)

**cairo2 is a migration and reorganization project, not a green-field rebuild.** The working app already exists. The job is to translate it into the enterprise structure described below, then *extend* it — not to reinvent the parts that work.

**Source-of-truth reference repos:**

| Source repo | What it is | Role for cairo2 |
|---|---|---|
| `~/cairo` | The current production cairo app — TUI, agent loop, LLM client, tools, learn, store, server, CLI. Single Go module `github.com/scotmcc/cairo`. | **Authoritative for behavior** of every package the legend marks ✅ Working. If you are touching `internal/agent/`, `internal/llm/`, `internal/tools/`, `internal/learn/`, `internal/store/`, `internal/server/`, `internal/tui/`, `internal/tuisetup/`, `internal/cli/`, `internal/hostedit/`, `internal/providers/`, `internal/worktree/`, `internal/commands/` — start by reading the cairo equivalent. The code in cairo is the spec. |
| `~/cairo-registry` | The standalone registry repo (now merged into cairo2 in Phase 2.1). Single Go module `github.com/scotmcc/cairo-registry`. | **Authoritative for behavior** of `cmd/cairo-registry/`, `cmd/cairo-ctl/`, `internal/registryserver/`, `internal/protocol/`. Frozen at the merge commit; no new work goes in there — it goes here. |
| `~/cairo2/docs/architecture/` | Design docs for the *enterprise structure* — `design.md` (.NET → Go analogy, package map), `decisions.md` (D1–D12), `research/registry-merge-plan.md`. | Authoritative for *how the pieces fit together* in cairo2. Doesn't override behavior — that's still cairo. |

### The migration rule

When porting a package marked ✅ Working from cairo:

1. **Copy the file unchanged.**
2. **Rewrite import paths** (`github.com/scotmcc/cairo/...` → `github.com/scotmcc/cairo2/...`).
3. **Update call sites** that the cairo2 reorganization forced to change — e.g., the `internal/db/` → `internal/store/<sub>/` split changed constructor signatures. Update the minimum needed for the file to compile against cairo2's package boundaries.
4. **Stop.** Anything beyond steps 1–3 is a rewrite, not a migration.

A faithful port should produce a diff that is overwhelmingly import-path lines plus a small number of call-site renames. If you find yourself restructuring a function, splitting a file, "improving" a pattern, or extracting an inline thing into a separate module — **stop and ask Scot.** That's invention, and invention has a separate budget (see "Where new code is allowed" below).

If you discover that a port already in cairo2's history *did* deviate from this rule and the deviation introduced a regression, the default is to revert to the cairo behavior — not to keep iterating on the deviated version.

### Where new code is allowed

The placeholder directories — `internal/access/`, `internal/audit/`, `internal/authn/`, `internal/connectors/`, `internal/guardrails/`, `internal/knowledge/`, `internal/modelmanager/`, `internal/services/`, `internal/telemetry/` — are the **extension surface**. They are intentionally empty in Milestone 1. They get filled in Milestones 2–5 per the ROADMAP. *That* is the new-code budget.

The cairo-registry merge (Phase 2.1) is also new code in cairo2's tree, but it isn't invented — it's a faithful port from `~/cairo-registry`. Same rule: copy, rewrite imports, update call sites that the merge forced, stop.

### Anti-patterns we have already hit

- **Treating "tests pass + build clean" as Milestone 1 success.** It isn't. Milestone 1 is *parity with cairo*. Behavioral parity has to be checked by running the app, not just by green CI.
- **Cleaning up code "while you're in there" during a migration.** No. Migrations are mechanical. Cleanups are separate work with their own justification.
- **Writing a plan from the ROADMAP success-criteria block without verifying the criteria match reality.** The ROADMAP has been wrong twice (Phase 2.1 endpoints, Phase 2.3 flags) — cite the source code or run the binary, don't trust the doc unaudited.

## Repository layout

```
cmd/
  cairo/              The agent binary — TUI, HTTP API, fleet node
  cairo-registry/     Enterprise control plane backend
  cairo-ctl/          Operator and admin CLI

internal/
  agent/              Core agent loop — the brain
  llm/                LLM client (Ollama, OpenAI-compatible)
  tools/              Tool registry + built-in tools
  learn/              Codebase and document indexing (RAG)
  store/              Local SQLite persistence (agent-private)
  connectors/         Enterprise data source adapters (Qdrant, Postgres, Neo4j, S3)
  services/           AI application surfaces (Code Assist, DocQA, Automation, Analytics)
  registryserver/     Fleet registry server logic
  registry/           Agent-side fleet client
  protocol/           Wire types — single source of truth
  authn/              Authentication (tsnet → OIDC/SAML)
  access/             RBAC, departments, access policy
  audit/              Immutable audit log
  guardrails/         Content safety, PII detection
  telemetry/          Metrics, health, monitoring
  server/             Agent HTTP API
  tui/                Bubble Tea TUI
  cli/                Non-TUI CLI modes

web-agent/            Node/React browser UI
vscode-extension/     VS Code extension
scripts/packaging/    RPM/deb build, systemd units
docs/architecture/    Design documents and research
```

## Package status legend

- ✅ Working — migrates from `~/cairo` (or `~/cairo-registry`) as-is per the migration rule above. Faithful port. If it diverges from the source repo's behavior in cairo2 today, that is a bug to be reverted, not a feature.
- 🔄 Reorganizing — mechanical restructure forced by the cairo2 package map (e.g., `internal/db/` → `internal/store/<sub>/`). No behavior change. Call-site updates only.
- 🔲 Slot — directory scaffolded for a Milestone 2+ enterprise feature. Empty in Milestone 1. This is the new-code surface.

See each package's `README.md` for status and responsibilities.  
See `docs/architecture/decisions.md` for the load-bearing architectural decisions.  
See `docs/architecture/design.md` for the full migration plan.

## Security model: Zero Trust

Every layer is a gate. No layer inherits trust from a previous layer. Every gate re-validates and emits to audit.

```
User → [Blazor: User gate — SSO/MFA]
     → [authn: token validation]
     → [registryserver: Device gate — node posture]
     → [access: Application gate — per-session policy decision]
     → [agent: re-validates forwarded identity]
     → [connectors: Data gate — source-level access control]
     → [audit: every gate logs here]
```

The seven ZT pillars map directly to packages. See `docs/architecture/decisions.md` D11 for the full mapping.

## Deployment modes

| Mode | Command | What it does |
|---|---|---|
| Standalone TUI | `cairo` | Local coding agent, no network |
| HTTP serve | `cairo serve` | Adds local HTTP API |
| Fleet node | `cairo serve --tsnet` | Enrolls in enterprise fleet |
| Enterprise registry | `cairo-registry` | Runs the control plane |
