# Sessions

## GET /api/sessions

List all sessions ordered by `last_active` descending. Auth-gated. Source: `internal/server/api_read.go:58`.

Returns a **bare array** — not wrapped in an object.

**Response 200**

```json
[
  {
    "id": 7,                    // int64
    "name": "feature work",     // string — empty string when unnamed
    "cwd": "/home/scot/cairo2", // string
    "role": "default",          // string
    "created_at": 1746700000,   // int64 — Unix epoch seconds
    "last_active": 1746712345,  // int64 — Unix epoch seconds
    "insight": "can you look at the reg" // string — first 80 chars of the latest user message; "" when no messages
  }
  // ...
]
```

Note: `sessionListItem` is defined at `internal/server/api_read.go:48`. It is a projection of `sessions.Session` — it does **not** include `discipline_mode`.

**curl**

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/sessions
```

---

## GET /api/sessions/{id}

Get a single session by numeric ID. Auth-gated. Source: `internal/server/api_read.go:87`.

**Response 200** — full `sessions.Session` struct (`internal/store/sessions/sessions.go:8`):

```json
{
  "id": 7,
  "name": "feature work",
  "cwd": "/home/scot/cairo2",
  "role": "default",
  "discipline_mode": 3,           // int — 1=readonly | 2=scoped | 3=full (default)
  "created_at": "2026-05-09T...", // time.Time, RFC3339
  "last_active": "2026-05-09T..."  // time.Time, RFC3339
}
```

**Response 404**

```json
{"error": "session not found"}
```

**curl**

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/sessions/7
```

---

## PATCH /api/sessions/{id}

Rename a session. Auth-gated. Source: `internal/server/api_mutations.go:24`.

**Request body**

```json
{"name": "new name"}
```

**Response 200** — full `sessions.Session` (same shape as GET /api/sessions/{id}).

**Response 404**

```json
{"error": "session not found"}
```

**curl**

```bash
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name": "registry work"}' \
  http://localhost:8080/api/sessions/7
```

---

## DELETE /api/sessions/{id}

Delete a session and cascade-delete its messages, summaries, facts, jobs, and tasks. Auth-gated. Source: `internal/server/api_mutations.go:53`.

Returns 404 on a missing session (R6 mitigation — no silent no-ops).

**Response 204** — no body.

**Response 404**

```json
{"error": "session not found"}
```

**curl**

```bash
curl -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/sessions/7
```

---

## GET /api/sessions/{id}/messages

Paginated message history for a session. Auth-gated. Source: `internal/server/api_read.go:105`.

**Query parameters**

| Param | Default | Max | Description |
|-------|---------|-----|-------------|
| `limit` | 50 | 200 | Number of messages to return |
| `before` | 0 | — | Return messages with `id < before`; pass 0 to start from the most recent |

**Response 200** — array of `sessions.Message` (`internal/store/sessions/messages.go:26`).

Returns `[]` (never `null`) when no messages match.

```json
[
  {
    "ID": 101,
    "SessionID": 7,
    "Role": "user",            // "user" | "assistant" | "tool"
    "Content": "hello",
    "ToolCalls": "",           // string — JSON array of tool calls; "" when none
    "ToolName": "",            // string — non-empty for role="tool" rows
    "ToolID": "",              // string — non-empty for role="tool" rows
    "InnerVoice": "",          // string — consider summary that framed this user message; "" when not set
    "ToolStatus": "",          // string — "ok" | "error" | "" (role="tool" rows only)
    "ToolLatencyMs": 0,        // int64 — wall-time ms for tool call; 0 on non-tool rows
    "ReviewedAt": "0001-01-01T00:00:00Z", // time.Time — zero when not reviewed
    "CreatedAt": "2026-05-09T12:00:00Z"
  }
  // ...
]
```

> **Note:** The `Message` struct uses exported field names (uppercase) without `json:` tags, so the wire keys are capitalized. This is a known inconsistency with the rest of the API where lowercase keys are used. Do not rely on this being fixed without a version bump.

**Cursor pagination**

To page backward through history, use the `ID` from the oldest message in the current page as the next `before` value:

```bash
# First page (50 most recent)
curl ".../messages?limit=50"

# Next page
curl ".../messages?limit=50&before=101"
```

**curl**

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/sessions/7/messages?limit=20"
```
