# Quickstart

Five minutes. Assumes you've already done [Installation](installation.md) — cairo binary on PATH, Ollama running, at least one chat model and one embedding model pulled.

---

## 1. Start a session

```bash
cairo -new
```

You'll see:

```
cairo · Selene · session 1 · role:thinking_partner
type /help for commands, /exit to quit

(Selene is here but hasn't met you yet — type /init to introduce yourself, or /config for direct setup)

>
```

---

## 2. Say hi

```
> hi, who are you?

Selene: Hi — I'm Selene. I don't know you yet. What should I call you?
```

Answer. Selene will store your name, explain what comes next, and ask about your project. See [First run](first-run.md) for the full `/init` flow.

If you want to skip the conversational setup, just start asking questions — Cairo works fine without `/init`, it's just smoother with it.

---

## 3. Try the TUI

The Bubble Tea TUI is richer than the line CLI — better transcript rendering, panels, motion.

```bash
cairo -new -tui
```

Key bindings to try:

- **Enter** — submit message
- **Alt-Enter** / **Shift-Enter** — newline within a message
- **Ctrl-C** — context-sensitive stop (cancel streaming / clear input / clear transcript)
- **Ctrl-D** — exit (on empty input)
- **/** (first char) — open slash command drawer (`/new`, `/learn`, `/reload`, `/config`, etc.)
- **@** (at word boundary) — open file picker, inject file path
- **!** (first char) — run a shell command and use its output as the message
- **Ctrl-?** — toggle help panel
- **Ctrl-E** — toggle memory spotlight panel
- **Ctrl-T** — toggle threads panel (also shows `learn add` progress)
- **Ctrl-P** — toggle prompt preview panel (fullscreen rail+detail view; `e` to edit Steering/Context)

**Smart paste:** if you paste more than 800 characters or 6 lines, the TUI automatically saves it to a temp file and inserts `@paste:N` in the input. On submit the full content reaches Selene; the textarea stays clean. Pastes below the threshold land in the textarea normally.

---

## 4. One-shot mode

For scripts or quick questions, pass a message as arguments:

```bash
cairo "what files are in internal/tools/?"
```

Cairo runs the message, streams the response to stdout, waits for background summarizer to finish, and exits. Resumes the most-recent session for the current working directory.

`cairo -new "..."` starts a fresh session for the one-shot.

---

## 5. Explore a codebase

Fire up cairo inside a project and let it tour itself:

```bash
cd ~/some-project
cairo -new
> /init codebase
```

Cairo will ls, read key files (README, CLAUDE.md, go.mod, etc.), and store what it learns as memories. Future sessions in this directory start already knowing the project.

---

## 6. Slash commands

In the line CLI:

- **`/help`** — list commands
- **`/init`** — guided setup
- **`/init codebase`** — codebase exploration only
- **`/session`** — current session info
- **`/sessions`** — list all sessions
- **`/memories`** — list stored memories
- **`/skills`** — list skills
- **`/jobs`** — list background jobs
- **`/tools`** — list custom tools (AI-written)
- **`/exit`** — exit

The TUI's slash drawer (type `/` on empty input) has additional commands:

- **`/new`** — start a fresh session (drains the current session's summarizer first)
- **`/learn [path]`** — index a directory; defaults to session cwd
- **`/reload`** — restart cairo in-place to apply config changes
- **`/config`** — open the config panel

---

## 7. Change the model

Cairo's chat model defaults to whatever `config.model` says. To change it:

```
> set model to qwen3:30b-a3b-instruct
  [calls bash sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('model', 'qwen3:30b-a3b-instruct')"]

(next session will use the new model; current session keeps the one it started with)
```

Role-specific models override the global default. The `coder` role defaults to `devstral-24b:latest`. Change per-role via the `db_access` skill:

```
> change the coder's model to mistral-small:24b
  [calls bash sqlite3 ~/.cairo/cairo.db "UPDATE roles SET model='mistral-small:24b' WHERE name='coder'"]
```

---

## 8. Switch roles

Each session has one role. Start a session in a specific role:

```bash
cairo -new -role planner    # planning mode
cairo -new -role coder      # coding mode
cairo -new -role reviewer   # reviewing mode
```

See [Roles](../concepts/roles.md) for what each one's like.

---

## 9. Your identity, in a file

Everything cairo knows about you and your work is in one SQLite database at `~/.cairo/cairo.db`. You can inspect it directly:

```bash
sqlite3 ~/.cairo/cairo.db "SELECT id, content FROM memories;"
sqlite3 ~/.cairo/cairo.db "SELECT key, value FROM config;"
```

Or export a snapshot:

```bash
cairo export snapshot.cairo
```

See [Portable identity](portable-identity.md) for the `export`, `import`, `diff` workflow.

---

## Next steps

- Run through [First run](first-run.md) to understand what `/init` does
- Read [Philosophy](../concepts/philosophy.md) to see why Cairo is shaped the way it is
- Skim [Built-in tools](../reference/tools.md) for what the model can call
- Check [Roadmap](../../ROADMAP.md) for where things are headed
