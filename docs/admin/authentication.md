# Authentication

Cairo uses bearer token authentication for `cairo serve`. The registry admin listener uses a separate operator identity mechanism (no shared token).

---

## cairo serve: bearer tokens

### Enabling auth

Pass `--auth` when starting the server:

```sh
cairo serve --auth
```

On first run with `--auth`, cairo generates a 16-character lowercase hex token using `crypto/rand` (8 random bytes, hex-encoded), stores it in the config database under the `server_token` key, and prints it once to stdout:

```
cairo server listening
  url:   http://localhost:1337
  token: 3f8a1c2d9e4b7f60
```

On subsequent restarts with `--auth`, the same token is read from the database. The token is not printed again unless it changes.

### Using the token

All authenticated endpoints require:

```
Authorization: Bearer <token>
```

Example:

```sh
curl -H "Authorization: Bearer 3f8a1c2d9e4b7f60" http://localhost:1337/api/metrics
```

Without the header (when auth is enabled):

```json
{"error": "unauthorized"}
```

HTTP 401.

### Token comparison

Comparison uses `crypto/subtle.ConstantTimeCompare` — timing-safe, resistant to timing attacks.

---

## Generating tokens

### Via `cairo token`

`cairo token` prints a new cryptographically random token to stdout without writing it to the database:

```sh
cairo token
# → a3f8c1e29d4b7605
```

Use this to generate a token for manual configuration, scripting, or rotation.

### Via `cairo config set`

To write a token directly to the database:

```sh
cairo config set server_token <value>
```

The next `cairo serve --auth` will use this value.

### Storing the token

The token lives in `~/.cairo/cairo.db` in the `config` table. It survives process restarts and is tied to the `CAIRO_DATA_DIR` data directory.

To read the current token:

```sh
cairo config get server_token
```

---

## Token rotation

Rotation is a three-step operation: generate, store, restart.

```sh
# 1. Generate a new token
NEW_TOKEN=$(cairo token)

# 2. Write it to the database
cairo config set server_token "$NEW_TOKEN"

# 3. Restart cairo serve (old token is immediately invalid after restart)
systemctl --user restart cairo.service

echo "New token: $NEW_TOKEN"
```

There is no grace period — the old token stops working the moment the process restarts. If clients (web agent, VS Code extension) hold the old token, they will get 401 until reconfigured.

### Rotating without downtime

Use a reverse proxy (nginx, caddy) in front of cairo serve, and reload the proxy after rotation. The process restart is still required, but the proxy can hold connections during the brief restart window.

---

## cairo-registry: operator identity

The registry admin listener does not use bearer token auth. Instead, all `cairo-ctl` requests carry an `X-Operator-Identity` header:

```
X-Operator-Identity: alice@example.com
```

In tsnet mode, the registry correlates this identity against the Tailscale node that owns the connection. In `-no-tsnet` mode, the header is accepted as-is with no verification — suitable for local dev only.

The admin listener is bound to `127.0.0.1:8081` (loopback) and is not reachable from the network without explicit forwarding. Network isolation is the primary access control for the admin API.

To restrict admin access further in production, place cairo-registry behind a firewall that only allows the `127.0.0.1` loopback interface to reach port `8081`.

---

## Registry agents: no agent-side auth

Agents (`cairo serve --register`) authenticate to the registry only via their assigned `agent_id` UUID and the registration round-trip. There is no per-agent token. Revocation (see [revocation.md](revocation.md)) is the mechanism by which a registry refuses a specific agent.

---

## Summary

| Surface | Mechanism | Config |
|---------|-----------|--------|
| `cairo serve` endpoints | Bearer token | `server_token` config key |
| `cairo-ctl` → registry admin | `X-Operator-Identity` header | `--operator` flag |
| Agent → registry registration | Agent UUID | `registry_agent_id` config key |
| Registry public listener | None | Admin listener is the only auth boundary |
