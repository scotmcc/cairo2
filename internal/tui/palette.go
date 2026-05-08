package tui

// palette.go — Ctrl+K command palette overlay. A centered modal that searches
// across commands, skills, sessions, and memories in one place. Additive to
// the slash drawer: both can coexist in the codebase; they are mutually
// exclusive at runtime (only one can be open at a time).

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// paletteItemKind distinguishes what a palette entry represents.
type paletteItemKind int

const (
	paletteCommand paletteItemKind = iota
	paletteSkill
	paletteSession
	paletteMemory
)

// paletteItem is one row in the command palette.
type paletteItem struct {
	kind     paletteItemKind
	label    string // main display text
	sublabel string // secondary / muted text (description or snippet)
	action   func(*model) tea.Cmd
}

// paletteState holds all mutable state for the palette overlay.
type paletteState struct {
	open    bool
	query   string
	items   []paletteItem
	index   int
	allCmds []paletteItem // snapshot of commands loaded at open time
	allSkls []paletteItem // snapshot of skills loaded at open time
	allSess []paletteItem // recent sessions loaded at open time
	allMems []paletteItem // recent memories loaded at open time
}

// openPalette loads all data and opens the palette.
func (m *model) openPalette() {
	m.palette.open = true
	m.palette.query = ""
	m.palette.index = 0

	// Commands.
	m.palette.allCmds = nil
	for _, c := range m.commands {
		c := c // capture
		m.palette.allCmds = append(m.palette.allCmds, paletteItem{
			kind:     paletteCommand,
			label:    c.Name,
			sublabel: c.Description,
			action: func(pm *model) tea.Cmd {
				return c.Handler(pm)
			},
		})
	}

	// Skills — loaded synchronously; the list is small (typically < 50).
	m.palette.allSkls = nil
	if skills, err := m.db.Skills.List(); err == nil {
		for _, sk := range skills {
			sk := sk // capture
			m.palette.allSkls = append(m.palette.allSkls, paletteItem{
				kind:     paletteSkill,
				label:    sk.Name,
				sublabel: sk.Description,
				action: func(pm *model) tea.Cmd {
					text := "Please follow this skill:\n\n" + sk.Content
					pm.appendUser("(applying skill: " + sk.Name + ")")
					pm.startAssistant()
					return pm.submit(text)
				},
			})
		}
	}

	// Recent sessions (top 5).
	m.palette.allSess = nil
	if sessions, err := m.db.Sessions.List(); err == nil {
		limit := 5
		if len(sessions) < limit {
			limit = len(sessions)
		}
		for _, s := range sessions[:limit] {
			s := s // capture
			label := s.Name
			if label == "" {
				label = fmt.Sprintf("session %d", s.ID)
			}
			age := humanAge(s.LastActive)
			sublabel := s.Role + " · " + age
			m.palette.allSess = append(m.palette.allSess, paletteItem{
				kind:     paletteSession,
				label:    label,
				sublabel: sublabel,
				action: func(pm *model) tea.Cmd {
					if s.ID == pm.session.ID {
						pm.appendSystem("This is the current session.")
					} else {
						pm.appendSystem(fmt.Sprintf(
							"To switch sessions, restart with: cairo --tui -session %d", s.ID))
					}
					return nil
				},
			})
		}
	}

	// Recent memories (top 5).
	m.palette.allMems = nil
	if mems, err := m.db.Memories.Recent(5); err == nil {
		for _, mem := range mems {
			mem := mem // capture
			snippet := oneLine(mem.Content, 48)
			label := snippet
			if mem.PinnedAt != nil {
				label = "[P] " + snippet
			}
			m.palette.allMems = append(m.palette.allMems, paletteItem{
				kind:     paletteMemory,
				label:    label,
				sublabel: "memory",
				action: func(pm *model) tea.Cmd {
					insertMemoryRef(pm, mem)
					return nil
				},
			})
		}
	}

	// Panel toggles — one entry per registered panel with a toggle key.
	for _, spec := range registeredPanels {
		if spec.ToggleKey == "" {
			continue
		}
		spec := spec // capture
		m.palette.allCmds = append(m.palette.allCmds, paletteItem{
			kind:     paletteCommand,
			label:    "panel: " + spec.Title,
			sublabel: spec.ToggleKey,
			action: func(pm *model) tea.Cmd {
				return pm.togglePanel(spec.ID)
			},
		})
	}

	m.palette.items = m.buildPaletteItems("")
}

// closePalette tears down palette state.
func (m *model) closePalette() {
	m.palette.open = false
	m.palette.query = ""
	m.palette.items = nil
	m.palette.index = 0
}

// buildPaletteItems filters and assembles the visible item list for a given
// query. Dispatch:
//
//   - empty  → all commands + recent sessions + recent memories
//   - "@..."  → substring-search across memories (label)
//   - else    → substring-filter across all four kinds
func (m *model) buildPaletteItems(query string) []paletteItem {
	q := strings.ToLower(strings.TrimSpace(query))

	if q == "" {
		// Default view: all commands + recent sessions + memories.
		var out []paletteItem
		out = append(out, m.palette.allCmds...)
		out = append(out, m.palette.allSess...)
		out = append(out, m.palette.allMems...)
		return out
	}

	if strings.HasPrefix(q, "@") {
		// Memory-focused search: strip the '@' and search memory labels.
		needle := strings.TrimPrefix(q, "@")
		var out []paletteItem
		for _, it := range m.palette.allMems {
			if strings.Contains(strings.ToLower(it.label), needle) ||
				strings.Contains(strings.ToLower(it.sublabel), needle) {
				out = append(out, it)
			}
		}
		return out
	}

	// General substring filter across all four pools.
	var out []paletteItem
	for _, pool := range [][]paletteItem{
		m.palette.allCmds,
		m.palette.allSkls,
		m.palette.allSess,
		m.palette.allMems,
	} {
		for _, it := range pool {
			if strings.Contains(strings.ToLower(it.label), q) ||
				strings.Contains(strings.ToLower(it.sublabel), q) {
				out = append(out, it)
			}
		}
	}
	return out
}

// updatePalette handles a keypress while the palette is open. Returns
// (handled, cmd). If handled is true the key should not fall through.
func (m *model) updatePalette(key string) (bool, tea.Cmd) {
	switch key {
	case "esc", "ctrl+c":
		m.closePalette()
		return true, nil

	case "up", "ctrl+p":
		if m.palette.index > 0 {
			m.palette.index--
		}
		return true, nil

	case "down", "ctrl+n":
		if m.palette.index < len(m.palette.items)-1 {
			m.palette.index++
		}
		return true, nil

	case "enter":
		if len(m.palette.items) == 0 || m.palette.index >= len(m.palette.items) {
			m.closePalette()
			return true, nil
		}
		item := m.palette.items[m.palette.index]
		m.closePalette()
		if item.action != nil {
			return true, item.action(m)
		}
		return true, nil

	case "backspace":
		if len(m.palette.query) > 0 {
			// Remove last rune (not byte) so multi-byte chars stay clean.
			runes := []rune(m.palette.query)
			m.palette.query = string(runes[:len(runes)-1])
			m.palette.items = m.buildPaletteItems(m.palette.query)
			if m.palette.index >= len(m.palette.items) {
				m.palette.index = 0
			}
		}
		return true, nil
	}

	// Any printable rune: append to query. We check for single-rune keys by
	// testing that the string is exactly one rune and has no modifier prefix.
	// Modifier combos like "ctrl+a" contain '+' so this safely excludes them.
	if !strings.Contains(key, "+") && len([]rune(key)) == 1 {
		m.palette.query += key
		m.palette.items = m.buildPaletteItems(m.palette.query)
		if m.palette.index >= len(m.palette.items) {
			m.palette.index = 0
		}
		return true, nil
	}

	// Unrecognized key — claim it (don't let it reach the input) but no-op.
	return true, nil
}

// renderPalette draws the centered modal overlay. Returns the composed view
// string with the palette placed on top of the existing view.
func (m model) renderPalette(base string) string {
	// Palette dimensions.
	paletteW := min(60, m.width-4)
	if paletteW < 20 {
		paletteW = 20
	}
	visibleItems := len(m.palette.items)
	paletteH := min(20, visibleItems+4) // +4: border top + search + divider + border bot
	if paletteH < 6 {
		paletteH = 6
	}

	// Inner width (inside the borders).
	innerW := paletteW - 2
	if innerW < 4 {
		innerW = 4
	}

	// Styles.
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Background(colSurface).
		Width(innerW)

	searchStyle := lipgloss.NewStyle().
		Foreground(colText).
		Background(colSurface)

	hintStyle := lipgloss.NewStyle().Foreground(colTextDim)

	selBg := lipgloss.NewStyle().
		Foreground(colFocus).
		Background(colSurfaceHi).
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(colText).
		Background(colSurface)

	// Search line.
	cursor := "█"
	queryDisplay := m.palette.query
	if queryDisplay == "" {
		queryDisplay = hintStyle.Render("search commands, skills, sessions, memories…")
		cursor = ""
	}
	searchLine := searchStyle.Render("> ") + queryDisplay + cursor

	// Divider.
	divLine := lipgloss.NewStyle().
		Foreground(colBorderThin).
		Background(colSurface).
		Render(strings.Repeat("─", innerW))

	// Item rows.
	maxListRows := paletteH - 4 // search + divider + top border + bot border
	if maxListRows < 1 {
		maxListRows = 1
	}

	var rows []string
	if len(m.palette.items) == 0 {
		rows = append(rows, hintStyle.Render(padRight("  no matches", innerW)))
	} else {
		// Window so selected item stays visible.
		start := 0
		if m.palette.index >= maxListRows {
			start = m.palette.index - maxListRows + 1
		}
		end := start + maxListRows
		if end > len(m.palette.items) {
			end = len(m.palette.items)
		}

		for i := start; i < end; i++ {
			it := m.palette.items[i]
			kindGlyph, kindStyle := paletteKindGlyph(it.kind)
			sublabelStyle := lipgloss.NewStyle().
				Foreground(colTextDim).
				Background(colSurface)
			if i == m.palette.index {
				sublabelStyle = lipgloss.NewStyle().
					Foreground(colTextDim).
					Background(colSurfaceHi)
			}

			// Truncate label and sublabel to fit.
			labelMax := innerW - 4 // glyph(1) + spaces(2) + min sublabel(1)
			sublabelMax := 20
			if labelMax < 4 {
				labelMax = 4
			}
			label := it.label
			if lipgloss.Width(label) > labelMax {
				label = label[:labelMax-1] + "…"
			}
			sublabel := it.sublabel
			if lipgloss.Width(sublabel) > sublabelMax {
				sublabel = sublabel[:sublabelMax-1] + "…"
			}

			// Pad label so sublabel aligns right-ish.
			gap := innerW - 2 - lipgloss.Width(kindGlyph) - 1 -
				lipgloss.Width(label) - 2 - lipgloss.Width(sublabel)
			if gap < 1 {
				gap = 1
			}
			padding := strings.Repeat(" ", gap)

			var row string
			if i == m.palette.index {
				row = selBg.Render(padRight(
					" "+kindStyle(kindGlyph)+" "+label+padding+sublabel+" ",
					innerW,
				))
			} else {
				row = normalStyle.Render(" ") +
					kindStyle(kindGlyph) +
					normalStyle.Render(" "+label+padding) +
					sublabelStyle.Render(sublabel+" ")
				row = padRightStyled(row, innerW, colSurface)
			}
			rows = append(rows, row)
		}
	}

	// Assemble inner content: search line + divider + rows.
	var body strings.Builder
	body.WriteString(searchLine)
	body.WriteByte('\n')
	body.WriteString(divLine)
	body.WriteByte('\n')
	for i, r := range rows {
		body.WriteString(r)
		if i < len(rows)-1 {
			body.WriteByte('\n')
		}
	}

	// Render with border.
	modal := borderStyle.Render(body.String())

	// Place centered.
	placed := lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		modal,
		lipgloss.WithWhitespaceBackground(colBg),
	)

	// Overlay placed modal onto base view. We compose by overlaying the
	// modal over the full base string. Since lipgloss.Place fills the whole
	// terminal, we can return it directly — it replaces the base completely,
	// which is the right UX for a modal.
	_ = base
	return placed
}

// paletteKindGlyph returns the glyph and a coloring func for a given item kind.
func paletteKindGlyph(kind paletteItemKind) (string, func(string) string) {
	switch kind {
	case paletteCommand:
		st := lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true)
		return "/", func(s string) string { return st.Render(s) }
	case paletteSkill:
		st := lipgloss.NewStyle().Foreground(colMemory).Bold(true)
		return "⚡", func(s string) string { return st.Render(s) }
	case paletteSession:
		st := lipgloss.NewStyle().Foreground(colThread)
		return "◷", func(s string) string { return st.Render(s) }
	case paletteMemory:
		st := lipgloss.NewStyle().Foreground(colMemory).Faint(true)
		return "◈", func(s string) string { return st.Render(s) }
	}
	return " ", func(s string) string { return s }
}

// padRightStyled pads a styled string to targetWidth display cells, filling
// with spaces rendered in the given background color.
func padRightStyled(s string, targetWidth int, bg lipgloss.Color) string {
	w := lipgloss.Width(s)
	if w >= targetWidth {
		return s
	}
	fill := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", targetWidth-w))
	return s + fill
}
