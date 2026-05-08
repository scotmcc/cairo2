# TUI — Findings

**Reviewed:** internal/tui/, internal/tuisetup/
**Date:** 2026-05-02
**Counts:** major: 2, medium: 2, small: 1

## Summary

The cairo TUI has multiple structural violations of its own hotkey discipline and Bubble Tea's rendering contract. The most critical issue is mutation of viewport state during View(), which breaks Bubble Tea's view/update separation. Additionally, numerous panel handlers use bare single-letter keys (a, r, d, x, g, G) that conflict with text input and violate the project's explicit ctrl+ hotkey policy documented in CLAUDE.md and enforced in the toggle-key validator.

## Findings

### [major] Viewport mutation in View() — breaks Bubble Tea contract
- **Where:** `internal/tui/tui_view.go:262-263`
- **What:** `renderTranscriptWithSides()` mutates `m.viewport.Width` and calls `SetContent()` during the View() function. This violates Bubble Tea's strict separation: View() must be pure (no side effects); state changes belong in Update().
- **Why it matters:** Bubble Tea calls View() repeatedly and expects it to be idempotent. Mutating viewport state here means the rendered output depends on render frequency, not just model state. This can cause layout corruption, double-reflows, and unpredictable behavior during window resize or panel toggles.
- **Action:** Move viewport state updates (Width, Content) to Update() handlers. relayout() already handles width recalculation — extend it or create a separate "prepare viewport for render" step that runs in handleWindowSize, handleTick, or panel-toggle handlers instead.

### [major] Bare-key hotkey violations across multiple panels
- **Where:** Multiple locations:
  - `internal/tui/panel_diff.go:358, 366, 407, 411` (a, r, g, G)
  - `internal/tui/panel_config.go:439, 442` (g, G)
  - `internal/tui/panel_config_consider.go:116, 186, 195, 207, 210` (a, d, x, g, G)
  - `internal/tui/panel_log.go:145, 148` (r, a)
  - `internal/tui/panel_prompt.go:318, 321, 330` (e, r, c)
  - `internal/tui/panel_inspector.go:212, 219` (r, g)
  - `internal/tui/panel_threads.go:239, 248, 253, 258` (f, o, c, r)
  - `internal/tui/panel_sessions.go:105` (r)
  - `internal/tui/panel_state.go:108` (r)
- **What:** Panel Update handlers bind bare single-letter keys (vim-style: a=approve, r=reject/refresh, d/x=delete, g/G=top/bottom) without ctrl+ prefix. These keys conflict with typing in the input field when a panel is focused and steal characters from legitimate text input.
- **Why it matters:** CLAUDE.md explicitly forbids bare vim hotkeys. The validateToggleKey() guard in panels.go:107-125 enforces this for toggle keys but does NOT cover the internal panel key handlers. Panels claiming keys without ctrl+ reintroduces a previously-fixed bug.
- **Action:** Prefix all bare single-letter handlers with ctrl+. Verify no collision with existing global hotkeys (ctrl+q, ctrl+k, ctrl+g, ctrl+l, ctrl+d already used).

### [medium] Mouse scroll handling incomplete in main Update
- **Where:** `internal/tui/tui.go:189-195`
- **What:** Only vertical scrollwheel (WheelUp/WheelDown) routes to viewport; other mouse button events (drag, click) are silently dropped without fallthrough. Focused panels don't receive mouse input.
- **Why it matters:** Panels with internal viewports (inspector, diff, etc.) can't scroll or select with the mouse when focused. The pattern doesn't route to focused panel Update like other event types do.
- **Action:** Either route all mouse events to the focused panel's Update handler (like eventMsg, tickMsg do), or explicitly handle each panel's mouse needs. Confirm whether this is intentional (mouse disabled) or an oversight.

### [medium] Viewport SetContent() called on every render when panels open
- **Where:** `internal/tui/tui_view.go:263`
- **What:** Even when panel state hasn't changed, `SetContent(m.composeTranscript())` runs on every View() frame. composeTranscript() rebuilds the transcript string each time.
- **Why it matters:** Wasteful; can cause flicker or jank when the transcript is large. Content only changes when new messages arrive (agent events), not on every render frame.
- **Action:** Cache the composed transcript and only recompute on EventTokens, EventToolEnd, etc. Move the SetContent() call to the event handler, not View(). This frees View() from side effects and improves performance. (Pairs with the major View()-mutation finding.)

### [small] Missing backpressure handling for ChoiceRequest channel
- **Where:** `internal/tui/tui_model.go:237`
- **What:** The choiceRequests channel is created and used without a documented capacity contract. If a tool fires a choice request while another is pending, the channel may block in the tool goroutine.
- **Why it matters:** If the choose tool's request processing is slow or handleTick() doesn't drain fast enough, the tool goroutine can block, stalling the agent loop.
- **Action:** Verify ChoiceRequest sends are non-blocking (use select + default, or a buffered channel). Document the contract in tui_model.go comments. Confirm handleTick() drains reliably.
