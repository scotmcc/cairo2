# Cairo

Private. Context-Aware. Enterprise-Ready.

Cairo is an AI coding agent and enterprise intelligence platform. The agent binary (`cairo`) is the brain — deployable standalone on any developer's box, or enrolled in the enterprise fleet via `cairo serve --tsnet`. The enterprise stack layers security, routing, knowledge, and governance on top without changing the agent.

## Repository layout

```
cmd/
  cairo/              The agent binary — TUI, HTTP API, fleet node
  cairo-registry/     Enterprise control plane backend
  cairo-ctl/          Operator and admin CLI

internal/
  agent/              Core agent loop — the brain
  llm/                LLM client (Ollama, OpenAI-compatible)
  tools/              Tool registry + built-in tools
  learn/              Codebase and document indexing (RAG)
  store/              Local SQLite persistence (agent-private)
  connectors/         Enterprise data source adapters (Qdrant, Postgres, Neo4j, S3)
  services/           AI application surfaces (Code Assist, DocQA, Automation, Analytics)
  registryserver/     Fleet registry server logic
  registry/           Agent-side fleet client
  protocol/           Wire types — single source of truth
  authn/              Authentication (tsnet → OIDC/SAML)
  access/             RBAC, departments, access policy
  audit/              Immutable audit log
  guardrails/         Content safety, PII detection
  telemetry/          Metrics, health, monitoring
  server/             Agent HTTP API
  tui/                Bubble Tea TUI
  cli/                Non-TUI CLI modes

web-agent/            Node/React browser UI
vscode-extension/     VS Code extension
scripts/packaging/    RPM/deb build, systemd units
docs/architecture/    Design documents and research
```

## Package status legend

- ✅ Working — migrates from `~/cairo` as-is
- 🔄 Reorganizing — mechanical restructure, no behavior change
- 🔲 Slot — directory scaffolded, implementation pending

See each package's `README.md` for status and responsibilities.  
See `docs/architecture/decisions.md` for the load-bearing architectural decisions.  
See `docs/architecture/design.md` for the full migration plan.

## Security model: Zero Trust

Every layer is a gate. No layer inherits trust from a previous layer. Every gate re-validates and emits to audit.

```
User → [Blazor: User gate — SSO/MFA]
     → [authn: token validation]
     → [registryserver: Device gate — node posture]
     → [access: Application gate — per-session policy decision]
     → [agent: re-validates forwarded identity]
     → [connectors: Data gate — source-level access control]
     → [audit: every gate logs here]
```

The seven ZT pillars map directly to packages. See `docs/architecture/decisions.md` D11 for the full mapping.

## Deployment modes

| Mode | Command | What it does |
|---|---|---|
| Standalone TUI | `cairo` | Local coding agent, no network |
| HTTP serve | `cairo serve` | Adds local HTTP API |
| Fleet node | `cairo serve --tsnet` | Enrolls in enterprise fleet |
| Enterprise registry | `cairo-registry` | Runs the control plane |
