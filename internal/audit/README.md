# internal/audit

**Layer:** Zero Trust — Visibility & Analytics Pillar (cross-cutting)  
**ZT Role:** Immutable audit trail — receives from every gate  
**Status:** 🔲 SLOT

*Log everything. Silence in the audit log is a red flag, not a green one.*

Every ZT gate emits to audit. This package is the single sink. It is write-only from the perspective of all other packages — nobody reads back through `audit` to make a decision. It exists to answer "what happened?" after the fact, and to feed `internal/telemetry/` for anomaly detection.

## Zero Trust principles this package enforces

- **Assume breach** — the audit log is how you detect and reconstruct an incident
- **Verify explicitly** — audit log entries are themselves integrity-protected (append-only, tamper-evident)
- **Log everything** — a missing audit event for a sensitive operation is a compliance failure

## Event types (every gate emits here)

| Gate | Events |
|---|---|
| User (authn) | `user_authenticated`, `token_rejected`, `session_expired`, `mfa_challenged` |
| Device (registry enrollment) | `node_enrolled`, `node_revoked`, `heartbeat_missed`, `posture_degraded` |
| Application (access) | `access_granted`, `access_denied`, `policy_evaluated`, `superadmin_override` |
| Data (connectors) | `data_accessed`, `data_access_denied`, `pii_detected` |
| Guardrails | `guardrail_fired`, `content_blocked`, `pii_masked` |
| Admin (cairo-ctl) | `broadcast_sent`, `agent_revoked`, `department_modified`, `role_assigned` |

## Responsibilities

- Append-only event log (no updates, no deletes — ever)
- Structured event format: timestamp, gate, actor, target, action, outcome, session ID, source IP
- Integrity protection: hash-chaining or external append-only store (S3, immutable blob)
- Retention policy enforcement (configurable: 90 days, 1 year, indefinite)
- Export API for compliance: SOC 2, HIPAA, GDPR, DoD IL4/IL5 audit requirements

## Gate position in the chain

```
Every gate → [audit.Log(event)] → immutable log
                                 ↓
                          [telemetry: anomaly detection]
```

## Usage contract

```go
// All other packages call this — never the reverse.
audit.Log(ctx, audit.Event{
    Gate:    audit.GateAccess,
    Actor:   identity.UserID,
    Target:  agentID,
    Action:  "address",
    Outcome: audit.OutcomeDenied,
    Reason:  "not a department member",
})
```

Build when: the first compliance-regulated deployment is configured. Wire the stub (no-op logger) immediately so all packages can call `audit.Log()` from day one without import cycles.
