# Cairo documentation

Cairo — **C**ollaborative **A**I **R**hizomatic **O**rchestrator — is a local-first coding harness backed by SQLite and Ollama. One being, many parallel threads of attention, with its complete identity stored in a single `.db` file.

This directory is the full documentation. The top-level [`README.md`](../README.md) is the landing page; start there if you're new. [`ROADMAP.md`](../ROADMAP.md) covers where the project is headed.

---

## Getting started

End-user quickstart: install, first run, and basic workflows. Read these in order if you're new.

- [Installation](getting-started/installation.md) — Go, Ollama, models, first build
- [Quickstart](getting-started/quickstart.md) — your first session in five minutes
- [First run](getting-started/first-run.md) — meeting Selene and running `/init`
- [Skills](getting-started/skills.md) — reusable instruction workflows: `/init`, `/init_codebase`, writing your own
- [Portable identity](getting-started/portable-identity.md) — export, import, and diff your identity bundle across machines

## Concepts

The ideas that make Cairo coherent. Read these before the architecture docs — they explain *why* the architecture is shaped the way it is.

- [Philosophy](concepts/philosophy.md) — one being, rhizomatic threads, SQLite-as-identity
- [Identity](concepts/identity.md) — soul, ai_name, prompt composition, template substitution
- [Memory model](concepts/memory-model.md) — memories, summaries, facts, notes — when each
- [Roles](concepts/roles.md) — modes of focus, tool allowlists, role-specific models
- [Sessions and steering](concepts/sessions-and-steering.md) — turn lifecycle, resume, steering queue

## Architecture

How the code is laid out. Read these after concepts.

- [Overview](architecture/overview.md) — subsystem diagram and data flow
- [Database](architecture/database.md) — schema, WAL, cascades, migrations
- [Agent loop](architecture/agent-loop.md) — `runLoop`, event bus, tool-call iteration
- [LLM client](architecture/llm-client.md) — Ollama interop, `StreamOnce`, embeddings
- [TUI](architecture/tui.md) — Bubble Tea model, panels, hotkeys

## AI

The user guide for the AI agent running *inside* Cairo. These docs are loaded into the system prompt and addressed directly to the model — they describe how to use Cairo's tools, how memory works, and how the agent should behave.

- [README](ai/README.md) — overview of the ai/ docs and how they're used
- [Tools](ai/tools.md) — every tool action the model can call
- [Skills](ai/skills.md) — how to read, create, and invoke skills
- [Memory and facts](ai/memory-and-facts.md) — when to store what
- [Sessions](ai/sessions.md) — session lifecycle from the agent's perspective
- [Identity](ai/identity.md) — soul, name, and template variables
- [Config](ai/config.md) — reading and writing config keys
- [Hooks](ai/hooks.md) — tool hooks and lifecycle events
- [Schema](ai/schema.md) — DB schema reference for `db_access` queries

## Reference

Dense tables and lookups for users and contributors alike.

- [CLI](reference/cli.md) — every flag and subcommand
- [Built-in tools](reference/tools.md) — the 23 tools the model can call
- [Config keys](reference/config-keys.md) — every row of the `config` table
- [Bundle format](reference/bundles.md) — what's inside a `.cairo` file

## Development

For contributors and readers cloning the repo. How to build, test, and extend Cairo.

- [Building](development/building.md) — toolchain, `make build`, `make install`
- [Testing](development/testing.md) — what's covered, how to add tests
- [Contributing](development/contributing.md) — code style, PR expectations
- [Adding a tool](development/adding-a-tool.md) — how to register a new built-in tool
- [Adding a config key](development/adding-a-config-key.md) — schema, seed, and migration
- [Adding a migration](development/adding-a-migration.md) — numbered migration conventions
- [Adding a panel](development/adding-a-panel.md) — TUI panel structure and wiring
- [Custom tools](development/custom-tools.md) — how the AI writes its own tools at runtime
- [Background work](development/background-work.md) — jobs, tasks, agents, and the dependency DAG

---

## A note on honesty

These docs document the **current state of the code**, including known rough edges. If something reads as imperfect or incomplete, that's deliberate — [ROADMAP.md](../ROADMAP.md) is where the "what it will become" lives. Cairo is early. It works, it's useful, and it has rough corners. Everything here is pitched at a reader who'd rather see the shape of what's really there than a polished sales brochure.
