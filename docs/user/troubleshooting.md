# Troubleshooting

Common problems and how to fix them.

---

## LLM is unreachable on first run

**Symptom:** Cairo starts but every message returns an error like `connection refused`, `no such host`, or `LLM error: context deadline exceeded`.

**Cause:** The `ollama_url` config key points to the wrong address (default is `http://localhost:11434` for a local Ollama server). If your LLM runs elsewhere, this needs updating.

**Fix:**

```bash
cairo config set ollama_url http://<your-server>:<port>
```

If you're using LiteLLM or another OpenAI-compatible server:
```bash
cairo config set ollama_url http://your-litellm-host:4000
cairo config set llm_api_key sk-yourkey
```

Verify by running a single-message prompt:
```bash
cairo "say hello"
```

If you see a response, the connection is good.

---

## I see the line CLI (the `>` prompt), not the TUI

**Symptom:** Running `cairo` drops you into a simple `>` REPL instead of the Bubble Tea full-screen interface.

**Cause:** The `-tui` flag is required. Bare `cairo` always starts the line CLI.

**Fix:**

```bash
cairo -tui          # resume most recent session in TUI
cairo -tui -new     # new session in TUI
```

You can add a shell alias to make this the default:
```bash
alias ai='cairo -tui'
```

---

## Web UI shows a Python error or "python3 not found"

**Symptom:** The web UI or a cairo operation prints an error mentioning `python3` or a Python traceback.

**Cause:** You have a stale version of cairo installed (pre-v0.3.2). Python 3 was removed as a dependency in v0.3.2. The current web agent runs on Node.js only.

**Fix:**

Update cairo by rebuilding from source:
```bash
bash scripts/build.sh
bash scripts/install.sh
```

Or reinstall from the latest package. After updating, verify the web agent version:
```bash
node web-agent/dist/server/server/src/index.js --version
```

---

## Where is my data?

Cairo stores everything in a single SQLite database.

**Default location:**
```
~/.cairo/cairo.db
```

**Override the location:**
```bash
export CAIRO_DATA_DIR=/path/to/data/dir
cairo
```

Or per-invocation:
```bash
cairo -data-dir /path/to/data/dir
```

**What's in the database:**

| What | DB table |
|---|---|
| Sessions and messages | `sessions`, `messages` |
| Memories | `memories` |
| Summaries | `summaries` |
| Facts | `facts` |
| Config key/value pairs | `config` |
| Identity (soul, roles, prompt parts) | `prompt_parts`, `roles`, `config` |
| Skills | `skills` |
| Custom tools | `custom_tools` |
| Jobs and tasks | `jobs`, `tasks` |
| Learn project index | `projects`, `indexed_files` |

**To inspect it directly:**
```bash
sqlite3 ~/.cairo/cairo.db .tables
sqlite3 ~/.cairo/cairo.db 'SELECT id, name, role, cwd FROM sessions ORDER BY last_active DESC LIMIT 10'
```

**Backup:** Before any destructive operation, back up the file:
```bash
cp ~/.cairo/cairo.db ~/.cairo/cairo.db.bak
```

---

## Cairo resumes the wrong session

**Symptom:** You navigate to a project directory, run `cairo`, and it picks up a session from a different project.

**Cause:** Cairo resumes the most recent session whose `cwd` matches the current directory. If the directory has changed (renamed, moved) or you want a specific session, you need to be explicit.

**Fix:**

List all sessions to find the right one:
```bash
cairo
> /sessions
```

Then restart with that id:
```bash
cairo -session 42
```

---

## The TUI is blank or rendering incorrectly

**Symptom:** The TUI launches but panels are blank, colors are wrong, or the layout is broken.

**Possible causes and fixes:**

1. **Terminal doesn't report background color correctly.** Cairo queries the terminal via OSC 11 to choose a theme. Some terminals (Waveterm, certain SSH setups) don't respond. Cairo falls back to a dark theme. If the colors are wrong, set the glamour style explicitly:
   ```bash
   cairo config set glamour_style light
   ```
   Valid values: `dark`, `light`, `notty`.

2. **Terminal window too small.** The TUI needs at least 80 columns and 24 rows. Resize and try again.

3. **Wrong `TERM` value.** Some SSH sessions set `TERM=vt100` which breaks advanced rendering. Set `TERM=xterm-256color` in your SSH config or shell profile.

---

## Memory search returns nothing relevant

**Symptom:** You ask cairo about something you've discussed before and it says it doesn't know.

**Cause:** The auto-injected memories (top 15) are by recency. Older memories may be there but not injected. The semantic search embedding may also not match if the `embed_model` config changed since those memories were written.

**Fix:**

Ask cairo to search explicitly:
```
> search your memories for anything about <topic>
```

If you changed `embed_model` since the memories were written, they won't appear in semantic search. Cairo warns about this at startup. To rebuild embeddings, run:
```bash
cairo dream
```

---

## `cairo dream` fails or does nothing

**Symptom:** `cairo dream` exits quickly with no output, or prints an error.

**Check the summarizer model:**
```bash
cairo config get summary_model
```

Make sure the model is actually pulled in your Ollama (or LiteLLM) backend:
```bash
ollama list
```

If the model isn't available, either pull it or point to a different one:
```bash
cairo config set summary_model mistral:7b
```

---

## Web UI can't find sessions or shows stale data

**Cause:** The web server is pointed at a different `cairo.db` than your CLI.

**Fix:** Set `CAIRO_DB_PATH` to match your CLI's data directory:
```bash
CAIRO_DB_PATH=~/.cairo/cairo.db bash scripts/cairo-web.sh
```

If `CAIRO_DATA_DIR` is set in your environment, the CLI honors it automatically. The web server requires it to be set explicitly via `CAIRO_DB_PATH`.

---

## Asking for more help

Run:
```bash
cairo -h           # top-level help
cairo dream -h     # subcommand help
cairo serve -h     # subcommand help
```

Or from inside a session:
```
> /help
```

For the full reference, see `docs/reference/cli.md`.
