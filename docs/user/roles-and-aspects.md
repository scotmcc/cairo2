# Roles and aspects

Cairo has two layered mechanisms for controlling how it works: **roles** and **aspects**. Roles set the broad mode — which tools are available and which model runs. Aspects modulate behavior at a finer grain within a session.

---

## Roles

A role is a mode of focus, not a separate identity. Cairo has one memory pool and one soul regardless of role. The role tilts the turn in a direction: tool availability, model selection, and system-prompt framing.

### Built-in roles

| Role | Purpose | Notable tool restrictions |
|---|---|---|
| `thinking_partner` | Interactive collaboration — the default | All tools available |
| `orchestrator` | Coordinate a job, split work into tasks | read, bash, memory_tool, job, task, agent, search, fetch |
| `planner` | Research and design before implementation | read, bash, memory_tool, skill, learn, search, fetch |
| `coder` | Write and edit code, run tests | read, write, edit, bash, memory_tool, task, learn |
| `reviewer` | Verify output — run tests, check requirements | read, bash, memory_tool, task, learn |
| `dream` | Headless maintenance — consolidate memories and facts | memory_tool only |
| `researcher` | Gather facts, read code and context | read, bash, memory_tool, learn, search, fetch |

The `thinking_partner` role has no tool restrictions — every built-in tool is available. Roles with a non-empty allowlist can only use the listed tools.

Custom tools (tools you create and store in the DB) are always available regardless of role.

### Starting a session with a role

```bash
cairo -new -role coder
cairo -new -role planner -name "auth refactor"
cairo -tui -new -role reviewer
```

A session's role is set at creation and does not change. To switch roles, start a new session.

### Why the tool allowlist matters

The allowlist is a hard boundary, not just framing. A `reviewer` session doesn't have `write` or `edit` in its registry — those tools simply aren't callable, regardless of what the AI decides. The framing says "do not modify code"; the allowlist enforces it.

### Inspecting roles

Cairo stores roles as rows in the `roles` table. To see the current role configuration:

```bash
sqlite3 ~/.cairo/cairo.db 'SELECT name, model, tools FROM roles'
```

Or ask cairo directly in a session:
```
> show me all roles and their models
```
(Cairo will call `bash sqlite3` to pull the data.)

### Changing a role's model

Models are per-role and configurable:

```bash
sqlite3 ~/.cairo/cairo.db "UPDATE roles SET model='mistral-small:24b' WHERE name='coder'"
```

The change takes effect in the next session started with that role. Existing sessions are unaffected.

### Adding a custom role

A custom role needs a DB row and a matching prompt part. The cleanest way is to ask cairo in a `thinking_partner` session:

```
> add a role called security_auditor — it should only have read and bash,
  and its framing should focus on threat modeling
```

Cairo will use `bash sqlite3` to insert the role row and prompt part. Then:

```bash
cairo -new -role security_auditor
```

See `docs/concepts/roles.md` for the exact schema.

---

## Aspects (the Consider system)

Aspects are fine-grained behavioral modifiers that sit between the user and the main AI turn. When enabled, each aspect runs as a short parallel call before (or during) the main response, providing an inner-dialogue layer.

Think of aspects as persistent lenses: "always check for security implications," "flag when assumptions are unverified," "notice when the scope is growing."

### Enabling aspects

The Consider system is off by default. Enable it globally:

```bash
cairo config set consider.enabled true
```

Or ask cairo to do it:
```
> enable the consider system
```

### Managing aspects

Aspects are stored in the `consider_aspects` table. You can create, edit, enable, and disable them through the config panel in the TUI (`/config`) or by asking cairo directly.

**List current aspects:**
```
> what aspects do you have configured?
```

**Add an aspect:**
```
> add an aspect called "scope-check" that flags when a request
  seems to expand scope beyond what was originally asked
```

**Disable an aspect without deleting it:**
```
> disable the scope-check aspect
```

**Remove an aspect:**
```
> delete the scope-check aspect
```

In the TUI, the config panel (Ctrl+G) has a dedicated Consider tab for managing aspects visually.

### Aspect scope

Aspects affect all sessions while enabled. There is no per-session aspect toggle — if you want role-specific behavior, use the role's prompt framing instead (or disable the aspect for sessions where it's unwanted).

### When to use aspects vs roles

- Use a **role** when you want to switch the entire mode of operation: different tools, different model, different core framing.
- Use an **aspect** when you want to add a persistent behavioral check on top of your normal working mode — something that applies across sessions without changing which tools are available.
