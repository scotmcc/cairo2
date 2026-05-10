# Cairo documentation

Cairo — **C**ollaborative **A**I **R**hizomatic **O**rchestrator — is a local-first coding harness backed by SQLite and any OpenAI-compatible LLM endpoint. One being, many parallel threads of attention, with its complete identity stored in a single `.db` file.

This directory is the full documentation. The top-level [`README.md`](../README.md) is the landing page; start there if you're new. [`ROADMAP.md`](../ROADMAP.md) covers where the project is headed.

The docs are organized **by audience** — pick the section that matches what you're trying to do.

---

## I'm a new user — set me up

Read in order. Friendly hand-holding tone, ~30 minutes start to finish.

- [01. What is cairo](getting-started/01-what-is-cairo.md)
- [02. Installation](getting-started/02-installation.md) — RPM/DEB, install.sh, or build from source
- [03. First run](getting-started/03-first-run.md) — TUI vs line CLI, configuring your LLM
- [04. Your first chat](getting-started/04-your-first-chat.md) — a real walkthrough
- [05. Customizing](getting-started/05-customizing.md) — roles, aspects, config
- [06. Where to go next](getting-started/06-where-to-go-next.md)

Topic deep-dives: [Portable identity](getting-started/portable-identity.md), [Skills](getting-started/skills.md).

## I'm using cairo day-to-day

End-user reference for someone who already has cairo installed.

- [Sessions](user/sessions.md) — create, switch, delete, export
- [Slash commands](user/slash-commands.md) — full reference
- [Roles and aspects](user/roles-and-aspects.md) — switch persona + tool allowlist
- [Memory](user/memory.md) — how cairo remembers, importance vs weight
- [Tools](user/tools.md) — what cairo can do for you
- [Web UI](user/web-ui.md) — browser interface via cairo-web
- [Troubleshooting](user/troubleshooting.md) — common issues and fixes

## I'm running cairo for a team — fleet operator

Admin docs for `cairo serve`, `cairo-registry`, and `cairo-ctl`.

- [cairo serve](admin/cairo-serve.md) — running cairo as an HTTP service
- [Registry setup](admin/registry-setup.md) — fleet coordination
- [cairo-ctl](admin/cairo-ctl.md) — operator CLI (list, revoke, broadcast)
- [Authentication](admin/authentication.md) — bearer tokens, --auth
- [Tailscale (--tsnet)](admin/tsnet-tailscale.md) — bind via tailnet, no public port
- [Revocation](admin/revocation.md) — revoking agents, the 403 handshake
- [Events and monitoring](admin/events-and-monitoring.md) — `/api/events` SSE, metrics
- [Packaging (RPM/DEB)](admin/packaging-rpm-deb.md) — building distribution packages

## I'm building a client against cairo's HTTP API

Reference-grade docs for every endpoint.

- [Overview](api/overview.md) — auth, error envelope, versioning
- [Health and metrics](api/health-and-metrics.md)
- [Config](api/config.md) — snapshot + PUT
- [Sessions](api/sessions.md) — list/get/messages/rename/delete
- [Roles and aspects](api/roles-and-aspects.md)
- [Events SSE](api/events-sse.md) — observer stream

## I'm a contributor or curious developer

How the code is laid out, how to extend it, how to think about it.

### Concepts (read these first)

- [Philosophy](concepts/philosophy.md) — one being, rhizomatic threads, SQLite-as-identity
- [Identity](concepts/identity.md) — soul, ai_name, prompt composition
- [Memory model](concepts/memory-model.md) — memories, summaries, facts, notes
- [Roles](concepts/roles.md) — modes of focus, tool allowlists
- [Sessions and steering](concepts/sessions-and-steering.md) — turn lifecycle, resume

### Architecture

- [Overview](architecture/overview.md) — subsystem diagram and data flow
- [Database](architecture/database.md) — schema, WAL, cascades, migrations
- [Agent loop](architecture/agent-loop.md) — `runLoop`, event bus, tool-call iteration
- [LLM client](architecture/llm-client.md) — OpenAI-compatible interop
- [Server](architecture/server.md) — HTTP server, bridge, tsnet
- [TUI](architecture/tui.md) — Bubble Tea model, panels, hotkeys
- [Decisions](architecture/decisions.md) — the D-series design decisions

### Reference

- [CLI](reference/cli.md) — every flag and subcommand
- [Built-in tools](reference/tools.md)
- [Config keys](reference/config-keys.md)
- [Bundle format](reference/bundles.md)

### Development

- [Building](development/building.md)
- [Testing](development/testing.md)
- [Contributing](development/contributing.md)
- [Adding a tool](development/adding-a-tool.md)
- [Adding a config key](development/adding-a-config-key.md)
- [Adding a migration](development/adding-a-migration.md)
- [Adding a panel](development/adding-a-panel.md)
- [Custom tools](development/custom-tools.md)
- [Background work](development/background-work.md)

### AI-facing docs

These docs are loaded into the system prompt and addressed to the model — they describe how cairo *uses* its own tools.

- [README](ai/README.md) — overview of ai/ and how it's used
- [Tools](ai/tools.md) · [Skills](ai/skills.md) · [Memory and facts](ai/memory-and-facts.md) · [Sessions](ai/sessions.md) · [Identity](ai/identity.md) · [Config](ai/config.md) · [Hooks](ai/hooks.md) · [Schema](ai/schema.md) · [Dreams](ai/dreams.md)

---

## I want the philosophy

Three whitepapers on the design choices behind cairo. Longer reads, written as essays.

- [01. Local-First Philosophy](whitepapers/01-local-first-philosophy.md) — what local-first means and why the trade-offs are worth it
- [02. Fleet Architecture](whitepapers/02-fleet-architecture.md) — the cairo + cairo-registry + cairo-ctl model in depth
- [03. The Soul System](whitepapers/03-the-soul-system.md) — the identity layer, system prompt assembly, and the case for an authored agent

---

## A note on honesty

These docs document the **current state of the code**, including known rough edges. If something reads as imperfect or incomplete, that's deliberate — [ROADMAP.md](../ROADMAP.md) is where the "what it will become" lives. Cairo is early. It works, it's useful, and it has rough corners. Everything here is pitched at a reader who'd rather see the shape of what's really there than a polished sales brochure.
