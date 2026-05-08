> **See also:** [memory-and-facts.md](memory-and-facts.md) for the broader memory model. Dreams are a nightly maintenance pass, not a memory layer.

# Dreams

A dream-pass is a nightly maintenance cycle that reviews and consolidates memories, facts, and summaries. It writes three artifacts:

1. **`dreams` row** — date, mood, themes, and a path to the narrative file.
2. **`dream_log` entries** — one row per mutation the pass made (merged memory, archived fact, etc.).
3. **Narrative file** — a Markdown document at `~/.cairo/dreams/<YYYY-MM-DD>.md` synthesizing what the pass found and what changed.

---

## Session-start context block

When you open a TUI session and a dream ran since the session was created, a `<ui-context>` block is injected for your first turn. It looks like this:

```
A dream-pass ran since this session began. Today's dream is at:

  ~/.cairo/dreams/2026-05-03.md

The dream-pass made these mutations (read dream_log if you need details):
- merge_memories: memories ids=[1,2] — merged A into B
- soft_archive: memories ids=[5] — low-weight duplicate

Before answering the user's first message, consider whether to read tonight's dream. The narrative may carry useful framing for today's work.
```

**How to respond:** if the user's first message is urgent, answer it. Otherwise read the narrative first — it carries context about what changed overnight that may be directly relevant.

---

## Reading the narrative

```bash
cat ~/.cairo/dreams/2026-05-03.md
```

Or use `/dreams <id>` from inside the TUI (opens in your editor) or CLI (prints to stdout).

---

## Reading dream_log

The `dream_log` table has one row per mutation. Columns: `id`, `dream_id`, `created_at`, `action`, `target_table`, `target_ids` (JSON array), `note`.

```sql
SELECT action, target_table, target_ids, note
FROM dream_log
WHERE dream_id = <id>
ORDER BY id;
```

From the `bash` tool:

```bash
sqlite3 ~/.cairo/cairo.db "SELECT action, target_table, note FROM dream_log WHERE dream_id = <id>;"
```

---

## Slash commands

### `/dreams`

Lists the 10 most recent dream-pass runs (TUI and CLI):

```
ID    Date        Mood      Themes                    Path
42    2026-05-03  focused   debugging, planning        ~/.cairo/dreams/2026-05-03.md
```

### `/dreams <id>` or `/dreams <YYYY-MM-DD>`

Opens the narrative in your editor (TUI) or prints it to stdout (CLI). Falls back to printing in the TUI when no GUI editor is detected.

### `/dream`

Manually triggers a dream-pass. TUI: runs in a background subprocess, shows a toast on completion. CLI: not available — run `cairo dream` from the shell directly.

---

## Triggering from the shell

```bash
cairo dream
```

Runs the full maintenance cycle: backup, writer role, curator role, nightly decay, dream agent, narrative generation, `last_dream_at` update.

---

## Schema quick-reference

```
dreams
  id            INTEGER PRIMARY KEY
  created_at    TEXT
  date          TEXT UNIQUE (YYYY-MM-DD)
  narrative_path TEXT
  themes        TEXT
  mood          TEXT
  state_daily_ref TEXT
  last_edited_at  TEXT

dream_log
  id            INTEGER PRIMARY KEY
  dream_id      INTEGER REFERENCES dreams(id)
  created_at    TEXT
  action        TEXT
  target_table  TEXT
  target_ids    TEXT  -- JSON array
  note          TEXT
```

`Dreams.Delete(id)` removes the narrative file, all `dream_log` entries, and the `dreams` row.
