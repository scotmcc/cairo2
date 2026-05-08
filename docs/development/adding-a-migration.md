# Adding a Migration

> *The store layer was split from `internal/db/` into `internal/store/` sub-packages in Phase 1.3 — see `docs/architecture/decisions.md` D4. This document describes the post-split layout.*

The DB schema is versioned via a slice of SQL statements in `internal/store/schema/schema.go`. Every new structural change — whether DDL (new table, new column) or DML backfill (new config key, new role tool grant) — belongs here. This guide explains the migration model, numbering conventions, what to put in a migration, and the paired seed requirement.

---

## 1. The Migration Model

### Where it lives

All migrations are in the `var migrations = []string{...}` slice in `internal/store/schema/schema.go` (line 151). Each entry is a raw SQL string. The slice is ordered — entries must be appended at the end, never inserted in the middle.

### How it runs

`schema.ApplyMigrations` (in `internal/store/schema/schema.go`) is called by `OpenAt` in `internal/store/sqliteopen/db.go` on every open:

```go
// internal/store/schema/schema.go:1863
func ApplyMigrations(sqldb *sql.DB) error {
    var version int
    if err := sqldb.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
        return fmt.Errorf("read user_version: %w", err)
    }

    for i, m := range migrations {
        if i < version {
            continue // already applied
        }
        if _, err := sqldb.Exec(m); err != nil {
            return fmt.Errorf("migration %d: %w", i, err)
        }
        // Bump immediately after each statement so a mid-run crash is resumable.
        if _, err := sqldb.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
            return fmt.Errorf("bump user_version to %d: %w", i+1, err)
        }
    }
    return nil
}
```

- **Version tracking**: `PRAGMA user_version` is the version counter. It starts at 0 on a new DB. After migration `i` succeeds, `user_version` is set to `i+1`.
- **One statement per slice entry**: each entry is executed as a single `Exec` call. Multi-statement entries are not supported.
- **Idempotency by design**: DDL uses `IF NOT EXISTS` / `IF EXISTS`; DML uses `INSERT OR IGNORE` or conditional `UPDATE ... WHERE NOT EXISTS`. Running migrations on an already-migrated DB is always safe.
- **No transaction across statements**: each migration statement commits individually. A crash between two paired statements (e.g. v008a and v008b) leaves the DB in an intermediate state. Cairo resumes from the last successfully bumped `user_version` on next startup.
- **Pre-migration backup**: `OpenAt` takes a `VACUUM INTO` backup before applying pending migrations. The backup lives in `~/.cairo/backups/` and the five most recent are kept.

### The base schema

Before migrations run, `execSchema` applies the `schema` constant (top of `schema.go`). This creates all base tables with `IF NOT EXISTS`. New tables added after the initial release go in migrations, not in the base schema — the base schema is for a clean slate, migrations are for evolution.

---

## 2. Numbering Convention

Migrations are numbered in comments using the pattern `[vNNN]`. The number is the 1-based migration index (migration at slice index 0 is `[v001]`, index 1 is `[v002]`, etc.).

When two SQL statements are logically part of the same change but cannot be combined into one `Exec` call, use alphabetical suffixes:

```go
// [v008] Deduplicate prompt_parts and enforce uniqueness on (key, trigger)
// These two are logically atomic but the runner executes them separately.
`DELETE FROM prompt_parts WHERE id NOT IN (SELECT MIN(id) ...`,   // v008a
`CREATE UNIQUE INDEX IF NOT EXISTS idx_prompt_parts_unique ...`,  // v008b
```

The suffix is documentation only — the runner doesn't see it. What matters is that the two entries are adjacent in the slice and their position in the slice matches the version they bump to.

To find the next available number: count the entries in the `migrations` slice (or look at the highest `[vNNN]` tag in the comments) and add one.

---

## 3. What Goes in a Migration

### DDL — new table

```go
// [v042] Add learn-about tables
`CREATE TABLE IF NOT EXISTS projects (
    name         TEXT    PRIMARY KEY,
    root_path    TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    file_count   INTEGER NOT NULL DEFAULT 0,
    indexed_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    last_updated INTEGER NOT NULL DEFAULT (unixepoch())
)`,
`CREATE TABLE IF NOT EXISTS indexed_files (
    id          INTEGER PRIMARY KEY,
    project     TEXT    NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
    rel_path    TEXT    NOT NULL,
    -- ...
    UNIQUE(project, rel_path)
)`,
`CREATE INDEX IF NOT EXISTS idx_indexed_files_project ON indexed_files(project, rel_path)`,
```

Each statement is a separate slice entry. `IF NOT EXISTS` on both the table and the index ensures idempotency.

### DDL — new column

```go
// [v001] Add pid and log_path columns to tasks
`ALTER TABLE tasks ADD COLUMN pid      INTEGER`,
`ALTER TABLE tasks ADD COLUMN log_path TEXT NOT NULL DEFAULT ''`,
```

SQLite's `ALTER TABLE ADD COLUMN` is idempotent when the column doesn't exist and fails when it does. Because `applyMigrations` skips already-applied migrations via the version counter, duplicate-column errors don't occur in practice. The `IF NOT EXISTS` clause on the `ALTER` itself is not supported in SQLite — version tracking is the mechanism.

### DML — config key backfill

```go
// [v030] Seed kokoro_url and kokoro_voice config keys for existing DBs
`INSERT OR IGNORE INTO config(key, value) VALUES('kokoro_url', '')`,
`INSERT OR IGNORE INTO config(key, value) VALUES('kokoro_voice', 'af_heart(8)+af_nicole(2)')`,
```

`INSERT OR IGNORE` is idempotent: existing rows are left untouched, missing rows are inserted with the default.

### DML — role tool grant

```go
// [v009] Grant summary_search to existing roles
`UPDATE roles SET tools = json_insert(tools, '$[#]', 'summary_search')
 WHERE name IN ('thinking_partner','orchestrator','coder','planner','reviewer')
   AND NOT EXISTS (SELECT 1 FROM json_each(roles.tools) WHERE value = 'summary_search')`,
```

The `NOT EXISTS (SELECT 1 FROM json_each(...) WHERE value = 'summary_search')` guard is the idempotency mechanism — re-running this never duplicates an entry in the tools array. This is the canonical pattern for all tool grants to existing roles.

### DML — conditional UPDATE

```go
// [v040] Populate per-role model defaults
`UPDATE roles SET model = 'devstral-24b' WHERE name = 'coder' AND (model IS NULL OR model = '')`,
```

Conditional updates avoid overwriting values the user has set. The `WHERE model = ''` guard is the idempotency layer.

### Things that do NOT belong in migrations

- Data transformations that take more than a few seconds (reindexing, re-embedding a large corpus). These should be background commands or one-shot CLI subcommands.
- Schema changes that require a full table rebuild (SQLite cannot `ALTER COLUMN` or `DROP COLUMN` portably in all versions). Plan around this by adding new columns rather than modifying old ones.
- Logic that depends on application code that may change. Migrations run on every DB open in perpetuity — write them to be self-contained SQL.

---

## 4. Companion Seed Entries

Every migration that adds a config key, role, or prompt part should have a matching entry in `internal/store/sqliteopen/seed.go`. This ensures fresh installs get the value without running through migrations (though migrations run on fresh installs too, so technically either path works — the seed is the canonical source of defaults).

The relationship:

- **`seedConfig()`** — authoritative list of config key defaults for fresh DBs. Uses `INSERT OR IGNORE`.
- **Migration** — backfills the same key into existing DBs. Uses `INSERT OR IGNORE`.

Both use `INSERT OR IGNORE` so the order of execution (seed runs after migrations in `OpenAt`) doesn't matter.

**Config key**: add to both `seedConfig()` map and a migration entry.

**Role**: `seedRoles()` is the seed. The migration pattern is:
```go
`INSERT OR IGNORE INTO roles(name, description, model, base_prompt_key, tools)
 VALUES('researcher', '...', '', 'role:researcher', '["read","bash",...]')`,
```

**Prompt part**: `seedPrompts()` uses `UPSERT` to propagate seed changes to existing DBs (for rows where `source = 'seed'`). A new prompt part that should reach existing DBs still needs a migration:
```go
`INSERT OR IGNORE INTO prompt_parts(key, content, trigger, load_order, source) VALUES(
    'my_part', '...', NULL, 10, 'seed'
)`,
```

**Skills**: `seedSkills()` uses `INSERT OR IGNORE` on `name`. New skills reach existing DBs via migrations (see v019 for the `orchestrate` skill).

---

## 5. Testing Migrations

### Fresh DB

Any test that calls `sqliteopen.OpenAt(t.TempDir() + "/test.db")` creates a fresh DB, runs all migrations, then seeds. The shared helper lives in `internal/store/testing/`:

```go
// internal/store/testing/testdb.go:10
func OpenTestDB(t *testing.T) *sqliteopen.DB {
    t.Helper()
    path := filepath.Join(t.TempDir(), "test.db")
    database, err := sqliteopen.OpenAt(path)
    if err != nil {
        t.Fatalf("OpenAt: %v", err)
    }
    t.Cleanup(func() { database.Close() })
    return database
}
```

Import `"github.com/scotmcc/cairo2/internal/store/testing"` (package name `testdb`) and call `testdb.OpenTestDB(t)`. Each call gets its own tempdir, so concurrent tests cannot interfere.

### Existing DB

To test that a migration correctly upgrades a pre-migration state, you need a DB that was created before your migration. The practical approach in development:

1. Build cairo before your migration and run `cairo` once to produce a `cairo2.db`.
2. Add your migration.
3. Run `go run ./cmd/cairo` against the same `cairo2.db` — the new migration should apply cleanly without errors.

Automated regression tests for individual migration steps do not currently exist in the test suite. The `TestDefault_RespectsSeededRoleAllowlists` test in `internal/tools/registry_test.go` is the closest: it opens a fresh DB and checks that every tool name in every seeded role allowlist exists in `Default()`. This catches the common drift between seed and tool name.

### Verifying idempotency

Run `OpenAt` twice on the same DB path. If your migration is idempotent, the second open is a no-op (all migrations are already at `user_version`). If it's not idempotent, the second open either errors (duplicate column, duplicate key) or silently double-inserts data.

---

## 6. Common Mistakes

### Non-idempotent `ALTER TABLE`

`ALTER TABLE foo ADD COLUMN bar TEXT` succeeds on the first run and fails on the second because SQLite does not support `ALTER TABLE ADD COLUMN IF NOT EXISTS`. The version counter prevents double-execution in the normal case, but if a migration is re-applied (e.g. during testing with a fresh DB that somehow has the schema but not the version), it will fail. There is no good fix for this in SQLite — rely on the version counter and document clearly that migrations must not be replayed.

### Forgetting the seed pair

Adding a config key to a migration without adding it to `seedConfig()` means the key is absent from the canonical defaults list. Future contributors reading the code will not know the key exists or what its default is. Always update both.

### Updating the schema base instead of adding a migration

If you add a new column to the base `schema` constant at the top of `internal/store/schema/schema.go`, fresh installs get the column but existing DBs do not (because `CREATE TABLE IF NOT EXISTS` skips if the table already exists, and `ExecSchema` doesn't add new columns to existing tables). Always add new columns via a migration. The base schema is written once; migrations are written forever.

### Schema drift between fresh-DB and migrated-DB paths

If your new table or column is only in the migration and not in the base schema (or vice versa), the two paths produce different schemas. Always add new tables to the base schema with `CREATE TABLE IF NOT EXISTS` AND add them to a migration with the same DDL. See how v042 adds `projects` and `indexed_files`: they are in the migration, not in the base schema — this means fresh DBs get them via the migration on first open, which runs before seed. This is acceptable because all migrations run on every open, including fresh ones.

### Multi-statement migration entries

The runner calls `sqldb.Exec(m)` on each slice entry. Some SQLite drivers accept multi-statement strings; others do not. cairo uses `modernc.org/sqlite`, which does not reliably support multi-statement `Exec`. Each slice entry must be exactly one SQL statement.

---

## 7. Walkthrough: Migration v042 — the `learn` Tables

Migration v042 adds the project-namespaced file indexing tables used by the `learn` tool. Here is the full path.

### The tables (schema.go, migrations slice)

```go
// [v042] Add learn-about tables
`CREATE TABLE IF NOT EXISTS projects (
    name         TEXT    PRIMARY KEY,
    root_path    TEXT    NOT NULL,
    description  TEXT    NOT NULL DEFAULT '',
    file_count   INTEGER NOT NULL DEFAULT 0,
    indexed_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    last_updated INTEGER NOT NULL DEFAULT (unixepoch())
)`,
`CREATE TABLE IF NOT EXISTS indexed_files (
    id          INTEGER PRIMARY KEY,
    project     TEXT    NOT NULL REFERENCES projects(name) ON DELETE CASCADE,
    rel_path    TEXT    NOT NULL,
    file_type   TEXT    NOT NULL DEFAULT '',
    bytes       INTEGER NOT NULL DEFAULT 0,
    sha256      TEXT    NOT NULL DEFAULT '',
    summary     TEXT    NOT NULL,
    embedding   BLOB    NOT NULL,
    embed_model TEXT    NOT NULL,
    indexed_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    UNIQUE(project, rel_path)
)`,
`CREATE INDEX IF NOT EXISTS idx_indexed_files_project ON indexed_files(project, rel_path)`,
```

Three slice entries (three separate migrations in the version counter). The foreign key from `indexed_files.project` to `projects.name` with `ON DELETE CASCADE` means deleting a project automatically removes all its indexed files.

### The seed pair

v042 adds two tables with no fixed-value rows — there are no seed entries for `projects` or `indexed_files` because the tables start empty. The only seed implication is that `OpenAt` in `internal/store/sqliteopen/db.go` wires two new query-set fields on the `DB` struct:

```go
// internal/store/sqliteopen/db.go
db.Projects     = index.NewProjectQ(sqldb)
db.IndexedFiles = index.NewIndexedFileQ(sqldb)
```

These are initialized after migrations run. No seed rows are needed because the tables are populated at runtime by the `learn` tool.

### The companion tool grant (v044a)

Tools are not automatically available to roles. v044a adds `learn` to the roles that need it:

```go
// [v044a] Grant the `learn` tool to roles that touch codebases
`UPDATE roles SET tools = json_insert(tools, '$[#]', 'learn')
 WHERE name IN ('thinking_partner','coder','planner','reviewer','researcher')
   AND NOT EXISTS (SELECT 1 FROM json_each(roles.tools) WHERE value = 'learn')`,
```

The seed in `seedRoles()` was simultaneously updated so fresh DBs get `learn` in the tools JSON from the start. v044a handles existing DBs.

### The `DB` struct

`internal/store/sqliteopen/db.go` wires `Projects *index.ProjectQ` and `IndexedFiles *index.IndexedFileQ` on the `DB` struct. The query implementations are in `internal/store/index/`. The two-layer split (`internal/store/schema/` for DDL, sub-packages like `index/` for Go query methods) is the standard pattern.

### What was NOT added

- No config keys (the `learn` tool reads `summary_model` and `embed_model`, which pre-exist).
- No prompt parts (behavior guidance is in the tool's `Description()` string).
- No role records (the `researcher` role pre-exists; v042 just adds to its tool list via v044a).

This is a clean example because v042 is purely additive: two new tables, one new index, one tool grant, and two new Go query types. Nothing had to change in existing tables.
