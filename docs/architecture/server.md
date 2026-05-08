# HTTP server

`cairo serve` exposes a local HTTP server that wraps the agent loop behind three API surfaces: a native chat API (`/api/chat`), an OpenAI-compatible layer (`/v1/…`), and a JSON-RPC 2.0 interface (`/rpc`). The server is stateless at the HTTP layer; all persistent state lives in the SQLite DB as usual. One agent instance, one session, one worker goroutine — concurrent HTTP requests are serialized through a channel queue before reaching the agent.

Source: `internal/server/`, `cmd/cairo/serve.go`, `cmd/cairo/token.go`.

---

## Endpoint table

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | Liveness check. Always 200, no auth. |
| `POST` | `/api/chat` | Native chat: sync or SSE streaming. |
| `GET` | `/v1/models` | OpenAI models list. Static single-entry response. |
| `POST` | `/v1/chat/completions` | OpenAI completions: sync or SSE streaming. |
| `POST` | `/rpc` | JSON-RPC 2.0 dispatcher. |
| `GET` | `/rpc/stream/{id}` | SSE consumer for a decoupled RPC stream. |

---

## Package layout

| File | What it owns |
|------|-------------|
| `constants.go` | `DefaultPort=1337`, `TokenBytes=8`, `ModelID="cairo"`, `BridgeQueueDepth=32` |
| `prompter.go` | `Prompter` interface — narrow seam between server and `*agent.Agent` |
| `token.go` | `GenerateToken()` — crypto/rand → 16-char hex |
| `session_bridge.go` | `SessionBridge` — serializes concurrent HTTP requests into the single-threaded agent loop |
| `server.go` | `Server` struct, route wiring, auth middleware, `ListenAndServe`, graceful shutdown |
| `chat.go` | `POST /api/chat` — message extraction, context formatting, sync and SSE paths |
| `openai.go` | `GET /v1/models` and `POST /v1/chat/completions` — OpenAI-shaped handlers |
| `rpc.go` | JSON-RPC dispatcher, stream registry, `GET /rpc/stream/{id}` SSE consumer |

---

## Session bridge

The session bridge is the central design choice in the server. The agent loop is not re-entrant — one `Prompt()` call runs until `EventAgentEnd` fires. Concurrent HTTP requests would corrupt agent state if dispatched directly. `SessionBridge` enforces serial execution via a buffered channel and a single worker goroutine.

```
  HTTP goroutine 1 ──┐
  HTTP goroutine 2 ──┤──▶  queue chan *bridgeReq (cap 32)  ──▶  worker goroutine  ──▶  agent.Prompt()
  HTTP goroutine N ──┘
```

**Queue mechanics** (`session_bridge.go:40`): `queue: make(chan *bridgeReq, BridgeQueueDepth)` — capacity 32. HTTP goroutines block on `b.queue <- req` if the queue is full. There is no immediate "queue full" rejection; the HTTP request's own context deadline is the only escape valve. When that deadline fires, the HTTP goroutine unblocks with `ctx.Err()` and the handler returns a 500.

**Worker goroutine**: started once via `sync.Once` in `Start()`. Reads from `queue`, calls `agent.Prompt()`, waits for `EventAgentEnd`, then writes to the per-request `replyCh`. Each request carries its own `replyCh chan bridgeResp` (capacity 1) so replies are routed back to the correct HTTP goroutine without shared state.

**Event bus subscription** (`session_bridge.go:118–119`): the worker subscribes to the agent's event bus — `ch, unsub := b.agent.Bus().Subscribe()` — before calling `Prompt`. This ensures no early tokens are missed. On `EventTokens`, the worker does a non-blocking send to the streaming tokens channel; tokens are dropped if the receiver can't keep up (lines 135–138). On `EventAgentEnd`, the worker captures `LastAssistantText()`, unsubscribes, closes the tokens channel if streaming, and sends the `bridgeResp`.

**Cancellation**: if the HTTP caller's context is cancelled (client disconnect, timeout), `Send`/`SendStream` returns early. The worker goroutine does not cancel — it continues running until `EventAgentEnd` arrives. This preserves agent state: a partially-completed turn reaches a clean end, is persisted to the DB, and the session remains consistent. The response text is simply never delivered to the disconnected caller.

**`Stop()`** (`session_bridge.go:56–58`): closes `b.done`, which signals the worker to exit. The currently-processing `bridgeReq` runs to completion before the worker exits; requests that are queued but not yet picked up are abandoned and their callers will block until their context is cancelled.

```go
type Prompter interface {
    Prompt(ctx context.Context, text string) error
    Bus() *agent.Bus
    LastAssistantText() string
    Model() string
    Session() *db.Session
    IsStreaming() bool
}
```

`*agent.Agent` satisfies `Prompter`. Tests inject a `fakeAgent` (`session_bridge_test.go:15–50`), keeping the bridge testable without a real Ollama connection.

---

## Request flow walkthroughs

### `POST /api/chat` — synchronous

```
  client  ──POST /api/chat──▶  handleChat()
                                 │
                                 ├── extractMessage()   ← req.Message or last user entry in req.Messages
                                 ├── formatContext()    ← appends [URL], [DOCUMENT], [SELECTION], [FILE] blocks
                                 │
                                 └── bridge.Send(ctx, text)
                                       │
                                       ├── enqueue bridgeReq
                                       ├── worker: agent.Prompt(text)
                                       ├── worker: EventAgentEnd → bridgeResp
                                       └── return response text
                                 │
                                 └── 200 {"response":"...", "session_id":3, "turn_id":47}
```

`extractMessage` (`chat.go:127–138`): uses `req.Message` if non-empty; otherwise walks `req.Messages` from the end and returns the last `role == "user"` entry. System messages are silently dropped. An empty result is a 400.

`formatContext` (`chat.go:141–167`) appends structured blocks to the message text before it reaches the agent:
- `url` → `[URL] {title} — {value}`
- `document` → `[DOCUMENT] {title}\n{content}` (content truncated to 4000 chars at line 153)
- `selection` → `[SELECTION from {source}]\n{content}`
- `file` → `[FILE] {path}\n{content}`

Non-streaming response shape (`chat.go:85–90`): `{"response": "...", "session_id": 3, "turn_id": 3}`. Both fields carry the session ID — `turn_id` is populated from `b.agent.Session().ID` (`session_bridge.go:144`) and is not an independent per-turn counter.

### `POST /v1/chat/completions` — streaming

```
  client  ──POST /v1/chat/completions (stream:true)──▶  handleCompletionsStream()
                                                           │
                                                           ├── tokens := make(chan string, 64)
                                                           ├── go bridge.SendStream(ctx, text, tokens) → errCh
                                                           │
                                                           ├── write SSE: {"choices":[{"delta":{"role":"assistant"}}]}
                                                           │
                                                           │   for tok := range tokens:
                                                           ├──   write SSE: {"choices":[{"delta":{"content":"<tok>"},"finish_reason":null}]}
                                                           │
                                                           ├── drain errCh
                                                           ├── write SSE: {"choices":[{"delta":{},"finish_reason":"stop"}]}
                                                           └── write: data: [DONE]
```

The handler writes SSE headers (`text/event-stream`, `no-cache`, `keep-alive`), then ranges over the tokens channel. The bridge goroutine is the sole writer; the handler goroutine is the sole reader. After `tokens` is closed, the handler drains `errCh` and emits the final `finish_reason: "stop"` chunk followed by `data: [DONE]\n\n` (`openai.go:153–216`).

Non-streaming completions (`openai.go:104–150`): same message extraction logic, synchronous `bridge.Send`, response shaped as `{"id":"cairo-turn-<turnID>","object":"chat.completion",...}` with usage fields zeroed.

---

## JSON-RPC surface

All RPC calls share one endpoint: `POST /rpc`. The dispatcher (`rpc.go:90–119`) requires `"jsonrpc":"2.0"` on every request (line 102). Unknown methods return HTTP 200 with a JSON-RPC error body — standard JSON-RPC behavior.

### Methods

**`cairo.send`**
- Params: `{message, context}`
- Result: `{response, session_id, turn_id}`
- Error `-32602` on empty message

**`cairo.send.stream`**
- Params: `{message, context}` — same as `cairo.send`
- Immediately returns: `{stream_id: "s_xxxxxxxx"}`
- The server allocates a `tokensCh` + `errCh`, registers them in `streamRegistry`, and launches a bridge goroutine. Caller then opens `GET /rpc/stream/{stream_id}` to consume tokens.

**`cairo.status`**
- No params
- Result: `{session_id, session_name, model, auth, busy}`
- `model` comes from `Prompter.Model()`; `busy` from `Prompter.IsStreaming()`
- Note: no `version` field — design doc specified one; not implemented.

**`cairo.slash`**
- Params: `{command}`
- Allowed commands: `/init` (stub), `/help`, `/session`, `/sessions`, `/jobs`, `/memories`, `/tools`, `/skills`
- These run as direct DB queries, not through the agent — slash commands that need LLM execution are not exposed here.
- Anything else: error `-32601 "unknown or unsafe slash command"`

### RPC error codes

| Code | Meaning |
|------|---------|
| `-32700` | Parse error |
| `-32600` | Invalid request |
| `-32601` | Method not found |
| `-32602` | Invalid params |
| `-32603` | Internal error |

### Decoupled SSE via `GET /rpc/stream/{id}`

`handleRPCStream` (`rpc.go:191–224`):
- Parses id via `strings.TrimPrefix(r.URL.Path, "/rpc/stream/")`
- Returns 404 if id is empty, contains `/`, or is not in the stream registry
- `s.streams.pop(id)` — one-shot; the entry is removed on first GET
- Ranges over `entry.tokens`, writes `data: {"token":"<t>"}\n\n`, flushes, then drains `errCh` and writes `data: [DONE]\n\n`

Stream ID format: `"s_"` prefix + 4 random bytes as hex = 10 chars total (`newStreamID()`, `rpc.go:413–417`).

---

## Auth model

Auth is optional. `cairo serve --auth` enables it; `cairo serve` (default) runs with no token check. The flag maps to `opts.Auth bool` on the `Server`.

**Middleware** (`server.go:97–110`): when `opts.Auth` is true, all routes except `/healthz` are wrapped in `s.auth()`. The middleware calls `bearerToken()` to extract the value after `"Bearer "` from the `Authorization` header (requires `len(h) > 7`; `server.go:112–121`), then compares with `crypto/subtle.ConstantTimeCompare` to prevent timing attacks. Mismatch → 401 with `{"error":"unauthorized"}` and `Content-Type: application/json`.

**Token storage** (`cmd/cairo/serve.go:104–119`): on startup, if `--auth` is set, the server reads `db.KeyServerToken` (`config` table key `"server_token"`, `internal/store/config_keys.go:79`). If a token already exists it is reused; otherwise `server.GenerateToken()` produces a fresh one and saves it.

**`GenerateToken()`** (`internal/server/token.go:10–16`): `crypto/rand.Read` fills `TokenBytes=8` bytes, `hex.EncodeToString` produces a 16-character lowercase hex string.

**`cairo token` subcommand** (`cmd/cairo/token.go`): always overwrites `db.KeyServerToken` with a freshly generated token. Run once to establish a stable token before starting the server with `--auth`.

**`/healthz` exemption**: registered directly without the auth wrapper (`server.go:50`). Health checks must succeed regardless of token configuration.

---

## Lifecycle

### Startup (`cmd/cairo/serve.go`)

```
  1. Parse flags: --port, --auth
  2. Open DB (db.Open), connect Ollama (llm.New), db.ResolveModel — fail fast if no model
  3. Resolve or create session: database.Sessions.Latest() → create new if nil (lines 53–65)
  4. Build tools, agent.New(...)
  5. signal.NotifyContext for SIGINT/SIGTERM
  6. cli.BackgroundRenderer(a.Bus(), os.Stdout)
  7. Resolve/generate token if --auth (lines 104–119)
  8. Resolve port: flag > db.KeyServerPort config > DefaultPort=1337 (lines 121–130)
  9. net.Listen — port conflict detection before creating http.Server
 10. server.NewBridge(a) → bridge.Start(ctx) → server.New(a, db, bridge, opts)
 11. Print banner
 12. srv.ListenAndServe(ctx, addr)
```

Port conflict (`cmd/cairo/serve.go`): `net.Listen` is called before `http.Server` is constructed. If the `Listen` call returns a `*net.OpError`, the binary exits with: `"port %d already in use — specify another with --port"`.

Role: hardcoded to `db.RoleThinkingPartner`. There is no `--role` flag on `serve`.

### Graceful shutdown

`ListenAndServe` (`server.go:81–92`) watches `ctx.Done()` in a goroutine and calls `httpSrv.Shutdown(shutdownCtx)` with a 5-second timeout. `http.ErrServerClosed` is swallowed and returned as `nil`.

Post-shutdown (deferred in `serve.go`): `bridge.Stop()` → `a.Close()` (drains background summarizer WaitGroup) → `database.Close()` → `stopRenderer()`. Shutdown order matters: the bridge must stop before the agent is closed, and the agent must finish before the DB is closed.

---

## Concurrency model

The agent loop is not concurrent. `agent.Prompt()` is synchronous and not re-entrant. The server allows concurrent HTTP connections; the bridge is the serialization layer.

```
  HTTP goroutine A  ───▶ ┐
  HTTP goroutine B  ───▶ ├──  queue (cap 32, blocks when full)  ──▶  worker  ──▶  agent.Prompt()
  HTTP goroutine C  ───▶ ┘                                              ▲
                                                                         │
                                                              one goroutine, started once (sync.Once)
```

Channel inventory per bridge instance:
- `queue chan *bridgeReq` (cap 32) — shared input; HTTP goroutines write
- `replyCh chan bridgeResp` (cap 1) — one per request; private to that request's goroutine pair
- `done chan struct{}` — closed by `Stop()` to signal worker exit
- `tokens chan string` (cap 64, per streaming request) — bridge goroutine writes, HTTP handler reads

Why can't two turns interleave? The worker goroutine holds the only write path to `agent.Prompt()`. The next `bridgeReq` is not dequeued until `EventAgentEnd` fires and `bridgeResp` is sent. History, streaming state, and `lastAssistantText` are all owned by the agent; no HTTP goroutine touches them directly.

**Stream registry concurrency** (`rpc.go`): `streamRegistry.mu sync.Mutex` protects the `entries map[string]*streamEntry`. `pop` acquires the lock, deletes the entry, and returns it — safe for concurrent GET requests racing on the same stream id.

**Token drop tradeoff**: non-blocking sends on `tokens` mean a slow SSE consumer loses intermediate tokens. The complete final text is always recoverable via `LastAssistantText()` after `EventAgentEnd`.

---

## Limits and failure modes

| Condition | HTTP status | Body |
|-----------|-------------|------|
| Auth failure | 401 | `{"error":"unauthorized"}` |
| Queue full (context deadline) | 500 | `{"error":"context deadline exceeded"}` |
| Empty message | 400 | `{"error":"message is required"}` |
| Unknown RPC method | 200 | `{"jsonrpc":"2.0","error":{"code":-32601,...}}` |
| Malformed RPC JSON | 200 | `{"jsonrpc":"2.0","error":{"code":-32700,...}}` |
| `/rpc/stream/{id}` unknown | 404 | `stream not found\n` (plain text) |

### SSE client disconnect

**Direct SSE** (`/api/chat`, `/v1/chat/completions`): the HTTP handler uses `r.Context()`, which is cancelled when the client disconnects. The handler's `range tokens` loop unblocks. The bridge goroutine's `SendStream` call returns early. The worker goroutine continues running — it does not observe the caller's context — and runs to `EventAgentEnd`. Agent state is preserved; the completed turn is persisted. The response text is simply never delivered.

**Decoupled SSE** (`/rpc` + `GET /rpc/stream/{id}`): if the `POST /rpc` caller disconnects before the response arrives, the bridge goroutine's context (`r.Context()` from the POST) is cancelled. The `streamEntry` in the registry is not cleaned up — it remains until `GET /rpc/stream/{id}` arrives or the process exits. There is no TTL or expiry. This is a known resource leak; a 5-minute auto-expiry was planned but not implemented.

### Queue depth and back-pressure

With `BridgeQueueDepth=32`, up to 32 concurrent requests can be queued. Requests beyond that block at the channel send. HTTP client timeouts are the only mechanism that frees stuck goroutines; there is no server-side per-request timeout.

---

## Known rough edges

- `cairo.status` response omits `version` — the design document specified it; `server.go` never populates it.
- System messages in `/api/chat` `messages[]` and `/v1/chat/completions` are silently dropped. The design called for injecting them as `user_context` into the prompt; that was not implemented.
- `/init` in `cairo.slash` is a stub — it returns success without triggering the actual init flow.
- `GET /rpc/stream/{id}` returns a plain-text 404 body, inconsistent with every other error response in the server (all JSON).
- `cairo.send.stream` context coupling: the bridge goroutine inherits `r.Context()` from the `POST /rpc` request. Once the POST response is sent, the context is live but the underlying connection is closed; if the runtime cancels it, the in-flight turn is orphaned.

---

## Cross-references

- CLI flags: [docs/reference/cli.md](../reference/cli.md)
- Config keys `server_token`, `server_port`: [docs/reference/config-keys.md](../reference/config-keys.md)
- Feature summary §22 (HTTP server): [docs/FEATURES.md](../FEATURES.md)
- Agent loop and event bus: [docs/architecture/agent-loop.md](agent-loop.md)
- Database schema: [docs/architecture/database.md](database.md)
