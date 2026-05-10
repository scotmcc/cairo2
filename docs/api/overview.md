# Cairo HTTP API — Overview

## Base URL

```
http://<host>:<port>
```

Default dev port: `8080`. No path prefix on `/api/*` routes — they are stable and breaking changes get a new path rather than a version segment.

## Authentication

When cairo is started with `--auth`, every `/api/*` route (except `/api/health` and `/healthz`) requires a Bearer token.

```
Authorization: Bearer <token>
```

Generate a token with `cairo token`. Tokens are HMAC-signed and validated with constant-time comparison.

Routes without auth configured accept all requests. Auth middleware source: `internal/server/server.go:104`.

### 401 response

```json
{"error": "unauthorized"}
```

## Error Envelope

All errors use the same shape:

```json
{"error": "<message string>"}
```

HTTP status codes used:

| Code | When |
|------|------|
| 400  | Bad JSON, invalid path parameter, unsupported field value |
| 401  | Missing or invalid Bearer token |
| 404  | Row not found (session, role, aspect) |
| 500  | Database or internal error |

`writeJSONError` is defined at `internal/server/openai.go:240` and used by every handler.

## Content-Type

All JSON responses carry `Content-Type: application/json`. SSE streams carry `Content-Type: text/event-stream`.

## Versioning Posture

No `/v1` prefix. `/api/*` routes are stable; if a breaking change is necessary, a new path is introduced alongside the old one. The old path is not removed until clients have migrated.

## Rate Limits

None. Cairo is designed for single-user or small-team deployments. Callers are responsible for their own throttling.

## Public vs. Auth-Gated Routes

| Route | Auth required |
|-------|--------------|
| `GET /healthz` | No |
| `GET /api/health` | No |
| All other `/api/*` | Yes (when `--auth` is set) |
| `/v1/*` (OpenAI compat) | Yes (when `--auth` is set) |
| `POST /rpc`, `GET /rpc/stream/` | Yes (when `--auth` is set) |

## Chat vs. Events vs. Read API

Three overlapping surfaces:

- **`POST /api/chat`** — send a message, get a response (blocking or SSE streaming). This is the primary integration point for UIs.
- **`GET /api/events`** — SSE observer stream of agent lifecycle events. Read-only. Does not send messages; use alongside `/api/chat` to watch what the agent is doing.
- **`/api/sessions`, `/api/config`, `/api/roles`, `/api/consider`** — management plane. CRUD for sessions, config, roles, consider-aspects.
