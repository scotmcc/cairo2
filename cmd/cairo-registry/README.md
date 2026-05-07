# cmd/cairo-registry

**Layer:** Enterprise Control Plane — Backend  
**Status:** 🔄 merging from `~/cairo-registry`

The fleet registry server and enterprise gateway. Runs centrally (one per org or per enclave).

Tracks all enrolled cairo nodes: who they belong to, what department, what type (personal / departmental / enterprise), and whether they're alive. Enforces access policy: validates that a user is allowed to address a given agent before proxying the conversation to it.

## Responsibilities

- Fleet registration and heartbeat tracking
- Agent catalog (type, owner, department, access policy)
- Enterprise gateway: routes chat sessions from cairo-ui to specific agent nodes
- Access policy enforcement (delegates to `internal/access/`)
- Audit event emission (delegates to `internal/audit/`)
- Admin API for `cairo-ctl`

## Source

Merges from `~/cairo-registry/cmd/cairo-registry/` and `~/cairo-registry/cmd/cairo-ctl/`.
