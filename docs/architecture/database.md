# Database

*This document describes the SQLite layer. As of cairo2 Phase 1.3, the package was split — see [docs/architecture/decisions.md](decisions.md) D4 for the split rationale and `internal/store/` for the current layout.*

The SQLite database at `~/.cairo/cairo.db` is the being's complete persistent state. Everything identity-related is here: who the being is, what it knows, who it's talked to, what work it's done.

This doc covers the schema, operational choices (WAL, pragmas, busy_timeout), and how migrations work. For "what each table is *for*" read the [concepts](../concepts/memory-model.md) docs.

## Table of contents

- [Opening the DB](#opening-the-db)
- [Pragmas](#pragmas)
- [Transaction helper](#transaction-helper)
- [Core schema](#core-schema)
- [Embedding model tracking and cross-model safety](#embedding-model-tracking-and-cross-model-safety)
- [Importance scoring and time decay](#importance-scoring-and-time-decay)
- [Generic search](#generic-search)
- [Cascade summary](#cascade-summary)
- [Migrations](#migrations)
- [The reaper](#the-reaper)
- [Known rough edges](#known-rough-edges)

---

## Opening the DB

`db.Open()` opens `~/.cairo/cairo.db`, creating the directory and file if needed (path overridable via `CAIRO_DATA_DIR` env var or `--data-dir` CLI flag). It returns a `*DB` with embedded query-structs for every table:

```go
db.Config, db.Sessions, db.Messages, db.Memories, db.Roles,
db.Prompts, db.Tools, db.Skills, db.Notes, db.Jobs,
db.Tasks, db.TaskArtifacts, db.Summaries, db.Facts,
db.Hooks, db.Dreams, db.DreamLog, db.CodeIndex,
db.Projects, db.IndexedFiles          // added in v0.2.0 (migration v042)
```

On open, the DB applies the schema, applies migrations, seeds defaults (idempotently), and sweeps orphaned running tasks. Every one of these is safe to run repeatedly — the code is structured so `Open()` is always the whole lifecycle check, not a fragile first-run gate.

---

## Pragmas

Set at open time, every time:

- **`PRAGMA journal_mode = WAL`** — write-ahead logging. Readers don't block writers.
- **`PRAGMA foreign_keys = ON`** — enforced explicitly, set on the pinned connection. (SQLite's default is off; the `_foreign_keys=on` DSN parameter turned out to be flaky in modernc.org/sqlite, so this is belt-and-suspenders.)
- **`PRAGMA busy_timeout = 15000`** — wait up to 15s for the write lock before giving up. Prevents `SQLITE_BUSY` when multiple subprocesses (background tasks) open the DB concurrently.

The `sql.DB` pool is pinned to `MaxOpenConns(1)` — one connection at a time. With WAL and busy_timeout, this is enough for Cairo's workload. Multiple subprocesses each open their own `sql.DB` and compete via the file lock.

---

## Transaction helper

`WithTx(fn func(*sql.Tx) error)` on `*DB` runs fn inside a transaction and rolls back on error or panic. Used for multi-table writes that must be atomic.

---

## Core schema

20 tables plus three FTS5 virtual tables. Listed in roughly the order they get referenced during a turn.

### `config` — key-value store for identity and runtime settings

```sql
config (
    key        TEXT    PRIMARY KEY,
    value      TEXT    NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL DEFAULT (unixepoch())
)
```

Every row is addressable as `{{key}}` in prompts. See [Config keys](../reference/config-keys.md) for the full list. Compile-time constants in `internal/db/config_keys.go` — `KeyModel`, `KeyEmbedModel`, `KeyOllamaURL`, `KeyModelCtx`, etc.

`ConfigQ` gains two convenience methods beyond `Get`/`Set`:
- `GetWithDefault(key, default string) string` — returns default if the key is missing or empty
- `GetRequired(key string) (string, error)` — returns an error if the key is missing

### `prompt_parts` — composable system-prompt fragments

```sql
prompt_parts (
    id         INTEGER PRIMARY KEY,
    key        TEXT    NOT NULL,
    content    TEXT    NOT NULL,
    trigger    TEXT,         -- NULL = always loaded; "role:coder", "tool:bash", ...
    load_order INTEGER NOT NULL DEFAULT 100,
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at, updated_at
)
UNIQUE (key, IFNULL(trigger,''))
```

Composed into the system prompt by `BuildSystemPrompt` (`internal/agent/prompt.go`).

### `roles` — modes of focus

```sql
roles (
    id              INTEGER PRIMARY KEY,
    name            TEXT    UNIQUE NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    model           TEXT    NOT NULL DEFAULT '',       -- which LLM for this role
    base_prompt_key TEXT    NOT NULL DEFAULT '',       -- convention: "role:<name>"
    tools           TEXT    NOT NULL DEFAULT '[]',     -- JSON array of allowed tool names
    created_at, updated_at
)
```

### `memories` — stable, curated identity knowledge

```sql
memories (
    id          INTEGER PRIMARY KEY,
    content     TEXT NOT NULL,
    tags        TEXT NOT NULL DEFAULT '[]',     -- JSON array
    embedding   BLOB,                            -- packed float32
    embed_model TEXT NOT NULL DEFAULT '',       -- which model produced the embedding
    importance  REAL NOT NULL DEFAULT 0.5,      -- time-decay ranking weight
    pinned_at   TIMESTAMP,                       -- non-NULL = immune to auto-dump and dream-pass merge
    archived_at TIMESTAMP,                       -- set by dream-pass curator on merge; hard-deleted next cycle
    reviewed_at TIMESTAMP,                       -- stamped by dream-pass when this row is inspected
    created_at, updated_at
)
```

`pinned_at IS NOT NULL` suppresses auto-dump regardless of `weight`, and prevents the row from being used as the archived source in a curator merge. `archived_at` is distinct from `deleted_at` (weight-driven soft-delete): `archived_at` is set by the dream-pass curator for deduplicated rows and holds them for one full dream cycle before hard-delete. The `dream_log` entry for each merge includes reversal SQL.

### `sessions` — one conversation

```sql
sessions (
    id          INTEGER PRIMARY KEY,
    name        TEXT,
    cwd         TEXT    NOT NULL DEFAULT '',
    role        TEXT    NOT NULL DEFAULT 'thinking_partner',
    created_at, last_active
)
```

### `messages` — every turn

```sql
messages (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role        TEXT    NOT NULL,           -- user | assistant | tool | system
    content     TEXT    NOT NULL,
    tool_calls  TEXT,                        -- JSON, when assistant made tool calls
    tool_name   TEXT,                        -- when role=tool
    tool_id     TEXT,                        -- synthetic call id for correlation
    summarized  INTEGER NOT NULL DEFAULT 0,
    reviewed_at TIMESTAMP,                   -- stamped by dream-pass writer role; scopes the writer's scan window
    created_at
)
INDEX (session_id, created_at)
```

Tool calls are first-class: an assistant message that requested tools writes a row with `role=assistant`, empty `content`, and the `tool_calls` JSON. Each tool result is a row with `role=tool`, `tool_name`, and `tool_id`.

### `custom_tools` — tools the being wrote for itself

```sql
custom_tools (
    id              INTEGER PRIMARY KEY,
    name            TEXT    UNIQUE NOT NULL,
    description     TEXT    NOT NULL,
    parameters      TEXT    NOT NULL DEFAULT '{}',    -- JSON Schema
    implementation  TEXT    NOT NULL,                 -- bash script or python code
    impl_type       TEXT    NOT NULL DEFAULT 'bash',  -- bash | python
    prompt_addendum TEXT    NOT NULL DEFAULT '',      -- appended to system prompt when enabled
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at, updated_at
)
```

### `skills` — reusable instructions

```sql
skills (
    id          INTEGER PRIMARY KEY,
    name        TEXT    UNIQUE NOT NULL,
    description TEXT    NOT NULL,
    content     TEXT    NOT NULL,          -- the instruction text
    tags        TEXT    NOT NULL DEFAULT '[]',
    embedding   BLOB,
    embed_model TEXT NOT NULL DEFAULT '',
    created_at, updated_at
)
```

### `notes` — ephemeral scratch text

```sql
notes (
    id          INTEGER PRIMARY KEY,
    title       TEXT    NOT NULL DEFAULT '',
    content     TEXT    NOT NULL,
    tags        TEXT    NOT NULL DEFAULT '[]',
    embedding   BLOB,
    embed_model TEXT NOT NULL DEFAULT '',
    importance  REAL NOT NULL DEFAULT 0.5,
    created_at, updated_at
)
```

### `jobs`, `tasks`, `task_artifacts` — background work

Jobs and tasks are split into separate files: `jobs.go` (`JobQ`), `tasks.go` (`TaskQ`), `dag.go` (cycle detection for `depends_on` graphs).

```sql
jobs (
    id                INTEGER PRIMARY KEY,
    title             TEXT    NOT NULL,
    description       TEXT    NOT NULL DEFAULT '',
    status            TEXT    NOT NULL DEFAULT 'pending',
    orchestrator_role TEXT    NOT NULL DEFAULT 'orchestrator',
    session_id        INTEGER REFERENCES sessions(id) ON DELETE CASCADE,
    result            TEXT    NOT NULL DEFAULT '',
    created_at, started_at, completed_at
)

tasks (
    id            INTEGER PRIMARY KEY,
    job_id        INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    title         TEXT    NOT NULL,
    description   TEXT    NOT NULL DEFAULT '',
    status        TEXT    NOT NULL DEFAULT 'pending',  -- pending|blocked|running|done|failed
    assigned_role TEXT    NOT NULL DEFAULT 'coder',
    depends_on    TEXT    NOT NULL DEFAULT '[]',        -- JSON array of task ids
    result        TEXT    NOT NULL DEFAULT '',
    pid              INTEGER,                              -- live pid while running
    log_path         TEXT    NOT NULL DEFAULT '',
    reported_at      INTEGER,                              -- background-inbox delivery tracking
    progress_current INTEGER NOT NULL DEFAULT 0,          -- for global progress bars
    progress_total   INTEGER NOT NULL DEFAULT 0,
    progress_label   TEXT    NOT NULL DEFAULT '',
    progress_detail  TEXT    NOT NULL DEFAULT '',
    created_at, started_at, completed_at
)
INDEX (job_id, created_at)

task_artifacts (
    id         INTEGER PRIMARY KEY,
    task_id    INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    type       TEXT    NOT NULL DEFAULT 'output',       -- output | file
    path       TEXT    NOT NULL DEFAULT '',
    content    TEXT    NOT NULL DEFAULT '',
    tool_name  TEXT    NOT NULL DEFAULT '',
    created_at
)
```

`JobQ.ResolveAndUpdateJobStatus(jobID)` (formerly `Reconcile`) checks whether all tasks for a job have reached terminal status and updates the job's status accordingly. Called automatically after each task completes.

See [Background work](../development/background-work.md) for the job→task→agent lifecycle.

### `summaries` — compressed conversation history

```sql
summaries (
    id             INTEGER PRIMARY KEY,
    session_id     INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    content        TEXT    NOT NULL,
    embedding      BLOB,
    embed_model    TEXT NOT NULL DEFAULT '',
    covers_from    INTEGER NOT NULL DEFAULT 0,
    covers_through INTEGER NOT NULL DEFAULT 0,
    reviewed_at    TIMESTAMP,               -- stamped by dream-pass; summaries are tidied but never merge-archived
    created_at
)
INDEX (session_id, created_at), (created_at DESC)
```

Global search scope — `session_id` is provenance, not a scope filter. Summaries from any session are findable from any other.

### `facts` — atomic observations

```sql
facts (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    summary_id  INTEGER REFERENCES summaries(id) ON DELETE CASCADE,
    content     TEXT    NOT NULL,
    embedding   BLOB,
    embed_model TEXT    NOT NULL DEFAULT '',
    importance  REAL    NOT NULL DEFAULT 0.5,
    archived_at TIMESTAMP,                   -- set by dream-pass curator on merge; hard-deleted next cycle
    reviewed_at TIMESTAMP,                   -- stamped by dream-pass when this row is inspected
    created_at
)
```

### `hooks` — lifecycle shell commands

```sql
hooks (
    id         INTEGER PRIMARY KEY,
    event      TEXT    NOT NULL,    -- session_start | session_end | pre_tool | post_tool
    command    TEXT    NOT NULL,    -- shell command to run
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at, updated_at
)
```

Shell commands stored here are fired by `RunHooks` at agent lifecycle events. Errors are logged but don't abort the trigger.

### `dreams` — dream-pass narrative index

```sql
dreams (
    id              INTEGER PRIMARY KEY,
    created_at      TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    date            TEXT      NOT NULL UNIQUE,   -- YYYY-MM-DD
    narrative_path  TEXT      NOT NULL,          -- path to the narrative file (~/.cairo/dreams/<date>.md)
    themes          TEXT,                         -- space-separated theme tags
    mood            TEXT,                         -- mood label from state_daily
    state_daily_ref TEXT,                         -- soft ref to state_daily.date (nullable, no FK; state_daily PK is date TEXT)
    last_edited_at  TIMESTAMP
)
INDEX (date)
```

One row per dream-pass date. The narrative prose lives in the file at `narrative_path`; this row is the index. No embedding column — dreams are excluded from semantic search by construction. Query via `db_access` (`db.Dreams`).

### `dream_log` — dream-pass mutation audit trail

```sql
dream_log (
    id           INTEGER PRIMARY KEY,
    dream_id     INTEGER NOT NULL REFERENCES dreams(id),
    created_at   TIMESTAMP NOT NULL DEFAULT (datetime('now')),
    action       TEXT NOT NULL,        -- e.g. "merge", "write", "archive"
    target_table TEXT NOT NULL,        -- table affected
    target_ids   TEXT NOT NULL,        -- JSON array of affected row ids
    note         TEXT NOT NULL         -- human-readable description; merge entries include reversal SQL
)
INDEX (dream_id)
```

Written by the dream-pass for every mutation: memory merges, new memory writes, archive stamps. Merge entries include reversal SQL in `note` — one day to intervene before the archived row is hard-deleted on the next dream cycle. Query via `db_access` (`db.DreamLog`).

### `projects` and `indexed_files` — per-project file map (learn feature)

Added in migration v042. These tables back the `learn` tool and `/learn` slash command. Unlike `code_index` (a single embedding pool over raw content), the learn tables are project-namespaced, summary-first, and intentional — the user explicitly runs `learn add` on a directory.

```sql
projects (
    name         TEXT    PRIMARY KEY,               -- user-chosen project name
    root_path    TEXT    NOT NULL,                  -- absolute path of the indexed root
    description  TEXT    NOT NULL DEFAULT '',       -- auto-generated summary from file summaries
    file_count   INTEGER NOT NULL DEFAULT 0,        -- maintained by RecountFiles()
    indexed_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    last_updated INTEGER NOT NULL DEFAULT (unixepoch())
)

indexed_files (
    id          INTEGER PRIMARY KEY,
    project     TEXT    NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
    rel_path    TEXT    NOT NULL,                   -- relative to root_path
    file_type   TEXT    NOT NULL DEFAULT '',        -- extension without dot
    bytes       INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT    NOT NULL DEFAULT '',        -- change-detection hash
    summary     TEXT    NOT NULL,                  -- 1-2 sentence model-generated summary
    embedding   BLOB    NOT NULL,                  -- packed float32 over the augmented summary
    embed_model TEXT    NOT NULL,                  -- which model produced the embedding
    indexed_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(project, rel_path)
)
INDEX (project, rel_path)
```

The embedding is computed over an augmented string: `"project=<name> file=<rel_path> · <summary>"` so retrieval works on file-location context, not just summary prose. Idempotent: SHA-256 comparison skips unchanged files on re-runs.

`learn(action="search")` embeds the query, calls `IndexedFiles.SearchSummaries(project, vec, embedModel, limit)`, and returns ranked results with score, relative path, file type, byte count, and summary.

`BuildSystemPrompt` appends a `## Indexed projects` section when any project exists, listing project name, file count, and the first sentence of the description so the model always knows what's queryable via `learn(action="search")`.

---

### `code_index` — embedded source files for semantic code search

```sql
code_index (
    id          INTEGER PRIMARY KEY,
    path        TEXT    NOT NULL,                   -- absolute path
    rel_path    TEXT    NOT NULL,                   -- relative to root
    root        TEXT    NOT NULL,                   -- the indexed root directory
    content     TEXT    NOT NULL,                   -- full file content (capped at 512KB by indexer)
    embedding   BLOB,                                -- packed float32 over the first ~4KB
    embed_model TEXT    NOT NULL DEFAULT '',
    lang        TEXT    NOT NULL DEFAULT '',        -- inferred from extension
    size_bytes  INTEGER NOT NULL DEFAULT 0,
    indexed_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(root, rel_path)
)
INDEX (root, rel_path)
```

Populated by `cairo learn` (previously `cairo index`, removed in v0.3.0). Was read by the `code_search` tool, which was removed in v0.2.1 (replaced by `learn`). The unique constraint on `(root, rel_path)` makes re-indexing a no-op for unchanged files and an upsert for changed ones.

### FTS5 virtual tables — exact-match search alongside embeddings

```sql
CREATE VIRTUAL TABLE memories_fts USING fts5(content, content=memories, content_rowid=id);
CREATE VIRTUAL TABLE notes_fts    USING fts5(title, content, content=notes, content_rowid=id);
CREATE VIRTUAL TABLE skills_fts   USING fts5(name, description, content, content=skills, content_rowid=id);
```

Each is a **contentless** FTS5 index linked back to its source table via `content=` and `content_rowid=`. AFTER INSERT / UPDATE / DELETE triggers on the source tables keep the FTS index in sync. Existing rows are backfilled on the migration that creates the virtual tables.

The `memory`, `note`, and `skill` search actions accept a `mode` argument: `semantic` (default, embedding cosine), `exact` (FTS5 keyword/phrase), or `hybrid` (deduplicated union, semantic first).

---

## Embedding model tracking and cross-model safety

Every embeddable table has an `embed_model TEXT` column. `SearchTopK` skips any row whose `embed_model` doesn't match the query's model — this prevents silently comparing embeddings produced by different models, which would give nonsense scores.

---

## Importance scoring and time decay

Memories, notes, and facts carry `importance REAL DEFAULT 0.5`. Search ranking multiplies cosine similarity by a decay factor:

```
decayImportance(base, updatedAt):
    days = time since updatedAt
    decay = 1.0 − (days / 180) × 0.4   -- linear decay over 180 days
    decay = max(decay, 0.6)             -- floor at 0.6× base
    return base × decay
```

A fresh item at importance 1.0 scores 1.0× cosine; that same item after 180+ days scores 0.6× cosine. The floor prevents old high-value items from disappearing entirely.

---

## Generic search

`SearchTopK[T Embeddable](items []T, query []float32, queryModel string, k int) []T` in `embed_search.go` is a generic function that works on any slice of types implementing `Embeddable`:

```go
type Embeddable interface {
    GetEmbedding() []float32
    GetEmbedModel() string
    GetImportance() float64
    GetUpdatedAt() time.Time
}
```

Scoring is `cosine × decayImportance`. Items with mismatched model or dimension are skipped. Returns the top-k by score.

---

## Cascade summary

Delete a session → cascades to its messages, summaries, facts, and jobs (jobs cascade further to tasks and task_artifacts). A full identity export (without `--full`) uses this: `DELETE FROM sessions` wipes conversation history cleanly while leaving memories, skills, roles, prompts, tools, hooks, and config intact. The `dreams` and `dream_log` tables have no `session_id` FK and are unaffected by session deletes.

---

## Migrations

Migrations in `internal/db/schema.go` are `ALTER TABLE ADD COLUMN` and `CREATE TABLE IF NOT EXISTS` statements, executed in order at every open. Failures are silently ignored — the idiom is "idempotent add, run unconditionally."

This keeps migration logic simple but means there's **no down-migration story**. Rolling back to an older binary against a newer DB relies on additive-only changes never breaking the older reader.

A few existing migrations do more than add columns — e.g. retroactively granting tools to all seeded roles whose `tools` array predates the tool. The pattern there is "read, JSON-edit, write" via SQLite's `json_insert`.

---

## The reaper

On every `Open()`, `ReapOrphanedTasks` runs. It finds rows in `tasks` with `status='running'` whose `pid` is no longer alive (or zero), and marks them `failed` with a result explaining what happened. Without this, a crashed or killed cairo process would leave `job_list` reporting a task as in-flight forever.

Reap failures are logged to stderr but don't block startup. The dream-pass writer (a separate concern) scans `messages` for unfollowed memory intent and writes to `memories` + `dream_log`; it runs on `cairo dream`, not on startup.

**`archived_at` is dream-pass territory, not the reaper.** Rows in `memories` and `facts` with `archived_at IS NOT NULL` were set by the dream-pass curator (deduplication merges). They are hard-deleted by the *next* dream-pass cycle — not by the reaper. The reaper only handles orphaned tasks.

---

## Known rough edges

- **Migrations silently ignore errors.** A real bug in a migration looks the same as "already applied." Rare in practice (the migrations are simple), but it means a malformed migration can hide.
- **`MaxOpenConns(1)` serializes writes across subprocess opens.** Each subprocess has its own pinned connection, but they compete for the file lock. With many parallel background tasks, write contention surfaces as latency spikes rather than errors.
- **No schema versioning in manifest.** `cairo export` ships the DB file verbatim. Bundle version 1 implicitly means "whatever schema was current at export." A future `version = 2` would need an import-time migration step. See [ROADMAP](../../ROADMAP.md).
