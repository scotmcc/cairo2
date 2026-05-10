# Fleet Architecture

**cairo2 Technical Whitepaper — No. 2**  
**Status:** Draft — pre-release  
**Date:** 2026-05-10

---

## Abstract

A single cairo instance running on a developer's laptop is a self-contained AI coding
partner. It keeps its own SQLite database, runs its own agent loop, and never touches
a network unless you tell it to. That's the local-first story from Whitepaper #1. But
when a team of ten developers each runs their own cairo instance, the picture changes:
no operator can tell who is running what version, there is no mechanism to pull a
compromised node, and there is no surface for fleet-wide coordination.

This paper describes cairo's answer: a lightweight federation model built on three
binaries (cairo, cairo-registry, cairo-ctl), a two-transport wire protocol (HTTP + WebSocket),
and an admin/public port split that keeps operator traffic off the agent's normal request
path. The model gives a team centralized visibility without giving up the local-first stance
that makes individual cairo instances low-risk to deploy. Each agent's SQLite database
remains local. No user data flows to the registry. The registry knows only that an agent
exists; it does not know what the agent has said or done.

---

## 1. The Problem

### 1.1 Solo use is frictionless

`cairo serve` starts an HTTP server on port 1337. The user opens a browser or IDE extension,
sends a chat message, gets a streaming response. Everything lives on their machine. Shutdown
means Ctrl-C. Recovery means restarting the binary. No external dependency beyond the LLM
endpoint they've configured. This is the happy path, and it works.

### 1.2 Teams create a coordination gap

When a second developer joins and spins up their own cairo instance, the two instances are
completely invisible to each other. This is fine until it isn't:

- An operator needs to push a version upgrade. There is no inventory of running instances.
- A developer's machine is compromised. There is no mechanism to revoke that node's
  ability to interact with the shared LLM endpoint or re-register with downstream services.
- A platform team wants to enforce a consistent model configuration. Each instance reads
  from its own local SQLite; there is no shared config surface.
- A team lead wants to see which agents are active before a maintenance window. There is
  no central roster.

None of these problems require changing the agent binary. They require a separate
coordination layer that knows which agents exist and can send them signals. That is
exactly what cairo-registry provides.

### 1.3 The design constraint: no behavior loss

The fleet layer must be additive. A cairo instance that is not enrolled in any registry
must continue to work exactly as it does today. Registration is opt-in: `cairo serve --register`.
Nothing about solo operation changes when the registry feature ships.

---

## 2. The Three Binaries

The fleet model is delivered as three separate binaries. This was an explicit architectural
decision (D3 in `docs/architecture/decisions.md`):

> The agent and the registry have fundamentally different deployment shapes — the agent
> runs on dev boxes, the registry runs centrally. They shouldn't be one binary.

### 2.1 cairo — the agent

`cmd/cairo` is the agent process. It runs on each developer's machine. When started with
`cairo serve --register <registry-url>`, it performs the following fleet-specific behaviors
on top of its normal operation:

1. **Initial registration** — `POST /register` to the registry with hostname, version, and
   an optional stable agent_id.
2. **Heartbeat loop** — re-registers every 30 seconds so the registry can track liveness.
3. **WebSocket stream** — maintains a persistent `GET /agents/{id}/stream` connection that
   responds to server-side pings. This is the liveness signal the registry uses to distinguish
   "agent is running and healthy" from "agent registered but the process died."

All three behaviors are implemented in `internal/registry/client.go`. They run as goroutines
alongside the HTTP server and do not touch the agent loop.

### 2.2 cairo-registry — the coordination point

`cmd/cairo-registry` is the central server. It is stateless at the HTTP layer; all state lives
in a SQLite database at `~/.cairo-registry/registry.db`. It exposes two listeners:

- **Public listener** (default port 443 / Tailscale, or configurable with `--addr` when
  `--no-tsnet` is set) — accepts `POST /register`, `GET /agents`, `GET /healthz`, and
  `GET /agents/{id}/stream`. This is where agent heartbeats land.
- **Admin listener** (default `127.0.0.1:8081`, configurable with `--admin-addr`) — accepts
  the operator-scoped endpoints described in Section 4.

The registry does not proxy chat messages. It does not store sessions, memories, or any agent
output. Its entire data model is one agents table and one commands table:

```sql
CREATE TABLE IF NOT EXISTS agents (
  agent_id      TEXT PRIMARY KEY,
  hostname      TEXT NOT NULL,
  owner         TEXT NOT NULL,
  tailnet_node  TEXT NOT NULL,
  version       TEXT NOT NULL,
  registered_at INTEGER NOT NULL,
  last_seen_at  INTEGER NOT NULL,
  status        TEXT NOT NULL,       -- 'active' | 'stale' | 'revoked'
  ws_connected  INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS commands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  operator   TEXT NOT NULL,
  command    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
```

Source: `internal/registryserver/ledger.go:15–33`

The schema is applied at startup via `execSchema()` — not a numbered migration system.
Adding a table means appending a `CREATE TABLE IF NOT EXISTS` statement to the `schema`
constant. This is intentional simplicity for a service with a narrow data model.

### 2.3 cairo-ctl — the operator interface

`cmd/cairo-ctl` is the operator CLI. It talks exclusively to the registry's admin listener
(default `127.0.0.1:8081`). It has five subcommands:

| Subcommand | Admin endpoint | Purpose |
|---|---|---|
| `list` | `GET /agents` | List agents visible to `--operator` |
| `get <id>` | `GET /agents/{id}` | Inspect a single agent |
| `health` | `GET /healthz` | Registry health + counts |
| `revoke <id>` | `POST /agents/{id}/revoke` | Revoke an agent permanently |
| `broadcast <cmd>` | `POST /broadcast` | Queue a broadcast command |

Source: `cmd/cairo-ctl/main.go:88–119`

cairo-ctl accepts `--addr` with or without the `http://` scheme prefix — the binary strips it
before constructing request URLs (`cmd/cairo-ctl/main.go:84`). The admin listener is loopback-only
by default; running cairo-ctl against a remote registry requires either an SSH tunnel or a
deliberate `--admin-addr` change on the registry side.

---

## 3. Wire Protocol

### 3.1 Overview

The fleet wire protocol uses two transports:

- **HTTP** for stateless operations: register, heartbeat, revoke, broadcast
- **WebSocket** for the liveness stream: persistent per-agent connection with ping/pong keep-alive

Both transports carry JSON payloads. The shared types are defined once in `internal/protocol/registry.go`
and imported by both the client (`internal/registry/client.go`) and server
(`internal/registryserver/server.go`, `internal/registryserver/ws.go`). Before Phase 2.2
consolidated the protocol, the wire types were duplicated across the two codebases; the single
`internal/protocol/` package is the canonical source of truth.

### 3.2 Registration

```
cairo serve --register http://registry.internal

  cairo                            cairo-registry
    │                                    │
    ├── POST /register ─────────────────▶│
    │   {                                │
    │     "agent_id": "uuid-if-known",   │
    │     "hostname": "devbox-1",        │
    │     "version":  "0.4.2",           │
    │     "tailnet_node": "100.x.y.z"    │
    │   }                                │
    │                                    ├── Ledger.Register()
    │                                    │   (upsert by agent_id+owner
    │                                    │    or owner+hostname+tailnet_node)
    │◀── 200 OK ─────────────────────────┤
    │   {                                │
    │     "agent_id": "uuid",            │
    │     "registered_at": 1746900000    │
    │   }                                │
    │                                    │
```

The `RegisterRequest` type (`internal/protocol/registry.go:11–17`) carries an optional
`agent_id` field. When provided and the (agent_id, owner) pair matches an existing row,
the registry updates that row in place and returns the same agent_id. This preserves identity
across hostname changes — a developer who renames their machine does not lose their agent's
history from the registry's perspective. If the supplied agent_id does not match (or is
absent), the registry falls back to the legacy `(owner, hostname, tailnet_node)` lookup.

Owner identity is derived from the Tailscale WhoIs response when running with tsnet enabled.
In `--no-tsnet` mode, owner is always `"local"`. Source: `internal/registryserver/server.go:102–116`.

### 3.3 Heartbeat

The heartbeat is implemented as a repeat of the initial register call. `HeartbeatLoop` in
`internal/registry/client.go:53–74` fires a ticker every N seconds (default 30) and calls
`Register()` again with the same parameters. The registry treats it as an upsert: `last_seen_at`
is updated, `status` is reset to `'active'` if it had gone stale.

This design means the heartbeat carries the full registration payload every time. This is
slightly wasteful but eliminates a separate `PATCH` or `touch` endpoint on the public listener.
The server does not need to distinguish "new registration" from "heartbeat" — both are idempotent
upserts.

```
  30s ticker
      │
      ├── POST /register  (same payload as initial)
      │
      ◀── 200 OK  (same response shape, same agent_id)
```

If the registry responds with 403, `HeartbeatLoop` treats this as a permanent revocation signal,
logs `"registry: agent revoked, heartbeat loop stopping"`, and returns without rescheduling.
Source: `internal/registry/client.go:65–69`.

### 3.4 WebSocket liveness stream

The heartbeat alone is insufficient to distinguish "agent is running" from "agent crashed since
last heartbeat." A 30-second heartbeat interval means the registry might show an agent as
active for up to 29 seconds after the process died. The WebSocket stream fills this gap.

After registration, `LivenessStream` in `internal/registry/client.go:77–123` opens a persistent
WebSocket connection to `GET /agents/{id}/stream`. The server sends a `{"type":"ping"}` frame
every 10 seconds. The client responds with `{"type":"pong"}`. The server registers the agent
as `ws_connected=1` when the connection is established and `ws_connected=0` when it drops.

```
  cairo                              cairo-registry
    │                                      │
    ├── GET /agents/{id}/stream ──────────▶│  (WebSocket upgrade)
    │                                      ├── SetWsConnected(true)
    │                                      │
    │    ◀── {"type":"ping"} ─────────────┤  (every 10 seconds)
    │                                      │
    ├── {"type":"pong"} ─────────────────▶│
    │                                      ├── Touch() — bumps last_seen_at
    │                                      ├── GetStatus() — revoke check
    │                                      │
    │    ◀── {"type":"ping"} ─────────────┤
    │    ...                               │
    │                                      │
    │   (process dies)                     │
    │                                      │
    │                                 WebSocket closes
    │                                      │
    │                                      ├── SetWsConnected(false)
```

Source: `internal/registryserver/ws.go:60–104`

The pong handler does two things in sequence (ws.go:93–101):

1. **Touch**: updates `last_seen_at` in the ledger — the WS pong is authoritative for liveness
   when it's arriving, superseding the HTTP heartbeat.
2. **Revoke check**: reads the agent's current status. If it has been set to `'revoked'` since
   the stream was opened, the server calls `cancel()` on the connection context. This causes
   the ping goroutine to stop and the read loop to unblock with a context error, which closes
   the WebSocket. The agent's `LivenessStream` will see a 403 on its next reconnect attempt
   and stop permanently.

### 3.5 Revoke and broadcast (Phase 2.4)

Phase 2.4 added two operator-facing capabilities:

**Revoke** (`POST /agents/{id}/revoke` on admin port):

```
  cairo-ctl                      cairo-registry (admin:8081)
      │                                   │
      ├── POST /agents/{id}/revoke ──────▶│
      │   X-Operator-Identity: alice      │
      │                                   ├── Ledger.Revoke(agentID, "alice")
      │                                   │   UPDATE agents SET status='revoked'
      │                                   │   WHERE agent_id=? AND owner=?
      │◀── 200 {"status":"revoked"} ──────┤
```

The revoke is scoped by owner: an operator can only revoke agents they own. The status transition
is permanent — there is no un-revoke operation in the current implementation. Once revoked, the
agent_id is blocked at both the `POST /register` handler (returns 403 immediately) and the WS
pong handler (closes the connection on the next ping cycle).

**Broadcast** (`POST /broadcast` on admin port):

```
  cairo-ctl                      cairo-registry (admin:8081)
      │                                   │
      ├── POST /broadcast ───────────────▶│
      │   {"command": "reload-config"}    │
      │   X-Operator-Identity: alice      │
      │                                   ├── Ledger.InsertCommand(operator, command)
      │◀── 202 {"status":"queued"} ───────┤
```

Broadcast persists a command to the `commands` table. The current implementation is
write-only — agents do not yet poll or receive push notifications for broadcast commands.
The table exists as the foundation for a future consumer phase. Source: `internal/registryserver/admin.go:88–110`,
`internal/registryserver/ledger.go:342–349`.

---

## 4. The Admin/Public Split

The registry exposes two listeners on separate ports. This is not conventional — most services
put everything behind one port and use path or auth to segment. The split is deliberate.

### 4.1 The public listener

The public listener is the registration surface. Its routes are:

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Liveness. Returns counts: total/active/stale/ws_connected. |
| `POST` | `/register` | Agent registration and heartbeat (idempotent upsert). |
| `GET` | `/agents` | Full agent roster. No auth currently; Tailscale provides network-layer identity. |
| `GET` | `/agents/{id}/stream` | WebSocket liveness stream per agent. |

Source: `internal/registryserver/server.go:39–45`

In the default (tsnet) deployment, this listener binds to Tailscale's virtual network. Only
machines on the same Tailnet can reach it. Network-layer identity (WhoIs) provides the owner
field; no application-layer bearer token is required.

### 4.2 The admin listener

The admin listener is the operator surface. It binds to `127.0.0.1:8081` — loopback only —
and is reachable by `cairo-ctl` either locally or over an SSH tunnel:

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Same health response as public. |
| `GET` | `/agents` | Agents scoped to `X-Operator-Identity` header. |
| `GET` | `/agents/{id}` | Single agent, scoped to operator. |
| `POST` | `/agents/{id}/revoke` | Revoke. Requires operator header. |
| `POST` | `/broadcast` | Enqueue broadcast command. Requires operator header. |

Source: `internal/registryserver/admin.go:13–21`

Identity on the admin port comes from the `X-Operator-Identity` request header. If the header
is absent, `GET /agents` returns an empty list and `POST /agents/{id}/revoke` returns 400. The
admin port trusts the operator identity header without cryptographic verification — the loopback
binding is the security boundary. This is explicitly a "simple enough for now" stance appropriate
for a system where the registry runs on a machine the operator controls.

### 4.3 Why the split?

The separation solves three problems:

1. **Blast radius.** Operator actions (revoke, broadcast) cannot be sent by any machine that
   can reach the public listener. Even if a compromised agent can POST to `/register`, it
   cannot POST to `/agents/{id}/revoke` because that path exists only on the loopback listener.

2. **Independent lifecycle.** The admin listener can be disabled entirely with
   `--admin-addr ""`. In read-only or locked-down deployments, removing the admin surface
   is a single flag rather than a code change.

3. **Operational clarity.** Operators know which port they target. `cairo-ctl --addr 127.0.0.1:8081`
   is always talking to the admin plane. There is no risk of accidentally issuing an admin command
   to the public plane.

Source: `cmd/cairo-registry/main.go` shows both listeners being constructed independently and
served from separate goroutines.

---

## 5. Agent Lifecycle

The full lifecycle of a fleet-enrolled agent from startup to revocation:

```
  cairo serve --register http://registry.internal:8080
       │
  [1]  ├── Register()          POST /register
       │                       └── Ledger.Register() → agent_id assigned
       │                           status = 'active'
       │
  [2]  ├── HeartbeatLoop()     (background goroutine)
       │   30s ticker ──────── POST /register
       │                       └── last_seen_at updated, status reset to 'active'
       │
  [3]  ├── LivenessStream()    (background goroutine)
       │   WebSocket open ──── GET /agents/{id}/stream
       │                       └── ws_connected = 1
       │   ping/pong every 10s
       │                       └── Touch() on each pong
       │                       └── GetStatus() on each pong → revoke check
       │
  [4]  │   (agent runs normally — chat, tools, sessions)
       │
  [5]  │   [operator: cairo-ctl revoke <id>]
       │                       └── Ledger.Revoke() → status = 'revoked'
       │
  [6]  │   (next pong from agent)
       │                       └── GetStatus() returns 'revoked'
       │                       └── cancel() closes WS connection
       │
  [7]  ├── LivenessStream sees close → reconnects → GET /agents/{id}/stream
       │                       └── server Touch() fails (agent_id exists but...)
       │                           actually server checks status=revoked on
       │                           WebSocket accept? No — the 403 happens on
       │                           the next POST /register (heartbeat).
       │
  [8]  ├── HeartbeatLoop fires
       │   POST /register ─────────────────────────────────────────────▶
       │                       └── Ledger.Register() sees status='revoked'
       │                           returns ErrRevoked
       │                           server returns HTTP 403
       │◀── 403 Forbidden ─────────────────────────────────────────────
       │
  [9]  └── HeartbeatLoop: errors.Is(err, ErrRevoked) → return (loop stops)
           LivenessStream: next reconnect dial → HTTP 403 on WS upgrade
           (coder/websocket returns the HTTP response; client checks StatusForbidden)
           → log "agent revoked, liveness stream stopping" → return
```

Sources:
- `internal/registry/client.go:56–73` — heartbeat 403 handling
- `internal/registry/client.go:84–94` — LivenessStream 403 on dial
- `internal/registryserver/ws.go:93–101` — pong revoke check
- `internal/registryserver/ledger.go:86–98` — Register() revoked path

### 5.1 Stale vs. revoked

The sweeper (`internal/registryserver/sweeper.go`) runs on a 30-second ticker and calls
`Ledger.Sweep()`. Sweep marks agents `'stale'` when their `last_seen_at` is older than 90
seconds AND they have no active WebSocket connection (`ws_connected = 0`). A stale agent
that re-registers is automatically returned to `'active'` — stale is a temporary condition
reflecting missed heartbeats, not an administrative action.

Revoked is permanent and scoped to owner. A revoked agent cannot re-register. The distinction
matters operationally: `'stale'` means "I haven't heard from this agent recently" and
`'revoked'` means "this agent is not permitted to run."

Source: `internal/registryserver/ledger.go:163–175`

---

## 6. Tailscale Integration

### 6.1 cairo-registry with tsnet

By default, cairo-registry runs as a Tailscale node via `tsnet`. The `NewTsnetListener`
function in `internal/registryserver/tsnet.go` bootstraps a tsnet.Server named
`"cairo-registry"`, obtains a TLS listener from the Tailscale network, and polls for the
login URL until auth completes. The cleanup function returned by `NewTsnetListener` closes
the tsnet server on shutdown.

Running under tsnet means:

- The public listener is exposed only within the Tailnet. There is no open internet port.
- Owner identity is derived from `LocalClient.WhoIs()` — Tailscale cryptographically attests
  who owns each machine, so the `owner` field in the agents table is reliable.
- TLS is provided by Tailscale's certificate authority. No manual certificate management.

For local development, `--no-tsnet` binds to plain TCP (default `:443`). In that mode, owner
is always `"local"` because there is no Tailscale identity to query.

Source: `internal/registryserver/tsnet.go`, `cmd/cairo-registry/main.go:57–72`

### 6.2 cairo serve with tsnet

`cairo serve --tsnet` binds the agent's own HTTP server to a Tailscale node instead of
`localhost:1337`. The `NewTsnetListener` in `internal/server/tsnet.go` creates a tsnet node
named `"cairo-<hostname>"` (hostname sanitized to lowercase alphanumeric, max 63 chars).

Binding to tsnet means:

- The agent's `/api/chat`, `/v1/chat/completions`, and all other endpoints are accessible
  from any machine in the Tailnet without opening a port on the local firewall.
- The operator's machine, CI runners, and web-agent server can all reach the agent's HTTP
  surface without static IP or VPN configuration.
- Auth (`--auth`) is still recommended when using tsnet — Tailscale provides network
  authentication but not application-layer bearer token checks.

### 6.3 Why tsnet over a public server

The alternative to tsnet is `cairo serve` on a public port with `--auth`. This works but
requires managing a static IP, a firewall rule, and a token secret. For a developer's laptop,
this is often impossible (dynamic IP) or inconvenient (firewall changes).

Tailscale provides:
- Persistent IP per node (100.x.y.z range) regardless of network location
- WireGuard encryption between nodes
- Identity via node key (for inter-node trust) and user profile (for WhoIs)
- No public port exposure

The tsnet model is the preferred deployment for both the registry and individual agents in
any environment where the team already uses Tailscale. The `--no-tsnet` mode exists for
local development and for deployments where Tailscale is not available.

---

## 7. The HTTP API Surface

Each `cairo serve` node exposes a full HTTP API. This section describes that surface in the
context of the fleet — what consumers can query and how the API relates to the registry.

### 7.1 What the API exposes

The cairo serve HTTP server is defined in `internal/server/server.go`. Routes fall into five
groups:

**Health (no auth):**

| Method | Path | Description |
|---|---|---|
| `GET` | `/healthz` | Returns `{"status":"ok"}`. Never requires auth. |
| `GET` | `/api/health` | Returns version, uptime_seconds, db_path. Also no auth. |

**Chat (Phase 1):**

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/chat` | Native chat: `{message, stream, context[]}`. Sync or SSE. |
| `GET` | `/v1/models` | OpenAI models list. Static single-entry `"cairo"` response. |
| `POST` | `/v1/chat/completions` | OpenAI-compatible: sync or `stream:true` SSE. |
| `POST` | `/rpc` | JSON-RPC 2.0: `cairo.send`, `cairo.send.stream`, `cairo.status`, `cairo.slash`. |
| `GET` | `/rpc/stream/{id}` | SSE consumer for decoupled RPC streams. |

**Read (Phase 3.1):**

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/config/snapshot` | Config keys + roles + consider-aspects in one response. |
| `GET` | `/api/sessions` | List sessions with last-message preview. |
| `GET` | `/api/sessions/{id}` | Single session detail. |
| `GET` | `/api/sessions/{id}/messages` | Paginated messages (limit, before params; max 200/page). |
| `GET` | `/api/metrics` | Session count, memory count, job count. |
| `GET` | `/api/events` | SSE stream of agent bus events (EventTokens, EventAgentEnd, etc.). |

**Mutation (Phase 3.1):**

| Method | Path | Description |
|---|---|---|
| `PUT` | `/api/config/{key}` | Write a config key as raw JSON value. |
| `PATCH` | `/api/sessions/{id}` | Rename a session. |
| `DELETE` | `/api/sessions/{id}` | Delete a session and its messages. |
| `PATCH` | `/api/roles/{name}` | Update role's model or think fields. |
| `PUT` | `/api/consider/aspects/{name}` | Upsert a consider-aspect. |
| `PATCH` | `/api/consider/aspects/{name}` | Update an existing aspect. |
| `DELETE` | `/api/consider/aspects/{name}` | Delete an aspect. |

Source: `internal/server/server.go:50–83`

### 7.2 The SessionBridge serialization contract

Every chat request reaching a cairo serve node goes through `SessionBridge`. The bridge
enforces that the underlying agent loop runs at most one turn at a time. Concurrent HTTP
requests queue in a buffered channel (capacity 32) and wait for the worker goroutine to
process them in order.

```
  HTTP goroutines                  SessionBridge                  agent.Prompt()
  ─────────────                    ─────────────                  ──────────────
  POST /api/chat ────────▶ queue (cap 32) ──▶ worker goroutine ──▶ Prompt()
  POST /v1/chat  ────────▶                                         EventAgentEnd
  POST /rpc      ────────▶                    ◀── replyCh ─────────
```

This is the key reason a cairo node is not a multi-tenant service. One user, one agent
loop, one session. Concurrent requests do not interleave — they queue. A slow turn blocks
all subsequent requests. The design is correct for the target use case: one developer using
one cairo instance.

Source: `docs/architecture/server.md:36–55`

### 7.3 The web-agent HTTP client

The `web-agent/` directory contains a Node.js server and React frontend that provide a
browser-accessible UI for cairo. As of Phase 3.1/3.2, the web-agent communicates with
cairo via the HTTP API documented above — it does not require a Python bridge or any
in-process embedding of the cairo binary.

The `CAIRO_CLI_PATH` environment variable points the web-agent at the correct cairo binary.
In development this should be set to `$(pwd)/bin/cairo` rather than the system `cairo` to
avoid hitting the legacy binary.

### 7.4 Auth on the agent's HTTP surface

`cairo serve --auth` enables bearer token authentication. All routes except `/healthz` and
`/api/health` require an `Authorization: Bearer <token>` header. The token is stored in the
local SQLite database under config key `server_token`. `cairo token` generates a fresh token
and stores it. The middleware uses `crypto/subtle.ConstantTimeCompare` to prevent timing
attacks.

Source: `internal/server/server.go:102–117`

---

## 8. The Fleet Diagram

```
                          ╔═══════════════════════════════╗
                          ║        cairo-registry         ║
                          ║                               ║
                          ║  Public (Tailscale / :8080)   ║
                          ║  ┌─────────────────────────┐  ║
                          ║  │ POST /register          │  ║
                          ║  │ GET  /agents/{id}/stream│  ║
                          ║  │ GET  /healthz           │  ║
                          ║  └─────────────────────────┘  ║
                          ║                               ║
                          ║  Admin (127.0.0.1:8081)       ║
                          ║  ┌─────────────────────────┐  ║
                          ║  │ GET  /agents            │  ║
                          ║  │ POST /agents/{id}/revoke│  ║
                          ║  │ POST /broadcast         │  ║
                          ║  └─────────────────────────┘  ║
                          ║                               ║
                          ║  Ledger (registry.db)         ║
                          ║  ┌──────────────────────────┐ ║
                          ║  │ agents  │ commands       │ ║
                          ║  └──────────────────────────┘ ║
                          ╚════════════╤══════════════════╝
                                       │
           ┌───────────────────────────┼───────────────────────────┐
           │                           │                           │
           ▼                           ▼                           ▼
  ╔═══════════════════╗       ╔════════════════╗        ╔════════════════════╗
  ║  cairo serve      ║       ║  cairo serve   ║        ║  cairo serve       ║
  ║  (alice's box)    ║       ║  (bob's box)   ║        ║  (shared server)   ║
  ║                   ║       ║                ║        ║                    ║
  ║  :1337 HTTP API   ║       ║  :1337 HTTP API║        ║  :1337 HTTP API    ║
  ║  /api/chat        ║       ║  /api/chat     ║        ║  /api/chat         ║
  ║  /api/sessions    ║       ║  /api/sessions ║        ║  /api/sessions     ║
  ║  /api/metrics     ║       ║  /api/metrics  ║        ║  /api/metrics      ║
  ║  /api/events      ║       ║  /api/events   ║        ║  /api/events       ║
  ║                   ║       ║                ║        ║                    ║
  ║  agent.db         ║       ║  agent.db      ║        ║  agent.db          ║
  ║  (local SQLite)   ║       ║  (local SQLite)║        ║  (local SQLite)    ║
  ╚═══════╤═══════════╝       ╚═══════╤════════╝        ╚══════════╤═════════╝
          │  register+heartbeat       │                            │
          │  (HTTP)                   │                            │
          │  WS liveness stream       │                            │
          └───────────────────────────┴────────────────────────────┘
                    (all three connect to registry public port)


  Operator CLI (admin's laptop):
  ╔═══════════════════╗
  ║  cairo-ctl        ║
  ║  --addr :8081     ║  ──SSH tunnel or loopback──▶  registry admin port
  ╚═══════════════════╝


  Browser / IDE consumers (connect directly to agent nodes):
  ╔═══════════════════╗
  ║  web-agent        ║  ──HTTP──▶  alice's cairo serve :1337
  ║  (React + Node)   ║  ──HTTP──▶  bob's cairo serve :1337
  ╚═══════════════════╝

  ╔═══════════════════╗
  ║  VS Code extension║  ──HTTP──▶  cairo serve :1337 (local or tsnet)
  ╚═══════════════════╝
```

**Reading the diagram:**

- The registry is the single coordination point. It does not forward chat traffic.
- Each cairo serve node connects outward to the registry. The registry does not connect
  inward to agents.
- Browser/IDE consumers connect directly to individual agent nodes over the HTTP API.
  The registry is not in the chat request path.
- cairo-ctl is an operator tool. It talks only to the admin port. Normal users never
  interact with it.

---

## 9. What This Is NOT

The fleet model is sometimes confused with patterns from adjacent domains. These
clarifications matter for deployment decisions.

### 9.1 Not a load balancer

Cairo does not route chat requests across a pool of agent nodes. There is no request
distribution, health-check-based routing, or failover. Each user connects to a specific
cairo node — typically the one running on their own machine or a dedicated shared server.
The registry tracks which nodes exist; it does not route traffic between them.

If a node goes down, the requests that were queued inside its SessionBridge are lost.
Recovery means restarting the agent process. The agent will re-register on startup and
pick up from the same SQLite state (last session, memories, etc.).

### 9.2 Not a sandbox

`cairo serve` runs on the developer's machine with that developer's file system permissions.
When the agent uses the `read_file` or `edit_file` tools, it reads and writes real files.
There is no container boundary, no chroot, no capability restriction. This is by design:
the agent's usefulness comes from its ability to act on the local environment.

Teams with security requirements should consider:
- `cairo serve --auth` to require a bearer token for all HTTP access
- `cairo serve --tsnet` to restrict network access to the Tailnet
- Running the agent process as a least-privilege user (not root)

The registry does not provide isolation between agents. Revoke prevents an agent from
re-registering; it does not kill the process or remove file system access.

### 9.3 Not multi-tenant SaaS

Each cairo node serves one user and one agent loop. There is no concept of routing
different users' chat messages to different personas or session branches within a single
node. The fleet is federated: N independent single-user agents coordinated by a single
registry, not a pooled service with shared state.

The architecture decisions document (D6) describes a future classification of agent types
(personal, departmental, enterprise) with RBAC-controlled visibility. Even in that model,
each agent's SQLite database remains local and private — the access controls govern which
agents the registry exposes to which operators, not data co-mingling between agents.

### 9.4 Not a message broker

The `commands` table in the registry's ledger is the embryo of a broadcast capability, not
a message broker. Commands written by `POST /broadcast` are persisted; they are not
distributed or consumed by agents in real time. The consumer side (agents polling for
commands, or the registry pushing over the liveness stream) is a future phase. For now,
broadcast is a write-only queue.

---

## 10. Deployment Guide

### 10.1 Minimal two-node setup (developer + registry, with Tailscale)

**Prerequisites:** Both machines enrolled in the same Tailnet. Go 1.22+ installed.

**Step 1: Start the registry on a server node**

```sh
# Build
cd ~/cairo2 && bash scripts/build.sh

# First run — prompts for Tailscale login
./bin/cairo-registry
# Logs:
#   Visit https://login.tailscale.com/a/... to authenticate cairo-registry
#   cairo-registry listening on Tailscale (cairo-registry.<tailnet>.ts.net:443)
#   cairo-registry admin listening on 127.0.0.1:8081
```

The registry binds to Tailscale automatically. Its Tailscale IP (100.x.y.z) is stable.

**Step 2: Start an agent and enroll it**

```sh
# On the developer's machine
./bin/cairo serve --register https://cairo-registry.<tailnet>.ts.net
# Logs:
#   registry: registered agent_id=<uuid>
#   registry: heartbeat loop started
#   registry: liveness stream opened
```

The `--register` flag accepts the full URL. cairo-ctl's URL leniency (stripping `http://`
prefix) applies only to cairo-ctl, not to cairo serve — provide the full URL here.

**Step 3: Verify from cairo-ctl**

```sh
# On the registry node (loopback access to admin port)
./bin/cairo-ctl list --operator <your-tailscale-login>
# Output:
#   AGENT_ID   HOSTNAME    OWNER                STATUS   LAST_SEEN
#   <uuid>     devbox-1    alice@example.com     active   12s ago
```

**Step 4: Revoke an agent**

```sh
./bin/cairo-ctl revoke <agent-id> --operator alice@example.com
# Output: revoked <agent-id>

# Within one heartbeat interval (≤30s), the agent logs:
#   registry: agent revoked, heartbeat loop stopping
# Within one ping interval (≤10s), the agent logs:
#   registry: agent revoked, liveness stream stopping
```

### 10.2 Team setup

A realistic team deployment:

```
Infrastructure:
  - One linux server running cairo-registry (Tailscale node)
  - N developer machines, each running cairo serve --register

Operator workflow:
  - Platform engineer has SSH access to the registry server
  - SSH in, run cairo-ctl from the loopback admin port
  - Or: configure --admin-addr to a non-loopback address and secure with a firewall rule

Per-developer workflow:
  - cairo serve --register https://<registry>  (added to shell profile or systemd user unit)
  - Use cairo normally — the enrollment is transparent
  - If revoked, cairo logs the event and stops heartbeating; operator restarts or re-enrolls
```

### 10.3 Development mode (no Tailscale)

For local development without a Tailscale setup:

```sh
# Terminal 1: registry in plain TCP mode
./bin/cairo-registry --no-tsnet --addr :8080 --admin-addr 127.0.0.1:8081

# Terminal 2: agent, registering to localhost registry
./bin/cairo serve --register http://localhost:8080

# Terminal 3: operator CLI
./bin/cairo-ctl list --operator local
```

In `--no-tsnet` mode, owner is always `"local"` because there is no Tailscale WhoIs to call.
The `--operator local` flag on cairo-ctl passes the matching identity header, so list/get/revoke
all work correctly.

### 10.4 Flags reference

**cairo-registry:**

| Flag | Default | Description |
|---|---|---|
| `--state-dir` | `~/.cairo-registry` | Directory for registry.db and tsnet state |
| `--addr` | `:443` | Public listener address (--no-tsnet only) |
| `--admin-addr` | `127.0.0.1:8081` | Admin listener; `""` disables it |
| `--no-tsnet` | false | Use plain TCP instead of Tailscale |

**cairo serve (fleet-relevant flags):**

| Flag | Description |
|---|---|
| `--register <url>` | Registry URL. Enables heartbeat + liveness stream. |
| `--tsnet` | Bind to Tailscale instead of localhost. |
| `--auth` | Require bearer token for all non-health routes. |
| `--port N` | Override the default port (1337). |

**cairo-ctl:**

| Flag | Default | Description |
|---|---|
| `--addr` | `127.0.0.1:8081` | Admin listener address |
| `--operator` | (empty) | `X-Operator-Identity` header value |

---

## 11. Current Limitations and Planned Work

### 11.1 Broadcast is write-only

`POST /broadcast` persists a command. No mechanism yet exists for agents to receive it.
The consumer side — whether polling, push over the liveness stream, or a separate channel —
is a future phase. The `commands` table has no index; it is fine for current insert-only use
but will need one before a query-heavy consumer phase lands.

### 11.2 Admin port relies on loopback security

The `X-Operator-Identity` header is not cryptographically verified. Any process that can
reach `127.0.0.1:8081` can claim any operator identity. This is acceptable when the admin
port is genuinely loopback-only; it becomes a risk if `--admin-addr` is widened to a
non-loopback address without additional controls (mTLS, bearer token, firewall rule).

### 11.3 No un-revoke

Revocation is permanent. An operator who revokes an agent by mistake must manually update
the SQLite database (`UPDATE agents SET status='active' WHERE agent_id=?`). An un-revoke
subcommand is a small addition but has not been implemented.

### 11.4 Stale WebSocket reconnect after revoke

When a revoked agent's liveness stream is closed by the server (via `cancel()` in the pong
handler), the client's `LivenessStream` function reconnects. The 403 response is only detected
at dial time, not at stream establishment. This means a brief reconnect attempt occurs before
the 403 is seen and the stream stops. The behavior is correct but produces a spurious reconnect
log line.

### 11.5 No agent-to-agent routing

The registry knows all enrolled agents. A future phase could add routing: the registry accepts
a chat message addressed to a specific agent (by agent_id or owner) and proxies it to that
agent's HTTP API. This would enable inter-agent orchestration without each agent needing to
know the other's address. This capability does not exist yet and is a Milestone 3+ concern.

---

## Appendix A — Wire Types

### RegisterRequest

```go
type RegisterRequest struct {
    AgentID     string `json:"agent_id,omitempty"`
    Hostname    string `json:"hostname"`
    Version     string `json:"version"`
    TailnetNode string `json:"tailnet_node"`
}
```

Source: `internal/protocol/registry.go:11–17`

### RegisterResponse

```go
type RegisterResponse struct {
    AgentID      string `json:"agent_id"`
    RegisteredAt int64  `json:"registered_at"`
}
```

Source: `internal/protocol/registry.go:20–23`

### Frame (WebSocket)

```go
type Frame struct {
    Type string          `json:"type"`           // "ping" | "pong"
    Body json.RawMessage `json:"body,omitempty"` // empty in current phase
}
```

Source: `internal/protocol/registry.go:26–29`

---

## Appendix B — Ledger Schema

```sql
CREATE TABLE IF NOT EXISTS agents (
  agent_id      TEXT PRIMARY KEY,
  hostname      TEXT NOT NULL,
  owner         TEXT NOT NULL,      -- Tailscale LoginName or "local"
  tailnet_node  TEXT NOT NULL,      -- Tailscale IP of the agent
  version       TEXT NOT NULL,      -- cairo binary version string
  registered_at INTEGER NOT NULL,   -- Unix timestamp, set on first registration
  last_seen_at  INTEGER NOT NULL,   -- Updated on heartbeat and WS pong
  status        TEXT NOT NULL,      -- 'active' | 'stale' | 'revoked'
  ws_connected  INTEGER NOT NULL DEFAULT 0  -- 1 while WS stream is open
);

CREATE INDEX IF NOT EXISTS idx_agents_owner_host ON agents(owner, hostname);

CREATE TABLE IF NOT EXISTS commands (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  operator   TEXT NOT NULL,   -- X-Operator-Identity at time of broadcast
  command    TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
```

Source: `internal/registryserver/ledger.go:15–33`

---

## Appendix C — Status State Machine

```
                ┌──────────────────────────────────────────────────┐
                │                                                  │
                ▼                                                  │
           ┌─────────┐   heartbeat or         ┌────────┐          │
  initial  │ active  │───re-register ─────────▶ active │          │
  insert   └────┬────┘                         └───┬───┘          │
                │                                  │              │
                │  no heartbeat AND                │  no heartbeat│
                │  ws_connected=0                  │  AND         │
                │  for >90s                        │  ws=0, >90s  │
                ▼                                  ▼              │
           ┌─────────┐                        ┌────────┐          │
           │  stale  │                        │ stale  │          │
           └────┬────┘                        └───┬────┘          │
                │                                 │               │
                │  heartbeat / re-register         │               │
                └────────────────────────────────▶  (back to active)
                │
                │  cairo-ctl revoke
                ▼
           ┌──────────┐
           │ revoked  │  (permanent — no automatic transition out)
           └──────────┘
```

Transitions are implemented in `Ledger.Register()` (active re-entry after stale),
`Ledger.Sweep()` (active → stale), and `Ledger.Revoke()` (→ revoked). There is no
code path that transitions out of `'revoked'`.

---

*End of Whitepaper #2 — Fleet Architecture*
