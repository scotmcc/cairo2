# Cairo Build Checklist

Every discrete piece of the system. No implied order — that lives in the roadmap.
Each item is a single responsibility. Group them however you like in the roadmap; they check off individually here.

**Status markers:** `[ ]` not started · `[~]` in progress · `[x]` done · `[!]` blocked

---

## Repository Setup

- [ ] Initialize `go.mod` for cairo2 (module path decision: `github.com/...` vs internal)
- [ ] Set up `Makefile` with standard targets (`build`, `test`, `lint`, `clean`, `install`)
- [ ] Configure `golangci-lint` (`.golangci.yml`)
- [ ] Set up CI pipeline (GitHub Actions or equivalent)
- [ ] `.gitignore` for Go + Node + .NET artifacts
- [ ] Top-level `go.sum` committed and stable

---

## cmd/cairo — The Agent Binary

### Composition root (Phase 2 decomposition)
- [ ] `app.go` — `App` struct + `newApp(ctx, opts)` composition root
- [ ] `surfaces.go` — `runTUI`, `runCLI`, `runVSCode`, `runOneShot`
- [ ] `main.go` reduced to ~80 lines: signal handling + subcommand dispatch

### Subcommands (one file each)
- [ ] `cmd_serve.go` — `cairo serve --tsnet / --auth / --port`
- [ ] `cmd_learn.go` — `cairo learn <path>`
- [ ] `cmd_dream.go` — `cairo dream`
- [ ] `cmd_config.go` — `cairo config get/set`
- [ ] `cmd_export.go` — `cairo export`
- [ ] `cmd_import.go` — `cairo import`
- [ ] `cmd_diff.go` — `cairo diff` (bundle diff)
- [ ] `cmd_task.go` — `cairo task <id>` (promote from `--task` flag)
- [ ] `cmd_token.go` — `cairo token`
- [ ] `wizard.go` — first-run setup wizard

---

## cmd/cairo-registry — Enterprise Control Plane

- [ ] Merge `~/cairo-registry/cmd/cairo-registry/` → `cmd/cairo-registry/`
- [ ] Update import paths to cairo2 module
- [ ] `main.go` with `--no-tsnet`, `--addr`, `--db` flags
- [ ] HTTP server wiring: registration, heartbeat, WebSocket, admin API
- [ ] Systemd service unit: `cairo-registry.service`

---

## cmd/cairo-ctl — Operator CLI

- [ ] Merge `~/cairo-registry/cmd/cairo-ctl/` → `cmd/cairo-ctl/`
- [ ] Update import paths
- [ ] `list` subcommand — tabular agent list with last-seen age
- [ ] `get <id>` subcommand — key:value detail view
- [ ] `health` subcommand — fleet summary
- [ ] `revoke <id>` subcommand — revoke agent enrollment
- [ ] `broadcast <command>` subcommand — queue command to all/filtered agents
- [ ] `departments` subcommand — manage departments and role assignments
- [ ] `audit` subcommand — export audit log entries
- [ ] `--addr`, `--operator` flags

---

## internal/agent — Core Agent Loop

- [ ] Migrate `~/cairo/internal/agent/` → `internal/agent/`
- [ ] Agent loop: user turn → tool dispatch → LLM response → repeat
- [ ] System prompt assembly from roles, providers, memory
- [ ] Response summarizer (long session compression)
- [ ] Tool call dispatch and result collection
- [ ] Agent context cancellation and graceful shutdown
- [ ] `consider/` inner-dialogue sub-agent (migrate as-is)

---

## internal/llm — LLM Client

- [ ] Migrate `~/cairo/internal/llm/` → `internal/llm/`
- [ ] Ollama client
- [ ] OpenAI-compatible client
- [ ] Streaming response support
- [ ] Model interface (swappable at startup via `app.go`)
- [ ] Retry/backoff on transient errors

---

## internal/tools — Tool Registry + Built-in Tools

- [ ] Migrate tool registry (`registry.go`) → `internal/tools/`
- [ ] `bashTool` — execute shell commands
- [ ] `readTool` — read file contents
- [ ] `writeTool` — write file contents
- [ ] `editTool` — string replacement edits
- [ ] `searchTool` — file/content search (grep/glob)
- [ ] `fetchTool` — HTTP fetch (web)
- [ ] `learnTool` — trigger learn pipeline from tool call
- [ ] `agentTool` (spawn) — spawn subagent
- [ ] `jobTool` — job management
- [ ] `taskTool` — task management
- [ ] `worktreeTool` — worktree sandbox operations
- [ ] `memoryTool` — memory read/write
- [ ] `configTool` — config read/write from tool context
- [ ] `considerTool` — trigger consider sub-agent
- [ ] `chooseTool` — structured choice/selection
- [ ] `sayTool` — structured output/narration
- [ ] `soulTool` — identity/persona access
- [ ] `skillTool` — skill invocation
- [ ] `promptPartTool` — prompt fragment injection
- [ ] `mergeJobTool` — merge job results
- [ ] `customTool` runtime — load and execute custom tools
- [ ] `dbtools` — tool list/introspection built-ins

---

## internal/commands — Slash Command Registry

- [ ] Migrate `~/cairo/internal/commands/` → `internal/commands/`
- [ ] Slash command registry and dispatch
- [ ] All existing slash command implementations

---

## internal/learn — Indexing Pipeline

- [ ] Migrate `~/cairo/internal/learn/` → `internal/learn/`
- [ ] Directory walker + file chunker
- [ ] Embedding generation
- [ ] Write to `store/index/` (SQLite, default)
- [ ] Write to `connectors/qdrant/` (enterprise, via VectorStore interface)
- [ ] Incremental indexing (skip unchanged files)
- [ ] Project-level index management
- [ ] Classification scope label on indexed chunks

---

## internal/worktree — Sandbox Manager

- [ ] Migrate `~/cairo/internal/worktree/` → `internal/worktree/`
- [ ] Git worktree creation and cleanup
- [ ] Sandbox process isolation
- [ ] Worktree lifecycle management (create, reap, status)

---

## internal/providers — Context Injection

- [ ] Migrate `~/cairo/internal/providers/` → `internal/providers/`
- [ ] Git context provider (branch, status, recent commits)
- [ ] Shell context provider (cwd, env)
- [ ] VS Code context provider (open files, selection)
- [ ] WaveTerm (wsh) context provider

---

## internal/store — Local Persistence (split from internal/db/)

### schema/
- [ ] Migrate `schema.go` + migrations → `internal/store/schema/`
- [ ] All DDL in one place; migration counter (`PRAGMA user_version`) preserved

### sqliteopen/
- [ ] Migrate `db.go` (Open, OpenAt, WithTx, premigration backup) → `internal/store/sqliteopen/`
- [ ] Composite `*DB` type that vends all sub-stores

### config/
- [ ] Migrate config queries + `KeyXxx` constants → `internal/store/config/`
- [ ] Typed KV get/set
- [ ] Enterprise connector config keys (qdrant_url, postgres_dsn, neo4j_uri, etc.)

### sessions/
- [ ] Migrate `SessionQ`, `MessageQ` → `internal/store/sessions/`
- [ ] Session CRUD
- [ ] Message history with pagination

### identity/
- [ ] Migrate roles, prompts, skills, hooks → `internal/store/identity/`
- [ ] Migrate consider_aspects, consider_activations → `internal/store/identity/`
- [ ] Migrate state + state_ritual (export/import bundles) → `internal/store/identity/`

### memory/
- [ ] Migrate `MemoryQ`, `FactQ`, `SummaryQ` → `internal/store/memory/`
- [ ] Migrate `DreamQ`, `DreamLogQ` → `internal/store/memory/`
- [ ] Migrate MMR scorer → `internal/store/memory/`
- [ ] Migrate memory curator → `internal/store/memory/`

### jobs/
- [ ] Migrate `JobQ`, `TaskQ`, `WorktreeQ`, `TaskArtifactQ` → `internal/store/jobs/`
- [ ] Migrate `reap.go` (stale job cleanup) → `internal/store/jobs/`
- [ ] Migrate `proc_unix.go` + `proc_windows.go` → `internal/store/jobs/`

### index/
- [ ] Migrate `IndexedFileQ`, `ChunkQ`, `ProjectQ` → `internal/store/index/`
- [ ] Migrate `embed_search.go` → `internal/store/index/`
- [ ] Define `VectorStore` interface with scope dimension
- [ ] Classification scope labels on indexed chunks

### registrations/
- [ ] Migrate registration ledger from cairo-registry → `internal/store/registrations/`
- [ ] Agent type field: `personal` | `departmental` | `enterprise`
- [ ] Department association field
- [ ] Access policy field (who can address this agent)
- [ ] Device posture fields (last seen, ws_connected, heartbeat history)

---

## internal/protocol — Wire Types (Single Source of Truth)

- [ ] Migrate `~/cairo/internal/protocol/registry.go` → `internal/protocol/`
- [ ] Verify byte-identical with `~/cairo-registry` equivalent; delete duplicate
- [ ] `RegisterRequest` / `RegisterResponse`
- [ ] `Frame` (heartbeat, liveness, ack)
- [ ] `KnowledgeQuery` frame type
- [ ] `KnowledgeContribution` frame type
- [ ] `KnowledgeResponse` frame type
- [ ] `KnowledgeContributionAck` frame type

---

## internal/registry — Agent-side Fleet Client

- [ ] Consolidate `~/cairo/internal/registry/` + `~/cairo/internal/registry-client/` → `internal/registry/`
- [ ] `Register()` — POST to registry, receive agent ID
- [ ] `HeartbeatLoop()` — periodic heartbeat
- [ ] `LivenessStream()` — WebSocket keep-alive
- [ ] Reconnect with exponential backoff

---

## internal/registryserver — Fleet Server (PEP)

- [ ] Migrate `~/cairo-registry/internal/` → `internal/registryserver/`
- [ ] HTTP handlers: POST `/register`, POST `/heartbeat/:id`, GET `/ws/:id`
- [ ] Admin API: GET `/admin/agents`, GET `/admin/agents/:id`, POST `/admin/agents/:id/revoke`
- [ ] POST `/admin/broadcast` — queue command to agents
- [ ] Enterprise gateway: route chat sessions from cairo-ui to specific agent nodes
- [ ] Call `internal/access/` (PDP) on every routing decision
- [ ] Emit to `internal/audit/` on every access decision
- [ ] Department management API endpoints
- [ ] Knowledge request routing: forward `KnowledgeQuery` frames to target agent

---

## internal/server — Agent HTTP API

- [ ] Migrate `~/cairo/internal/server/` → `internal/server/`
- [ ] Existing: `POST /api/chat`, `GET /api/events` (SSE), `POST /v1/chat/completions`, `POST /rpc`, `GET /healthz`
- [ ] Add: `GET /api/health` (version, uptime, db path)
- [ ] Add: `GET /api/config/snapshot` (all config + roles + aspects)
- [ ] Add: `PUT /api/config/{key}` — set config value
- [ ] Add: `GET /api/sessions` — list with insight
- [ ] Add: `GET /api/sessions/{id}` — session detail
- [ ] Add: `PATCH /api/sessions/{id}` — rename session
- [ ] Add: `DELETE /api/sessions/{id}` — delete session
- [ ] Add: `GET /api/sessions/{id}/messages?limit&before` — paginated history
- [ ] Add: `PATCH /api/roles/{name}` — update role model/think
- [ ] Add: `PUT /api/consider/aspects/{name}` — upsert aspect
- [ ] Add: `PATCH /api/consider/aspects/{name}` — toggle enabled
- [ ] Add: `DELETE /api/consider/aspects/{name}` — delete aspect
- [ ] Add: `GET /api/metrics` — snapshot counts for dashboard
- [ ] Validate forwarded identity on every request (ZT: application gate)

---

## internal/tui — Bubble Tea TUI

- [ ] Migrate `~/cairo/internal/tui/` → `internal/tui/`
- [ ] Transcript panel
- [ ] Input panel
- [ ] Status bar
- [ ] Tool call display
- [ ] Progress indicators
- [ ] Toast notifications

---

## internal/tuisetup — TUI Init

- [ ] Migrate `~/cairo/internal/tuisetup/` → `internal/tuisetup/`
- [ ] Lipgloss renderer init
- [ ] Blank import for terminal capability detection

---

## internal/cli — Non-TUI CLI Modes

- [ ] Migrate `~/cairo/internal/cli/` → `internal/cli/`
- [ ] Background / one-shot mode (`RunOnce`)
- [ ] VS Code mode (`RunVSCode`) — JSONL event stream
- [ ] Learn CLI mode

---

## internal/authn — User Gate (ZT: User Pillar)

- [ ] No-op stub wired into all gates from day one
- [ ] tsnet peer certificate → extract Tailscale user identity
- [ ] OIDC `id_token` validation (signature, expiry, audience)
- [ ] Identity claim extraction (user ID, email, roles, dept memberships)
- [ ] Session revocation check
- [ ] SAML 2.0 assertion validation (DoD PKI / CAC card)

---

## internal/access — Policy Decision Point (ZT: Application Pillar)

- [ ] No-op stub (allow all) wired from day one; replace incrementally
- [ ] Department model: create dept, assign user, assign agent
- [ ] Role definitions: admin, developer, dept-lead, analyst, super-admin
- [ ] `CanAddress(identity, agentID, sessionCtx) (bool, reason)`
- [ ] `ListVisible(identity) []AgentID` — agent catalog scoping
- [ ] Super-admin bypass (explicit flag, always audit-logged)
- [ ] Per-session authorization (re-evaluate on every routing request)
- [ ] Knowledge scope access: `CanReadScope(identity, scope) bool`
- [ ] Knowledge scope access: `CanContributeTo(identity, scope) bool`

---

## internal/audit — Immutable Audit Log (ZT: Visibility Pillar)

- [ ] No-op stub (`audit.Log()` compiles but does nothing) — wire immediately
- [ ] Append-only event log (SQLite WAL mode to start; swap backend later)
- [ ] Structured event: timestamp, gate, actor, target, action, outcome, session ID, source IP
- [ ] Event type: `user_authenticated`
- [ ] Event type: `token_rejected`
- [ ] Event type: `session_expired`
- [ ] Event type: `node_enrolled`
- [ ] Event type: `node_revoked`
- [ ] Event type: `heartbeat_missed`
- [ ] Event type: `access_granted`
- [ ] Event type: `access_denied`
- [ ] Event type: `superadmin_override`
- [ ] Event type: `data_accessed`
- [ ] Event type: `pii_detected`
- [ ] Event type: `guardrail_fired`
- [ ] Event type: `knowledge_queried`
- [ ] Event type: `knowledge_contribution_submitted`
- [ ] Event type: `knowledge_contribution_approved`
- [ ] Event type: `knowledge_contribution_rejected`
- [ ] Event type: `broadcast_sent`
- [ ] Event type: `department_modified`
- [ ] Event type: `role_assigned`
- [ ] Retention policy enforcement
- [ ] Export API: SOC 2 format
- [ ] Export API: HIPAA format
- [ ] Export API: GDPR format

---

## internal/guardrails — Content Safety (ZT: Data Pillar)

- [ ] No-op stub wired at agent loop boundary from day one
- [ ] Input scanning: prompt injection detection
- [ ] Input scanning: jailbreak pattern detection
- [ ] Input scanning: blocked topic enforcement (per-role policy)
- [ ] Output scanning: PII detection (names, SSNs, credentials, keys)
- [ ] Output scanning: sensitive data masking
- [ ] Per-role guardrail profiles
- [ ] Emit to `internal/audit/` when guardrail fires

---

## internal/knowledge — Knowledge Federation (ZT: Data Pillar + IPC)

- [ ] `scopes.go` — scope definitions, flow rules, hierarchy
- [ ] `federation.go` — route knowledge queries to right store or agent
- [ ] `contribution.go` — write-up approval flow (submit, review, approve/reject, index)
- [ ] `sync.go` — pull-on-demand caching with staleness TTL (DDIL resilience)
- [ ] DDIL tier fallback: L1 (local) → L2 (Qdrant) → L3 (main agent) → L4 (internet)
- [ ] `KnowledgeQuery` dispatch via registry
- [ ] `KnowledgeResponse` assembly (scope-filtered, only authorized scopes returned)
- [ ] `KnowledgeContribution` submission and approval gate
- [ ] Knowledge cache warm-up on `cairo serve --tsnet` startup

---

## internal/connectors — Enterprise Data Plane

### qdrant/
- [ ] Qdrant HTTP client
- [ ] Implement `store/index.VectorStore` interface
- [ ] Per-scope namespace/collection isolation
- [ ] Semantic search with scope filter
- [ ] Upsert (index contribution)
- [ ] Delete by chunk ID

### postgres/
- [ ] Postgres driver + connection pool
- [ ] Shared session store implementation
- [ ] Shared memory/fact store implementation
- [ ] Schema migrations for shared tables

### neo4j/
- [ ] Neo4j Bolt driver
- [ ] Knowledge graph query interface
- [ ] Org graph traversal (people, projects, systems, relationships)
- [ ] Dependency mapping queries

### s3/
- [ ] S3-compatible client (AWS SDK + MinIO)
- [ ] Document source for `learn/` pipeline (walk bucket, download, chunk)
- [ ] Artifact upload (task outputs, exported bundles)
- [ ] Presigned URL generation for audit export

---

## internal/services — AI Application Surfaces

### codeassist/
- [ ] Formalize existing cairo code assistant as named service
- [ ] Service registration with enterprise control plane

### docqa/
- [ ] Query interface: accept natural language question + scope
- [ ] Retrieve top-K chunks from `store/index/` or `connectors/qdrant/`
- [ ] Optional graph traversal via `connectors/neo4j/`
- [ ] Retrieval-augmented prompt assembly
- [ ] Answer synthesis with source citations

### automation/n8n/
- [ ] N8n HTTP API client
- [ ] Workflow discovery (list available workflows)
- [ ] Workflow trigger as agent tool
- [ ] Workflow result polling and return

### automation/ansible/
- [ ] Ansible runner integration
- [ ] Playbook execution as agent tool
- [ ] Playbook output parsing for agent context
- [ ] Inventory management

### analytics/
- [ ] Per-user usage metrics (sessions, messages, tool calls)
- [ ] Per-department aggregation
- [ ] Per-agent usage metrics
- [ ] Feedback collection (thumbs up/down on responses)
- [ ] Response latency metrics
- [ ] Prometheus metrics endpoint (`/metrics`)
- [ ] Export for Grafana / Datadog

---

## internal/modelmanager — Model Lifecycle

- [ ] Approved model registry (name, version, endpoint, tier)
- [ ] Model selection policy per role/department
- [ ] A/B serving config (traffic split between models)
- [ ] Evaluation scaffolding (run test set against candidate model)
- [ ] Model promotion workflow (candidate → approved)

---

## internal/telemetry — Monitoring + Health

- [ ] Health check aggregation (agent, DB, LLM, registry)
- [ ] `GET /metrics` — Prometheus-compatible exposition
- [ ] Anomaly detection hooks (error rate, unusual tool patterns)
- [ ] Alert emission (PagerDuty, Slack, email)
- [ ] Structured logging (JSON, leveled)

---

## internal/hostedit, internal/version (unchanged)

- [ ] Migrate `~/cairo/internal/hostedit/` → `internal/hostedit/`
- [ ] Migrate `~/cairo/internal/version/` → `internal/version/`

---

## web-agent — Node/React Browser UI

- [ ] Migrate `~/cairo/web-agent/` → `web-agent/`
- [ ] Replace `cairoDb.ts` Python bridge with HTTP client (`GET /api/config/snapshot`, etc.)
- [ ] Drop `python3` runtime dependency from packaging
- [ ] TypeScript HTTP client for all new `/api/` endpoints
- [ ] `CAIRO_HTTP_URL` config (defaults to spawned cairo's port)
- [ ] Chat UI (existing — keep)
- [ ] Session list + management UI
- [ ] Config editor UI
- [ ] Role management UI
- [ ] Consider aspects UI
- [ ] End-to-end test: web-agent works without Python installed

---

## vscode-extension — VS Code Extension

- [ ] Migrate `~/cairo/vscode-extension/` → `vscode-extension/`
- [ ] JSONL event stream integration
- [ ] In-editor chat panel
- [ ] Status bar integration

---

## cairo-ui — Blazor Enterprise UI (separate repo, tracked here)

- [ ] `Cairo.Client.Registry` classlib — typed HTTP client for `cairo-registry` API
- [ ] `Cairo.Client.Agent` classlib — typed HTTP client for agent HTTP API
- [ ] `Cairo.UI` classlib — shared Blazor components
- [ ] SSO / OIDC integration (User gate — Blazor side)
- [ ] MFA enforcement
- [ ] Agent selector: dropdown with type labels (personal / departmental / enterprise)
- [ ] Chat surface: connected to registry routing
- [ ] Session list view
- [ ] Fleet management page (admin: all agents, status, last seen)
- [ ] Department management page (admin: create dept, assign users/agents)
- [ ] Role management page (admin: assign roles to users)
- [ ] Analytics dashboard (usage metrics, model usage, feedback)
- [ ] Audit log viewer (admin: export, filter by event type)
- [ ] `HttpCairoClient.SendAsync` — implement against `/rpc cairo.send`
- [ ] `HttpCairoClient.SendStreamAsync` — SSE consumption
- [ ] `HttpCairoClient.GetMetricsAsync` — against `/api/metrics`

---

## Build Artifacts

### cairo binary
- [ ] Build: Linux x86_64 binary (`cairo`)
- [ ] Build: Linux ARM64 binary (`cairo`)
- [ ] Build: macOS ARM64 binary (`cairo`) — `.pkg` installer
- [ ] Build: RPM package (`cairo-<version>.rpm`)
- [ ] Build: DEB package (`cairo-<version>.deb`)
- [ ] Systemd service unit: `cairo.service`
- [ ] Install script: Linux (`install.sh`)
- [ ] Install script: macOS

### cairo-registry binary
- [ ] Build: Linux x86_64 binary (`cairo-registry`)
- [ ] Build: Linux ARM64 binary (`cairo-registry`)
- [ ] Build: macOS ARM64 binary (`cairo-registry`)
- [ ] Build: RPM package (`cairo-registry-<version>.rpm`)
- [ ] Build: DEB package (`cairo-registry-<version>.deb`)
- [ ] Systemd service unit: `cairo-registry.service`

### cairo-ctl binary
- [ ] Build: Linux x86_64 binary (`cairo-ctl`)
- [ ] Build: Linux ARM64 binary (`cairo-ctl`)
- [ ] Build: macOS ARM64 binary (`cairo-ctl`)
- [ ] Build: RPM package (`cairo-ctl-<version>.rpm`)
- [ ] Build: DEB package (`cairo-ctl-<version>.deb`)

### Build tooling
- [ ] `scripts/build.sh` — build all three binaries
- [ ] `scripts/build-web-agent.sh` — Node build
- [ ] `scripts/build-extension.sh` — VS Code extension build
- [ ] `scripts/packaging/build-packages.sh` — RPM + DEB for all binaries
- [ ] `scripts/packaging/pre-package.sh` — pre-package validation
- [ ] `scripts/install.sh` — system install (Linux)
- [ ] `scripts/install-web-agent.sh`
- [ ] `scripts/install-extension.sh`
- [ ] `scripts/install-hooks.sh`
- [ ] `scripts/install-deps.sh` — dependency check + install
- [ ] `scripts/reset-userdata.sh` — dev tooling for clean state

---

## Smoke Tests

- [ ] `scripts/smoke/registry-client.sh` — register + heartbeat + ws_connected
- [ ] Smoke: `cairo serve` + `cairo-ctl health` end-to-end
- [ ] Smoke: `cairo learn <dir>` + semantic search returns results
- [ ] Smoke: web-agent starts, sessions load, config editable (no Python)
- [ ] Smoke: `cairo export` + `cairo import` round-trip
- [ ] Smoke: knowledge query L1 (local SQLite, disconnected)
- [ ] Smoke: knowledge query L2 (Qdrant, enterprise network)
- [ ] Smoke: ZT gate chain: authn → access → agent → audit log populated

---

## Documentation

- [ ] `docs/architecture/decisions.md` — D1–D11 (done ✓, keep updated)
- [ ] `docs/reference/http-api.md` — all `/api/` endpoints with request/response shapes
- [ ] `docs/reference/wire-protocol.md` — all frame types in `internal/protocol/`
- [ ] `docs/guide/deploy-standalone.md` — standalone cairo install
- [ ] `docs/guide/deploy-fleet.md` — cairo + cairo-registry enterprise setup
- [ ] `docs/guide/deploy-ddil.md` — air-gapped / DDIL deployment guide
- [ ] `docs/guide/zero-trust.md` — ZT gate chain, pillar mapping, compliance
- [ ] `docs/guide/knowledge-federation.md` — scopes, flow rules, agent-to-agent queries
- [ ] `docs/guide/departments.md` — creating departments, assigning agents and users

---

*Checklist last updated: 2026-05-07. Roadmap lives separately — this is the source of truth for what exists to be built.*
