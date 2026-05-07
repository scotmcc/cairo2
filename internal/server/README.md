# internal/server

**Layer:** Presentation  
**Status:** ✅ working → gains missing endpoints (Phase 3)

The cairo agent's local HTTP API. Runs when `cairo serve` is active.

## Current endpoints

- `POST /api/chat` — send a message to the agent
- `GET /api/events` — SSE stream of agent events
- `POST /v1/chat/completions` — OpenAI-compatible API
- `POST /rpc` — JSON-RPC surface
- `GET /healthz` — liveness probe

## Endpoints to add (Phase 3)

Sessions, config, roles, consider aspects, metrics — replacing web-agent's direct SQLite reads.
See `~/cairo2/docs/architecture/research/server-api-surface.md` for the full list.

Source: `~/cairo/internal/server/`.
