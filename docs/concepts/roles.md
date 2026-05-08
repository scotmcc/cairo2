# Roles

A role is a **mode of focus**, not a separate identity. The being has one voice, one memory pool, and one soul. A role just tilts the turn in a direction: which tools are allowed, which model runs, what framing goes into the system prompt.

---

## The seven built-in roles

| Role | What it's for | Default model | Tools allowed |
|---|---|---|---|
| `thinking_partner` | Interactive collaboration — the default | global default | all |
| `orchestrator` | Coordinate a job — split work into tasks, track progress | global default | read, bash, memory_tool, job, task, agent, search, fetch |
| `planner` | Research and design before implementation | global default | read, bash, memory_tool, skill, learn, search, fetch |
| `coder` | Write and edit code, run tests | `devstral-24b:latest` | read, write, edit, bash, memory_tool, task, learn |
| `reviewer` | Verify output — run tests, check requirements | global default | read, bash, memory_tool, task, learn |
| `dream` | Headless maintenance — consolidate memories, facts, and summaries | `ministral-8b:latest` | memory_tool |
| `researcher` | Gather facts — read code and context, return structured findings | global default | read, bash, memory_tool, learn, search, fetch |

The authoritative tool allowlist is in `docs/reference/tools.md` (Per-role availability table). The table above shows the current seeded defaults; role models are configurable and vary per installation.

Each role is a row in the `roles` table with four fields:

- **`name`** — `thinking_partner`, `coder`, etc.
- **`description`** — one-line summary of the role's purpose
- **`model`** — which Ollama model to use when this role runs (optional; falls back to `config.model`)
- **`base_prompt_key`** — which `prompt_parts` row supplies the role's framing (convention: `role:<name>`)
- **`tools`** — JSON array of tool names this role can use. Empty array means unrestricted.

---

## How a role shapes a turn

Three things happen when a session's role is, say, `coder`:

1. **Model selection.** `db.ResolveModel(db, "coder", fallback)` reads `roles.model` and returns `qwen35-35b-coding:latest` — a coding-tuned model. The thinking_partner uses a different (bigger, generalist) model.
2. **Tool filtering.** `tools.FilterByAllowlist` returns only the tools listed in `roles.tools`. The coder role has `write`/`edit` but not `soul`, `job`, or `agent` — those aren't in its allowlist.
3. **Prompt framing.** `BuildSystemPrompt` appends the `role:coder` prompt part, which reads:

> You are operating as a coder — one of Selene's focused attention modes. Your job is to implement: write clean, minimal code that solves the task exactly as specified. Do not add features beyond what was asked. Do not refactor surrounding code. Test your work with bash before reporting done.

Same being, different framing.

---

## Switching roles

Every session has a role, set at creation:

```bash
cairo -new -role coder       # start a new session in coder mode
cairo -new -role planner     # start in planner mode
cairo -new                    # default: thinking_partner
```

A session's role does not change after it's created. If you want a different mode, start a new session or use `agent(action="spawn")` to delegate to a task with a different assigned role.

You can inspect and change the *model* for a role at any time via the `db_access` skill:

```
> show roles
[calls bash sqlite3 ~/.cairo/cairo.db 'SELECT name, model FROM roles']
> change the coder's model to a smaller one
[calls bash sqlite3 ~/.cairo/cairo.db "UPDATE roles SET model='mistral-small:24b' WHERE name='coder'"]
```

---

## Adding your own roles

Roles are just DB rows. A new role needs:

1. **A row in `roles`** with a name, description, model, `base_prompt_key`, and tools array. Use the `db_access` skill:

```
bash sqlite3 ~/.cairo/cairo.db "INSERT INTO roles (name, description, model, base_prompt_key, tools) VALUES ('security_auditor', 'threat modeling and vulnerability review', '', 'role:security_auditor', '[\"read\",\"bash\",\"memory_tool\"]')"
```

2. **A matching prompt part** with `key = "role:<name>"` (convention) or whatever you set in `base_prompt_key`. Insert via `db_access`:

```
bash sqlite3 ~/.cairo/cairo.db "INSERT INTO prompt_parts (key, content, trigger, load_order) VALUES ('role:security_auditor', 'You are operating as a security auditor...', 'role:security_auditor', 100)"
```

3. **Start a session with it:** `cairo -new -role security_auditor`.

---

## Why the tool allowlist matters

The tool allowlist is the main way a role changes behavior beyond its framing. A reviewer role without `write` or `edit` simply can't modify files — those tools aren't in its registry this turn. The framing says "do not modify code"; the allowlist makes it impossible.

This is belt-and-suspenders. The framing sets intent; the allowlist enforces it.

---

## Tool filtering semantics

From `tools.FilterByAllowlist`:

- **Empty `roles.tools` JSON array** → unrestricted. Role gets every built-in tool.
- **Non-empty array** → intersection: role gets only tools whose names appear in the array.
- **Custom tools** are *always* available regardless of allowlist. Custom tools are the being's own work product; restricting a role from using its own tools didn't match the rhizomatic intent.

Names in the allowlist that don't match any registered tool are silently ignored.

---

## Known rough edges

- **Role management is DB-direct.** There is no dedicated `role` tool (it was removed in v0.3.0). All role management goes through the `db_access` skill with `bash sqlite3`. This is intentional — direct DB access is more flexible than a thin wrapper.
- **Role framings are built-in defaults.** The seven default prompt-part framings live in `internal/store/seed.go`. Edits to that seed don't back-populate existing DBs (seed uses `INSERT OR IGNORE`). To customize existing framings, update the `prompt_parts` row directly via `db_access`.
