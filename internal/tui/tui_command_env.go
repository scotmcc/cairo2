package tui

// tui_command_env.go — implements commands.CommandEnv for the TUI model.
// Commands in the shared registry call env.Output/Submit/SetPanel instead of
// touching the model directly; this adapter wires those calls back to the
// model's real methods.
//
// Because Bubble Tea requires side-effects to be returned as tea.Cmd values,
// Submit queues an agent-prompt cmd into an internal slice. The TUI adapter
// (adaptSharedCmd) drains those cmds after the handler returns and batches
// them into the normal Bubble Tea command stream.

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/scotmcc/cairo2/internal/commands"
)

// tuiEnv is the TUI's implementation of commands.CommandEnv.
// It holds a pointer to the live model so mutations take effect immediately,
// and accumulates any tea.Cmds produced during handler execution so the
// caller can return them to Bubble Tea.
type tuiEnv struct {
	m    *model
	cmds []tea.Cmd
}

func newTUIEnv(m *model) *tuiEnv { return &tuiEnv{m: m} }

// Output appends a system-level line to the visible transcript.
func (e *tuiEnv) Output(text string) {
	e.m.appendSystem(text)
}

// Submit sends a message to the agent as if the user typed it.
// The resulting tea.Cmd is captured and returned via Drain().
func (e *tuiEnv) Submit(text string) {
	e.m.appendUser("(starting initialization)")
	e.m.startAssistant()
	e.cmds = append(e.cmds, e.m.submit(text))
}

// SetPanel opens or closes a named panel by panelID string.
// Unknown names are silently ignored.
func (e *tuiEnv) SetPanel(name string) {
	id := panelID(name)
	if findPanel(id) == nil {
		return
	}
	if cmd := e.m.togglePanel(id); cmd != nil {
		e.cmds = append(e.cmds, cmd)
	}
}

// IsStreaming reports whether the agent is currently mid-turn.
func (e *tuiEnv) IsStreaming() bool { return e.m.streaming }

// Drain returns all accumulated tea.Cmds and resets the slice.
func (e *tuiEnv) Drain() tea.Cmd {
	if len(e.cmds) == 0 {
		return nil
	}
	batch := tea.Batch(e.cmds...)
	e.cmds = e.cmds[:0]
	return batch
}

// adaptSharedCmd wraps a *commands.Command into the TUI's native
// func(*model) tea.Cmd shape so it can be stored in the existing
// tui.Command.Handler field and called from handleKey.
// The args parameter is empty for commands invoked without arguments
// from the slash drawer; the slash-command dispatcher can pass the
// remainder of the input line as args when needed.
func adaptSharedCmd(sc *commands.Command, args string) func(*model) tea.Cmd {
	return func(m *model) tea.Cmd {
		env := newTUIEnv(m)
		_ = sc.Handler(args, env)
		return env.Drain()
	}
}
