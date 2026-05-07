# internal/access

**Layer:** Zero Trust — Application Pillar  
**ZT Gate:** Policy Decision Point (PDP)  
**Status:** 🔲 SLOT

*Least privilege. Per-session authorization. Never persistent grants.*

This is the Application gate — the Policy Decision Point. Given a verified identity from `internal/authn/` and a target resource (an agent, a service, a data scope), it answers: "Is this access allowed, for this session, right now?"

The registry (`internal/registryserver/`) is the Policy Enforcement Point (PEP) — it calls this package and acts on the decision. This package only decides; it does not enforce.

## Zero Trust principles this gate enforces

- **Verify explicitly** — access is re-evaluated on every routing request, not at enrollment
- **Least privilege** — grants are scoped to the minimum: this user, this agent, this session type
- **Assume breach** — a previously valid grant does not carry forward; each session is independent
- **Dynamic policy** — decisions can change based on time of day, device posture, behavioral signals

## The agent access model

| Agent type | Who can address it |
|---|---|
| Personal | Owner only + super-admins |
| Departmental | Department members + department leads + super-admins |
| Enterprise | All authenticated users |

## Responsibilities

- `CanAddress(identity, agentID, sessionContext) (bool, reason)` — the primary decision function
- Department and role management (create dept, assign user to dept, assign role)
- Agent catalog scoping: `ListVisible(identity) []AgentID` — what can this user see?
- Super-admin bypass (explicit flag, always audit-logged)
- Policy evaluation against device posture signals from `store/registrations/`

## What changes per-session vs. persistent

**Persistent (stored in `store/registrations/`):** agent type, owner, department association, revocation status.

**Per-session (evaluated at request time):** device posture score, user's current role set, time-based restrictions, behavioral anomaly signals from `internal/telemetry/`.

## Gate position in the chain

```
... → [authn: identity verified] → [access: PDP — is this allowed?] → [registryserver: PEP — enforce decision] → [agent: re-validate] → ...
```

Build when: the first multi-user enterprise deployment is configured.
