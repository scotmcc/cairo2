package tui

// panel_log.go — top slide-in showing the last 1000 lines of ~/.cairo/cairo.log.
// Snapshot only (no live follow). Press ctrl+r to refresh, ctrl+a to load the full file
// when the view is truncated. Accent: amber, because this is a diagnostic surface.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

const panelLogID panelID = "log"

const logMaxLines = 1000

// colLogAccent is amber — diagnostic surface, not an error, not a success.
var colLogAccent = lipgloss.Color("#FFA726")

type logState struct {
	viewport   viewport.Model
	content    string // rendered content loaded into the viewport
	totalLines int    // total line count of the file (0 if file missing)
	truncated  bool   // true when showing fewer than totalLines
	lastWidth  int    // width content was rendered at; re-render on resize
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelLogID,
		Position:    posFullscreen,
		Accent:      colLogAccent,
		Title:       "cairo.log",
		Description: "Snapshot view of ~/.cairo/cairo.log (last 1000 lines). Press ctrl+r to refresh, ctrl+a to load the full file.",
		ToggleKey:   "ctrl+l",
		ShowInHelp:  true,
		OnOpen:      logOpen,
		OnClose:     logClose,
		Update:      logUpdate,
		View:        logView,
	})
}

func logOpen(m *model) tea.Cmd {
	vp := viewport.New(0, 0)
	m.log.viewport = vp
	logResize(m)
	logLoad(m, false)
	return nil
}

func logResize(m *model) {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height
	if h <= 0 {
		h = 24
	}
	// Reserve 3 rows: title + rule + hint.
	h -= 3
	if h < 3 {
		h = 3
	}
	m.log.viewport.Width = w
	m.log.viewport.Height = h
}

func logClose(m *model) {
	m.log.content = ""
	m.log.totalLines = 0
	m.log.truncated = false
}

// logLoad reads the log file. If full is false, only the last logMaxLines are
// shown; if full is true, the entire file is loaded.
func logLoad(m *model, full bool) {
	logPath := filepath.Join(sqliteopen.DefaultDataDir(), "cairo.log")

	raw, err := os.ReadFile(logPath)
	if err != nil || len(raw) == 0 {
		m.log.content = "(cairo.log is empty or not yet created)"
		m.log.totalLines = 0
		m.log.truncated = false
		m.log.viewport.SetContent(m.log.content)
		m.log.viewport.GotoBottom()
		m.log.lastWidth = m.width
		return
	}

	// Split, remove trailing empty line from the final newline.
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	total := len(lines)
	m.log.totalLines = total

	var shown []string
	if !full && total > logMaxLines {
		shown = lines[total-logMaxLines:]
		m.log.truncated = true
	} else {
		shown = lines
		m.log.truncated = false
	}

	// Build footer note when truncated.
	var parts []string
	parts = append(parts, strings.Join(shown, "\n"))
	if m.log.truncated {
		dim := lipgloss.NewStyle().Foreground(colTextDim)
		parts = append(parts, dim.Render(
			fmt.Sprintf("… showing last %d lines of %d total. Press ctrl+a to load full file.", logMaxLines, total),
		))
	}

	m.log.content = strings.Join(parts, "\n")
	m.log.lastWidth = m.width
	m.log.viewport.SetContent(m.log.content)
	m.log.viewport.GotoBottom()
}

func logUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		logResize(m)
		m.log.viewport.SetContent(m.log.content)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		newVp, cmd := m.log.viewport.Update(msg)
		m.log.viewport = newVp
		return false, cmd
	}
	switch key.String() {
	case "esc":
		m.closePanel(panelLogID)
		return true, nil
	case "ctrl+r":
		logLoad(m, false)
		return true, nil
	case "ctrl+a":
		if m.log.truncated {
			logLoad(m, true)
		}
		return true, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		newVp, cmd := m.log.viewport.Update(msg)
		m.log.viewport = newVp
		return true, cmd
	}
	return true, nil
}

func logView(width, height int, m *model) string {
	// Refresh the viewport's height when the panel renders — the framework
	// invokes View with the actual fullscreen dims, which can differ from
	// what logResize last computed (e.g. on first paint before WindowSizeMsg).
	if height > 3 && m.log.viewport.Height != height-3 {
		m.log.viewport.Height = height - 3
		m.log.viewport.Width = width
		m.log.viewport.SetContent(m.log.content)
		m.log.viewport.GotoBottom()
	}
	accent := lipgloss.NewStyle().Foreground(colLogAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	var subtitle string
	if m.log.totalLines == 0 {
		subtitle = "  ·  (empty)"
	} else if m.log.truncated {
		subtitle = fmt.Sprintf("  ·  last %d of %d lines", logMaxLines, m.log.totalLines)
	} else {
		subtitle = fmt.Sprintf("  ·  %d lines", m.log.totalLines)
	}

	title := accent.Render("cairo.log") + dim.Render(subtitle)
	rule := lipgloss.NewStyle().Foreground(colBorderThin).Render(strings.Repeat("─", max(0, width)))
	body := m.log.viewport.View()

	var hint string
	if m.log.truncated {
		hint = dim.Render("  ↑↓ PgUp/PgDn scroll · ctrl+r refresh · ctrl+a load full file · esc close")
	} else {
		hint = dim.Render("  ↑↓ PgUp/PgDn scroll · ctrl+r refresh · esc close")
	}

	return title + "\n" + rule + "\n" + body + "\n" + hint
}
