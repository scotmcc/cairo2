# internal/knowledge

**Layer:** Zero Trust — Data Pillar + Inter-Agent IPC  
**ZT Gate:** Data classification + knowledge flow control  
**Status:** 🔲 SLOT

*Read down freely. Write up with approval. Never write down automatically.*

The knowledge federation layer. Manages how agents discover, query, and contribute to the shared knowledge base — while enforcing information flow rules derived from the classification model used in sensitive data environments.

## The information flow model

Knowledge exists at three scopes, ordered from most open (enterprise) to most private (personal):

| Scope | Who can read | Who can write | Example |
|---|---|---|---|
| `enterprise` | All authenticated agents | Approved contributions only | Company docs, shared research, policies |
| `department:{id}` | Dept members + leads | Dept members (lead approval) | Project docs, dept-specific procedures |
| `personal:{user}` | Owner only | Owner freely | Private notes, WIP, sensitive work |

**Flow rules (analogous to classified systems):**
- **Read down is free** — any agent can query enterprise knowledge. A personal agent reading company docs is normal and expected.
- **Write up requires approval** — a personal agent contributing a finding to the enterprise knowledge base goes through a review gate before indexing. It doesn't happen automatically.
- **No write down** — enterprise knowledge never automatically flows into a departmental or personal scope. Agents pull what they need; nothing is pushed down.
- **Enclave isolation** — a department or enclave operating at a higher sensitivity level does not automatically share its findings with the enterprise base. Cross-scope contribution is always explicit and authorized.

## DDIL knowledge tiers

In Disconnected, Degraded, Intermittent, or Low-bandwidth environments, agents fall back through tiers:

```
L1 → Local SQLite index       (always available, no network)
L2 → Shared Qdrant            (enterprise network, no internet required)
L3 → Main agent synthesis     (enterprise network, main agent running)
L4 → Internet search          (full connectivity)
```

An agent exhausts each tier before escalating. In a fully air-gapped enclave, L1 + L2 is the operating model.

## Agent-to-agent knowledge queries (IPC)

When a personal agent needs synthesized knowledge (not just a vector search result), it sends a `KnowledgeQuery` frame via the registry:

```
Personal agent → [KnowledgeQuery{scopes: ["enterprise"], query: "post-quantum crypto"}]
               → registry routes to main agent
               → main agent synthesizes from enterprise scope only
               → KnowledgeResponse returned to requesting agent
               → requesting agent continues its task
```

The main agent responds **only from scopes the requester is authorized to read**. It does not expose dept or personal knowledge to other agents.

## Knowledge contribution (write-up approval flow)

When an agent finds something worth contributing to a broader scope:

```
Personal agent → [KnowledgeContribution{from: "personal:user", to: "enterprise", content: ...}]
               → approval gate (main agent review or admin approval)
               → on approval: learn/ pipeline indexes into enterprise scope
               → KnowledgeContributionAck returned
```

Contributions can be rejected (stays personal), approved (indexed at target scope), or escalated (needs human admin review).

## Responsibilities

- `scopes.go` — scope definitions, flow rule enforcement, scope hierarchy
- `federation.go` — route knowledge queries to right store or agent based on requester's authorized scopes
- `contribution.go` — write-up approval flow: submit, review, approve/reject, index
- `sync.go` — pull-on-demand caching: cache enterprise knowledge locally for DDIL resilience, with staleness TTL

## Wire protocol (extends internal/protocol/)

Two new frame types added to the registry wire protocol:

- `KnowledgeQuery{scopes[], query, requester_id, session_id}` — routed by registry to the right knowledge store or agent
- `KnowledgeContribution{from_scope, to_scope, content, contributor_id, summary}` — approval-gated write-up request

## Relationship to other packages

- `internal/store/index/` — defines `VectorStore` interface with scope dimension; knowledge/federation queries it
- `internal/connectors/qdrant/` — enterprise-scale VectorStore implementation with per-scope namespace isolation
- `internal/access/` — knowledge/ calls access to verify read/write permissions before any query or contribution
- `internal/audit/` — every knowledge query and contribution attempt is logged
- `internal/learn/` — handles the actual indexing when a contribution is approved
- `internal/registryserver/` — routes KnowledgeQuery frames between agents

Build when: the first multi-agent enterprise deployment needs shared knowledge access.
