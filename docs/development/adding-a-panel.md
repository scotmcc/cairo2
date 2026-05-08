# Adding a Panel

A panel is a TUI overlay registered with the panel system in `internal/tui/`. Panels can be positioned on the left, right, top, bottom, or fullscreen. This guide uses `panel_threads.go` as the primary example (the most complete panel) and notes where simpler patterns suffice.

---

## 1. Anatomy of a Panel

Every panel is defined by a `panelSpec` struct registered in `init()`. The registry, open/close logic, and focus tracking all live in `internal/tui/panels.go`.

### `panelSpec`

```go
// internal/tui/panels.go:50
type panelSpec struct {
    ID          panelID
    Position    panelPosition
    Accent      lipgloss.Color
    Title       string
    Description string
    ToggleKey   string
    ShowInHelp  bool

    OnOpen  func(*model) tea.Cmd
    OnClose func(*model)

    Update func(msg tea.Msg, m *model) (handled bool, cmd tea.Cmd)
    View   func(width, height int, m *model) string

    PreferredWidth  int
    PreferredHeight int
    DynamicWidth    func(*model) int
}
```

Key fields:

- `ID` — a `panelID` string constant, unique across all panels. Convention: declare it as a package-level `const panelFooID panelID = "foo"`.
- `Position` — where the panel renders. One of: `posLeft`, `posRight`, `posTop`, `posBottom`, `posFullscreen`.
- `ToggleKey` — the `tea.Key` string that opens/closes this panel (e.g. `"ctrl+t"`). Must be unique. Leave empty for programmatically-opened panels.
- `ShowInHelp` — set `true` to appear in the `?` help overlay. Panels that the user opens via a hotkey should always be `true`.
- `OnOpen` — called once when the panel becomes visible. Use it to reset state and kick off a data load. May return a `tea.Cmd`.
- `OnClose` — called once when the panel closes. Use it to clear state and release any resources.
- `Update` — called with each `tea.Msg` when this panel is keyboard-focused. Return `(true, cmd)` to claim the message; `(false, nil)` to let it fall through.
- `View` — called every render frame with the panel's allocated `width` and `height`. Must return a string exactly that tall (or shorter — the layout system does not pad).

### Position constants

```go
// internal/tui/panels.go:34
const (
    posTop       panelPosition = iota // between header rule and transcript
    posLeft                           // transcript | panel (left split)
    posRight                          // panel | transcript (right split)
    posBottom                         // between transcript and input frame
    posFullscreen                     // replaces transcript; header + status stay
)
```

At most one panel per non-fullscreen position is open at a time. Opening a second `posLeft` panel closes the first. Fullscreen panels stack over each other (last wins).

---

## 2. State and Rendering

Panel state lives as a field on the main `model` struct in `tui_model.go`. The model is passed by pointer to all panel hooks:

```go
// internal/tui/tui_model.go:149
type model struct {
    // ...
    threads  threadsState
    files    filesState
    memory   memoryState
    // ...
}
```

Add your state type here. The pattern is a dedicated `fooState` struct:

```go
type myPanelState struct {
    items    []MyItem
    selected int
    flash    string
}
```

Add a `myPanel myPanelState` field to `model`, then add a `myPanel myPanelState` entry in the same location in `tui_model.go`. Do not store panel state inside the `panelSpec` — specs are initialized at `init()` time and shared.

### Rendering

`View(width, height int, m *model) string` receives the panel's allocated pixel budget. The `width` and `height` come from the layout engine in `tui_view.go`:

- **posLeft / posRight**: `width` = `PreferredWidth` (or `DynamicWidth(m)` if set); `height` = viewport height.
- **posTop / posBottom**: `width` = terminal width; `height` = `PreferredHeight` (default 8 if 0).
- **posFullscreen**: `width` = terminal width; `height` = `m.height - reserved` (header + input frame).

The layout engine does not enforce line count. Return fewer lines than `height` and the panel will be shorter. Return more and it will clip. For list panels, calculate available list rows by subtracting known fixed rows (title, rule, footer) from `height`.

### Resizing

Panels do not need to handle `tea.WindowSizeMsg` explicitly — `tui_handlers.go` calls `m.relayout()` on every resize, which recalculates all widths and heights before the next render. Your `View` function will simply receive the new dimensions on the next frame.

---

## 3. Activation

### Via hotkey

Set `ToggleKey` on the `panelSpec`. The key handler in `tui.go`'s `Update` method checks `panelByToggleKey` on every `tea.KeyMsg`:

```go
// The main Update checks panelByToggleKey and calls m.togglePanel(spec.ID)
```

The key must not conflict with any other registered panel toggle or TUI global key. Check `panelToggleIndex` (built by `registerPanel`) by grepping existing panels.

### Via slash command

Slash commands in `internal/tui/commands.go` can call `m.openPanel(panelFooID)` directly. The command handler receives `*model` so it can reach `openPanel`. Look at how `/config` or `/threads` open their panels for the pattern.

### Focus rules

When a panel is opened, `m.focusedPanel` is set to its ID. The `Update` loop in `tui.go` checks `m.focusedPanelSpec()` first and routes `tea.KeyMsg` to the panel's `Update` function. When the panel's `Update` returns `(false, nil)`, the message falls through to the input field. When the panel closes, focus returns to another open panel or to the input field.

---

## 4. Two-Pane (Rail + Detail) Pattern

`panel_threads.go` uses the canonical two-pane layout: a list on the left, a detail block on the right, with a `│` divider. The panel's `DynamicWidth` function expands the total width when a detail pane is active:

```go
// internal/tui/panel_threads.go:27
const (
    threadsListWidth   = 42
    threadsDetailWidth = 48
)

func threadsWidth(m *model) int {
    if threadsSelectedTask(m) != nil {
        return threadsListWidth + threadsDetailWidth
    }
    return threadsListWidth
}
```

The `View` function splits the horizontal space and joins with `lipgloss.JoinHorizontal`:

```go
// internal/tui/panel_threads.go:321
func threadsView(width, height int, m *model) string {
    hasDetail := threadsSelectedTask(m) != nil
    listW := width
    detailW := 0
    if hasDetail && width > threadsListWidth+8 {
        listW = threadsListWidth
        detailW = width - listW - 1 // -1 for divider
    }

    tree := threadsRenderTree(m, listW, height)
    if !hasDetail || detailW <= 0 {
        return tree
    }
    divider := strings.Repeat(m.styles.thinRule.Render("│")+"\n", max(1, height))
    detail := threadsRenderDetail(m, detailW, height)
    return lipgloss.JoinHorizontal(lipgloss.Top, tree, divider, detail)
}
```

Use this pattern for any panel with a list + detail layout. The `memory` panel on the right uses a simpler single-pane layout because its detail is inline (the selected item injects into the input rather than rendering a separate pane).

---

## 5. Help Text

The `?` help overlay in `panel_help.go` auto-discovers all panels:

```go
// internal/tui/panel_help.go:113
if panels := helpablePanels(); len(panels) > 0 {
    for _, p := range panels {
        // renders Title, ToggleKey, and Description
    }
}
```

`helpablePanels()` returns all specs where `ShowInHelp == true`, sorted alphabetically by `Title`. To appear in the help overlay, set both `ShowInHelp: true` and a non-empty `Description`. The `Description` string is the one-line explanation shown beneath the panel's name in the overlay.

The help panel itself has `ShowInHelp: false` — a panel listing itself in its own help output would be redundant.

---

## 6. Talking to the DB / Agent Without Blocking

Never do blocking DB work inside `View`. The render loop calls `View` every frame — any latency here stalls the entire TUI.

**Synchronous reads on open**: Small DB reads (a list of sessions, a count of memories) can go in `OnOpen`. It runs once, not on every frame. `sessions.go` does this:

```go
// internal/tui/panel_sessions.go:46
func sessionsOpen(m *model) tea.Cmd {
    list, err := m.db.Sessions.List()
    // ...
    m.sessions.sessions = list
    return nil
}
```

**Async reads via tea.Cmd**: For anything that might be slow (embed + semantic search), kick off a `tea.Cmd` from `OnOpen` or from `Update`. The Cmd runs in a goroutine and sends a result message back to `Update`. `panel_memory.go` shows the full pattern:

```go
// internal/tui/panel_memory.go:103
func memorySearchCmd(m *model, query string) tea.Cmd {
    // capture what we need by value — the Cmd runs outside the model lifecycle
    embedModel, _ := m.db.Config.Get("embed_model")
    ag := m.agent
    database := m.db
    q := strings.TrimSpace(query)
    return func() tea.Msg {
        vec, err := ag.Embed(q)
        // ...
        return memorySearchResultMsg{query: query, results: results}
    }
}
```

The result message type (`memorySearchResultMsg`) is matched in `Update`:

```go
// internal/tui/panel_memory.go:150
if result, ok := msg.(memorySearchResultMsg); ok {
    if result.query == m.memory.lastQuery { // stale-result guard
        m.memory.results = result.results
    }
    return true, nil
}
```

The stale-result guard is important: if the user types fast, multiple searches may be in flight. Check that the returned query still matches what the user typed before applying the results.

### Ticking

`panel_threads.go` subscribes to `tickMsg` to refresh its job list every 300ms:

```go
// internal/tui/panel_threads.go:186
func threadsUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
    if _, ok := msg.(tickMsg); ok {
        threadsRefresh(m)
        return false, nil // don't claim — let tick reach other handlers
    }
    // ...
}
```

Return `(false, nil)` from tick handling so the tick also reaches the animation handlers in the main update loop. Claiming the tick (`return true, nil`) would break the global 300ms pulse.

---

## 7. Common Mistakes

### Forgetting to clear state on close

`OnClose` must nil out or zero the panel's state fields. If you don't, the next open will show stale data from the previous session. Look at `memoryClose`:

```go
// internal/tui/panel_memory.go:77
func memoryClose(m *model) {
    m.memory.results = nil
    m.memory.selected = 0
}
```

### Blocking the UI thread

Any call to `m.db.*` or `m.agent.*` inside `View` or inside `Update`'s synchronous path blocks the entire terminal for the duration of the call. Use `tea.Cmd` for anything that might take more than a millisecond. The one exception is `OnOpen`, which runs once and is acceptable for a quick DB list call.

### Leaking subscriptions

If your panel creates a goroutine in `OnOpen`, make sure `OnClose` stops it. Without a cleanup path, closing and reopening the panel spawns a new goroutine while the old one keeps running. Use a context cancellation or a done channel.

### Duplicate toggle key

`registerPanel` panics on duplicate toggle keys:

```go
// internal/tui/panels.go:100
if existing, dup := panelToggleIndex[s.ToggleKey]; dup {
    panic(fmt.Sprintf("duplicate panel toggle key %q: panels %q and %q", ...))
}
```

This fires at startup (via `init()`) and is immediately obvious. Check existing panels before picking a key.

---

## 8. The Simplest Existing Panel: `sessions`

`panel_sessions.go` is the smallest full-featured panel and the best starting point for a new fullscreen panel.

**Registration** (`panel_sessions.go:31`):

```go
func init() {
    registerPanel(&panelSpec{
        ID:          panelSessionsID,
        Position:    posFullscreen,
        Accent:      colVoiceSelene,
        Title:       "sessions",
        Description: "Browse every session in Selene's memory. Switching requires a cairo restart.",
        ToggleKey:   "ctrl+b",
        ShowInHelp:  true,
        OnOpen:      sessionsOpen,
        OnClose:     sessionsClose,
        Update:      sessionsUpdate,
        View:        sessionsView,
    })
}
```

**State** (on `model`): `sessions sessionsState` — holds `sessions []*db.Session`, `counts map[int64]int`, and `selected int`.

**OnOpen**: loads all sessions and message counts synchronously, sets the selection to the current session. No tea.Cmd needed because the data load is fast.

**OnClose**: nils the session list and count map.

**Update**: handles `esc` (close), `up`/`k`, `down`/`j` (navigation). All keys are claimed (`return true, nil`) to keep the panel modal.

**View**: renders a title row, a rule, a scrollable list of session rows with the current one highlighted, and a footer hint.

The `sessions` panel has no async work, no DB writes, and no tick subscription — it is the minimal viable panel pattern.

---

## Checklist for a New Panel

1. Declare `const panelFooID panelID = "foo"` at the top of `panel_foo.go`.
2. Define `fooState` struct with the fields your panel needs.
3. Add `foo fooState` to `model` in `tui_model.go`.
4. Write `fooOpen`, `fooClose`, `fooUpdate`, `fooView` functions.
5. Call `registerPanel(...)` from `init()` with a unique `ToggleKey` and `ID`.
6. Set `ShowInHelp: true` and write a one-line `Description`.
7. Clear all state in `fooClose` — zero values or nil slices.
8. Test: open with the hotkey, navigate, close with `esc`, reopen.
