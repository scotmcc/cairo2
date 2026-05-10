# Events and monitoring

cairo exposes three monitoring surfaces on the `cairo serve` HTTP server: an SSE event stream, a metrics endpoint, and two health endpoints. All require authentication when `--auth` is enabled.

---

## SSE event stream

```
GET /api/events
Authorization: Bearer <token>
Accept: text/event-stream
```

Returns a continuous Server-Sent Events stream of agent loop events. Each event is a JSON-encoded `agent.Event` object, sent as:

```
data: {"type":"AgentStart","session_id":3,...}

data: {"type":"TurnStart","session_id":3,"turn_id":7,...}
```

The connection stays open until the client disconnects. The server sends no keepalive pings — use a client with reconnection support (EventSource in browsers, curl with `--no-buffer`).

```sh
curl -N -H "Authorization: Bearer <token>" http://localhost:1337/api/events
```

### Allowed event types

Not all internal event types are forwarded to the SSE stream. The allowlist:

| Type | When it fires |
|------|---------------|
| `AgentStart` | Agent loop begins processing a request |
| `AgentEnd` | Agent loop completes a request |
| `TurnStart` | A new turn begins (LLM call issued) |
| `TurnEnd` | A turn completes (LLM response received) |
| `ToolStart` | A tool call is dispatched |
| `ToolEnd` | A tool call returns |
| `Error` | An error is encountered in the agent loop |
| `StallDetected` | The agent loop detects a stall condition |

Filtered out (not forwarded): token-level streaming events and internal thinking events. These are high-frequency and not useful for monitoring.

### Event shape

The full event shape depends on the event type. Common fields:

```json
{
  "type":       "ToolStart",
  "session_id": 3,
  "turn_id":    7,
  "data":       { ... }
}
```

`data` is event-specific. For `ToolStart`/`ToolEnd`, it includes the tool name and arguments/result.

### Multiple consumers

Multiple clients can connect to `/api/events` simultaneously. Each gets its own subscription to the internal event bus — events are broadcast to all connected consumers.

---

## Metrics

```
GET /api/metrics
Authorization: Bearer <token>
```

Returns aggregate counts from the local database:

```json
{
  "sessions": 42,
  "memories": 1337,
  "jobs":     15
}
```

All values are integers. `sessions` counts all sessions ever created (not just active). `jobs` counts all job records. `memories` counts non-deleted memory entries.

This endpoint is suitable for a simple dashboard or alerting rule — poll it at whatever interval suits your needs.

```sh
curl -s -H "Authorization: Bearer <token>" http://localhost:1337/api/metrics | jq .
```

---

## Health endpoints

### `/healthz` — liveness probe

```
GET /healthz
```

No authentication required. Returns immediately with `200 OK`:

```json
{"status": "ok"}
```

Use this for load balancer health checks and liveness probes. It does not check the database or the agent loop — it only confirms the HTTP server is accepting connections.

```sh
curl http://localhost:1337/healthz
```

### `/api/health` — richer health

```
GET /api/health
Authorization: Bearer <token>
```

Returns:

```json
{
  "ok":             true,
  "version":        "0.4.0",
  "uptime_seconds": 86400,
  "db_path":        "/home/alice/.cairo/cairo.db"
}
```

Includes the binary version, process uptime, and the path to the database being used. Useful for debugging configuration drift across instances (e.g., wrong `CAIRO_DATA_DIR`).

---

## cairo-registry health

The registry exposes its own `/healthz` on both the public and admin listeners:

```
GET /healthz
```

No authentication required. Returns fleet-wide counts:

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

`stale` counts agents whose `last_seen_at` has exceeded the stale threshold. `ws_connected` counts agents with an open WebSocket liveness stream.

Via `cairo-ctl`:

```sh
cairo-ctl health
```

Or directly:

```sh
curl http://127.0.0.1:8081/healthz | jq .
```

---

## Monitoring recipe: watch for errors

```sh
curl -N -H "Authorization: Bearer <token>" http://localhost:1337/api/events \
  | grep '"type":"Error"'
```

## Monitoring recipe: poll metrics every 30 s

```sh
while true; do
  echo "$(date -u +%T) $(curl -s -H "Authorization: Bearer <token>" \
    http://localhost:1337/api/metrics)"
  sleep 30
done
```

## Monitoring recipe: liveness check in a shell script

```sh
if ! curl -sf http://localhost:1337/healthz > /dev/null; then
  echo "cairo serve is down" >&2
  exit 1
fi
```
