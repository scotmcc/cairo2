# Adding a Config Key

Config keys live in the SQLite `config` table: `key TEXT PRIMARY KEY, value TEXT, updated_at INTEGER`. Every new key requires three coordinated changes: a constant, a seed entry, and a migration backfill. Skipping either the seed or the migration breaks one of the two DB paths (fresh install vs. existing install).

---

## 1. Where Config Keys Are Defined

### Constants: `internal/store/config_keys.go`

All key names are declared as typed constants:

```go
// internal/store/config_keys.go:5
const (
    KeyModel          = "model"
    KeyEmbedModel     = "embed_model"
    KeyOllamaURL      = "ollama_url"
    KeyKokoroURL       = "kokoro_url"
    KeyKokoroVoice     = "kokoro_voice"
    KeyToolOutputLimit = "tool_output_limit"
    // ...
)
```

These constants are used throughout the codebase in place of bare string literals. Any file that reads or writes a config key should import and use the constant, not a string literal. This gives you a compile-time error if a key name changes.

### Default values: `internal/store/seed.go`

`seedConfig()` inserts defaults into a fresh database using `INSERT OR IGNORE`:

```go
// internal/store/seed.go:44
func (db *DB) seedConfig() error {
    defaults := map[string]string{
        "ollama_url":        "http://localhost:11434",
        "model":             "devstral-24b:latest",
        "tool_output_limit": "65536",
        "kokoro_url":        "",
        "kokoro_voice":      "af_heart(8)+af_nicole(2)",
        // ...
    }
    for k, v := range defaults {
        if _, err := db.sql.Exec(
            `INSERT OR IGNORE INTO config(key, value) VALUES(?, ?)`, k, v); err != nil {
            return err
        }
    }
    return nil
}
```

`INSERT OR IGNORE` means re-running seed (which happens on every `OpenAt` call) does not overwrite values the user has changed. New keys that aren't in the DB get inserted; existing keys are untouched.

### Migration backfill: `internal/store/schema.go`

The migration slice handles existing DBs that pre-date the new key:

```go
// internal/store/schema.go — example from v030
// [v030] Seed kokoro_url and kokoro_voice config keys for existing DBs
`INSERT OR IGNORE INTO config(key, value) VALUES('kokoro_url', '')`,
`INSERT OR IGNORE INTO config(key, value) VALUES('kokoro_voice', 'af_heart(8)+af_nicole(2)')`,
```

Same `INSERT OR IGNORE` pattern. The migration runs once on each existing DB and is idempotent by construction.

---

## 2. The Seed-vs-Migration Dual

This is the most important invariant to understand:

| DB state | What runs | What provides the key |
|---|---|---|
| Fresh install | `OpenAt` runs `schema` + seed | `seedConfig()` inserts the default |
| Existing install | `OpenAt` runs pending migrations, then seed | migration `INSERT OR IGNORE` inserts it first; seed's `INSERT OR IGNORE` no-ops |

If you only add to `seedConfig()` and skip the migration: existing users never get the key. Their code paths that call `db.Config.Get(KeyMyKey)` return `("", nil)` — an empty string with no error. That silently means "key not set", which often looks like a configuration mistake rather than a missing default.

If you only add the migration and skip `seedConfig()`: fresh installs get the key via the migration (migrations run before seed on every open), but the seed's record of what defaults exist is incomplete. This matters less in practice because migrations run before seed, but it's still a gap in the source of truth.

**Always add both.**

---

## 3. The Access Pattern

All config access goes through `db.ConfigQ`, which lives in `internal/store/config.go`:

```go
// internal/store/config.go:8
type ConfigQ struct{ db *sql.DB }

func (q *ConfigQ) Get(key string) (string, error)
func (q *ConfigQ) GetWithDefault(key, defaultValue string) string
func (q *ConfigQ) GetRequired(key string) (string, error)
func (q *ConfigQ) Set(key, value string) error
func (q *ConfigQ) All() (map[string]string, error)
```

There are no typed accessor functions per key (no `GetOllamaURL()`, no `SetOllamaURL()`). The pattern is to call `db.Config.Get(db.KeyOllamaURL)` directly. The constant from `config_keys.go` provides the type safety.

### Reading a config value from a tool

```go
// from internal/tools/say.go:55
kokoroURL, _ := t.db.Config.Get(db.KeyKokoroURL)
if kokoroURL == "" {
    return agent.ToolResult{Content: "say: no kokoro_url configured — skipped"}
}
```

Ignore the error when the key is optional — `Get` returns `("", nil)` for missing keys, and `("", err)` only for actual DB errors. An empty string typically means "not configured".

Use `GetRequired` when the feature cannot proceed without the key:

```go
url, err := db.Config.GetRequired(db.KeySearxNGURL)
if err != nil {
    return agent.ToolResult{Content: err.Error(), IsError: true}
}
```

Use `GetWithDefault` when you want a fallback value without checking for empty:

```go
// internal/store/config.go:19
func (q *ConfigQ) GetWithDefault(key, defaultValue string) string {
    val, _ := q.Get(key)
    if val == "" {
        return defaultValue
    }
    return val
}
```

### Reading from the TUI (panel code)

Panel code accesses config through `m.db.Config.Get(key)` with the same API. The config panel in `panel_config.go` uses `configValueOf(m, key)`, which is a wrapper that handles both regular config keys and the special `role:<name>:<field>` synthetic keys.

---

## 4. Surfacing in `/config`

The `/config` panel (`internal/tui/panel_config.go`) does **not** auto-discover config keys. It uses an explicit layout definition:

```go
// internal/tui/panel_config.go:64
var configLayout = []configSectionDef{
    {
        title:  "Identity",
        accent: colVoiceSelene,
        keys: []configKeyDef{
            {key: "ai_name", label: "ai_name"},
            {key: "user_name", label: "user_name"},
        },
    },
    {
        title:  "LLM Backend",
        accent: colTool,
        keys: []configKeyDef{
            {key: "ollama_url", label: "ollama_url"},
            {key: "model", label: "model", hint: "Enter opens the Ollama model picker"},
            // ...
        },
    },
    // ...
}
```

To expose a new key in the `/config` panel, add a `configKeyDef` to the appropriate section:

```go
{key: "my_key", label: "my_key", hint: "Brief description of what it does and what values are valid"},
```

The `hint` string appears beneath the field when the user selects it. Use it to document the format ("30m / 1h / -1 for indefinite"), the default, or what happens at the boundaries.

If none of the existing sections fits, add a new `configSectionDef` entry. Also add a tagline for it in `configSectionTagline(title string)` — that function is a switch over section titles returning a one-line description shown in the right pane.

Keys not listed in `configLayout` are still readable and writable via direct sqlite3 access (`db_access` skill) — the panel is just the UI surface, not the authoritative source of keys.

---

## 5. Documenting the Key

Add a row to `docs/reference/config-keys.md` in the appropriate section:

```markdown
| `my_key` | `default_value` | What this key controls and when it applies. |
```

If the key controls a feature that has behavioral edge cases (empty = disable, 0 = unlimited, etc.), document those in the description column. Look at `tool_output_limit` and `kokoro_url` for examples of keys that are no-ops when empty.

---

## 6. Edge Cases

### Empty value means "not configured"

`db.Config.Get` returns `("", nil)` for missing rows and for rows where `value = ''`. There is no distinction between "key exists with empty value" and "key does not exist". Code that reads config keys must treat `""` as "not configured" and handle it gracefully — either returning early, using a default, or returning an error. Do not assume a non-empty value.

### Defaulting at read time vs. seed time

Prefer defaulting at seed time (via `seedConfig()`) so the DB is always in a predictable state. Defaulting at read time (`GetWithDefault`) is acceptable for internal defaults that should not be user-visible, but it creates a gap: the user cannot inspect or override the value via direct DB query until they explicitly set it.

The current codebase uses both patterns. `tool_output_limit` is seeded (and therefore visible in `SELECT key, value FROM config`). The `say` tool hardcodes `"af_heart(8)+af_nicole(2)"` as a fallback when `kokoro_voice` is empty — but `kokoro_voice` is also seeded with that default, so the fallback never actually fires on a well-initialized DB.

### User with old DB, missing key

When `Get` returns `""` and your tool needs a real value, return an informative error:

```go
v, _ := t.db.Config.Get(db.KeyMyKey)
if v == "" {
    return agent.ToolResult{
        Content: "error: my_key is not configured — set it via db_access: bash sqlite3 ~/.cairo/cairo.db \"INSERT OR REPLACE INTO config (key, value) VALUES ('my_key', 'value')\"",
        IsError: true,
    }
}
```

This is better than a nil pointer or a cryptic downstream failure.

---

## 7. Walkthrough: `tool_output_limit`

This traces the full path of how `tool_output_limit` was added.

### Constant: `internal/store/config_keys.go:24`

```go
KeyToolOutputLimit = "tool_output_limit"
```

### Seed: `internal/store/seed.go:71`

```go
"tool_output_limit": "65536", // 64KB default
```

Added to the `defaults` map in `seedConfig()`. Fresh DBs get 65536.

### Migration: `internal/store/schema.go` (migration v035)

```go
// [v035] Default tool output size cap (64KB)
`INSERT OR IGNORE INTO config(key, value) VALUES('tool_output_limit', '65536')`,
```

Existing DBs get the same default via this migration. The comment tag `[v035]` is a convention for tracking which feature each migration corresponds to.

### Config panel: `internal/tui/panel_config.go`

`tool_output_limit` is absent from `configLayout`. It is intentionally not surfaced in the `/config` panel — it's an advanced tuning knob documented in `docs/reference/config-keys.md` under "Limits" but not something most users need to change. This is a valid design choice: not every key needs a UI entry.

### Usage: `internal/agent/loop.go`

The agent loop reads the limit and truncates tool results before appending them to the message history. The `db.KeyToolOutputLimit` constant is used at the read site.

### Reference: `docs/reference/config-keys.md`

Listed under "Limits" with its default (65536), type (integer), and behavioral description (truncation with notice).

---

## Checklist for a New Config Key

1. Add `KeyMyKey = "my_key"` to `config_keys.go`.
2. Add `"my_key": "default_value"` to `seedConfig()` in `seed.go`.
3. Add a new migration entry at the end of the `migrations` slice in `schema.go`:
   ```go
   `INSERT OR IGNORE INTO config(key, value) VALUES('my_key', 'default_value')`,
   ```
4. If the key should appear in `/config`, add a `configKeyDef` to `configLayout` in `panel_config.go`.
5. Add a row to `docs/reference/config-keys.md`.
6. Use `db.KeyMyKey` (the constant) wherever the key is read or written.
