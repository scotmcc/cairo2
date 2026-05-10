# Events (SSE)

## GET /api/events

Server-Sent Events stream of agent lifecycle events. Auth-gated. Source: `internal/server/api_events.go:22`.

Connect once; the stream stays open until the client closes the connection or the server shuts down.

### Connection lifecycle

1. Client opens a GET request with `Accept: text/event-stream` (or equivalent).
2. Server subscribes to the agent's event bus (`agent.Bus.Subscribe`) — a buffered channel of 512 events.
3. Each allowed event is JSON-serialized and written as `data: <json>\n\n`.
4. When the request context is cancelled (client disconnects), the handler returns and calls `unsub()`, removing the channel from the bus.

If the client falls behind and the 512-event buffer fills, events are **dropped** for that subscriber. The bus logs each drop with a count. Reconnect to reset.

### Event allowlist

Only the following `type` values are forwarded to SSE subscribers. All others (including `tokens`, `thinking`, step heartbeats, consider internals, job-review actions) are filtered out at `internal/server/api_events.go:11`.

| `type` | Meaning |
|--------|---------|
| `agent_start` | Agent loop started a new turn sequence |
| `agent_end` | Agent loop finished (all turns complete) |
| `turn_start` | A single LLM turn began |
| `turn_end` | A single LLM turn finished; `payload.HasMore` indicates whether the agent will take another turn |
| `tool_start` | A tool call began; payload includes `Name`, `Args`, `PID` |
| `tool_end` | A tool call finished; payload includes `Name`, `Result`, `IsError` |
| `error` | An error occurred in the agent loop; payload includes `Err` |
| `stall_detected` | The model ended a turn with forward-looking intent text but zero tool calls — prompt it to "continue" |

### Event wire shape

Each `data:` line carries a JSON-encoded `agent.Event` (`internal/agent/events.go:56`):

```json
{
  "Type": "tool_start",
  "Payload": {
    "Name": "bash",
    "Args": {"command": "go build ./..."},
    "PID": 12345
  }
}
```

> **Note:** `Type` and `Payload` use uppercase keys — the `Event` struct has no `json:` tags. Payload shape varies by event type; type-assert on `Type` before using `Payload`.

### Payload shapes by type

| `Type` | `Payload` fields |
|--------|-----------------|
| `agent_start` | `nil` |
| `agent_end` | `nil` |
| `turn_start` | `nil` |
| `turn_end` | `HasMore bool` |
| `tool_start` | `Name string`, `Args map[string]any`, `PID int` (non-zero for bash subprocess) |
| `tool_end` | `Name string`, `Result string`, `IsError bool` |
| `error` | `Err` (serialized as `{}` — the `error` interface does not JSON-serialize cleanly; treat as a signal, not a message carrier) |
| `stall_detected` | `nil` |

> **Ambiguity flag:** `error` payload — `PayloadError{Err error}` at `internal/agent/events.go:79`. The `error` interface marshals to `{}` in standard Go JSON encoding. If you need the error text, watch the server logs or check `GET /api/health`. This may be improved in a future phase.

### curl example

```bash
curl -N \
  -H "Authorization: Bearer $TOKEN" \
  -H "Accept: text/event-stream" \
  http://localhost:8080/api/events
```

Sample output while a turn runs:

```
data: {"Type":"agent_start","Payload":null}

data: {"Type":"turn_start","Payload":null}

data: {"Type":"tool_start","Payload":{"Name":"bash","Args":{"command":"go build ./..."},"PID":18432}}

data: {"Type":"tool_end","Payload":{"Name":"bash","Result":"","IsError":false}}

data: {"Type":"turn_end","Payload":{"HasMore":false}}

data: {"Type":"agent_end","Payload":null}
```

### Reconnection

The server does not emit SSE `id:` or `retry:` headers. Implement reconnect logic in the client (exponential backoff recommended). Events emitted while the client is disconnected are not replayed — the stream is live-only.

### Concurrent subscribers

Multiple clients may subscribe simultaneously. Each gets its own 512-event buffer. Slow clients are dropped independently without affecting others.
