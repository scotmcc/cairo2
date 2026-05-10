# Slash commands

Type `/` at the start of any input to run a local command rather than send a message to the AI. Commands are handled by cairo directly — they never go to the LLM.

---

## Availability by interface

Some commands are available in both the **TUI** (launched with `-tui`) and the **line CLI**. Others are TUI-only or CLI-only, noted in the table below.

---

## Full command reference

### Session and navigation

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/session` | ✓ | — | Show current session id, name, role, cwd, and last-active time. |
| `/sessions` | ✓ | panel | List all sessions with id, name, role, and last-active timestamp. The current session is marked with `*`. (TUI: open the sessions panel with Ctrl+B instead of typing /sessions.) |
| `/new` | — | ✓ | Drain the current session's summarizer and start a fresh session in the same terminal. Alias: `/fresh`. |
| `/reload` | — | ✓ | Restart cairo to pick up config changes (new model, `ollama_url`, etc.). Same terminal, fresh process. Alias: `/restart`. |
| `/export [path]` | — | ✓ | Export the current transcript to a markdown file. Defaults to `~/.cairo/exports/<session-id>-<timestamp>.md`. Accepts `~/...` paths. |

---

### Memory

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/memories` | ✓ | panel | List all stored memories with id and content. Pinned memories are marked `[P]`. (TUI: open the memory panel with Ctrl+E.) |
| `/pinned` | ✓ | ✓ | List only pinned memories. |
| `/pin <id>` | ✓ | ✓ | Pin a memory so it is never auto-dumped by the dream maintenance cycle. Example: `/pin 7`. |
| `/unpin <id>` | ✓ | ✓ | Remove the pin from a memory. Example: `/unpin 7`. |

---

### Jobs and tasks

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/jobs` | ✓ | panel | List all jobs with id, status, and title. (TUI: open the threads panel with Ctrl+T — the same data is shown as a live jobs/tasks tree.) |

---

### Custom tools and skills

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/tools` | ✓ | ✓ | List custom tools (not built-in tools). Shows name, description, and whether each tool is enabled. |
| `/skills` | ✓ | ✓ | List skills — reusable instruction blocks the AI can load. |

---

### Context and learning

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/deepen [topic]` | — | ✓ | Run a context briefing. Cairo searches memories, summaries, indexed projects, and facts, then reports what it currently knows about your active work. Optional topic narrows the focus, e.g. `/deepen auth refactor`. |
| `/learn [path]` | — | ✓ | Index a directory: walk files, summarize, and embed. Defaults to the session's cwd. Runs in the background; a toast confirms the task id. Example: `/learn ~/myproject`. |

---

### Dream (maintenance)

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/dream` | ✓ | ✓ | Trigger a dream-pass manually. Runs `cairo dream` as a subprocess. In the CLI, output streams to stdout synchronously. In the TUI, runs in the background and shows a toast on completion. |
| `/dreams` | ✓ | ✓ | List the 10 most recent dream-pass runs (id, date, mood, themes, narrative path). |
| `/dreams <id>` | ✓ | ✓ | Print or open the narrative for a specific dream by numeric id. |
| `/dreams <YYYY-MM-DD>` | ✓ | ✓ | Print or open the narrative for a specific date. |

---

### Configuration

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/config` | — | ✓ | Open the configuration panel to browse and edit settings (model, voice, memory limits, etc.). Hotkey: Ctrl+G. Alias: `/settings`. |

---

### Setup

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/init` | ✓ | ✓ | Run the guided setup. Cairo introduces itself, asks your name, and learns the current project. Safe to re-run. Pass `codebase` (`/init codebase`) to skip the personal questions and only learn the project. |

---

### Diagnostics

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/stackdump` | — | ✓ | Write a goroutine stack dump to `~/.cairo/stack_dump_<timestamp>.txt`. Useful for reporting hangs. Hotkey: Ctrl+\. Alias: `/stack`. |

---

### Help and exit

| Command | CLI | TUI | Description |
|---|---|---|---|
| `/help` | ✓ | ✓ | Show available commands. In the TUI, opens a help overlay (Esc to dismiss). Alias: `/?`. |
| `/clear` | ✓ | ✓ | Clear the visible transcript. Cairo's memory is untouched — this is view-only. |
| `/exit` | ✓ | ✓ | Exit cairo. |
| `/quit` | ✓ | ✓ | Exit cairo. Alias for `/exit`. In the TUI, drains the background summarizer before closing. Hotkey: Ctrl+Q. |
| `/q` | ✓ | — | Short alias for `/exit` in the line CLI. |

---

## Examples

```
# Start guided setup
/init

# See what sessions exist, then restart with a specific one
/sessions
# (exit, then: cairo -session 5)

# Index the current project in the background (TUI)
/learn

# Run a context briefing focused on auth
/deepen auth

# Pin memory id 3 so it survives the next dream cycle
/pin 3

# Trigger a dream-pass and wait for it (CLI)
/dream

# Show recent dream runs and open one
/dreams
/dreams 2026-05-08

# Export the current transcript
/export ~/Desktop/session-notes.md
```
