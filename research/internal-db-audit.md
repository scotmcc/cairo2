# internal/db audit

## Scale

- **Total Go LOC in `internal/db/`:** ~12,343 lines across ~50 .go files (incl. tests).
- **Production code (excluding `_test.go`):** ~8,800 lines.
- **`schema.go` alone:** 1,838 lines — the embedded `const schema` DDL plus `var migrations []string` (~80+ migration entries).
- **`seed.go`:** 855 lines (29KB). Inserts default config keys, roles, base prompts, consider aspects, etc.

This is far too large for one package by Go standards (a healthy leaf package is 500–2,000 lines).

## Responsibilities (one package, ten jobs)

| Responsibility | File(s) | LOC est. |
|---|---|---|
| 1. Schema bootstrap + migrations | `schema.go` | 1,838 |
| 2. Seed defaults on first run | `seed.go`, `seed/` dir | ~900 |
| 3. Connection lifecycle (`Open`, `OpenAt`, `WithTx`, busy_timeout, FK pragmas, premigration backup) | `db.go` | 290 |
| 4. **Config store** (typed key/value KV) | `config.go`, `config_keys.go`, `config_test.go` | ~470 |
| 5. **Session/message store** | `sessions.go`, `messages.go` | ~640 |
| 6. **Memory engine** (mem rows, FTS5, MMR re-ranking, importance/weight, retrieval scoring) | `memories.go`, `memories_test.go`, `mmr.go` | ~2,460 (incl tests) |
| 7. **Fact/Summary/Dream stores** (curator, dream log) | `summaries.go`, `dreams.go`, `dream_log.go`, `curator.go` | ~1,030 |
| 8. **Job/Task/Worktree/Artifact orchestration** (PID tracking, orphan reaping, OS subprocess hooks) | `jobs.go`, `tasks.go`, `worktrees.go`, `artifacts.go`, `reap.go`, `proc_unix.go`, `proc_windows.go` | ~1,950 |
| 9. **Learn/Index pipeline** (chunks, indexed_files, projects, embed_search) | `learn.go`, `chunks.go`, `embed_search.go` | ~620 |
| 10. **Roles/Skills/Tools/Prompts/Hooks** (configuration domain objects) | `roles.go`, `skills.go`, `tools.go`, `prompts.go`, `hooks.go` | ~720 |
| 11. **Consider sub-agent** state | `consider_aspects.go`, `consider_activations.go` | ~165 |
| 12. **Identity/State ritual** (export/import bundles) | `state.go`, `state_const.go`, `state_ritual.go` | ~700 |
| 13. **Registry-side state** | `registrations.go` | ~30 |
| 14. **Glue** | `db.go`, `helpers.go`, `model.go`, `dag.go`, `constants.go` | ~120 |

The `*Q` type pattern (`ConfigQ`, `MemoryQ`, `SessionQ`, ...) aggregated on `*db.DB` already represents the seam lines — every `*Q` is its own bounded query namespace and could live in its own package.

## Logically independent seams

The following four groups have **no inbound deps from other groups** (verified by reading file imports — every file in `internal/db/` imports only `database/sql`, `time`, `errors`, encoding/json, and standard library; none import from another sibling sub-area) and each owns its own tables:

1. **Memory subsystem** (memories + mmr + facts + summaries + dreams + dream_log + curator). All revolve around embedded vectors + FTS5 + scoring rules. Owns ~30% of total LOC.
2. **Jobs/Tasks subsystem** (jobs + tasks + worktrees + task_artifacts + reap + proc_unix/windows + hooks). All revolve around subprocess lifecycle + DB-backed task queues.
3. **Learn/Index subsystem** (learn + chunks + embed_search + projects + indexed_files). Owns the codebase-RAG path.
4. **Identity subsystem** (state + state_ritual + state_const). Owns export/import bundles. Touches many other tables on read but is itself only consumed by the `cairo export`/`cairo import` commands.

The remaining responsibilities form the **kernel**: connection, schema/migrations, seed, config, sessions, messages, roles, skills, prompts, hooks, registrations, consider. These are tightly coupled (sessions reference config keys; seed inserts roles/prompts/aspects; messages cascade-delete with sessions).

## Proposed split

Recommended target packages (under `internal/store/`):

```
internal/store/
  schema/         - schema.go + migrations + seed (the bootstrap layer)
  sqliteopen/     - Open/OpenAt/WithTx + premigration backup + DB struct (the "kernel" *DB)
  config/         - ConfigQ + KeyXxx constants
  sessions/       - SessionQ + MessageQ
  identity/       - StateQ + roles + prompts + skills + hooks + consider (read-mostly user-config)
  memory/         - MemoryQ + facts + summaries + dreams + curator + mmr
  jobs/           - JobQ + TaskQ + WorktreeQ + artifacts + reap + proc_unix
  index/          - IndexedFilesQ + ChunksQ + ProjectsQ + embed_search (the Learn-RAG store)
  registrations/  - RegistrationQ
```

Each `*Q` becomes the package-level public type, e.g. `memory.Q`, with `New(*sql.DB) *Q`. The aggregator struct `db.DB` becomes a thin composition root that just wires sub-stores.

## What makes splitting hard

1. **Single embedded `schema.go` with all DDL.** Migrations are global (one PRAGMA user_version counter). Cannot be sharded per-package without inventing a per-store migration table. Solution: keep `schema/` central; sub-packages only own queries, not DDL. This is fine — every store imports `schema` only as "transitive dependency through `*sql.DB`".
2. **Shared `*sql.DB` connection.** Single connection (max_open=1) is required for SQLite WAL semantics. The composite `db.DB` must continue to vend that connection to every sub-store. Not a real obstacle; sub-stores accept `*sql.DB`.
3. **Seed touches everything.** `seed.go` inserts into config, roles, prompts, consider_aspects, and reads from many. Solution: move seed into `schema/` (it runs once after migrations) or keep a top-level `seed/` package that imports all sub-stores.
4. **Cross-store joins.** A few queries join across boundaries (e.g. memory retrieval reads role from sessions). These are rare; keep the query in the package that owns the dominant table.
5. **Tests share a `testhelper_test.go`** that opens a tempdir DB. After split, each package needs its own helper — small duplication, ~20 lines per package.
6. **Schema vs seed parity** is documented as a landmine in CLAUDE.md. Keeping schema+seed together (both in `schema/`) preserves this invariant.

## Key signals from reading

- `db.go`'s `DB struct` has **23 sub-`*Q` fields** — each is already a logical sub-store. The split is essentially "promote each `*Q` to its own package".
- `schema.go` is alphabetically growing — every new feature adds tables here, making this file a bottleneck for merge conflicts. Splitting won't fix that (DDL stays central) but it will prevent feature work from ALSO touching the same query files.
- `memories.go` at 20KB and `memories_test.go` at 24KB is by itself a reasonable mid-sized package. It is the single most independent surface and should be the first to extract.
