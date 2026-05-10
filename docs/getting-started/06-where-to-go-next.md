# Where to Go Next

You've installed Cairo, connected it to an LLM, had a first conversation, and started to customize it. What you do next depends on what you're trying to accomplish.

---

## For end users

You want to get more out of Cairo as a daily tool.

**Understand memory and identity**
Cairo's memory system is what makes it feel like a persistent colleague rather than a stateless chatbot. Learning how memories are stored, recalled, and decayed helps you work with it rather than against it.

- [docs/ai/memory-and-facts.md](../ai/memory-and-facts.md) — how memory works in detail
- [docs/ai/identity.md](../ai/identity.md) — what identity means in Cairo (soul, aspects, prompt parts)
- [docs/ai/sessions.md](../ai/sessions.md) — how sessions are structured and what gets saved

**Learn the built-in tools**
Cairo's LLM can call about 15 built-in tools: file read/write, shell commands, memory operations, codebase search, and more. Knowing what tools are available helps you ask for things more effectively.

- [docs/reference/tools.md](../reference/tools.md) — full tool catalog with descriptions

**Explore the full config surface**
There are more config keys than the ones covered in this guide. Setting the right summarizer model, adjusting memory scoring behavior, and configuring how Cairo handles large codebases are all controlled via config.

- [docs/reference/config-keys.md](../reference/config-keys.md) — every config key documented

**Build your own skills**
Skills let you package multi-step workflows so you don't have to re-explain the same sequence every time. The AI docs cover how to write and manage them.

- [docs/ai/skills.md](../ai/skills.md) — writing and managing skills
- [docs/ai/tools.md](../ai/tools.md) — the difference between tools and skills, and when to use each

**Learn about the dream cycle**
Cairo has a background maintenance cycle (the "dream" process) that summarizes sessions, promotes important memories, and does housekeeping. Understanding it helps you reason about what Cairo knows and doesn't know.

- [docs/ai/dreams.md](../ai/dreams.md) — what the dream cycle does and when it runs

---

## For team admins and fleet operators

You're deploying Cairo across a team, or you're running `cairo-registry` to manage multiple Cairo instances.

**Fleet architecture**
Cairo in fleet mode (`cairo serve --tsnet`) enrolls as a node in a registry. The registry (`cairo-registry`) is the control plane — it tracks live agents, manages enrollment, and provides admin endpoints.

- [docs/architecture/overview.md](../architecture/overview.md) — the overall system design
- [docs/architecture/server.md](../architecture/server.md) — the HTTP server and SSE bridge
- [docs/ROADMAP.md](../ROADMAP.md) — what's implemented vs. planned across the enterprise feature set

**Running cairo-registry**
`cairo-registry` is the fleet server. It maintains the agent ledger, handles registration and heartbeats, and exposes admin endpoints that `cairo-ctl` talks to.

```bash
cairo-registry
```

By default it listens on `:8080` (public) and `127.0.0.1:8081` (admin). See the server documentation for configuration.

**cairo-ctl**
`cairo-ctl` is the operator CLI. It talks to the admin listener (default `127.0.0.1:8081`) and lets you list agents, revoke them, and broadcast messages.

```bash
cairo-ctl list
cairo-ctl revoke <agent-id>
cairo-ctl broadcast "message to all agents"
```

Pass `--addr host:port` to point at a non-default registry address.

**Security model**
Cairo is designed around Zero Trust principles — every layer re-validates identity and emits to audit. The architecture documents describe the intended security posture and which packages implement which pillar.

- [docs/architecture/decisions.md](../architecture/decisions.md) — the load-bearing architectural decisions (D1–D12)

---

## For contributors and curious developers

You want to understand how Cairo is built, extend it, or contribute.

**Start with the architecture**
The design and decisions documents explain *why* Cairo is structured the way it is — the package map, the migration strategy from the original `~/cairo` codebase, the Zero Trust security layers, and the enterprise extension surface.

- [docs/architecture/design.md](../architecture/design.md) — the full migration plan and package map
- [docs/architecture/decisions.md](../architecture/decisions.md) — every load-bearing architectural decision with rationale
- [docs/architecture/agent-loop.md](../architecture/agent-loop.md) — how the agent loop works
- [docs/architecture/tui.md](../architecture/tui.md) — the Bubble Tea TUI architecture
- [docs/architecture/llm-client.md](../architecture/llm-client.md) — the LLM client interface

**Development workflows**
The development docs cover the day-to-day work: how to add a config key, a new tool, a database migration, a TUI panel.

- [docs/development/contributing.md](../development/contributing.md) — contributor onboarding and conventions
- [docs/development/building.md](../development/building.md) — build system details
- [docs/development/adding-a-tool.md](../development/adding-a-tool.md) — how to add a built-in tool
- [docs/development/adding-a-config-key.md](../development/adding-a-config-key.md) — how to add a config key
- [docs/development/adding-a-migration.md](../development/adding-a-migration.md) — how to add a database migration
- [docs/development/testing.md](../development/testing.md) — testing approach and conventions

**The AI-facing docs**
The `docs/ai/` directory is written for the AI model that runs inside Cairo — it's what the agent reads to understand its own system. If you're building features that interact with the agent loop or the identity system, these docs explain the contracts:

- [docs/ai/README.md](../ai/README.md) — overview of the AI docs
- [docs/ai/schema.md](../ai/schema.md) — the full database schema the agent can query
- [docs/ai/hooks.md](../ai/hooks.md) — lifecycle hooks the agent can use

**The whitepapers and research**
The `docs/architecture/research/` directory contains the exploratory work done before major structural decisions. If you're curious about *why* certain patterns were chosen over alternatives:

- [docs/architecture/research/go-project-layout-patterns.md](../architecture/research/go-project-layout-patterns.md)
- [docs/architecture/research/registry-merge-plan.md](../architecture/research/registry-merge-plan.md)
- [docs/architecture/research/server-api-surface.md](../architecture/research/server-api-surface.md)

---

## Quick reference

| I want to... | Start here |
|---|---|
| Understand memory in depth | [docs/ai/memory-and-facts.md](../ai/memory-and-facts.md) |
| See all available tools | [docs/reference/tools.md](../reference/tools.md) |
| See all config keys | [docs/reference/config-keys.md](../reference/config-keys.md) |
| Deploy Cairo for a team | [docs/architecture/overview.md](../architecture/overview.md) |
| Add a feature or fix a bug | [docs/development/contributing.md](../development/contributing.md) |
| Understand the overall design | [docs/architecture/design.md](../architecture/design.md) |
| See what's planned | [docs/ROADMAP.md](../ROADMAP.md) |
