# internal/registry

**Layer:** Fleet — Agent-side client  
**Status:** 🔄 consolidating from `registry/` + `registry-client/`

The cairo agent's fleet client. Used by `cairo serve --tsnet` to enroll in the fleet and maintain liveness.

## Responsibilities

- `Register()` — POST to registry, get agent ID back
- `HeartbeatLoop()` — periodic heartbeat, updates last-seen
- `LivenessStream()` — WebSocket stream, keeps `ws_connected: 1` alive
- Reconnect with backoff on disconnect

Consolidated from `~/cairo/internal/registry/` (LivenessStream) and `~/cairo/internal/registry-client/` (Register + Heartbeat) — these were split by accident during a merge conflict.
