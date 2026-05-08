# TUI

`cairo -tui` launches the Bubble Tea terminal UI. The line-oriented CLI still exists (and runs by default); the TUI is an optional richer surface that subscribes to the same agent event bus and adds panels, hotkeys, and motion.

Source: `internal/tui/`.

---

## High-level shape

```
┌────────────────────────────────────────────────┐
│  Selene  ·  session 42                         │  ← header
│━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━│
│                                                │
│  You: how should we structure the docs?        │
│                                                │
│  Selene: i'd suggest …                         │  ← transcript
│  [2 tools · 1.1 KB · read×2]                   │     (viewport)
│                                                │
│────────────────────────────────────────────────│
│  ▸ read  docs/architecture/tui.md  0.3s        │  ← tool toasts (ephemeral)
│  ▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱  32%  indexing cairo     │  ← progress bar
│────────────────────────────────────────────────│
│  ▸ tell me more about …|                       │  ← input (textarea, grows 1–8 rows)
│────────────────────────────────────────────────│
│  • thinking  ·  12 mem  ·  ◇ 2 threads   …     │  ← status bar
└────────────────────────────────────────────────┘
```

Regions, from top to bottom:

- **Header** — name + session label. Role appears as "mode: X" when non-default.
- **Top-position panels** (optional) — slide-in from top (prompt preview, inspector, diff).
- **Transcript viewport** — scrollable conversation history. Scroll with PgUp/PgDn/↑/↓ when input is empty.
- **Left/right-position panels** (optional) — side panels (memory spotlight, file picker, threads).
- **Slash drawer** (optional) — appears above the input when `/` is typed.
- **Bottom-position panels** (optional) — slide-in from bottom.
- **Input** — 3-row textarea. Enter submits; Alt-Enter / Shift-Enter inserts newline.
- **Status bar** — ambient stats (thinking pulse when streaming, memory count, thread count) and hint row.

---

## File structure

The TUI is split across multiple files rather than a single large file:

```
tui.go              Run, Update dispatcher, OSC leak filter, smart-paste logic
tui_model.go        model struct, newModel constructor
tui_view.go         View, render* helpers
tui_transcript.go   append* functions, appendTurnSummary, transcript management
tui_events.go       listenEvents, handleEvent
tui_handlers.go     handleKey, handleTick, and other input handlers
tui_command_env.go  CommandEnv implementation for the TUI frontend
activity.go         activityTracker type (activity states, awaiting/stale logic)
panels.go           panel framework (registration, toggle dispatch, O(1) index)
panel_*.go          individual panel files (10 panels)
tool_toasts.go      ephemeral per-tool-call toast rows (replaces inline rendering)
tool_family.go      toolFamily enum, familyOf/familyIcon/familyColor helpers
progress.go         global in-flight progress bars for background tasks
prefixes.go         PrefixExpander struct (!shell, @file, @paste handling)
style.go            color palette, lipgloss styles
```

`Update` (the Bubble Tea message dispatcher) lives in `tui.go`. It routes messages to `handleKey`, `handleTick`, `handleEvent`, or panel hooks, then returns. `View` lives in `tui_view.go`.

---

## Model struct

The `model` struct holds everything the View function needs. Key fields:

```go
type model struct {
    agent   *agent.Agent
    db      *db.DB
    session *db.Session

    viewport   viewport.Model
    input      textarea.Model   // auto-grows 1–8 rows; Alt-Enter inserts newline
    transcript *strings.Builder

    streaming bool
    cancel    context.CancelFunc

    aiName      string
    modelName   string
    memoryCount int
    threadCount int
    contextLen  int    // model context window in tokens (from model_ctx config key)
    tickCounter int

    expander    PrefixExpander   // handles !shell, @file, @paste prefixes
    pasteRefs   map[string]*PasteRef  // diverted-paste payloads keyed by id
    pasteCounter int                  // monotonically increasing per session

    progressTasks []*db.Task   // background tasks with SetProgress data
    toolToasts    []toolToast  // ephemeral foreground tool-call rows

    // Per-turn aggregate for the end-of-turn summary line
    turnToolCount int
    turnToolBytes int
    turnToolNames []string

    // Loop detection
    recentTools    []recentToolCall
    lastLoopWarnAt time.Time

    activity activityTracker  // what the agent is doing right now

    commands     []Command
    slashOpen    bool
    slashMatches []Command
    slashIndex   int

    openPanels   map[panelID]bool
    focusedPanel panelID

    threads, files, memory, prompt, sessions, inspector, diff, config, help  // per-panel state
    changedFiles []string   // file paths written/edited this session (for diff panel)

    reload     bool  // /reload: exec a fresh cairo process after tea.Quit
    newSession bool  // /new: re-exec with -new after draining summarizer

    eventCh <-chan agent.Event
    unsub   func()
    styles  styles
}
```

Three inputs feed `Update`:

1. **`tea.KeyMsg`** — keystrokes from the user.
2. **`eventMsg`** wrapping an `agent.Event` — streaming tokens, tool starts/ends, turn lifecycle.
3. **`tickMsg`** — fires every 300ms from a self-rescheduling timer. Drives the breathing "thinking" pulse and the thread spinner.

---

## activityTracker

`activityTracker` in `activity.go` tracks what the agent is doing right now and drives the state-aware status bar token. Key transitions:

```go
activity.SetIdle()
activity.SetStreaming()
activity.SetThinking()            // generic thinking
activity.SetThinkingPostTool()    // post-tool-call re-prefill gap
activity.SetTool(name, family)
activity.Tick()                   // called on each EventTokens / EventThinking arrival
```

**Activity states rendered in the status bar:**

| State | Condition | Label |
|---|---|---|
| idle | not streaming | (nothing shown) |
| streaming | tokens arriving | `● Selene` |
| awaiting | thinking, no activity yet, or gap > 1.5s | `⋯ awaiting model` |
| thinking (post-tool) | `cameFromTool` flag set | `⤓ processing tool result` |
| thinking (think tokens) | `thinkEvents > 0` | `❋ thinking · N think` |
| stale | thinking, silent > 30s | `⚠ silent Xs` |
| tool | active tool call | `<icon> <name>` in family color |

`Awaiting()` is `true` only when no activity has been seen yet this turn (`seenAnyActivity == false`) — i.e. genuine cold start / initial prefill. Once any token or tool event has arrived, `seenAnyActivity` flips and the between-tools gap is labeled as thinking, not awaiting. `Stale()` fires when the state is `thinking` and no event has arrived in 30 seconds.

The status bar also reads `activity.ToolCount()` and `activity.TurnElapsed()` for the in-flight `[N tools · Xs]` footer.

---

## Event subscription

The TUI subscribes to the agent bus at `newModel`:

```go
ch, unsub := a.Bus().Subscribe()
```

`listenEvents` in `tui_events.go` reads one event from the channel, wraps it as a `tea.Msg`, and returns. `Update` handles the event via `handleEvent` and re-issues `listenEvents` to keep the pump going. This is the standard Bubble Tea pattern for async sources.

When `EventTokens` fires, `handleEvent` calls `appendAssistantToken(token)` which writes into `m.transcript` and pushes into the viewport. Tool events drive the `toolToasts` slice rather than inline transcript marks — `EventToolStart` appends a new toast entry; `EventToolEnd` stamps the end time and result size. At turn end, `appendTurnSummary()` writes the dim one-liner `[N tools · X KB · name×count, ...]`. `EventToolStart` for write/edit tools also appends to `m.changedFiles` for the diff panel.

---

## Panel system

Panels are pluggable overlays registered at startup via `registerPanel` in their `init()` functions. Each has a `panelSpec` with ID, position, toggle key, and render/update hooks.

**Toggle key dispatch** is O(1) via `panelToggleIndex map[string]panelID`. Registering two panels with the same toggle key panics at startup — collisions are code bugs, not runtime data.

**10 registered panels:**

| Panel | Toggle | Position | Notes |
|-------|--------|----------|-------|
| help | `?` | fullscreen | hotkeys and panel list |
| memory | `Ctrl+E` | right | memory spotlight |
| prompt | `Ctrl+P` | fullscreen | rail+detail prompt preview; editable Steering and Context sections |
| threads | `Ctrl+T` | left | collapsible jobs/tasks tree |
| files | `Ctrl+O` | right | file picker |
| sessions | `Ctrl+B` | right | session browser |
| inspector | `Ctrl+Y` | fullscreen | model, context window, counts, token budget |
| diff | `Ctrl+D` | top | git diff of session-changed files |
| log | `Ctrl+L` | fullscreen | snapshot of ~/.cairo/cairo.log (last 1000 lines) |
| config | `Ctrl+G` | right | browse and inline-edit config keys by section |

**threads panel** — collapsible jobs/tasks tree with status icons. Space/Enter on a job row toggles expand/collapse. `o` on a task row opens its log file via `internal/hostedit` (VS Code / WaveTerm / `$EDITOR`).

**prompt panel** — fullscreen rail+detail layout. The rail lists prompt sections with per-section token estimates; the detail pane shows the content. Two sections are user-editable: `Steering` (top of prompt, user directives) and `Context` (after Soul, user identity/preferences). Press `e` to open the selected section in the host editor via `internal/hostedit`; press `r` to reload from disk and save to the config key; press `c` to cancel. Read-only sections show `· read-only — Selene owns this section`.

**inspector panel** — shows model name, context window size, memory/summary/fact counts, and a token budget breakdown.

**diff panel** — tracks files written or edited via tool events during the session, runs `git diff HEAD -- <file>` for each, and renders colorized output. Refresh with `r`.

**config panel** — sectioned view of config keys (Identity, LLM Backend, Voice, Memory, Limits, Search, Safety). Arrow keys navigate value rows (section headers are skipped). `Enter` opens an inline `textinput` for the selected key; `Enter` saves, `Esc` cancels. `r` reloads values from the DB.

`Update` routes a key first to the focused panel (if any), then to panel-toggle detection, then to slash-drawer handling, then to global hotkeys, then to the input field.

Adding a new panel is ~1 file in `internal/tui/panel_<name>.go` plus one `registerPanel` call in its `init()`.

`Update` routes a key first to the focused panel (if any), then to panel-toggle detection, then to slash-drawer handling, then to global hotkeys, then to the input field.

Adding a new panel is ~1 file in `internal/tui/panel_<name>.go` plus one `registerPanel` call in its `init()`.

---

## PrefixExpander and smart paste

`PrefixExpander` in `prefixes.go` is a struct with `WorkDir` and `PasteRefs` fields:

```go
type PrefixExpander struct {
    WorkDir   string
    PasteRefs map[string]*PasteRef   // shared with model; nil disables @paste
}
```

It's decoupled from `*model` and independently testable. It handles:

- **`!<command>`** — runs `<command>` via bash from `WorkDir` and uses the output as the user turn.
- **`@<path>`** — injects the named file's contents. The transcript shows `@path` but the agent sees the full body.
- **`@paste:N`** — reads the tempfile for diverted paste N, appends it under a `Pasted content` section heading. The tempfile is cleaned up on first consumption.

**Smart paste** (`tui.go`): bracketed-paste events (terminals that support it send pastes as a single `tea.PasteMsg`) that exceed either threshold are diverted:

```
smartPasteMinChars = 800   // runes
smartPasteMinLines = 6     // newlines + 1
```

`handleSmartPaste` writes the content to a temp file (`cairo-paste-*.txt`), increments `pasteCounter`, stores a `PasteRef` in `pasteRefs`, and inserts `@paste:N` at the cursor. A toast confirms the diversion. Pastes below the thresholds fall through to the textarea normally.

The paste is consumed (tempfile deleted, registry entry removed) when `Expand` processes the `@paste:N` token on submit.

---

## The slash drawer

Typing `/` as the first character of empty input opens the command drawer. It filters the registered command list as you type, selection via ↑/↓, Enter to run.

Commands are registered in `internal/tui/commands.go` and `internal/commands/commands.go`. The `CommandEnv` interface in `internal/commands/registry.go` is implemented by `tui_command_env.go` for the TUI and by the CLI for its own frontend. This decoupling means commands work in both surfaces without knowing which one they're in.

**Built-in TUI commands:**

| Name | Aliases | Hotkey | Notes |
|---|---|---|---|
| `quit` | `q`, `exit` | `Ctrl+Q` | Drains summarizer |
| `clear` | — | — | View-only |
| `help` | `?` | — | Opens help panel |
| `init` | — | — | Guided setup skill |
| `deepen` | — | — | Second-pass context briefing; `HandlerWithArgs`: accepts optional topic |
| `config` | `settings` | `Ctrl+G` | Opens config panel |
| `reload` | `restart` | — | Re-execs cairo process |
| `new` | `fresh` | — | Drains session, re-execs with `-new` |
| `learn` | — | — | `HandlerWithArgs`: accepts optional path |
| `export` | — | — | `HandlerWithArgs`: accepts optional output path; defaults to `~/.cairo/exports/` |

`HandlerWithArgs`, when set on a `Command`, takes precedence over `Handler` and receives any text typed after the command name (e.g. `/learn ~/myproject`). Used by `/learn` to accept an optional directory argument; commands that don't use args use `Handler` only.

---

## Input prefixes

See [PrefixExpander](#prefixexpander) above. Both prefixes live in `internal/tui/prefixes.go`.

`@` at a word boundary also opens the file picker panel for discoverability.

---

## Context-aware Ctrl-C

Ctrl-C means "stop whatever I'm doing," and the "whatever" shifts with state:

- **Streaming** → cancel the context, kill the LLM request. `runLoop` catches the cancel, persists partial text with `(interrupted)`, and the UI resets.
- **Input has content** → clear the input field.
- **Idle, input empty** → clear the transcript viewport. DB is untouched.

Three levels of "stop," each non-destructive relative to the DB. Ctrl-D on empty input exits the program.

---

## OSC leak filter

Some terminal emulators (Waveterm has been observed) respond slowly to Bubble Tea's OSC 10/11 background-color probes. The late response arrives *after* the input reader is live, so the raw escape sequence — like `]11;rgb:0000/0000/0000\` — leaks into the textarea.

`oscFilter` in `tui.go` detects the characteristic `alt+]` prefix followed by a body that matches `[0-9a-fA-F;:/rgbRGB()\\\s,.]+` and drops both messages. Conservative by design — lets real keystrokes through if unsure.

---

## Motion

Animation uses a 300ms tick:

- **Thinking pulse** — breathing `• thinking` in the status bar, alternates bright/dim bullet per tick. Low-amplitude on purpose (presence, not alarm).
- **Thread spinner** — four-frame `◇ ◈ ◆ ◈` rotation next to the running-task count when any background task is live.
- **Live counts** — memory and thread counts refresh on every tick via cheap `COUNT(*)` queries.

The tick is cheap and the refresh is synchronous with a DB hit. At default cadence that's a handful of counts per second while Cairo is open — negligible.

---

## internal/hostedit integration

`internal/hostedit` detects the terminal host environment and routes file-open requests to the appropriate editor without suspending the TUI for GUI hosts:

```
TERM_PROGRAM=vscode   → VS Code / Cursor: code -r -g <path>:<line>
TERM_PROGRAM=waveterm → WaveTerm: wsh editor <path>
TERMINAL_EMULATOR=JetBrains-JediTerm → GoLand / IntelliJ: idea/goland/webstorm --line N <path>
(none / unknown)      → $EDITOR (or vi): needs TUI suspend
```

`hostedit.WantsTUISuspend()` returns `true` only for the terminal-fallback case. GUI hosts open files in a separate pane without disturbing cairo. The prompt panel checks `WantsTUISuspend()` before offering to open an editable section — if a GUI host isn't detected, it shows a hint to use the `config` tool instead.

---

## Loop detection

`model.recentTools` is a ring buffer (max 32 entries) of `recentToolCall{name, at}` values. On each `EventToolStart`, `handleEvent` appends to the ring, counts how many entries with the same `name` fall within the last 90 seconds (`loopWarnWindow`), and fires a warn toast if the count reaches `loopWarnThreshold` (5). `loopWarnCooldown` (30s) rate-limits successive toasts for the same loop so a sustained repetition doesn't spam.

---

## Known rough edges

- **Textarea auto-grows 1–8 rows.** `syncInputHeight` in `tui.go` updates the height after every keystroke; `relayout` reclaims viewport rows freed by shrinking.
- **Mid-turn steering works.** Typing while Selene streams enqueues; the outer loop drains between inner-loop iterations. Steering events are not shown in the transcript until they run — the input just empties.
- **Viewport re-rendering on panel open/close is expensive at large transcripts.** Current transcripts fit fine; pathological sessions (hours, many tool calls) might show a hitch.
- **`contextLen` is read once at startup.** If the user switches models mid-session (via `config(set, model=...)`), the inspector panel's context window display won't update until restart.
- **`hostedit.Open` in the prompt panel is fire-and-forget for GUI hosts.** Cairo doesn't know when the user finishes editing — the `r` key is the manual reload signal.
