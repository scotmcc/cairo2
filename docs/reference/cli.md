# CLI reference

`cairo` — the binary. All modes share one binary; behavior depends on flags and subcommands.

---

## Synopsis

```
cairo [flags]                              # interactive / single-message mode
cairo export [--full] <bundle.cairo>       # export identity
cairo import [--force] <bundle.cairo>      # import identity
cairo diff <bundle.cairo>                  # compare a bundle to local
cairo dream                                # headless memory-consolidation cycle
cairo learn [flags] [path]                 # per-project file indexer with summaries
cairo serve [--port N] [--auth]            # HTTP server: chat, OpenAI-compat, JSONRPC
cairo token                                # generate bearer token for serve --auth
```

`cairo -h` prints the top-level summary; `cairo <subcommand> -h` prints a subcommand's options.

---

## Interactive flags

| Flag | Type | Default | Effect |
|---|---|---|---|
| `-new` | bool | false | Start a new session instead of resuming the most recent one for this cwd |
| `-session <id>` | int64 | 0 | Resume a specific session by id |
| `-name <label>` | string | "" | Optional label for a new session |
| `-role <role>` | string | `thinking_partner` | Role for a new session: `thinking_partner`, `orchestrator`, `planner`, `coder`, `reviewer`, or any role you've added |
| `-tui` | bool | false | Use the Bubble Tea TUI instead of the line CLI |
| `-task <id>` | int64 | 0 | **Background task mode.** Run as a subprocess worker for this task id. Writes results to the DB and exits. Spawned by `agent(action="spawn")` — not usually invoked directly. |
| `-background` | bool | false | Background mode: plain log output, no banner. Pairs with `-task`. |
| `-data-dir <path>` | string | "" | Override the data directory (default: `$CAIRO_DATA_DIR` or `~/.cairo`) |
| `-discipline <mode>` | string | `full` | Discipline mode for this session: `readonly` (no state changes), `scoped` (file writes within CWD only), or `full` (current behavior, all tools). Persisted to the session so background agents see the same mode. |
| `-vscode` | bool | false | VS Code integration mode: emit JSONL events over stdout instead of terminal rendering |

Positional args after flags are treated as a **single-message prompt**: `cairo "one-shot question"` sends one message and exits.

---

## Interactive modes

**Resume (default).** `cairo` with no flags resumes the most-recent session for the current working directory.

```bash
cairo                       # resume most recent
cairo -session 42           # resume by id
```

**New session.** `cairo -new` starts a fresh session.

```bash
cairo -new                               # new thinking_partner session
cairo -new -role coder                   # new coder session
cairo -new -role planner -name "refactor"  # new planner session, labeled
```

**TUI.** `cairo -tui` runs the richer terminal interface. Works for new or resumed sessions.

```bash
cairo -tui                  # resume most recent in TUI
cairo -new -tui             # new session in TUI
```

**Single-message.** Pass a prompt as positional args to send one message and exit.

```bash
cairo "summarize what we did last session"
cairo -new "start a new thread — what does this codebase do?"
```

Single-message mode streams the response to stdout, waits for the background summarizer to drain, then exits.

---

## Slash commands (line CLI)

In the line CLI, `/` at the start of input is a local command, not a message.

| Command | Effect |
|---|---|
| `/help` | Show available commands |
| `/init` | Run the guided setup skill (learns project + preferences) |
| `/init codebase` | Run the codebase-exploration skill (skips personal questions) |
| `/session` | Show current session info |
| `/sessions` | List all sessions |
| `/jobs` | List all jobs |
| `/memories` | List stored memories |
| `/pinned` | List all pinned memories |
| `/pin <id>` | Pin a memory — protects it from nightly auto-dump |
| `/unpin <id>` | Remove the pin from a memory |
| `/tools` | List custom tools (tools the AI has written) |
| `/skills` | List skills |
| `/exit`, `/quit`, `/q` | Exit |

The TUI has a richer slash-drawer (type `/` on empty input) with additional commands:

| TUI Command | Effect |
|---|---|
| `/new` | Start a fresh session. Drains the current session's unsummarized backlog (SummarizeAll), then re-execs with `-new`. |
| `/deepen [topic]` | Second-pass context briefing. Selene searches memories, recent summaries, indexed projects, and facts, then reports what she currently knows. Optional topic argument scopes the search. |
| `/learn [path]` | Index a directory: walk files, summarize, embed. Defaults to session cwd. Accepts an optional path argument (`/learn ~/myproject`). |
| `/export [path]` | Export the current transcript to a markdown file. Defaults to `~/.cairo/exports/<session-id>-<timestamp>.md`. |
| `/reload` | Restart cairo in-place to pick up config changes (model, ollama_url, etc.). Equivalent to `exit + re-run` in the same terminal. |
| `/config` | Open the config panel (also `Ctrl+G`). |
| `/clear` | Clear the visible transcript (DB untouched). |
| `/pinned` | List all pinned memories. |
| `/pin <id>` | Pin a memory — protects it from nightly auto-dump. |
| `/unpin <id>` | Remove the pin from a memory. |

See [TUI](../architecture/tui.md) for the full command list and panel hotkeys.

---

## Smart paste (TUI only)

Bracketed-paste events in the TUI that exceed either threshold are automatically diverted:

- **≥ 800 characters** — always diverted
- **> 6 lines** — always diverted

When diverted, cairo writes the paste to a temp file and inserts `@paste:N` at the cursor. A toast confirms the diversion. On submit, `PrefixExpander` reads the temp file and appends its content under a `Pasted content` heading to what Selene receives. The temp file is deleted on first consumption.

Pastes below both thresholds fall through to the textarea normally.

---

## Subcommands

### `cairo learn [flags] [path]`

Walk a directory, summarize each file with the summary model, embed the summary, and store everything in the `projects` and `indexed_files` tables. Honors `.gitignore` and a built-in set of always-skipped directories.

| Flag | Effect |
|---|---|
| `--path <dir>` | Directory to index (default: current directory). Equivalent to passing `path` as a positional argument. |
| `--project <name>` | Project name (default: directory basename) |
| `--summary-model <model>` | Override the `summary_model` config key for this run only |
| `--exclude <patterns>` | Comma-separated additional glob patterns to exclude from the walk |
| `--task <id>` | Task ID for progress reporting (background mode — not usually invoked directly) |
| `--background` | Background mode: silence stderr progress output |

Example:

```bash
cairo learn                          # index current directory
cairo learn ~/myproject              # index a specific path
cairo learn --summary-model llama3  # use a different model for this run
cairo learn --exclude "*.gen.go,vendor/**"
```

### `cairo export [--full] <bundle.cairo>`

Export the current identity to a `.cairo` bundle (gzipped tar with manifest.json + a snapshot of the DB).

| Flag | Effect |
|---|---|
| `--full` | Include conversation history (sessions, messages, summaries, facts, jobs, tasks, task_artifacts). Default excludes them — the bundle carries identity without private transcripts. |

Example:

```bash
cairo export selene-snapshot.cairo          # identity only
cairo export --full selene-full.cairo       # everything
```

Output:

```
exported to selene-snapshot.cairo
  format: identity-only (no conversation history)
  memories: 47  skills: 3  roles: 5  prompt_parts: 12
```

### `cairo import [--force] <bundle.cairo>`

Replace the current DB with the contents of a bundle. Backs up the existing DB alongside the target before overwriting.

| Flag | Effect |
|---|---|
| `--force` | Skip the interactive confirmation |

Example:

```bash
cairo import selene-snapshot.cairo
```

Interactive prompt:

```
bundle:
  exported: 2026-04-22T15:30:00-05:00
  format:   identity-only (no conversation history)
  memories: 47  skills: 3  roles: 5  prompt_parts: 12
this will REPLACE your current cairo identity with the contents of the bundle.
a backup will be written alongside. proceed? [y/N]: y
backup: /Users/scot/.cairo/cairo.db.pre-import-20260423T153045Z
imported into /Users/scot/.cairo/cairo.db — next cairo run uses the bundle's identity
```

### `cairo diff <bundle.cairo>`

Compare a bundle to the local DB without touching anything.

```bash
cairo diff selene-snapshot.cairo
```

Output:

```
bundle (2026-04-22T15:30:00-05:00) vs local:
    memories        local=52   bundle=47
  * skills          local=3    bundle=5
    notes           local=2    bundle=0
    roles           local=5    bundle=5
    prompt_parts    local=12   bundle=12
    custom_tools    local=1    bundle=3
    config_keys     local=14   bundle=14

soul matches

role→model differs:
  coder: local="qwen35-35b-coding:latest" bundle="mistral-small-24b:latest"
```

A `*` marker highlights count deltas.

### `cairo dream`

Run a headless maintenance cycle in the `dream` role. Selene (or your renamed persona) is given one instruction — "begin your maintenance cycle; review and consolidate all memories, facts, and summaries" — and exits when it returns. No interactive prompt, no banner; event output streams as plain log lines (same renderer the `-background` mode uses).

Before any work starts, `dream` snapshots the live DB into `~/.cairo/backups/dream-YYYY-MM-DD-HH-MM.cairo` using `VACUUM INTO`, so if a consolidation pass goes sideways you can `cairo import` the pre-dream bundle to roll back. If the backup fails, `dream` aborts without touching the DB.

```bash
cairo dream
```

Takes no flags or positional arguments. Intended for scheduled runs (cron, launchd, systemd timer) or manual "clean up after a long work session" invocations.

### `cairo serve`

Start the cairo HTTP server. Exposes the following endpoints:

| Endpoint | Description |
|---|---|
| `POST /api/chat` | Native chat API |
| `GET /v1/models` | OpenAI-compatible model list |
| `POST /v1/chat/completions` | OpenAI-compatible chat completions |
| `POST /rpc` | JSON-RPC 2.0 |
| `GET /rpc/stream/{id}` | SSE streaming for an in-flight RPC call |
| `GET /healthz` | Health check (unauthenticated) |

Requests are serialized through a session bridge — concurrent requests queue rather than parallelize. Session context is the most recent session for the current directory.

| Flag | Effect |
|---|---|
| `--port N` | TCP port to listen on (default: 1337 or `server_port` config key) |
| `--auth` | Require Bearer token authentication; generate a token with `cairo token` |

Example:

```bash
cairo serve                     # listen on :1337, no auth
cairo serve --port 8080         # listen on :8080
cairo serve --auth              # require bearer token (reuses stored token or generates one)
```

### `cairo token`

Generate a cryptographically random bearer token, store it in the `server_token` config key, and print it to stdout. Re-running overwrites the stored token. Use with `cairo serve --auth`. Takes no flags.

```bash
cairo token
# prints: a1b2c3d4e5f67890
```

---

## Environment

| Variable | Effect |
|---|---|
| `CAIRO_DATA_DIR` | Override the data directory (equivalent to `-data-dir`). Takes precedence over the default `~/.cairo`. |
| `OLLAMA_URL` | Override the Ollama server URL without touching the DB config. Takes precedence over `ollama_url` in config. Useful for CI, Docker, or headless setups. |
| `HOME` | Used to resolve `~/.cairo/` when `CAIRO_DATA_DIR` is not set |

Cairo doesn't read any other environment variables at the top level. Custom tools can read the environment they're given; see [Custom tools](../development/custom-tools.md).

---

## Paths

| Path | Purpose |
|---|---|
| `~/.cairo/cairo.db` | The main SQLite database — the being itself |
| `~/.cairo/cairo.db-wal`, `~/.cairo/cairo.db-shm` | SQLite WAL sidecars (auto-managed) |
| `~/.cairo/cairo.db.pre-import-<timestamp>` | Backup written before `cairo import` |
| `~/.cairo/backups/dream-<timestamp>.cairo` | Snapshot bundle written at the start of each `cairo dream` run |
| `~/.cairo/dreams/<YYYY-MM-DD>.md` | Narrative dream file written by the dreamer role at the end of each `cairo dream` run |
| `~/.cairo/logs/task_<id>.log` | Stdout/stderr capture for background tasks |

---

## Exit codes

- **0** — clean exit
- **non-zero** — any fatal error; message goes to stderr

Single-message mode propagates agent errors as non-zero exits; the background-task mode marks the task `failed` in the DB and exits non-zero.
