# internal/registryserver

**Layer:** Fleet + Zero Trust — Device Pillar + Policy Enforcement Point (PEP)  
**ZT Gate:** Device gate + enforcement of access decisions  
**Status:** 🔄 merging from `~/cairo-registry/internal/`

The fleet registry server logic. Runs inside `cmd/cairo-registry`.

In ZT terms: this is the **Policy Enforcement Point**. It calls `internal/access/` (the PDP) on every routing request and enforces the decision — allowing, denying, or challenging. It also maintains device posture (node health, enrollment status) for the Device pillar.

## Responsibilities (current)

- HTTP handlers for registration, heartbeat, WebSocket liveness
- SQLite ledger (`store/registrations/`) for agent state
- Admin API endpoints (list, get, revoke, broadcast)

## Responsibilities (growing into)

- **Enterprise gateway**: validate access policy, proxy chat sessions from cairo-ui to a specific agent node
- **Department management**: create/assign departments and roles
- **Audit emission**: write events to `internal/audit/` for every access decision

Source: `~/cairo-registry/internal/registry/` and `~/cairo-registry/internal/ledger/`.
