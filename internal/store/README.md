# internal/store

**Layer:** Persistence  
**Status:** 🔄 split from `internal/db/`

The agent's local persistence layer. Every cairo instance has this — it's the agent's private SQLite database, never shared, never remote.

Split from the current `internal/db/` package (~12K LOC, ~14 responsibilities) into focused sub-packages. Each sub-package owns one domain.

## Sub-packages

| Package | Owns |
|---|---|
| `schema/` | DDL, migrations, seed data |
| `sqliteopen/` | `Open`, `OpenAt`, `WithTx`, composite `*DB` |
| `config/` | Typed KV config store |
| `sessions/` | Sessions and messages |
| `identity/` | Roles, prompts, skills, hooks, state, state rituals |
| `memory/` | Memory, facts, summaries, dreams |
| `jobs/` | Jobs, tasks, worktrees, artifacts, reap, proc |
| `index/` | Indexed files, chunks, embed search + `VectorStore` interface |
| `registrations/` | Agent registrations + catalog metadata + access policies |

## What does NOT go here

Enterprise data sources (Qdrant, Postgres, Neo4j) live in `internal/connectors/`. The `index/` sub-package defines the `VectorStore` interface; connectors implement it.
