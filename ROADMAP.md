# Cairo Roadmap

**Living document.** Phases are updated as work completes, bugs surface, and scope shifts.  
**Source of truth for sequence.** CHECKLIST.md is the source of truth for what exists to build.  
**Goal:** Translate the working cairo app into the cairo2 enterprise structure — no reinvention, just reorganization and extension.

---

## Standard Crew Workflow

Every phase runs through this pipeline. Job artifacts go in `~/cairo2/.claude/jobs/<phase-id>/`.

| Step | Agent | Model | Input | Output |
|---|---|---|---|---|
| 1 | Research | Sonnet/Opus | Briefing + codebase access | `research.md` |
| 2 | Plan | Sonnet | Research + briefing | `plan.md` (step-by-step with success criteria) |
| 3 | Implement | Haiku/Sonnet | Plan | Code changes + `implementation.md` |
| 4 | Review | Sonnet | Plan + diff + implementation.md | `review.md` (LGTM / CHANGES REQUESTED) |
| 5 | Spot check | Selene | All artifacts | Run CLI success criteria, report to Scot |

**Key rules:**
- Implementation agent follows the plan exactly — no improvising
- Every phase ends with CLI-verifiable success criteria, not "it compiles"
- Review agent reads the diff against the plan; flags drift, missing items, regressions
- Spot check runs the actual shell commands and reports pass/fail before calling it done
- TUI / Web UI demo requested explicitly by Scot at milestone boundaries

---

## Milestone 1 — Cairo Parity

> Get cairo2 doing everything cairo does today. No new features. The brain works, the fleet works, the API works. At the end of this milestone: `cairo "hello"` runs, `cairo serve` serves, `cairo-registry` tracks nodes, `cairo-ctl` inspects them.

---

### Phase 1.1 — Module Foundation

**Action:** Initialize the Go module, Makefile, and dev tooling for cairo2.

**Resolves:**
- `go.mod` initialization
- `Makefile` with build/test/lint/clean/install targets
- `golangci-lint` config
- `.gitignore`

**Crew notes:**  
Research: what module path to use (`github.com/scotmcc/cairo2`?), what lint rules match the existing cairo conventions.  
Implementation: mechanical — no logic, just scaffolding files.

**Success criteria:**
```bash
cd ~/cairo2
go build ./...          # compiles (no packages yet — empty main stubs ok)
make build              # produces bin/ artifacts
make test               # go test ./... exits 0
make lint               # golangci-lint exits 0
```

---

### Phase 1.2 — Core Package Migration (agent, llm, tools, commands, providers)

**Action:** Migrate the foundation packages from `~/cairo` into cairo2 with updated import paths. No behavior changes.

**Resolves:**
- `internal/agent/` + `internal/agent/consider/` migrated
- `internal/llm/` migrated
- `internal/tools/` + all 20 built-in tools migrated
- `internal/commands/` migrated
- `internal/providers/` migrated
- `internal/hostedit/` migrated
- `internal/version/` migrated
- `internal/worktree/` migrated
- `internal/learn/` migrated

**Crew notes:**  
Research: map every import of `github.com/scotmcc/cairo/internal/X` → `github.com/scotmcc/cairo2/internal/X`. Note any circular deps or packages that import `internal/db/` directly (those will need a shim until Phase 1.3).  
Implementation: `cp -r` + global import path rewrite + compile check per package.

**Success criteria:**
```bash
go build ./internal/agent/...
go build ./internal/llm/...
go build ./internal/tools/...
go test ./internal/tools/...    # tool unit tests pass
go test ./internal/agent/...
```

---

### Phase 1.3 — Store Layer Migration (internal/db/ → internal/store/)

**Action:** Split `internal/db/` (~12K LOC) into focused sub-packages under `internal/store/`. Mechanical move — no logic changes.

**Resolves:**
- `internal/store/schema/` — DDL + migrations
- `internal/store/sqliteopen/` — Open, OpenAt, WithTx, composite \*DB
- `internal/store/config/` — typed KV + KeyXxx constants
- `internal/store/sessions/` — SessionQ, MessageQ
- `internal/store/identity/` — roles, prompts, skills, hooks, consider, state
- `internal/store/memory/` — memory, facts, summaries, dreams, MMR, curator
- `internal/store/jobs/` — jobs, tasks, worktrees, artifacts, reap, proc
- `internal/store/index/` — indexed files, chunks, embed search; define VectorStore interface

**Crew notes:**  
Research: read `internal-db-audit.md` (already done — in `docs/architecture/research/`). The seam lines are already identified. Each `*Q` struct is its own sub-package.  
Implementation: one sub-package per PR, each must compile and pass its tests before moving to the next. Order: schema → sqliteopen → config → sessions → identity → memory → jobs → index.  
Risk: any package that does `import "internal/db"` needs updating. The research agent should list every import site first.

**Success criteria:**
```bash
go build ./internal/store/...
go test ./internal/store/...

# Verify no remaining references to old internal/db path
grep -r '"github.com/scotmcc/cairo2/internal/db"' . --include="*.go"
# ^ should return nothing
```

---

### Phase 1.4 — Presentation Layer Migration (tui, tuisetup, cli, server)

**Action:** Migrate the presentation packages. These depend on the store layer (Phase 1.3 must be complete).

**Resolves:**
- `internal/tui/` migrated (all Bubble Tea panels)
- `internal/tuisetup/` migrated
- `internal/cli/` migrated (background, vscode, learn modes)
- `internal/server/` migrated (existing endpoints only — new ones come in Phase 3.1)

**Crew notes:**  
These are straightforward import-path rewrites. The server package has the most external surface — verify all existing routes still resolve correctly.

**Success criteria:**
```bash
go build ./internal/tui/...
go build ./internal/server/...

# Start the server, hit existing endpoints
cairo serve --port 11434 --auth=false &
sleep 2
curl -s http://localhost:11434/healthz | jq .
curl -s -X POST http://localhost:11434/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"hello","session_id":"test"}' | jq .message
kill %1
```

---

### Phase 1.5 — cmd/cairo Assembly + First Binary

**Action:** Wire cmd/cairo in cairo2 — App struct, surfaces, subcommands — and produce a working `cairo` binary.

**Resolves:**
- `cmd/cairo/app.go` — App struct + newApp()
- `cmd/cairo/surfaces.go` — runTUI, runCLI, runVSCode, runOneShot
- `cmd/cairo/main.go` — ~80 lines: signal handling + dispatch
- `cmd/cairo/cmd_serve.go`, `cmd_learn.go`, `cmd_dream.go`, `cmd_config.go`
- `cmd/cairo/cmd_export.go`, `cmd_import.go`, `cmd_diff.go`, `cmd_task.go`, `cmd_token.go`
- `cmd/cairo/wizard.go`

**Crew notes:**  
This is the composition root — it wires all packages together. The research agent should map the exact dependency graph from the current `main.go` init sequence before the implementation agent touches a line. The App struct pattern replaces the linear init.

**Success criteria:**
```bash
make build   # produces bin/cairo

bin/cairo --version
bin/cairo --help
bin/cairo "what is 2 + 2"           # one-shot: agent responds
bin/cairo config get llm_model       # config subcommand
bin/cairo learn ./docs               # learn subcommand
bin/cairo export > /tmp/bundle.zip   # export
```

---

### Phase 1.6 — Build Artifacts (all three binaries + packaging)

**Action:** `make build` produces all three binaries. `make package` produces RPM and DEB for each.

**Resolves:**
- `scripts/build.sh` (all three binaries)
- `scripts/packaging/build-packages.sh` (RPM + DEB for cairo, cairo-registry, cairo-ctl)
- `scripts/packaging/pre-package.sh`
- `cairo.service` systemd unit
- `cairo-registry.service` systemd unit
- macOS `.pkg` build (cairo)
- `scripts/install.sh`

**Crew notes:**  
The packaging scripts from `~/cairo/scripts/packaging/` are the reference — adapt them for three binaries instead of one.

**Success criteria:**
```bash
make build
ls -lh bin/cairo bin/cairo-registry bin/cairo-ctl

make package
ls dist/
# cairo-*.rpm, cairo-*.deb
# cairo-registry-*.rpm, cairo-registry-*.deb
# cairo-ctl-*.rpm, cairo-ctl-*.deb

# Install and verify
sudo rpm -i dist/cairo-*.rpm   # or dpkg on Debian
cairo --version
cairo-registry --version
cairo-ctl --version
```

> **Milestone 1 demo checkpoint:** `cairo "hello"` works. `cairo serve` starts and accepts chat. All three binaries build and install. Request Scot TUI verification.

---

## Milestone 2 — Fleet Integration

> Merge cairo-registry into cairo2. One repo, one module, zero protocol drift. `cairo serve --tsnet` registers a node. `cairo-ctl list` shows it. The control plane is live.

---

### Phase 2.1 — Registry Merge

**Action:** Move `~/cairo-registry` source into cairo2 — cmd/, internal/, and scripts. Update all import paths.

**Resolves:**
- `cmd/cairo-registry/` merged from `~/cairo-registry/cmd/cairo-registry/`
- `cmd/cairo-ctl/` merged from `~/cairo-registry/cmd/cairo-ctl/`
- `internal/registryserver/` merged from `~/cairo-registry/internal/`
- `internal/store/registrations/` merged from registry ledger
- `scripts/packaging/` gains cairo-registry + cairo-ctl package specs

**Crew notes:**  
Follow `docs/architecture/research/registry-merge-plan.md` exactly — the four-step plan is already written. The research agent should verify the plan is still accurate against current code before the implementation agent runs.

**Success criteria:**
```bash
make build   # all three binaries still build

cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
sleep 1
curl -s http://localhost:8080/health | jq .
curl -s http://localhost:8080/admin/agents \
  -H 'X-Operator-Identity: local' | jq .
kill %1
```

---

### Phase 2.2 — Protocol Consolidation

**Action:** Eliminate the copy-paste protocol types. One definition of `RegisterRequest`, `Frame`, `HeartbeatPayload` in `internal/protocol/`. Delete the duplicate.

**Resolves:**
- `internal/protocol/` is the single source of truth
- Duplicate in old registry location deleted
- `grep -rn "type RegisterRequest"` returns exactly one hit

**Success criteria:**
```bash
grep -rn "type RegisterRequest" . --include="*.go"
# exactly 1 result: internal/protocol/registry.go

go build ./...
go test ./internal/protocol/...
```

---

### Phase 2.3 — Fleet Client Consolidation

**Action:** Merge `internal/registry/` and `internal/registry-client/` into one `internal/registry/` package. These were split by accident during a merge conflict.

**Resolves:**
- `internal/registry/` — Register, HeartbeatLoop, LivenessStream (consolidated)
- Old `internal/registry-client/` deleted
- All import sites updated

**Success criteria:**
```bash
# Start registry
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &

# Start cairo in serve mode (connects to registry)
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 3

# Verify registration
cairo-ctl --addr http://localhost:8080 --operator local list
# shows one agent with ws_connected: 1

cairo-ctl --addr http://localhost:8080 --operator local health
# active: 1

kill %1 %2
```

---

### Phase 2.4 — cairo-ctl Feature Completion

**Action:** Implement `revoke` and `broadcast` subcommands, and the corresponding registry endpoints.

**Resolves:**
- `cairo-ctl revoke <id>`
- `cairo-ctl broadcast <command>`
- `POST /admin/agents/:id/revoke` on registry
- `POST /admin/broadcast` on registry (commands table in ledger)

**Success criteria:**
```bash
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2

AGENT_ID=$(cairo-ctl --addr http://localhost:8080 --operator local list \
  | awk 'NR==2{print $1}')

cairo-ctl --addr http://localhost:8080 --operator local revoke $AGENT_ID
cairo-ctl --addr http://localhost:8080 --operator local list
# agent shows status: revoked

kill %1 %2
```

> **Milestone 2 demo checkpoint:** `cairo serve --tsnet` + `cairo-ctl list` end-to-end. Request Scot verification.

---

## Milestone 3 — API Completion

> Fill the HTTP gaps. Everything the web-agent reads from SQLite directly becomes an API call. The Python bridge dies.

---

### Phase 3.1 — Missing HTTP Endpoints

**Action:** Add the 14 missing endpoints to `internal/server/`. Additive only — nothing breaks.

**Resolves:**
- `GET /api/health`
- `GET /api/config/snapshot`
- `PUT /api/config/{key}`
- `GET /api/sessions`
- `GET /api/sessions/{id}`
- `PATCH /api/sessions/{id}` (rename)
- `DELETE /api/sessions/{id}`
- `GET /api/sessions/{id}/messages`
- `PATCH /api/roles/{name}`
- `PUT /api/consider/aspects/{name}`
- `PATCH /api/consider/aspects/{name}`
- `DELETE /api/consider/aspects/{name}`
- `GET /api/metrics`
- `GET /api/events` (SSE observer stream)

**Crew notes:**  
See `docs/architecture/research/server-api-surface.md` for the exact proposed shapes. Each handler is ~30–60 lines delegating to store sub-packages.

**Success criteria:**
```bash
cairo serve --port 11434 --auth=false &
sleep 2

curl -s http://localhost:11434/api/health | jq .
curl -s http://localhost:11434/api/config/snapshot | jq '.roles | keys'
curl -s http://localhost:11434/api/sessions | jq 'length'
curl -s http://localhost:11434/api/metrics | jq .

# Modify a config value
curl -s -X PUT http://localhost:11434/api/config/llm_model \
  -H 'Content-Type: application/json' \
  -d '"llama3.2"' | jq .

curl -s http://localhost:11434/api/config/snapshot | jq '.config.llm_model'
# "llama3.2"

kill %1
```

---

### Phase 3.2 — Web Agent Migration (drop Python bridge)

**Action:** Replace `web-agent/server/src/cairoDb.ts` Python bridge with a TypeScript HTTP client. Remove the `python3` runtime dependency.

**Resolves:**
- `cairoDb.ts` Python bridge replaced with HTTP client
- `python3` runtime dependency removed from packaging
- `CAIRO_HTTP_URL` config added to web-agent
- Web agent end-to-end test passes without Python installed

**Success criteria:**
```bash
# Verify python3 is not invoked
python3_path=$(which python3)
sudo mv $python3_path ${python3_path}.bak   # temporarily remove

cairo serve --port 11434 --auth=false &
npm start --prefix web-agent/server &
sleep 3

curl -s http://localhost:3000/api/sessions | jq 'length'
curl -s http://localhost:3000/api/config | jq '.roles | keys'
# Both work without python3

sudo mv ${python3_path}.bak $python3_path   # restore
kill %1 %2
```

> **Milestone 3 demo checkpoint:** Web UI loads, sessions list, config editable — all without SQLite direct reads. Request Scot web UI verification.

---

## Milestone 4 — Zero Trust Foundation

> Wire all ZT gates. Implement access control and audit log. The system now enforces who can do what, and logs everything.

---

### Phase 4.1 — ZT Stubs (wire all gates, no-op implementations)

**Action:** Wire `audit.Log()`, `access.CanAddress()`, and `authn.Verify()` at all gate call sites with no-op implementations. Every gate compiles and calls through; nothing is enforced yet.

**Resolves:**
- `internal/audit/` no-op stub — `audit.Log()` accepts calls, discards them
- `internal/access/` no-op stub — `CanAddress()` returns `(true, "stub")`
- `internal/authn/` no-op stub — `Verify()` returns identity from header
- All call sites in registryserver, server, knowledge (future) wired

**Crew notes:**  
This is the most important setup phase — every future gate implementation just replaces the stub. Getting the call sites right now means nothing is ever missing from the audit trail retroactively.

**Success criteria:**
```bash
go build ./...   # all packages build with gate calls in place

cairo serve --port 11434 --auth=false &
sleep 1

# All existing behavior unchanged — stubs are transparent
curl -s http://localhost:11434/api/health | jq .
curl -s http://localhost:11434/api/sessions | jq 'length'

kill %1
```

---

### Phase 4.2 — Access Control (departments + RBAC)

**Action:** Implement `internal/access/` — department model, role assignments, `CanAddress()`, `ListVisible()`.

**Resolves:**
- Department CRUD (create, list, assign user, assign agent)
- Role definitions (admin, developer, dept-lead, analyst, super-admin)
- `CanAddress(identity, agentID, sessionCtx) (bool, reason)`
- `ListVisible(identity) []AgentID`
- Per-session authorization (re-evaluate on every routing request)
- Super-admin bypass (always audit-logged)
- Knowledge scope access: `CanReadScope`, `CanContributeTo`
- `cairo-ctl departments` subcommand

**Success criteria:**
```bash
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2

# Create a department
cairo-ctl --addr :8080 --operator admin departments create infra

# Assign the running agent to that department
AGENT_ID=$(cairo-ctl --addr :8080 --operator admin list | awk 'NR==2{print $1}')
cairo-ctl --addr :8080 --operator admin departments assign-agent infra $AGENT_ID

# List as a non-member — should not see the dept agent
cairo-ctl --addr :8080 --operator other-user list
# empty or no infra agents

# List as dept member — should see it
cairo-ctl --addr :8080 --operator infra-member list
# shows the infra agent

kill %1 %2
```

---

### Phase 4.3 — Audit Log (real implementation)

**Action:** Replace the no-op audit stub with an append-only SQLite-backed event log. Wire every event type.

**Resolves:**
- Append-only audit log (SQLite WAL, no updates/deletes)
- All event types wired from every gate
- `cairo-ctl audit` subcommand (list, filter, export)
- Retention policy config key

**Success criteria:**
```bash
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2

# Trigger some access decisions
curl -s http://localhost:11434/api/sessions | jq 'length'
cairo-ctl --addr :8080 --operator local list

# Read the audit log
cairo-ctl --addr :8080 audit list | jq '.[0:5]'
# Shows: node_enrolled, access_granted events with timestamps

cairo-ctl --addr :8080 audit list --gate access | jq 'length'
# > 0

# Verify append-only — attempt delete should fail or be rejected
cairo-ctl --addr :8080 audit list | jq '.[0].id' | \
  xargs -I{} curl -s -X DELETE http://localhost:8080/admin/audit/{}
# 405 Method Not Allowed or 403

kill %1 %2
```

---

### Phase 4.4 — authn: tsnet Identity Extraction

**Action:** Replace the header-based identity stub with real tsnet peer certificate identity extraction. The User gate is now real.

**Resolves:**
- tsnet peer certificate → extract Tailscale user identity
- Identity claim extraction (user ID, email, node key)
- `authn.Verify()` returns real identity when running with `--tsnet`
- Graceful fallback in `--no-tsnet` mode (local identity from operator header)

**Success criteria:**
```bash
# --no-tsnet mode: identity from operator header (unchanged behavior)
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2
cairo-ctl --addr :8080 --operator local list
# works as before

# Audit log shows real identity
cairo-ctl --addr :8080 audit list | jq '.[0].actor'
# "local" (in --no-tsnet mode) or tsnet email in real mode

kill %1 %2
```

> **Milestone 4 demo checkpoint:** Create a dept, assign an agent, verify access scoping via CLI. Audit log shows events. Request Scot verification.

---

## Milestone 5 — Knowledge Federation

> Agents can query the shared knowledge base. Personal agents can ask the main agent. Knowledge flows up with approval. DDIL tiers work offline.

---

### Phase 5.1 — VectorStore Interface + Scope Labels

**Action:** Add the `VectorStore` interface to `store/index/` with scope dimension. Add classification labels to indexed chunks.

**Resolves:**
- `store/index.VectorStore` interface (Search, Upsert, Delete — all scope-aware)
- Classification scope field on `Chunk` schema (migration)
- SQLite implementation of VectorStore (existing embed_search, now scope-filtered)
- `cairo learn <path> --scope enterprise` flag (default: personal)

**Success criteria:**
```bash
# Index some docs at enterprise scope
cairo learn ./docs --scope enterprise

# Verify scope labels in DB
sqlite3 ~/.cairo/cairo.db \
  "SELECT scope, count(*) FROM chunks GROUP BY scope;"
# enterprise|N

# Search within scope
cairo "what do you know about Zero Trust?" \
  --knowledge-scope enterprise
# Returns results from enterprise-scoped chunks only
```

---

### Phase 5.2 — Qdrant Connector

**Action:** Implement `internal/connectors/qdrant/` as a `VectorStore` implementation with per-scope collection isolation.

**Resolves:**
- Qdrant HTTP client
- `connectors/qdrant/` implements `store/index.VectorStore`
- Per-scope Qdrant collection naming (`cairo-enterprise`, `cairo-dept-infra`, etc.)
- Config keys: `qdrant_url` in `store/config/`
- `cairo learn` writes to Qdrant when `qdrant_url` is configured

**Success criteria:**
```bash
# Start Qdrant (docker or binary)
docker run -d -p 6333:6333 qdrant/qdrant

# Configure cairo to use Qdrant
cairo config set qdrant_url http://localhost:6333

# Index and verify in Qdrant
cairo learn ./docs --scope enterprise
curl -s http://localhost:6333/collections | jq '.result.collections[].name'
# "cairo-enterprise"

# Search still works
cairo "what do you know about the registry?"
# Returns results (now from Qdrant, not SQLite)
```

---

### Phase 5.3 — Knowledge Federation L1→L2

**Action:** Implement `internal/knowledge/federation.go` with DDIL tier fallback: local SQLite → Qdrant → skip.

**Resolves:**
- `knowledge/scopes.go` — scope definitions and flow rules
- `knowledge/federation.go` — tier routing (L1 → L2)
- `knowledge/sync.go` — pull-on-demand caching with TTL
- Agent queries knowledge federation layer before tool/internet calls

**Success criteria:**
```bash
# L1: disconnected — local SQLite only
cairo config set qdrant_url ""   # disable Qdrant
cairo "what do you know about Zero Trust?"
# Returns from local SQLite index

# L2: Qdrant available
cairo config set qdrant_url http://localhost:6333
cairo "what do you know about Zero Trust?"
# Returns from Qdrant (enterprise scope visible)

# Tier fallback logged
cairo-ctl audit list --gate knowledge | jq '.[0] | {tier, scope, query}'
```

---

### Phase 5.4 — Agent-to-Agent IPC (KnowledgeQuery)

**Action:** Implement `KnowledgeQuery` and `KnowledgeResponse` frame routing through the registry. Personal agents can query the main/enterprise agent.

**Resolves:**
- `internal/protocol/` — KnowledgeQuery, KnowledgeResponse frame types
- `internal/registryserver/` — route KnowledgeQuery to target agent by type
- Agent handler: receive KnowledgeQuery, synthesize from authorized scopes, return response
- Knowledge federation L3: send KnowledgeQuery via registry before going to internet

**Success criteria:**
```bash
# Start registry + enterprise agent + personal agent
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
CAIRO_AGENT_TYPE=enterprise cairo serve --port 11434 \
  --registry http://localhost:8080 --auth=false &
CAIRO_AGENT_TYPE=personal cairo serve --port 11435 \
  --registry http://localhost:8080 --auth=false &
sleep 3

# Send a knowledge query directly (via registry admin API)
curl -s -X POST http://localhost:8080/admin/knowledge/query \
  -H 'Content-Type: application/json' \
  -H 'X-Operator-Identity: local' \
  -d '{"query":"post-quantum cryptography","scopes":["enterprise"]}' \
  | jq .response

# Verify audit log captures the inter-agent query
cairo-ctl --addr :8080 audit list --gate knowledge | jq '.[0]'

kill %1 %2 %3
```

---

### Phase 5.5 — Knowledge Contribution + Approval Flow

**Action:** Implement `knowledge/contribution.go` — agents can submit findings for write-up approval.

**Resolves:**
- `KnowledgeContribution` frame type
- Contribution submission from agent
- Admin approval/rejection via `cairo-ctl knowledge contributions`
- On approval: `learn/` pipeline indexes at target scope
- Audit events: submitted, approved, rejected

**Success criteria:**
```bash
cairo-registry --no-tsnet --addr :8080 --db /tmp/reg.db &
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2

# Submit a contribution (agent contributes a finding to enterprise scope)
curl -s -X POST http://localhost:11434/api/knowledge/contribute \
  -H 'Content-Type: application/json' \
  -d '{"content":"Post-quantum crypto uses lattice-based algorithms...","to_scope":"enterprise"}' \
  | jq .contribution_id

# List pending contributions
cairo-ctl --addr :8080 knowledge contributions list
# Shows pending contribution

# Approve it
cairo-ctl --addr :8080 knowledge contributions approve <id>

# Verify it's now searchable at enterprise scope
cairo "what do you know about lattice-based algorithms?" \
  --knowledge-scope enterprise
# Returns the contributed content

kill %1 %2
```

> **Milestone 5 demo checkpoint:** Query the knowledge base, see L1/L2 tier in action, submit and approve a contribution. Request Scot CLI + web UI verification.

---

## Milestone 6 — Enterprise Services

> Document Q&A, automation tools, analytics. The AI application surfaces from the enterprise diagram come alive.

---

### Phase 6.1 — Document Q&A Surface

**Action:** Implement `internal/services/docqa/` — natural language questions answered from indexed documents with source citations.

**Resolves:**
- `services/docqa/` query interface
- Retrieval + answer synthesis with source citations
- Scope-filtered retrieval (only docs the requester can see)
- `GET /api/services/docqa` endpoint on agent HTTP API
- Optional Neo4j traversal (if configured)

**Success criteria:**
```bash
# Index a document corpus
cairo learn ./docs --scope enterprise

cairo serve --port 11434 --auth=false &
sleep 2

curl -s -X POST http://localhost:11434/api/services/docqa \
  -H 'Content-Type: application/json' \
  -d '{"question":"What is the Zero Trust gate chain?","scopes":["enterprise"]}' \
  | jq '{answer, citations}'

# Returns answer + array of source document citations
kill %1
```

---

### Phase 6.2 — Automation: N8n Tool Integration

**Action:** Implement `internal/services/automation/n8n/` — N8n workflows registered as agent tools.

**Resolves:**
- N8n HTTP client (workflow discovery + trigger)
- N8n workflows registered as tools in tool registry at startup
- Config keys: `n8n_url`, `n8n_api_key`
- Agent can invoke N8n workflows via natural language

**Success criteria:**
```bash
cairo config set n8n_url http://localhost:5678
cairo config set n8n_api_key <key>

cairo serve --port 11434 --auth=false &
sleep 2

# List available tools — should include N8n workflows
curl -s http://localhost:11434/api/tools | jq '[.[] | select(.source=="n8n")] | length'
# > 0

# Ask the agent to trigger a workflow
cairo "trigger the daily-report workflow"
# Agent confirms workflow triggered

kill %1
```

---

### Phase 6.3 — Analytics + Telemetry

**Action:** Implement `internal/services/analytics/` and `internal/telemetry/` — usage metrics, health endpoint, Prometheus exposition.

**Resolves:**
- Per-user, per-agent session/message/tool-call counts
- `GET /api/metrics` fully implemented (not stub)
- `GET /metrics` Prometheus endpoint on registry
- Structured JSON logging
- Health check aggregation

**Success criteria:**
```bash
cairo serve --port 11434 --auth=false &
sleep 2

# Send some messages to generate data
cairo "hello" ; cairo "what time is it" ; cairo "list files here"

# Check metrics
curl -s http://localhost:11434/api/metrics | jq '{sessions, messages, tool_calls}'
# Non-zero counts

curl -s http://localhost:11434/metrics | grep cairo_messages_total
# Prometheus format output

kill %1
```

> **Milestone 6 demo checkpoint:** Ask a document Q&A question via curl, see citations. Metrics show real usage numbers. Request Scot verification.

---

## Milestone 7 — cairo-ui Integration

> The Blazor enterprise UI is wired to real data. Users can select agents, chat, and see the fleet. ZT gates are enforced from browser to agent.

*(Phases 7.1–7.4 are Blazor/.NET work — tracked here for sequencing, implemented in the cairo-ui repo.)*

---

### Phase 7.1 — Cairo.Client.Registry Classlib

**Resolves:** `Cairo.Client.Registry` — typed C# client for cairo-registry HTTP API (agent list, health, department management).

**Success criteria:**
```bash
# From cairo-ui integration test
dotnet test cairo-ui/Cairo.Client.Registry.Tests \
  -- --registry-url http://localhost:8080
# All tests pass against a live registry
```

---

### Phase 7.2 — Cairo.Client.Agent Classlib

**Resolves:** `Cairo.Client.Agent` — typed C# client for agent HTTP API (chat, sessions, config, metrics).

**Success criteria:**
```bash
dotnet test cairo-ui/Cairo.Client.Agent.Tests \
  -- --agent-url http://localhost:11434
```

---

### Phase 7.3 — Agent Selector + Fleet Page

**Resolves:** Agent dropdown UI (personal / departmental / enterprise). Fleet management page (admin). Department management page.

**Success criteria:** Scot loads the enterprise UI, sees agents in the dropdown, can select one. Fleet page shows node health. (TUI/UI verification requested.)

---

### Phase 7.4 — Enterprise Chat Surface

**Resolves:** `HttpCairoClient.SendAsync` + `SendStreamAsync` + `GetMetricsAsync`. Chat works end-to-end from browser through registry to agent and back.

**Success criteria:** Scot sends a chat message from the Blazor UI, gets a streamed response from their local cairo agent via registry routing. (UI verification requested.)

> **Milestone 7 demo checkpoint:** Full enterprise demo — browser → Blazor UI → registry → local agent → streamed response. The diagram is alive.

---

## Milestone 8 — Production Hardening

> Guardrails, model management, DDIL hardening, compliance exports. Enterprise-ready.

---

### Phase 8.1 — Guardrails

**Resolves:** Input scanning (prompt injection, blocked topics), output scanning (PII detection, masking), per-role profiles, audit hooks.

**Success criteria:**
```bash
cairo config set guardrails_pii_scan true
cairo serve --port 11434 --auth=false &

# Send a message containing fake PII
curl -s -X POST http://localhost:11434/api/chat \
  -d '{"message":"My SSN is 123-45-6789, can you help?"}' \
  | jq .response
# PII is masked in response: "My SSN is [REDACTED]"

cairo-ctl --addr :8080 audit list --gate guardrails | jq '.[0].event'
# "pii_detected"

kill %1
```

---

### Phase 8.2 — Model Manager

**Resolves:** Approved model registry, per-role model selection policy, A/B serving config.

**Success criteria:**
```bash
cairo-ctl --addr :8080 models list
# Shows approved models with tier

cairo-ctl --addr :8080 models policy set developer llama3.2
cairo "hello"   # Uses llama3.2 for developer role

cairo-ctl --addr :8080 audit list --gate model | jq '.[0]'
# model_selected event logged
```

---

### Phase 8.3 — DDIL Hardening

**Resolves:** Knowledge cache warm-up on connect, staleness TTL, graceful degradation logging, offline operation smoke test.

**Success criteria:**
```bash
# Warm cache, then disconnect Qdrant
cairo serve --port 11434 --registry http://localhost:8080 --auth=false &
sleep 2
# Bring down Qdrant
docker stop qdrant

# L2 falls back to L1 gracefully
cairo "what do you know about Zero Trust?"
# Returns from local SQLite cache, no error

# Log shows tier fallback
cairo-ctl --addr :8080 audit list --gate knowledge | jq '.[0].tier'
# "L1"
```

---

### Phase 8.4 — Compliance Exports

**Resolves:** SOC 2, HIPAA, GDPR audit log export formats. Retention policy enforcement.

**Success criteria:**
```bash
cairo-ctl --addr :8080 audit export --format soc2 --since 2026-01-01 \
  > /tmp/audit-soc2.json
jq 'keys' /tmp/audit-soc2.json
# ["events", "generated_at", "period", "summary"]

cairo-ctl --addr :8080 audit export --format hipaa \
  > /tmp/audit-hipaa.json
# Valid HIPAA audit log format
```

> **Final demo checkpoint:** Full system running. Guardrails catch PII. DDIL fallback works offline. Audit log exports for compliance. Request Scot full system verification.

---

## Roadmap Notes

- **Phases are sequential within a milestone; milestones 1–3 are sequential; milestones 4–8 can partially overlap once M3 is stable.**
- **When a phase reveals unexpected complexity, split it — don't expand scope.**
- **Success criteria are the contract. If the CLI commands don't pass, the phase isn't done.**
- **The checklist (`CHECKLIST.md`) is updated as each phase completes. Items checked off there, not here.**
- **Bug fixes and regressions get inserted as unplanned phases with the same structure.**
- **"Request Scot verification" at milestone boundaries = ask Scot to load the TUI or Web UI for the user-facing demo. Everything before that is CLI-only.**
