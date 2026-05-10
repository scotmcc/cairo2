# cairo-registry setup

`cairo-registry` is the fleet registry server. It maintains a ledger of cairo agents, tracks their liveness via WebSocket connections, and exposes an admin interface for operator commands. It is a separate binary — it runs independently of any cairo agent.

---

## What it does

- Accepts `POST /register` from cairo agents (`cairo serve --register`).
- Maintains per-agent status (`active`, `stale`, `revoked`) in a SQLite ledger.
- Tracks liveness via WebSocket (`GET /agents/{id}/stream`): agents ping every 10 s; the server marks stale agents that go silent.
- Runs a background sweeper that marks agents stale after inactivity.
- Exposes an admin listener (loopback only) for `cairo-ctl` commands.

---

## Two listeners

cairo-registry binds two independent HTTP servers:

| Listener | Default address | Purpose |
|----------|-----------------|---------|
| Public | `:443` (tsnet) or flag-specified | Agent registration + WebSocket liveness |
| Admin | `127.0.0.1:8081` | Operator commands via `cairo-ctl` |

The admin listener is loopback-only by design. `cairo-ctl` must run on the same host (or via SSH tunnel) to reach it.

To disable the admin listener:

```sh
cairo-registry -admin-addr ""
```

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-state-dir` | `~/.cairo-registry` | Directory for the ledger database and tsnet state |
| `-addr` | `:443` | Public listener address (ignored when tsnet is active) |
| `-admin-addr` | `127.0.0.1:8081` | Admin listener address; empty string disables it |
| `-no-tsnet` | `false` | Use plain TCP instead of Tailscale (for local dev/CI) |
| `-version` | — | Print version and exit |

---

## Running locally (dev / no tsnet)

For local development, use `-no-tsnet` to bind plain TCP:

```sh
cairo-registry -no-tsnet -addr :8080 -state-dir /tmp/registry-dev
```

This listens on `:8080` (public) and `127.0.0.1:8081` (admin). Agents register with:

```sh
cairo serve --register http://localhost:8080
```

---

## Running in production (tsnet)

Without `-no-tsnet`, cairo-registry bootstraps a tsnet node named `cairo-registry` and binds TLS on `:443` within the tailnet.

On first run, a LoginURL is printed to stdout. Visit it to authorize the node:

```
cairo-registry listening via tailnet
  url:   https://cairo-registry.your-tailnet.ts.net
  authorize this node: https://login.tailscale.com/a/...
```

Subsequent restarts reuse the authorized node (state persisted in `{state-dir}/tsnet/`).

Agents then register with the tailnet hostname:

```sh
cairo serve --register https://cairo-registry.your-tailnet.ts.net
```

---

## Ledger storage

The ledger is a SQLite database at `{state-dir}/registry.db` (WAL mode).

Schema:

```sql
CREATE TABLE agents (
    agent_id       TEXT PRIMARY KEY,
    hostname       TEXT NOT NULL,
    owner          TEXT NOT NULL DEFAULT 'local',
    tailnet_node   TEXT NOT NULL DEFAULT '',
    version        TEXT NOT NULL DEFAULT '',
    registered_at  INTEGER NOT NULL,
    last_seen_at   INTEGER NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active',
    ws_connected   INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE commands (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    operator   TEXT NOT NULL,
    command    TEXT NOT NULL,
    created_at INTEGER NOT NULL
);
```

The `(owner, hostname, tailnet_node)` tuple is used to identify returning agents across restarts. A returning agent gets its existing `agent_id` back rather than a new UUID.

The database is safe to inspect directly with `sqlite3`. Do not write to it while the registry is running.

---

## Running as a systemd service

The `.deb` and `.rpm` packages install a system unit at `/usr/lib/systemd/system/cairo-registry.service`. It runs as the `cairo` system user.

```sh
# Enable and start
sudo systemctl enable --now cairo-registry.service

# View logs
journalctl -u cairo-registry.service -f

# Restart
sudo systemctl restart cairo-registry.service
```

The service runs with `-state-dir /var/lib/cairo-registry`. Adjust `ExecStart` in the unit file if you need a different path.

### Manual systemd unit (if not installed via package)

```ini
[Unit]
Description=cairo fleet registry
After=network.target

[Service]
Type=simple
User=cairo
Group=cairo
ExecStart=/usr/local/bin/cairo-registry -no-tsnet -addr :8080 -state-dir /var/lib/cairo-registry
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

---

## Public endpoints

These are served on the public listener and reached by agents:

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/healthz` | `{status, uptime_seconds, total, active, stale, ws_connected}` |
| `POST` | `/register` | Agent registration |
| `GET` | `/agents/{id}/stream` | WebSocket liveness stream |

`POST /register` accepts:

```json
{
  "agent_id":     "uuid-or-empty-string",
  "hostname":     "myhost",
  "version":      "0.4.0",
  "tailnet_node": ""
}
```

Returns:

```json
{
  "agent_id":      "550e8400-e29b-41d4-a716-446655440000",
  "registered_at": 1746876000
}
```

If the agent has been revoked, `POST /register` returns `403 Forbidden`.

## Admin endpoints

Served on `127.0.0.1:8081`. All require the `X-Operator-Identity` header. Consumed by `cairo-ctl`.

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/agents` | List all agents |
| `GET` | `/agents/{id}` | Get single agent |
| `GET` | `/healthz` | Registry health and counts |
| `POST` | `/agents/{id}/revoke` | Revoke an agent |
| `POST` | `/broadcast` | Enqueue a broadcast command |

See [cairo-ctl.md](cairo-ctl.md) for the operator CLI that wraps these endpoints.

---

## Agent owner resolution

In tsnet mode, ownership is determined by calling Tailscale's `WhoIs()` on the connecting node. Each agent is owned by the Tailscale identity that registered it, and `GET /agents` filters results to the requesting operator's identity.

In `-no-tsnet` mode, ownership is hardcoded to `"local"`. All agents are visible to any operator.

---

## Stale sweep

A background goroutine periodically marks agents stale when `last_seen_at` is older than the stale threshold. Stale agents remain in the ledger; they are not deleted. Use `cairo-ctl` to inspect or revoke them.
