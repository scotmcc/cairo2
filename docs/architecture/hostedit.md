# Host editor

`internal/hostedit` routes file-open requests to whichever editor the user's terminal host makes available — VS Code, WaveTerm, JetBrains, or `$EDITOR` — without cairo needing to know which one.

Source: `internal/hostedit/hostedit.go`.

---

## Design rationale

Cairo already shells out to bash, ollama, git, and similar tools. Rather than embed a text editor inside the Bubble Tea TUI (which would be an entire subsystem), it leverages the existing GUI editor the user already has open in the same window. GUI hosts can open files in a separate pane or tab without disturbing cairo's rendering. The `$EDITOR` terminal fallback needs the TUI to suspend — `WantsTUISuspend()` tells callers when that's required.

---

## Detection: `Detect() Host`

`Detect` inspects environment variables on each call (cheap — just `os.Getenv`). Returns one of four `Host` values:

| `Host` constant | Detection condition |
|---|---|
| `HostVSCode` | `TERM_PROGRAM=vscode`, OR `VSCODE_IPC_HOOK_CLI` set, OR `VSCODE_PID` set |
| `HostWaveTerm` | `TERM_PROGRAM=waveterm`, OR `WAVETERM_VERSION` set, OR `WAVETERM` set |
| `HostJetBrains` | `TERMINAL_EMULATOR=JetBrains-JediTerm` |
| `HostUnknown` | none of the above |

Note: VS Code and Cursor both set `TERM_PROGRAM=vscode`, so Cursor is treated as VS Code (uses the `code` / `code-insiders` / `cursor` binary search in that order).

---

## Opening files: `Open(path string, line int) error`

`Open` builds and runs the appropriate editor command for the detected host:

| Host | Command | Line support |
|---|---|---|
| `HostVSCode` | `code -r -g <path>:<line>` | Yes — `<path>:<line>` syntax |
| `HostWaveTerm` | `wsh editor <path>` | No — `wsh` doesn't accept a line arg |
| `HostJetBrains` | `idea/pycharm/goland/webstorm --line <N> <path>` | Yes |
| `HostUnknown` | `$EDITOR <path>` (or `vi` if `$EDITOR` is empty) | Only if `$EDITOR` supports it |

Binary availability is checked with `exec.LookPath`. For VS Code, the first available binary in `[code, code-insiders, cursor]` is used. For JetBrains, the first available in `[idea, pycharm, goland, webstorm]` is used. If the binary isn't found, the host falls through to `$EDITOR`.

`$EDITOR` may contain multiple words (e.g. `"code --wait"`). `Open` splits on whitespace and builds the `exec.Cmd` from the parts.

For GUI hosts (`HostVSCode`, `HostWaveTerm`, `HostJetBrains`), `cmd.Start()` is called — fire-and-forget, cairo keeps rendering. For `HostUnknown`, `cmd.Run()` is called and stdio is inherited, so the terminal editor takes over the controlling terminal. The caller is responsible for suspending its TUI first (see `WantsTUISuspend()`).

---

## `WantsTUISuspend() bool`

Returns `true` only when `Detect() == HostUnknown`. GUI hosts open in a separate pane; terminal editors need the TUI out of the way.

Callers in a Bubble Tea program should check this before calling `Open`:

```go
if hostedit.WantsTUISuspend() {
    // show a message to the user explaining why editor can't open
    // or wrap with tea.ExecProcess
    return
}
if err := hostedit.Open(path, line); err != nil {
    // handle error
}
```

---

## Where it's used

**Prompt panel** (`internal/tui/panel_prompt.go`):

- The `Steering` and `Context` sections of the prompt panel are user-editable.
- Pressing `e` (or `Enter`) on an editable section:
  1. Checks `hostedit.WantsTUISuspend()` — if true, flashes a message to use the `config` tool instead (no TUI suspend is implemented here).
  2. Writes the current config value to a temp file (`cairo-steering-*.md` or `cairo-context-*.md`).
  3. Calls `hostedit.Open(tempfile, 0)`.
  4. Shows a flash: `"editing in VS Code — press r when done"`.
- Pressing `r` reads the tempfile back and saves to the config key via `DB.Config.Set`.
- Pressing `c` discards the tempfile without saving.

**Threads panel** (`internal/tui/panel_threads.go`):

- Pressing `o` on a task row calls `hostedit.Open(task.LogPath, 0)` to open the task's captured log.
- Checks `WantsTUISuspend()` first; if true, flashes a message rather than attempting to open.

---

## Known rough edges

- **WaveTerm doesn't support line numbers.** The `line` argument is silently ignored for `HostWaveTerm`.
- **No round-trip notification.** For GUI hosts, cairo has no way to know when the user finishes editing. The prompt panel requires a manual `r` keypress to reload. This is intentional (polling or inotify would complicate the Bubble Tea event loop) but means the workflow is: edit → save in editor → return to cairo → press `r`.
- **`EDITOR="code --wait"` works but blocks.** If `$EDITOR` is set to something blocking, `Open` in `HostUnknown` mode will block until the editor closes. For GUI editors with `--wait`, the TUI appears to hang during the edit. Prefer the detection-based paths or set `TERM_PROGRAM` appropriately.
