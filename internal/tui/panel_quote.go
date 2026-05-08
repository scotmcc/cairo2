package tui

// panel_quote.go — quote-reply panel (ctrl+r).
//
// Opens a slide-in showing the last assistant turn's text, line-numbered so
// the user can pick a range. On accept the selection is injected into the
// input area as a markdown blockquote ("> line\n") and focus returns to input.
//
// Key bindings while panel is focused:
//   - ↑/↓ / k/j  move the cursor line
//   - enter       accept current line as a single-line range
//   - esc         cancel (no changes to input)
//   - typed range "5" or "5-12" then enter to jump to that range
//
// The range-input buffer accumulates digits and "-"; any other key
// (except the navigation/action keys) clears it.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const panelQuoteID panelID = "quote"

// colQuoteAccent is a soft blue — quote-reply is a "reach back" gesture, not
// a structural action (green) or an error (red).
var colQuoteAccent = lipgloss.Color("#64b5f6")

// quoteState holds all mutable state for the quote panel.
type quoteState struct {
	viewport   viewport.Model
	lines      []string // raw lines of the last assistant turn
	cursor     int      // highlighted line (0-based)
	rangeInput string   // partially-typed range string, e.g. "5" or "5-1"
	lastWidth  int
}

func init() {
	registerPanel(&panelSpec{
		ID:              panelQuoteID,
		Position:        posTop,
		Accent:          colQuoteAccent,
		Title:           "quote-reply",
		Description:     "Pick a line range from Selene's last response and quote it into the input.",
		ToggleKey:       "ctrl+r",
		ShowInHelp:      true,
		PreferredHeight: 20,
		OnOpen:          quoteOpen,
		OnClose:         quoteClose,
		Update:          quoteUpdate,
		View:            quoteView,
	})
}

// quoteOpen loads the last assistant turn and resets cursor state.
func quoteOpen(m *model) tea.Cmd {
	vp := viewport.New(0, 0)
	m.quote.viewport = vp
	m.quote.rangeInput = ""
	m.quote.cursor = 0

	text := m.agent.LastAssistantText()
	if text == "" {
		m.quote.lines = nil
	} else {
		m.quote.lines = strings.Split(strings.TrimRight(text, "\n"), "\n")
	}

	quoteResize(m)
	quoteRender(m)
	return nil
}

func quoteClose(m *model) {
	m.quote.lines = nil
	m.quote.rangeInput = ""
}

func quoteResize(m *model) {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := 20 - 4 // reserve title + rule + range-input row + hint
	m.quote.viewport.Width = w
	m.quote.viewport.Height = h
	m.quote.lastWidth = w
}

// quoteRender rebuilds the viewport content with line numbers and cursor highlight.
func quoteRender(m *model) {
	if len(m.quote.lines) == 0 {
		m.quote.viewport.SetContent(
			lipgloss.NewStyle().Foreground(colTextDim).Render(
				"  (no assistant response yet — send a message first)"),
		)
		return
	}

	accent := lipgloss.NewStyle().Foreground(colQuoteAccent)
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	hi := lipgloss.NewStyle().Background(lipgloss.Color("#1e3a50")).Foreground(colText)

	var sb strings.Builder
	digits := len(fmt.Sprintf("%d", len(m.quote.lines)))
	for i, line := range m.quote.lines {
		num := fmt.Sprintf("%*d", digits, i+1)
		row := accent.Render(num+" ") + line
		if i == m.quote.cursor {
			row = hi.Render(fmt.Sprintf("%-*s", m.quote.viewport.Width-1, row))
		} else {
			row = dim.Render(fmt.Sprintf("%*s", digits, "")) + dim.Render("  ") + line
			// Keep consistent: line-number is already in row, just dim the number.
			row = accent.Render(num+" ") + line
		}
		sb.WriteString(row)
		if i < len(m.quote.lines)-1 {
			sb.WriteByte('\n')
		}
	}
	m.quote.viewport.SetContent(sb.String())
}

// quoteUpdate handles keyboard input while the panel is focused.
func quoteUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		quoteResize(m)
		quoteRender(m)
		return false, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		newVp, cmd := m.quote.viewport.Update(msg)
		m.quote.viewport = newVp
		return false, cmd
	}

	switch key.String() {
	case "esc":
		m.closePanel(panelQuoteID)
		return true, nil

	case "up", "k":
		if m.quote.cursor > 0 {
			m.quote.cursor--
			quoteScrollToCursor(m)
			quoteRender(m)
		}
		return true, nil

	case "down", "j":
		if m.quote.cursor < len(m.quote.lines)-1 {
			m.quote.cursor++
			quoteScrollToCursor(m)
			quoteRender(m)
		}
		return true, nil

	case "enter":
		// If a typed range is present, parse it; otherwise use cursor line.
		var text string
		var ok bool
		if m.quote.rangeInput != "" {
			text, ok = quoteResolveRange(m.quote.rangeInput, m.quote.lines)
			if !ok {
				// Bad range — clear buffer, stay open.
				m.quote.rangeInput = ""
				quoteRender(m)
				return true, nil
			}
		} else {
			if len(m.quote.lines) == 0 {
				m.closePanel(panelQuoteID)
				return true, nil
			}
			text = m.quote.lines[m.quote.cursor]
		}
		quoted := quoteFormat(text)
		// Prepend quoted text to any existing input content.
		existing := m.input.Value()
		m.input.SetValue(quoted + existing)
		// Move cursor to end so the user types after the quote.
		m.input.CursorEnd()
		m.closePanel(panelQuoteID)
		m.input.Focus()
		return true, nil

	default:
		// Accumulate digits and "-" into the range buffer;
		// clear on any other printable key so mistyped ranges don't get stuck.
		ch := key.String()
		if isRangeChar(ch) {
			m.quote.rangeInput += ch
			quoteRender(m)
		} else if len(ch) == 1 {
			// Single printable char that isn't a range char — clear buffer.
			m.quote.rangeInput = ""
			quoteRender(m)
		}
		return true, nil
	}
}

// isRangeChar returns true for characters that can appear in a range spec.
func isRangeChar(s string) bool {
	if len(s) != 1 {
		return false
	}
	c := s[0]
	return (c >= '0' && c <= '9') || c == '-'
}

// quoteScrollToCursor ensures the cursor line is visible in the viewport.
func quoteScrollToCursor(m *model) {
	h := m.quote.viewport.Height
	if h <= 0 {
		return
	}
	// viewport.YOffset is the top-visible line (0-based).
	top := m.quote.viewport.YOffset
	bot := top + h - 1
	if m.quote.cursor < top {
		m.quote.viewport.SetYOffset(m.quote.cursor)
	} else if m.quote.cursor > bot {
		m.quote.viewport.SetYOffset(m.quote.cursor - h + 1)
	}
}

// quoteView renders the panel.
func quoteView(width, _ int, m *model) string {
	accent := lipgloss.NewStyle().Foreground(colQuoteAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	lineCount := len(m.quote.lines)
	var subtitle string
	switch lineCount {
	case 0:
		subtitle = "  ·  no response"
	case 1:
		subtitle = "  ·  1 line"
	default:
		subtitle = fmt.Sprintf("  ·  %d lines", lineCount)
	}

	title := accent.Render("quote-reply") + dim.Render(subtitle)
	rule := lipgloss.NewStyle().Foreground(colBorderThin).Render(strings.Repeat("─", max(0, width)))
	body := m.quote.viewport.View()

	// Range-input row: show what the user has typed so far, or a placeholder.
	var rangeRow string
	if m.quote.rangeInput != "" {
		rangeRow = "\n" + dim.Render("  range: ") + accent.Render(m.quote.rangeInput)
	} else if lineCount > 0 {
		rangeRow = "\n" + dim.Render(fmt.Sprintf("  cursor at line %d — type a range (e.g. 3 or 2-7) then enter", m.quote.cursor+1))
	}

	hint := dim.Render("  ↑↓/j/k navigate · enter accept · esc cancel")

	return title + "\n" + rule + "\n" + body + rangeRow + "\n" + hint
}

// quoteFormat converts selected text into a markdown blockquote block.
// Each line is prefixed with "> "; the block ends with two newlines so the
// user's cursor lands after it and can type immediately.
func quoteFormat(text string) string {
	lines := strings.Split(text, "\n")
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString("> ")
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	return sb.String()
}

// parseRange parses a range string ("5" or "5-12") into (start, end) inclusive
// 1-based line numbers. Returns ok=false for invalid input.
func parseRange(s string) (start, end int, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0, false
	}
	if idx := strings.Index(s, "-"); idx >= 0 {
		// Range form: start-end
		startStr := s[:idx]
		endStr := s[idx+1:]
		a, err1 := strconv.Atoi(startStr)
		b, err2 := strconv.Atoi(endStr)
		if err1 != nil || err2 != nil || a <= 0 || b <= 0 || a > b {
			return 0, 0, false
		}
		return a, b, true
	}
	// Single line form.
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, 0, false
	}
	return n, n, true
}

// quoteResolveRange parses rangeStr, clamps to available lines, and returns
// the selected text joined with newlines. Returns ok=false on parse failure.
func quoteResolveRange(rangeStr string, lines []string) (string, bool) {
	start, end, ok := parseRange(rangeStr)
	if !ok {
		return "", false
	}
	n := len(lines)
	if start > n {
		return "", false
	}
	if end > n {
		end = n
	}
	selected := lines[start-1 : end]
	return strings.Join(selected, "\n"), true
}
