package tui

// panel_help.go — fullscreen help overlay. Shows special keys, slash
// commands, and registered panels with their toggle bindings. Scrolls
// when the content is taller than the viewport; any non-scroll key
// dismisses it.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const panelHelpID panelID = "help"

// helpState tracks the scroll offset for the help overlay.
type helpState struct {
	scroll int // line offset into the rendered help content
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelHelpID,
		Position:    posFullscreen,
		Accent:      colVoiceSelene,
		Title:       "help",
		Description: "Show keyboard shortcuts, slash commands, and panels.",
		ToggleKey:   "?", // opened via ? on empty input (handled in tui.go),
		// or explicitly via /help.
		ShowInHelp: false, // the help panel itself doesn't need to list itself
		OnOpen:     func(m *model) tea.Cmd { m.help.scroll = 0; return nil },
		Update:     helpUpdate,
		View:       helpView,
	})
}

// helpUpdate handles scroll keys (↑↓ PgUp PgDn Home End j k g G) and
// dismisses only on Esc. Other keys are swallowed so help stays modal
// and a stray keystroke (or mouse-wheel-as-arrow-keys in some terminals)
// can't accidentally close it.
func helpUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc":
		m.closePanel(panelHelpID)
		return true, nil
	case "up", "k":
		if m.help.scroll > 0 {
			m.help.scroll--
		}
		return true, nil
	case "down", "j":
		m.help.scroll++ // helpView clamps to maxScroll
		return true, nil
	case "pgup":
		m.help.scroll -= 10
		if m.help.scroll < 0 {
			m.help.scroll = 0
		}
		return true, nil
	case "pgdown":
		m.help.scroll += 10
		return true, nil
	case "home", "g":
		m.help.scroll = 0
		return true, nil
	case "end", "G":
		m.help.scroll = 1 << 30 // clamped in helpView
		return true, nil
	}
	// Swallow anything else so the input field doesn't see it and the
	// overlay doesn't dismiss on stray keys.
	return true, nil
}

// helpBody renders the full help content as a single string. helpView
// then splits it into lines and windows by scroll offset + height.
func helpBody(width int, m *model) string {
	var b strings.Builder

	title := m.styles.headerName.Render(m.aiName + " — help")
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(m.styles.headerRule.Render(strings.Repeat("━", max(0, width))))
	b.WriteString("\n\n")

	// Special keys — context-sensitive behavior documented up front.
	b.WriteString(m.styles.statusMode.Render("  Keys"))
	b.WriteString("\n\n")
	specialKeys := []struct{ key, desc string }{
		{"Ctrl-C", "context-aware: cancel turn if Selene is replying · clear input if typed · otherwise clear transcript view"},
		{"Ctrl-D", "EOF — quit if input is empty"},
		{"Ctrl-Q", "quit explicitly"},
		{"?", "this help (when input is empty)"},
		{"/", "open the command drawer (when input is empty)"},
		{"Enter", "send message · execute selected command · dismiss help"},
		{"Ctrl+Enter", "dispatch input as background task (non-blocking) — Ctrl+T to watch"},
		{"Esc", "close focused panel or overlay"},
		{"↑ ↓ PgUp PgDn", "scroll the transcript (when input is empty and no panel focused)"},
	}
	for _, k := range specialKeys {
		fmt.Fprintf(&b, "    %s   %s\n",
			m.styles.statusMemLbl.Render(padRight(k.key, 16)),
			m.styles.body.Render(k.desc))
	}
	b.WriteString("\n")

	// Panels — everything registered that opts into help.
	if panels := helpablePanels(); len(panels) > 0 {
		b.WriteString(m.styles.statusMode.Render("  Panels"))
		b.WriteString("\n\n")
		for _, p := range panels {
			name := titleCase(p.Title)
			accent := p.Accent
			if accent == "" {
				accent = colVoiceSelene
			}
			title := lipgloss.NewStyle().Foreground(accent).Bold(true).Render(name)
			hotkey := ""
			if p.ToggleKey != "" {
				hotkey = m.styles.statusMemLbl.Render("   " + p.ToggleKey)
			}
			fmt.Fprintf(&b, "    %s%s\n", title, hotkey)
			fmt.Fprintf(&b, "      %s\n\n", m.styles.body.Render(p.Description))
		}
	}

	// Input-prefixes, since they're conceptually commands without being
	// slash-based.
	b.WriteString(m.styles.statusMode.Render("  Prefixes"))
	b.WriteString("\n\n")
	prefixes := []struct{ key, desc string }{
		{"!<cmd>", "run a shell command in the session CWD; output becomes the user turn"},
		{"@<path>", "reference a file — Selene sees its contents appended, your transcript stays clean"},
	}
	for _, p := range prefixes {
		fmt.Fprintf(&b, "    %s   %s\n",
			m.styles.statusMemLbl.Render(padRight(p.key, 16)),
			m.styles.body.Render(p.desc))
	}
	b.WriteString("\n")

	// Slash commands.
	b.WriteString(m.styles.statusMode.Render("  Commands"))
	b.WriteString("\n\n")
	cmds := filterCommands(m.commands, "")
	for _, c := range cmds {
		name := m.styles.statusMode.Render(fmt.Sprintf("/%s", c.Name))
		aliases := ""
		if len(c.Aliases) > 0 {
			aliases = m.styles.statusHint.Render(
				" (" + strings.Join(prependSlash(c.Aliases), ", ") + ")")
		}
		hotkey := ""
		if c.Hotkey != "" {
			hotkey = m.styles.statusMemLbl.Render("   " + c.Hotkey)
		}
		fmt.Fprintf(&b, "    %s%s%s\n", name, aliases, hotkey)
		fmt.Fprintf(&b, "      %s\n\n", m.styles.body.Render(c.Description))
	}

	return b.String()
}

func helpView(width, height int, m *model) string {
	body := helpBody(width, m)
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")

	// Reserve one line for the footer hint.
	viewHeight := max(1, height-1)

	maxScroll := len(lines) - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.help.scroll > maxScroll {
		m.help.scroll = maxScroll
	}
	if m.help.scroll < 0 {
		m.help.scroll = 0
	}

	start := m.help.scroll
	end := start + viewHeight
	if end > len(lines) {
		end = len(lines)
	}

	var b strings.Builder
	for i := start; i < end; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}

	// Footer: scroll position when scrollable, otherwise just the dismiss hint.
	hint := "  esc to dismiss"
	if maxScroll > 0 {
		more := ""
		if start > 0 && end < len(lines) {
			more = " · ↑↓ PgUp PgDn to scroll"
		} else if start > 0 {
			more = " · ↑ PgUp for more"
		} else {
			more = " · ↓ PgDn for more"
		}
		hint = fmt.Sprintf("  %d–%d of %d%s · esc to dismiss", start+1, end, len(lines), more)
	}
	b.WriteString(m.styles.statusHint.Render(hint))
	return b.String()
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
