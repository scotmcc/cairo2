# Agent revocation

Revocation permanently bars an agent from the fleet. A revoked agent cannot re-register, cannot send heartbeats, and its liveness stream is closed by the server. Revocation is a one-way operation — there is no unrevoke.

This feature landed in Phase 2.4.

---

## Revoking an agent

Find the agent ID, then revoke it:

```sh
# List agents to find the ID
cairo-ctl --operator ops list

# Revoke
cairo-ctl --operator ops revoke 550e8400-e29b-41d4-a716-446655440000
```

On success:

```json
{"status": "revoked"}
```

The registry sets `status = 'revoked'` in the ledger immediately.

---

## What happens server-side

### Ledger update

The `POST /agents/{id}/revoke` endpoint sets `status = 'revoked'` in the `agents` table. The agent remains in the ledger — its record is not deleted.

### WebSocket liveness stream

If the agent has an open WebSocket liveness connection (`ws_connected = 1`), the server acts on the next pong:

1. Agent sends a `pong` frame.
2. Server calls `Touch()` to update `last_seen_at`, then checks `status` in the ledger.
3. If `status = 'revoked'`, the server closes the WebSocket with a `1008 Policy Violation` close code.
4. The server logs: `ws: agent {id} revoked, closing stream`.

### Re-registration rejected

Any subsequent `POST /register` from the revoked `(owner, hostname, tailnet_node)` tuple returns:

```
HTTP 403 Forbidden
{"error": "agent revoked"}
```

The registry identifies the agent by the `(owner, hostname, tailnet_node)` composite key, not just by the submitted `agent_id`. An agent cannot escape revocation by omitting or changing its stored agent ID.

---

## What happens client-side

Both background goroutines started by `cairo serve` stop cleanly on receiving `403`:

### HeartbeatLoop

`HeartbeatLoop` re-registers on a ticker. When the registry returns `403`:

```
registry: agent revoked, heartbeat loop stopping
```

The loop exits. No further registration attempts are made for the lifetime of the process.

### LivenessStream

`LivenessStream` maintains the WebSocket connection and auto-reconnects on disconnect (5 s backoff). When the server closes the connection and the next reconnect attempt returns `403`:

```
registry: agent revoked, liveness stream stopping
```

The goroutine exits. No further reconnect attempts are made.

Both goroutines are driven by the same `context.Context` passed from `cairo serve`. Revocation stops the goroutines permanently without cancelling the context — the cairo process continues running and serving requests.

---

## ErrRevoked sentinel

`internal/registry/client.go` exports:

```go
var ErrRevoked = errors.New("agent revoked")
```

`registry.Register()` returns `ErrRevoked` when the registry responds with `403`. Callers can check:

```go
if errors.Is(err, registry.ErrRevoked) {
    // handle permanent rejection
}
```

`cmd/cairo/cmd_serve.go` does not currently surface `ErrRevoked` to the user as a fatal error — revocation is treated as a registry connectivity issue and the server continues. Agents that should halt on revocation can check this sentinel and exit.

---

## Audit trail

Revoked agents remain in the ledger with `status = 'revoked'`. To audit:

```sh
cairo-ctl --operator ops list | jq '[.[] | select(.status == "revoked")]'
```

The ledger also records `registered_at` and `last_seen_at`, which can be used to establish when the agent was last active before revocation.

The `commands` table records broadcast and revoke operations with `operator` and `created_at`. This is visible directly in the SQLite database:

```sh
sqlite3 ~/.cairo-registry/registry.db \
  "SELECT operator, command, datetime(created_at, 'unixepoch') FROM commands ORDER BY id DESC LIMIT 20;"
```

---

## Revoking a lost or compromised agent

If an agent's machine is compromised or decommissioned:

```sh
# Find agent by hostname
cairo-ctl --operator ops list | jq '.[] | select(.hostname == "compromised-host")'

# Revoke
cairo-ctl --operator ops revoke <agent-id>

# Verify
cairo-ctl --operator ops get <agent-id>
# → {"status":"revoked", ...}
```

The agent is blocked immediately. If the agent process is still running on the compromised machine, its HeartbeatLoop and LivenessStream will stop on next contact with the registry (within one heartbeat interval, default 60 s).
