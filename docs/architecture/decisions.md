# Architecture Decisions

**Session:** 2026-05-07  
**Authors:** Scot + Selene

These are the load-bearing decisions made during the cairo2 scaffolding session. Each decision has a rationale and the alternatives considered. Revisit these before changing structure, not after.

---

## D1 — cairo2 is the enterprise solution; cairo is the brain inside it

**Decision:** `~/cairo2` is the target home for the full enterprise system. The cairo agent binary (`cmd/cairo`) is the AI intelligence layer — reusable standalone, and also a node inside the enterprise ecosystem.

**Rationale:** The enterprise diagram shows multiple layers (security, services, connectors, governance) that don't belong in the agent binary. The agent should be deployable anywhere — a developer's laptop, a DoD enclave, a shared server — without pulling in enterprise dependencies. Enterprise capability is layered on top, not baked in.

**Alternative considered:** Grow ~/cairo into the enterprise system directly. Rejected because it would entangle the standalone agent with enterprise concerns, making standalone deployment heavier and harder to reason about.

---

## D2 — Single `go.mod`, no workspace

**Decision:** One `go.mod` at the repo root. All binaries in `cmd/`. No `go.work`.

**Rationale:** Cairo is a product, not a collection of publishable libraries. Everything ships together. The two-repo situation (cairo + cairo-registry) caused protocol type drift that required a documented "sync ritual" — the exact cost of having two modules. One module eliminates that class of problem entirely.

**Alternative considered:** `go.work` workspace with multiple modules. Rejected because Cairo doesn't publish any module, so there's nothing for workspace coordination to buy.

---

## D3 — Three binaries

**Decision:** `cmd/cairo` (the agent), `cmd/cairo-registry` (fleet registry + enterprise gateway), `cmd/cairo-ctl` (operator CLI).

**Rationale:** The agent and the registry have fundamentally different deployment shapes — the agent runs on dev boxes, the registry runs centrally. They shouldn't be one binary. `cairo-ctl` pairs with the registry as its operator interface.

**Alternative considered:** Single binary with subcommands. Rejected because the registry runs as a service on infrastructure hosts where the full agent binary isn't appropriate.

---

## D4 — `internal/store/` split from `internal/db/`

**Decision:** The current `internal/db/` (~12K LOC, ~14 responsibilities) splits into focused sub-packages under `internal/store/`.

**Rationale:** `internal/db/` is three things in a trenchcoat: schema management, config store, memory engine, session store, job store, identity store, and vector index. Each of these has independent change velocity. Splitting them makes each independently testable and maintainable.

**Alternative considered:** Keep `internal/db/` and add sub-directories inside it. Rejected in favor of the rename (`store/`) which signals the broader role — it's not just SQL anymore.

---

## D5 — `internal/connectors/` for enterprise data sources

**Decision:** External enterprise systems (Qdrant, Postgres, Neo4j, S3) get adapters in `internal/connectors/`. They implement interfaces defined in domain packages (`store/index.VectorStore`, etc.). Wired in `cmd/cairo/app.go` based on config.

**Rationale:** The agent brain must not change between standalone and enterprise deployments. The interface/adapter pattern lets `app.go` swap backends at startup. Standalone: SQLite. Enterprise: Qdrant/Postgres/Neo4j. The loop is identical.

**Key insight:** `internal/store/` is the agent's private local data (always SQLite, always present). `internal/connectors/` is the enterprise shared data plane (optional, config-driven).

---

## D6 — Agent types: personal, departmental, enterprise

**Decision:** cairo nodes enrolled in the fleet are classified into three types, stored in `store/registrations/` and enforced by `internal/access/`.

| Type | Who runs it | Who can address it |
|---|---|---|
| Personal | Dev's own box | Owner + super-admins |
| Departmental | Shared server | Dept members + dept-leads + super-admins |
| Enterprise | Central org host | All authenticated users |

**Rationale:** A DoD enclave, a small company, or a department all have the same pattern: some agents are private, some are shared with a group, some are org-wide. RBAC controls visibility, not data — each agent's SQLite is its own, so there's no co-mingling by design.

---

## D7 — `internal/authn/` and `internal/access/` are separate packages

**Decision:** Authentication (who you are) and authorization (what you can do) are separate packages, not one `auth/` package.

**Rationale:** They evolve at different speeds. `authn/` changes when we add OIDC/SAML. `access/` changes when we add department features. Keeping them separate means each can be built and tested independently.

**Current state:** `authn/` is a slot — tsnet already handles authentication implicitly. `access/` is a slot — the registry enforces a simple owner-match today.

---

## D8 — `internal/services/` for AI application surfaces

**Decision:** The four surfaces from the enterprise diagram (Code Assistant, Document Q&A, Automation Hub, Analytics) live as sub-packages under `internal/services/`. They are not separate binaries.

**Rationale:** These surfaces are capabilities, not deployments. They compose the foundation layer and expose specific behaviors to the enterprise control plane. The control plane routes user requests to the right surface; binaries don't change.

**Note:** `codeassist/` is what cairo already is — the slot just formalizes it. The other three are genuinely new capabilities to build.

---

## D9 — Automation backends as tool extensions

**Decision:** N8n and Ansible live under `internal/services/automation/` and register their workflows/playbooks as tools in the cairo tool registry at startup. The agent invokes them the same way it invokes built-in tools.

**Rationale:** The agent loop doesn't need to know the automation backend is external. Registering automation as tools means the agent can use natural language to trigger them with no new code in the loop.

---

## D10 — `internal/audit/` is a write-only sink

**Decision:** The audit package is append-only. Other packages call `audit.Log(event)` and never read back through this package. Compliance export is via a separate query path.

**Rationale:** Simplifies the package contract (no risk of reads affecting writes), makes it easy to swap the backend (SQLite → immutable log service), and keeps the audit log trustworthy — nothing in the normal code path reads and potentially mutates it.

---

## D11 — Zero Trust as the security model

**Decision:** Cairo's security architecture follows the Zero Trust framework. Every layer is a gate. No layer inherits trust from a previous layer. Every gate re-validates and emits to audit.

**Rationale:** The target market (enterprise, DoD enclaves, sensitive data environments) expects Zero Trust. More importantly, ZT is the right model for a system where agents run on untrusted networks, data crosses organizational boundaries, and the same binary serves both a developer's laptop and a classified enclave. Building ZT in from the start is far less painful than retrofitting it.

**The seven pillars, mapped to this codebase:**

| Pillar | Implementation |
|---|---|
| **User** | `internal/authn/` — identity verified at every request, not just login. Blazor is the user gate: SSO redirect, MFA, token issuance. |
| **Device** | tsnet node key + `store/registrations/` — cairo enrollment = device registration. Node posture tracked via heartbeat. |
| **Network** | tsnet — Tailscale WireGuard mesh. This pillar is provided, not built. No implicit trust based on network position. |
| **Application** | `internal/access/` — per-session access decisions. The registry re-validates on every routing request, not just at enrollment. |
| **Data** | `internal/guardrails/` (content/PII scanning) + connector-level access validation (Qdrant, Postgres, Neo4j each gate their own data). |
| **Visibility & Analytics** | `internal/telemetry/` + `internal/audit/` — continuous monitoring, anomaly detection, immutable event log. |
| **Automation & Orchestration** | `internal/services/automation/` — automated response to policy violations, runbook execution. |

**The gate chain for a user chat request:**

```
User → [Blazor: identity gate] → [Registry: device gate] → [Registry: access gate] → [Agent: application gate] → [Connector: data gate] → [Audit: all gates log here]
```

Each gate validates independently. If any gate fails, the request stops. The agent does not trust that the registry already approved the request — it validates the forwarded identity itself.

**Key principles this establishes for all future code:**

1. **Verify explicitly** — authenticate and authorize every request at every layer, never assume.
2. **Least privilege** — every access grant is scoped to the minimum necessary (session-scoped, not persistent).
3. **Per-session authorization** — "registered" does not mean "trusted forever." The registry re-evaluates on every session routing decision.
4. **Assume breach** — design each layer as if the previous layer has already been compromised. The data gate doesn't trust the application gate; the application gate doesn't trust the network gate.
5. **Log everything** — every gate emits to `internal/audit/`. Silence in the audit log is a red flag, not a green one.

**On IDP:** We are not building a full Identity Provider. tsnet provides the network pillar and device identity. Blazor integrates with an existing IDP (Okta, Azure AD, or any OIDC provider) for the user pillar. The Go layer validates tokens issued by that IDP. We build gates, not the identity infrastructure.

**Alternative considered:** Perimeter-based security (trust the network, trust internal traffic). Rejected because cairo nodes run on untrusted developer machines, cross organizational network boundaries, and must operate correctly in zero-trust network environments (DoD IL4/IL5). A perimeter model would be wrong from day one.

---

## Open questions (for future decision)

1. Does `cairo-registry` eventually become the enterprise gateway (proxying chat sessions to agent nodes), or does that grow into a separate `cmd/cairo-gateway`? Current answer: registry grows into it; split if it gets too large.
2. Should `services/` packages eventually become separately deployable services (microservices) or stay as packages within the registry binary? Current answer: packages; promote to binaries only if deployment requirements force it.
3. When does the `web-agent/` Node.js UI become the enterprise UI, vs. staying as a developer tool while cairo-ui (.NET/Blazor) is the enterprise UI? Current answer: they serve different audiences; both stay.
