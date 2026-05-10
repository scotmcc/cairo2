# Sessions

A session is a conversation. Cairo keeps one per working directory by default and resumes where you left off each time you launch.

---

## What a session is

Every session stores:

- **Messages** — the full turn history (user, assistant, and tool calls)
- **Role** — which mode cairo is operating in (`thinking_partner`, `coder`, etc.)
- **CWD** — the working directory the session was started from
- **Name** — an optional label you can set at creation

Sessions live in `~/.cairo/cairo.db` (table `sessions`). They share the same memory pool, identity, and soul as every other session — only the conversation is session-scoped.

---

## Creating a session

```bash
cairo -new                              # new thinking_partner session
cairo -new -role coder                  # new coder session
cairo -new -role planner -name "sprint" # new planner session, labeled
cairo -tui -new                         # new session, TUI mode
```

In the TUI you can also type `/new` to drain the current session's summarizer and start fresh without leaving the terminal.

---

## Resuming a session

By default `cairo` resumes the most recent session for the **current working directory**. Navigate to the same directory and launch — you pick up where you left off.

```bash
cairo               # resume most recent for this cwd
cairo -session 42   # resume a specific session by id
cairo -tui          # resume most recent in TUI
```

The `-session` flag works with any integer id shown by `/sessions`.

---

## Listing sessions

**In the line CLI:**
```
> /sessions
```
Output:
```
* [1] sprint — planner — 2026-05-09 14:20
  [2] (unnamed) — thinking_partner — 2026-05-08 11:00
```
The `*` marks the current session.

**In the TUI:**
```
/sessions
```
Same output, printed in the transcript area.

---

## Switching sessions

Sessions are chosen at launch, not mid-conversation. To switch:

1. Exit the current session (`/quit` or Ctrl+Q in TUI; `/exit` or Ctrl+D in CLI)
2. Relaunch with the desired session id:

```bash
cairo -session 42
cairo -tui -session 42
```

---

## Viewing the current session

In the line CLI:
```
> /session
```
Prints the session id, name, role, cwd, and when it was last active.

The TUI shows session info in the status bar at the bottom of the screen.

---

## Deleting a session

There is no `/delete` command. Deletion goes through direct DB access:

```bash
sqlite3 ~/.cairo/cairo.db "DELETE FROM sessions WHERE id=42"
```

Deleting a session cascades to its messages, summaries, and facts. Memories and skills are not session-scoped and are unaffected.

---

## CWD binding

Cairo records the working directory where a session was started. When you run `cairo` with no flags, it resumes the most recent session whose `cwd` matches the current directory. Sessions from other directories are not affected.

If you want to work across directories in the same session, use `-session <id>` explicitly.

---

## Cross-machine portability

Sessions themselves are not exported — they are conversation records and are local to the machine. What you can export and import is **identity**: the soul, memories, config, roles, and prompt parts that travel with you.

```bash
# Export your identity to a portable bundle
cairo export myidentity.cairo

# On another machine, import it
cairo import myidentity.cairo

# See what differs between a bundle and local state
cairo diff myidentity.cairo
```

The `--full` flag on `export` includes extended data (indexed projects, etc.). The `--force` flag on `import` overwrites local identity without prompting.

After importing, run `cairo` normally — a new session starts with the imported identity.

---

## Session role

A session's role is set at creation and does not change during the session. The role controls which tools cairo can use and which model framing is applied. To work in a different role, start a new session:

```bash
cairo -new -role reviewer
```

See [Roles and Aspects](roles-and-aspects.md) for the full role list.

---

## Long sessions and summarization

Cairo automatically compresses old conversation turns into summaries as a session grows. From your perspective nothing changes — the context is still there, just more efficiently represented. Summaries are searchable across sessions, so knowledge from a previous conversation is findable even after many new sessions have started.

The summarizer runs in the background after each turn. You can trigger it manually with `/dream` (which runs the full maintenance cycle including summarization).
