# Skills

A skill is a **named, reusable instruction block** stored as a row in the `skills` table. It is *content*, not code: when you `read` it, you treat the returned content as the next user turn and act on it.

> Note: skills are NOT prompt_parts. They live in the `skills` table and are managed via the `skill` tool. Prompt parts are different — they get pasted into the system prompt every turn (subject to trigger).

---

## Shape

| Field | Notes |
|---|---|
| `name` | Short identifier — `init`, `code_review`, `spike`. Unique. |
| `description` | One-line what-it-does. |
| `content` | The instruction text. Markdown. Supports `{{key}}` templates. |
| `tags` | JSON array of strings, optional. |

---

## When to make a skill

- Multi-step workflow you (or the user) will invoke by name more than once.
- Process you want to capture so a future you (or another role) executes it consistently.

When **not** to:

- One-off prose → just say it inline.
- Always-on framing/rule → use a `prompt_part` instead (loads every turn).
- A capability that needs to *do* something procedural with arguments → use a `custom_tool` instead.

See the comparison table in [skills.md](../getting-started/skills.md).

---

## Tool actions

```
skill(action="list")
skill(action="read",   name="<name>")
skill(action="create", name=..., description=..., content=..., tags=?)
skill(action="update", name=..., content=? description=? tags=?)
skill(action="delete", name=...)
skill(action="search", query=..., limit?, mode="semantic|exact|hybrid")
```

`update` accepts any subset of fields to change. `search` defaults to semantic.

---

## Examples already seeded

- `init` — guided setup. Asks the user's name, learns the project, captures preferences as memories. Long form.
- `init_codebase` — codebase exploration only. Skips personal questions; surveys cwd.

Read them with `skill(action="read", name="init")` to see the structure of a well-shaped skill.

---

## Writing a good skill

- Open with a one-line goal statement.
- Numbered steps with explicit stop points (*"wait for the user's OK"*).
- State proposals explicitly before acting (*"the experiment is: run Z..."*).
- Use `{{ai_name}}` / `{{user_name}}` instead of hard-coded names — substitution happens at dispatch time, so renames are safe.

---

## Invoking

Most natural path: the user says *"run the spike skill"* and you call `skill(action="read", name="spike")`. The returned content is what you do next.

You can also dispatch yourself: when a situation matches a skill you know exists, read it and execute it.

There is no slash-command shortcut for arbitrary skills (only `/init` and `/init codebase` are wired in the CLI today). Discovery is via `skill(action="list")` or `skill(action="search")`.

---

## Cross-references

- [Memory vs. skill vs. note vs. fact](memory-and-facts.md)
- [tools.md](tools.md) — `skill` action signatures
- Human reference: `docs/getting-started/skills.md`
