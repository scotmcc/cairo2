# Server API surface

## 1. Cairo's current HTTP endpoints (`internal/server/`)

Routes registered in `internal/server/server.go`:

| Method | Path | File | Handler | Notes |
|---|---|---|---|---|
| GET  | `/healthz`              | `server.go:49`  | `handleHealthz` | No auth. Returns `{"status":"ok"}`. |
| POST | `/api/chat`             | `server.go:52`  | `chat.go:43 handleChat` | Auth-wrapped. Body: `{message, context, stream, messages}`. Returns `{response, session_id, turn_id}`. SSE when `stream=true`. |
| GET  | `/v1/models`            | `server.go:55`  | `openai.go handleModels` | OpenAI-compat list. |
| POST | `/v1/chat/completions`  | `server.go:56`  | `openai.go handleCompletions` | OpenAI-compat. |
| POST | `/rpc`                  | `server.go:59`  | `rpc.go:90 handleRPC` | JSON-RPC 2.0. Methods: `cairo.send`, `cairo.send.stream`, `cairo.status`, `cairo.slash`. |
| GET  | `/rpc/stream/{id}`      | `server.go:60`  | `rpc.go:191 handleRPCStream` | SSE stream paired with a prior `cairo.send.stream` call. |

Also: `/api/events` (SSE) is mentioned in the briefing but **not in the current routing table** — that endpoint is on the **web-agent** Node server (`/api/sessions/:id/events` in `createApp.ts:101`), not on cairo's Go server. Cairo's only streaming surfaces today are `POST /api/chat` with `stream=true` and `/rpc/stream/{id}`.

### `/rpc cairo.slash` subcommands (`rpc.go:279 execSlash`)

`/help`, `/session`, `/sessions`, `/jobs`, `/memories`, `/tools`, `/skills`, `/init`. These return rendered text; not structured JSON. **This is the only path the .NET cairo-ui currently has into cairo state.** (See `cairo-ui/src/Lib/Cairo/HttpCairoClient.cs` — currently a stub; intended to use `/rpc cairo.send` and `cairo.send.stream`.)

## 2. What `cairoDb.ts` reads directly from SQLite

`web-agent/server/src/cairoDb.ts` is a TypeScript file that spawns a Python subprocess (`PYTHON_BRIDGE` const) that opens `~/.cairo/cairo.db` directly with `sqlite3`. The actions handled:

| Action | What it queries | Tables touched |
|---|---|---|
| `snapshot` | All config keys + roles + consider_aspects | `config`, `roles`, `consider_aspects` |
| `list_sessions` | Sessions list with derived "insight" (last user message preview) | `sessions`, `messages` |
| `rename_session` | UPDATE name | `sessions` |
| `delete_session` | DELETE by id | `sessions` (cascade) |
| `set_config` | UPSERT key/value | `config` |
| `set_role` | UPSERT role.model or role.think | `roles` |
| `upsert_aspect` | INSERT/UPDATE consider aspect | `consider_aspects` |
| `delete_aspect` | DELETE by name | `consider_aspects` |
| `set_aspect_enabled` | UPDATE enabled flag | `consider_aspects` |

It also does runtime schema introspection (`PRAGMA table_info(...)`, `sqlite_master`) to handle older DBs. That introspection is a direct symptom of "schema-coupled with no contract".

## 3. Proposed HTTP endpoints (one-for-one replacement)

| Web-agent direct read | Replace with | Method | Response |
|---|---|---|---|
| `snapshot` | `GET /api/config/snapshot` | GET | `{config: {key:value...}, roles: [{name, description, model, think, basePromptKey, tools}], considerAspects: [{name, traits, enabled, position}]}` |
| `list_sessions` | `GET /api/sessions` | GET | `{sessions: [{id, name, insight, role, cwd, lastActive}]}` |
| `rename_session` | `PATCH /api/sessions/{id}` | PATCH | body `{name}`, returns updated session |
| `delete_session` | `DELETE /api/sessions/{id}` | DELETE | 204 |
| `set_config` | `PUT /api/config/{key}` | PUT | body `{value}`, returns updated config |
| `set_role` | `PATCH /api/roles/{name}` | PATCH | body `{field: "model"\|"think", value}` |
| `upsert_aspect` | `PUT /api/consider/aspects/{name}` | PUT | body `{traits, enabled, position?}` |
| `delete_aspect` | `DELETE /api/consider/aspects/{name}` | DELETE | 204 |
| `set_aspect_enabled` | `PATCH /api/consider/aspects/{name}` | PATCH | body `{enabled}` |

Plus the surfaces the briefing flagged as missing:

| Path | Purpose |
|---|---|
| `GET /api/sessions/{id}` | Single session detail. |
| `GET /api/sessions/{id}/messages?limit&before` | Paginated messages for a session (replaces direct query in `session_insight`). |
| `GET /api/health` | Public health, returns `{ok, version, uptime, db_path}`. (Already have `/healthz`; keep both — `/healthz` for liveness probes, `/api/health` for richer shape.) |
| `GET /api/metrics` | `cairo-ui` already drafted this contract in `~/cairo-ui/docs/cairo-api-requests/metrics.md`. Counts of sessions/memories/jobs for dashboard. |

## 4. Effort estimate

The infrastructure is in place: `Server.registerRoutes()`, `s.auth(...)` middleware, JSON encode/decode patterns, `s.db` already wired. Each new endpoint is:

- One handler function (~30–60 lines).
- One route line in `registerRoutes()`.
- One typed request/response struct.
- One DB query method (most already exist on `*db.DB`).

Per endpoint: ~1 hour. **Total replacement set: ~12 endpoints × 1h = ~1.5 days of work.** Plus an integration test harness. Call it **3 days end-to-end** including tests + web-agent client refactor.

The work is low-risk because the queries already exist (in `internal/db/sessions.go`, `config.go`, `roles.go`, `consider_aspects.go`) — the HTTP layer just wraps them.

## 5. Streaming: SSE vs WebSocket

**Current state.** Cairo uses SSE for both `/api/chat?stream=true` and `/rpc/stream/{id}`. The web-agent Node server also re-emits SSE to its browser clients.

**SSE strengths for Cairo:**
- One-way server→client, which is exactly the chat-token-stream shape.
- Trivially proxiable through HTTP middlewares, including tsnet.
- Auto-reconnect built into `EventSource`.
- No protocol upgrade negotiation.

**WebSocket would be needed if:**
- The client needs to send mid-turn signals (e.g. cancel, steer) on the same connection. (Today cancel is a separate `POST /api/sessions/{id}/cancel` on the web-agent layer.)
- We want full-duplex tool-use protocols (e.g. tool_call ↔ tool_result on the same socket).
- Bandwidth-sensitive deployments need binary frames.

**Recommendation:** **Keep SSE for chat token streaming.** The streaming story is fine. Add **one** `GET /api/events` SSE endpoint that emits agent-loop events (`tool_call_started`, `tool_result`, `summarizer_run`, `task_completed`) for observers (the cairo-ui dashboard, debugging panes) — this matches the registry's existing WS Frame protocol and is cheap to add.

Adopt WebSockets only if a future feature needs them (e.g. interactive tool-use from the browser with mid-tool client input). The registry already uses WS internally for the agent↔registry stream; if we eventually unify the surfaces, add WS at the cairo-server level then. Today, SSE is sufficient.

## 6. cairo-ui's current API usage

Read `~/cairo-ui/src/Lib/Cairo/HttpCairoClient.cs`:

```csharp
public sealed class HttpCairoClient : ICairoClient
{
    public Task<ChatResponse> SendAsync(ChatRequest request, CancellationToken ct = default)
        => throw new NotImplementedException("HttpCairoClient not wired up yet — use MockCairoClient");
    // ...
    public Task<MetricsSnapshot> GetMetricsAsync(CancellationToken ct = default)
        => throw new NotImplementedException("GET /api/metrics not yet exposed by cairo — see docs/cairo-api-requests/metrics.md");
}
```

`HttpCairoClient` is a **stub**. cairo-ui currently uses `MockCairoClient` everywhere (search for `ICairoClient` registrations — `Bloc/Metrics/MetricsService.cs:6` and `Bloc/Chat/ChatService.cs:6` consume `ICairoClient` via DI). Drafted contracts live in `~/cairo-ui/docs/cairo-api-requests/`:

- `metrics.md` — `GET /api/metrics` snapshot for dashboard cards.
- (Other contracts in flight per `cairo-ui/notes/project-vision.md`; the PR-handoff workflow is documented there.)

Per the cairo-ui briefing, the intent is to call `cairo.send` / `cairo.send.stream` over `/rpc` once available — but this is also the chat path. So cairo-ui needs:
- `/rpc cairo.send` + `cairo.send.stream` (already exist).
- `GET /api/metrics` (drafted, not implemented).
- Bearer auth via `cairo serve --auth` (already supported).

## 7. Net design implication

The web-agent's direct-SQLite reads collapse to one `GET /api/config/snapshot` plus per-resource CRUD endpoints. cairo-ui's drafted contracts add `/api/metrics`. Both consumers converge on one stable HTTP surface served by `internal/server/`. After this refactor:

- web-agent Node server keeps its OWN HTTP surface (the React frontend talks to it, not directly to cairo) but its CairoDb shim is replaced by an `httpClient` calling the Go cairo binary.
- The Python bridge in `cairoDb.ts` deletes entirely (~280 lines gone).
- Schema coupling is broken: when cairo's schema changes, only the cairo Go code changes; HTTP shapes stay stable.
