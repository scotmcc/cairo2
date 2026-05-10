# cairo-ctl

`cairo-ctl` is the operator CLI for the cairo fleet registry. It talks to the registry's admin listener (`127.0.0.1:8081` by default) and sends commands on behalf of an operator identity.

`cairo-ctl` must run on the same host as `cairo-registry`, or with SSH port-forwarding to the admin listener.

---

## Global flags

| Flag | Default | Description |
|------|---------|-------------|
| `--addr` | `127.0.0.1:8081` | Admin listener address |
| `--operator` | — | Operator identity sent as `X-Operator-Identity` header |
| `--version` | — | Print version and exit |

`--addr` accepts both bare `host:port` and `http(s)://host:port`. All of these are equivalent:

```sh
cairo-ctl --addr 127.0.0.1:8081 list
cairo-ctl --addr http://127.0.0.1:8081 list
```

`--operator` is required for `list`, `get`, `revoke`, and `broadcast`. The registry uses it to filter agents in tsnet mode (where each operator only sees their own agents). In `-no-tsnet` dev mode it can be any non-empty string.

---

## Subcommands

### `list`

List all agents visible to the operator.

```sh
cairo-ctl --operator alice list
```

Output (JSON array):

```json
[
  {
    "agent_id":      "550e8400-e29b-41d4-a716-446655440000",
    "hostname":      "myhost",
    "owner":         "alice@example.com",
    "tailnet_node":  "",
    "version":       "0.4.0",
    "registered_at": 1746876000,
    "last_seen_at":  1746876300,
    "status":        "active",
    "ws_connected":  1
  }
]
```

`status` values: `active`, `stale`, `revoked`.
`ws_connected`: `1` if the agent currently has an open WebSocket liveness stream.

---

### `get <agent-id>`

Show details for a single agent.

```sh
cairo-ctl --operator alice get 550e8400-e29b-41d4-a716-446655440000
```

Returns the same shape as a single element from `list`. Returns `404` if the agent does not exist or is not visible to the operator.

---

### `health`

Show registry health and aggregate counts.

```sh
cairo-ctl health
```

Output:

```json
{
  "status":          "ok",
  "total":           12,
  "active":          9,
  "stale":           2,
  "ws_connected":    8,
  "uptime_seconds":  86400
}
```

`--operator` is not required for `health`.

---

### `revoke <agent-id>`

Revoke an agent. The agent is permanently barred from re-registering.

```sh
cairo-ctl --operator alice revoke 550e8400-e29b-41d4-a716-446655440000
```

On success:

```json
{"status": "revoked"}
```

What happens after revocation:
- The ledger sets `status = 'revoked'` for the agent.
- Any in-progress WebSocket liveness stream is closed by the server on the next pong.
- The agent's `HeartbeatLoop` and `LivenessStream` both receive `403` and stop cleanly.
- Any subsequent `POST /register` from that agent returns `403 Forbidden`.

See [revocation.md](revocation.md) for the full client-side flow.

Returns `404` if the agent ID is not found. Returns `400` if the ID is malformed.

---

### `broadcast <command>`

Enqueue a broadcast command to all agents. The command is persisted to the `commands` table in the ledger.

```sh
cairo-ctl --operator alice broadcast "reload-config"
```

On success:

```json
{"status": "queued"}
```

The `broadcast` subcommand stores the command string in the registry ledger for future consumption by agents polling the registry. The delivery mechanism (how agents discover and act on broadcast commands) is a planned Phase 3+ feature.

---

## Common workflows

### Check fleet status after a deployment

```sh
cairo-ctl health
cairo-ctl --operator ops list | jq '.[] | select(.status != "active")'
```

### Identify stale agents

```sh
cairo-ctl --operator ops list | jq '[.[] | select(.status == "stale")] | length'
```

### Revoke a compromised agent

```sh
# Find the agent
cairo-ctl --operator ops list | jq '.[] | select(.hostname == "compromised-host")'

# Revoke it
cairo-ctl --operator ops revoke <agent-id>

# Confirm
cairo-ctl --operator ops get <agent-id>
# → {"status":"revoked", ...}
```

### Reach a non-default admin listener

```sh
# Registry running on non-standard port
cairo-ctl --addr 127.0.0.1:9091 --operator ops list

# Via SSH tunnel: ssh -L 8081:127.0.0.1:8081 registry-host
cairo-ctl --addr 127.0.0.1:8081 --operator ops list
```

---

## Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Request error (non-2xx response, network failure, parse error) |

Error messages are written to stderr.
