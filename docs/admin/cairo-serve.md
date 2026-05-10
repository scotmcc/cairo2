# cairo serve

`cairo serve` starts the cairo HTTP server — the agent loop exposed over a local or Tailscale-routed HTTP interface. This is the mechanism by which external tools, the VS Code extension, and the web agent reach a running cairo instance.

---

## Quick start

```sh
# Open server, no auth, default port
cairo serve

# Require bearer token auth
cairo serve --auth

# Custom port
cairo serve --port 8090

# Register with a fleet registry
cairo serve --register http://127.0.0.1:8080

# Serve over Tailscale (no public TCP port)
cairo serve --tsnet
```

On startup, cairo prints its listen address and token (if auth is enabled):

```
cairo server listening
  url:   http://localhost:1337
  token: (none — open)
```

With `--auth`:

```
cairo server listening
  url:   http://localhost:1337
  token: 3f8a1c2d9e4b7f60
```

---

## Flags

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--port` | int | `1337` | TCP port to listen on |
| `--auth` | bool | `false` | Require Bearer token on all authenticated endpoints |
| `--tsnet` | bool | `false` | Serve over Tailscale (TLS on `:443`); mutually exclusive with plain TCP |
| `--register` | string | — | URL of a cairo-registry to register with at startup |

### Port resolution order

1. `--port` flag
2. `server_port` config key (`cairo config set server_port 8090`)
3. Hardcoded default: `1337`

If the port is already in use, cairo exits immediately:

```
port 1337 already in use — specify another with --port
```

---

## Authentication

When `--auth` is set, cairo requires `Authorization: Bearer <token>` on all endpoints except `/healthz`.

Token resolution on startup:

1. Read `server_token` from the config database.
2. If missing, generate a new 16-character hex token via `crypto/rand`, store it in the database, and print it once to stdout.
3. On subsequent restarts with `--auth`, the same token is reused from the database.

To pre-generate or rotate a token:

```sh
cairo token           # prints a new token; does NOT write to the database
cairo config set server_token <value>   # write it explicitly
```

See [authentication.md](authentication.md) for rotation strategies.

---

## Registry registration

`--register URL` registers this cairo instance with a cairo-registry server at startup.

```sh
cairo serve --register http://192.168.1.10:8080
```

On startup, cairo:
1. Sends `POST {URL}/register` with its hostname, version, and any stored `agent_id`.
2. Persists the assigned `agent_id` in the local database (`Registrations` table, keyed by registry URL).
3. Starts a `LivenessStream` WebSocket goroutine to `{URL}/agents/{id}/stream` for heartbeat pings.

The `--register` flag starts a liveness stream but not the periodic heartbeat loop (that loop is controlled by the `registry_url` config key, which persists across restarts). For persistent fleet membership, set the config key instead:

```sh
cairo config set registry_url http://192.168.1.10:8080
```

If the registry returns `403 Forbidden`, the agent has been revoked. The liveness stream stops cleanly and logs:

```
registry: agent revoked, liveness stream stopping
```

See [revocation.md](revocation.md) for the full revocation flow.

---

## Process management

cairo writes all logs to stdout. There is no built-in log file or log rotation.

### systemd user unit (package installs)

When installed via `.deb` or `.rpm`, a `cairo.service` user unit is provided:

```sh
systemctl --user enable --now cairo.service
systemctl --user status cairo.service
journalctl --user -u cairo.service -f
```

The unit runs as the installing user. Token and config are read from `~/.cairo/cairo.db`.

### Manual background run

```sh
nohup cairo serve --auth >> ~/.cairo/serve.log 2>&1 &
echo $! > ~/.cairo/serve.pid
```

### Graceful shutdown

cairo handles `SIGINT` and `SIGTERM`. On receipt:
- The agent loop completes any in-flight turn before the DB connection closes.
- Background registry goroutines stop via context cancellation.
- The listener closes.

---

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/healthz` | no | Liveness probe: `{"status":"ok"}` |
| `GET` | `/api/health` | yes | Richer health: `{ok, version, uptime_seconds, db_path}` |
| `POST` | `/api/chat` | yes | Native chat (sync or SSE) |
| `GET` | `/api/events` | yes | SSE stream of agent loop events |
| `GET` | `/api/metrics` | yes | Session/memory/job counts |
| `GET` | `/api/sessions` | yes | List sessions |
| `GET` | `/api/sessions/{id}` | yes | Get session |
| `GET` | `/api/sessions/{id}/messages` | yes | Paginated messages (`?limit=50&before=<id>`) |
| `GET` | `/api/config/snapshot` | yes | Config + roles + aspects dump |
| `PUT` | `/api/config/{key}` | yes | Set a config key |
| `GET` | `/v1/models` | yes | OpenAI models list (`[{id:"cairo"}]`) |
| `POST` | `/v1/chat/completions` | yes | OpenAI-compatible completions (sync or SSE) |
| `POST` | `/rpc` | yes | JSON-RPC 2.0 dispatcher |
| `GET` | `/rpc/stream/{id}` | yes | SSE consumer for decoupled `cairo.send.stream` |

See [events-and-monitoring.md](events-and-monitoring.md) for the SSE event schema and `GET /api/metrics` shape.

---

## Tsnet mode

`cairo serve --tsnet` binds via Tailscale instead of a local TCP port. On first run, a LoginURL is printed to stdout — visit it to authorize the node. See [tsnet-tailscale.md](tsnet-tailscale.md).

---

## Config keys

| Key | Type | Description |
|-----|------|-------------|
| `server_port` | int | Default listen port (overridden by `--port`) |
| `server_token` | string | Bearer token (written on first `--auth` start) |
| `registry_url` | string | Registry URL for persistent heartbeat + liveness |
| `registry_agent_id` | string | Assigned agent UUID (written by registry on first register) |
| `registry_heartbeat_interval` | int | Heartbeat frequency in seconds (default: 60) |
