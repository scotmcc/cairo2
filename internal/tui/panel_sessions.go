package tui

// panel_sessions.go — fullscreen session browser. List every session with
// its metadata; select to see a detail block on the right.
//
// Press Enter on any session to queue it for resume: cairo writes the
// session ID to config, exits cleanly, and resolveSession picks it up on
// the next launch. Delete happens via conversation with Selene (she owns
// the DB); fork-from-turn is a future feature once we have the
// session-copy primitives.

import (
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
)

const panelSessionsID panelID = "sessions"

type sessionsState struct {
	sessions   []*sessions.Session
	counts     map[int64]int // messages per session — cached at open time
	selected   int
	pendingMsg string // non-empty after Enter queues a session for resume
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelSessionsID,
		Position:    posFullscreen,
		Accent:      colVoiceSelene,
		Title:       "sessions",
		Description: "Browse every session in Selene's memory. Press Enter to queue a session for resume on next launch.",
		ToggleKey:   "ctrl+b",
		ShowInHelp:  true,
		OnOpen:      sessionsOpen,
		OnClose:     sessionsClose,
		Update:      sessionsUpdate,
		View:        sessionsView,
	})
}

func sessionsOpen(m *model) tea.Cmd {
	list, err := m.db.Sessions.List()
	if err != nil {
		m.sessions.sessions = nil
		return nil
	}
	counts := make(map[int64]int, len(list))
	for _, s := range list {
		if n, err := m.db.Messages.CountForSession(s.ID); err == nil {
			counts[s.ID] = n
		}
	}
	m.sessions.sessions = list
	m.sessions.counts = counts
	// Put selection on the current session so "where am I" is obvious.
	m.sessions.selected = 0
	for i, s := range list {
		if s.ID == m.session.ID {
			m.sessions.selected = i
			break
		}
	}
	return nil
}

func sessionsClose(m *model) {
	m.sessions.sessions = nil
	m.sessions.counts = nil
}

func sessionsUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc":
		m.closePanel(panelSessionsID)
		return true, nil
	case "up", "k":
		if m.sessions.selected > 0 {
			m.sessions.selected--
		}
		return true, nil
	case "down", "j":
		if m.sessions.selected < len(m.sessions.sessions)-1 {
			m.sessions.selected++
		}
		return true, nil
	case "home":
		m.sessions.selected = 0
		return true, nil
	case "end":
		if len(m.sessions.sessions) > 0 {
			m.sessions.selected = len(m.sessions.sessions) - 1
		}
		return true, nil
	case "ctrl+r":
		// Re-run the load logic to pick up sessions created since this panel opened.
		sessionsOpen(m)
		return true, nil
	case "enter":
		if m.sessions.selected >= len(m.sessions.sessions) {
			return true, nil
		}
		s := m.sessions.sessions[m.sessions.selected]
		if s.ID == m.session.ID {
			// Already the current session — nothing to do.
			return true, nil
		}
		// Queue the chosen session ID and exit so resolveSession picks it up
		// on the next launch.
		_ = m.db.Config.Set(config.KeyPendingSessionID, strconv.FormatInt(s.ID, 10))
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("session %d", s.ID)
		}
		m.sessions.pendingMsg = fmt.Sprintf(
			"Session %d (%s) queued. Re-run cairo (or restart the cairo-tmux pane) to resume it.",
			s.ID, name,
		)
		return true, tea.Quit
	}
	return true, nil // claim everything while focused
}

func sessionsView(width, height int, m *model) string {
	accent := lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true)

	var b strings.Builder
	b.WriteString(accent.Render(m.aiName + " — sessions"))
	b.WriteByte('\n')
	b.WriteString(m.styles.headerRule.Render(strings.Repeat("━", max(0, width))))
	b.WriteString("\n\n")

	if len(m.sessions.sessions) == 0 {
		b.WriteString(m.styles.statusHint.Render("  (no sessions yet — type /new to start a fresh session)\n"))
		return b.String()
	}

	// Two-column layout: list on the left, detail on the right. Allocate
	// roughly 40% to list, 60% to detail, with a vertical divider.
	listW := width / 2
	if listW > 48 {
		listW = 48
	}
	if listW < 30 {
		listW = 30
	}
	detailW := max(20, width-listW-3) // -3 for divider+padding

	// Reserve: title(1) + heavy rule(1) + blank(1) + footer(2) = 5 non-body.
	bodyH := max(4, height-5)

	// List rows.
	start := 0
	if m.sessions.selected >= bodyH {
		start = m.sessions.selected - bodyH + 1
	}
	end := start + bodyH
	if end > len(m.sessions.sessions) {
		end = len(m.sessions.sessions)
	}

	var list strings.Builder
	for i := start; i < end; i++ {
		s := m.sessions.sessions[i]
		row := formatSessionRow(s, m.session.ID, listW-2, m.sessions.counts[s.ID])
		if i == m.sessions.selected {
			sel := lipgloss.NewStyle().
				Foreground(colFocus).
				Background(colSurfaceHi).
				Bold(true)
			list.WriteString(sel.Render(padRight(row, listW)))
		} else {
			list.WriteString(colorizeSessionRow(s, m.session.ID, m.sessions.counts[s.ID], listW-2, m))
		}
		list.WriteByte('\n')
	}
	// Pad list to bodyH for consistent divider height.
	rendered := end - start
	for i := rendered; i < bodyH; i++ {
		list.WriteByte('\n')
	}

	// Detail of selected.
	var detail strings.Builder
	if m.sessions.selected < len(m.sessions.sessions) {
		s := m.sessions.sessions[m.sessions.selected]
		name := s.Name
		if name == "" {
			name = "(unnamed)"
		}
		nameStyle := lipgloss.NewStyle().Foreground(colText).Bold(true)
		labelStyle := m.styles.statusMemLbl
		valStyle := m.styles.body

		detail.WriteString(nameStyle.Render(name))
		detail.WriteString("\n\n")

		lines := []struct{ k, v string }{
			{"id", fmt.Sprintf("%d", s.ID)},
			{"role", s.Role},
			{"cwd", s.CWD},
			{"created", s.CreatedAt.Local().Format("2006-01-02 15:04")},
			{"last active", s.LastActive.Local().Format("2006-01-02 15:04")},
			{"messages", fmt.Sprintf("%d", m.sessions.counts[s.ID])},
		}
		for _, ln := range lines {
			detail.WriteString("  ")
			detail.WriteString(labelStyle.Render(padRight(ln.k, 13)))
			detail.WriteString(valStyle.Render(ln.v))
			detail.WriteByte('\n')
		}

		detail.WriteString("\n")
		if s.ID == m.session.ID {
			detail.WriteString(m.styles.statusMode.Render("  ← this is your current session\n"))
		} else {
			hint := fmt.Sprintf("  to switch:  cairo --tui -session %d", s.ID)
			detail.WriteString(m.styles.statusHint.Render(hint))
			detail.WriteByte('\n')
		}
	}

	// Compose horizontally. lipgloss.JoinHorizontal with Top alignment.
	divider := strings.Repeat(m.styles.thinRule.Render("│")+"\n", bodyH)
	body := lipgloss.JoinHorizontal(
		lipgloss.Top,
		padBlock(list.String(), listW, bodyH),
		divider,
		padBlock(detail.String(), detailW, bodyH),
	)

	b.WriteString(body)
	b.WriteString("\n\n")
	if m.sessions.pendingMsg != "" {
		b.WriteString(m.styles.statusMode.Render("  ✓ " + m.sessions.pendingMsg))
	} else {
		b.WriteString(m.styles.statusHint.Render("  ↑↓ / jk navigate · home/end top/bottom · ctrl+r refresh · enter resume · esc close"))
	}
	return b.String()
}

// padBlock pads (or trims) a multi-line string to exactly height rows and
// width columns per row. Lets us place side-by-side content without ragged
// vertical edges.
func padBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = padRight(ln, width)
	}
	for len(lines) < height {
		lines = append(lines, padRight("", width))
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// formatSessionRow returns the plain text of a session row (used as the
// layout skeleton for the selection highlighter).
func formatSessionRow(s *sessions.Session, currentID int64, width int, msgCount int) string {
	marker := "  "
	if s.ID == currentID {
		marker = "▸ "
	}
	name := s.Name
	if name == "" {
		name = fmt.Sprintf("session %d", s.ID)
	}
	// Layout: marker + name (truncated) + role + count
	tail := fmt.Sprintf("  %s  %d", s.Role, msgCount)
	maxName := width - len(tail) - len(marker) - 2
	if maxName < 8 {
		maxName = 8
	}
	if len(name) > maxName {
		name = name[:maxName-1] + "…"
	}
	return fmt.Sprintf("%s%-*s%s", marker, maxName, name, tail)
}

// colorizeSessionRow renders the un-selected row with per-field color.
func colorizeSessionRow(s *sessions.Session, currentID int64, msgCount, width int, m *model) string {
	marker := "  "
	if s.ID == currentID {
		marker = m.styles.statusMode.Render("▸ ")
	}
	name := s.Name
	if name == "" {
		name = fmt.Sprintf("session %d", s.ID)
	}
	tail := fmt.Sprintf("  %s  %d", s.Role, msgCount)
	maxName := width - len(tail) - 4 // approx, for layout parity
	if maxName < 8 {
		maxName = 8
	}
	if len(name) > maxName {
		name = name[:maxName-1] + "…"
	}
	nameStyle := m.styles.body
	roleStyle := lipgloss.NewStyle().Foreground(roleAccent(s.Role))
	countStyle := m.styles.statusHint

	return fmt.Sprintf("%s%s  %s  %s",
		marker,
		nameStyle.Render(padRight(name, maxName)),
		roleStyle.Render(s.Role),
		countStyle.Render(fmt.Sprintf("%d", msgCount)))
}
