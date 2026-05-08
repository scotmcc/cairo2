package tui

// panel_config_consider.go — custom rendering and interaction for the Consider
// section of the settings panel. Handles the aspects table, inline add/edit
// form, and multi-line template textarea. All persistence goes to SQLite
// immediately (no save-all button — matches existing settings pattern).

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/agent/consider"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
)

// considerFocus tracks which sub-section of the Consider panel has focus.
type considerFocus int

const (
	conFocusFields   considerFocus = iota // enabled/model/summary_model fields (default)
	conFocusAspects                       // aspect table row selection
	conFocusForm                          // add/edit aspect form
	conFocusTemplate                      // template textarea
)

// considerState holds the extended state for the Consider section.
type considerState struct {
	focus considerFocus

	// Cached aspect list — refreshed on section open and after mutations.
	aspects []*identity.ConsiderAspect
	aspIdx  int // selected row index

	// Add/edit form.
	addMode    bool // true = adding new; false = editing existing
	formName   textinput.Model
	formTraits textinput.Model
	formField  int // 0 = name, 1 = traits

	// Delete confirmation.
	deleteConfirm bool

	// Template textarea.
	templateTA textarea.Model
}

// considerOpen initialises the consider sub-state when the section is entered.
// Called from configOpen (and when the rail selection lands on Consider).
func considerOpen(m *model) {
	// Load aspects.
	aspects, _ := m.db.ConsiderAspects.List()
	m.config.consider.aspects = aspects
	if m.config.consider.aspIdx >= len(aspects) {
		m.config.consider.aspIdx = 0
	}

	// Init form inputs.
	ni := textinput.New()
	ni.Placeholder = "name"
	ni.CharLimit = 64
	m.config.consider.formName = ni

	ti := textinput.New()
	ti.Placeholder = "traits (comma-separated)"
	ti.CharLimit = 512
	m.config.consider.formTraits = ti

	// Init template textarea.
	tmpl, _ := m.db.Config.Get(config.KeyConsiderTemplate)
	ta := textarea.New()
	ta.SetValue(tmpl)
	ta.CharLimit = 0
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.SetWidth(60)
	ta.SetHeight(5)
	m.config.consider.templateTA = ta
}

// isConsiderSection reports whether the currently selected config section is Consider.
func isConsiderSection(m *model) bool {
	if m.config.sectionIdx < 0 || m.config.sectionIdx >= len(configLayout) {
		return false
	}
	return configLayout[m.config.sectionIdx].title == "Consider"
}

// considerReloadAspects re-fetches the aspect list from the DB and clamps aspIdx.
func considerReloadAspects(m *model) {
	aspects, _ := m.db.ConsiderAspects.List()
	m.config.consider.aspects = aspects
	if m.config.consider.aspIdx >= len(aspects) {
		m.config.consider.aspIdx = max(0, len(aspects)-1)
	}
}

// considerHandleKey routes key events when the Consider section is active.
// Returns (consumed, cmd). consumed=false passes the key to the generic handler.
func considerHandleKey(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	cs := &m.config.consider

	switch cs.focus {
	case conFocusForm:
		return considerHandleForm(key, m)
	case conFocusTemplate:
		return considerHandleTemplate(key, m)
	case conFocusAspects:
		return considerHandleAspects(key, m)
	default: // conFocusFields — handled by generic path but intercept 'ctrl+a', tab-into
		switch key.String() {
		case "ctrl+a":
			considerStartAdd(m)
			return true, textinput.Blink
		case "down", "j":
			// If on last field, wrap into aspects table.
			section := configLayout[m.config.sectionIdx]
			if m.config.fieldIdx == len(section.keys)-1 {
				cs.focus = conFocusAspects
				return true, nil
			}
		case "tab":
			// tab from fields moves to aspects.
			if m.config.focus == cfgFocusFields {
				cs.focus = conFocusAspects
				return true, nil
			}
		}
	}
	return false, nil
}

func considerHandleAspects(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	cs := &m.config.consider

	if cs.deleteConfirm {
		switch key.String() {
		case "y", "Y", "enter":
			if cs.aspIdx < len(cs.aspects) {
				name := cs.aspects[cs.aspIdx].Name
				_ = m.db.ConsiderAspects.Delete(name)
				considerReloadAspects(m)
				configFlash(m, "deleted "+name)
			}
			cs.deleteConfirm = false
		default:
			cs.deleteConfirm = false
			configFlash(m, "delete cancelled")
		}
		return true, nil
	}

	switch key.String() {
	case "esc", "left", "h":
		cs.focus = conFocusFields
		m.config.fieldIdx = len(configLayout[m.config.sectionIdx].keys) - 1
		return true, nil
	case "up", "k":
		if cs.aspIdx > 0 {
			cs.aspIdx--
		}
		return true, nil
	case "down", "j":
		if cs.aspIdx < len(cs.aspects)-1 {
			cs.aspIdx++
		} else {
			// Wrap down into template — jump the viewport so the
			// textarea is visible.
			cs.focus = conFocusTemplate
			cs.templateTA.Focus()
			m.config.fieldsVP.GotoBottom()
			return true, textarea.Blink
		}
		return true, nil
	case " ":
		if cs.aspIdx < len(cs.aspects) {
			a := cs.aspects[cs.aspIdx]
			_ = m.db.ConsiderAspects.SetEnabled(a.Name, !a.Enabled)
			considerReloadAspects(m)
		}
		return true, nil
	case "ctrl+a":
		considerStartAdd(m)
		return true, textinput.Blink
	case "enter":
		if cs.aspIdx < len(cs.aspects) {
			considerStartEdit(m)
			return true, textinput.Blink
		}
		return true, nil
	case "ctrl+d", "ctrl+x":
		if len(cs.aspects) > 0 {
			cs.deleteConfirm = true
		}
		return true, nil
	case "pgup", "pgdown", "home", "end":
		// Scroll the right-pane viewport — Aspects + Aspect Body +
		// Compiled Preview can overflow on small terminals and the
		// arrow keys are bound to selection.
		newVp, cmd := m.config.fieldsVP.Update(key)
		m.config.fieldsVP = newVp
		return true, cmd
	}
	return true, nil
}

func considerHandleForm(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	cs := &m.config.consider

	switch key.String() {
	case "esc":
		cs.focus = conFocusAspects
		cs.addMode = false
		cs.formName.Blur()
		cs.formTraits.Blur()
		return true, nil
	case "tab", "down":
		cs.formField = 1 - cs.formField
		if cs.formField == 0 {
			cs.formName.Focus()
			cs.formTraits.Blur()
		} else {
			cs.formTraits.Focus()
			cs.formName.Blur()
		}
		return true, textinput.Blink
	case "enter":
		name := strings.TrimSpace(cs.formName.Value())
		traits := strings.TrimSpace(cs.formTraits.Value())
		if name == "" {
			configFlash(m, "name required")
			return true, nil
		}
		var err error
		if cs.addMode {
			err = m.db.ConsiderAspects.Add(name, traits)
		} else {
			err = m.db.ConsiderAspects.Update(name, traits)
		}
		if err != nil {
			configFlash(m, "error: "+err.Error())
			return true, nil
		}
		considerReloadAspects(m)
		// Select the saved aspect.
		for i, a := range cs.aspects {
			if a.Name == name {
				cs.aspIdx = i
				break
			}
		}
		cs.focus = conFocusAspects
		cs.addMode = false
		cs.formName.Blur()
		cs.formTraits.Blur()
		configFlash(m, "saved "+name)
		return true, nil
	default:
		var cmd tea.Cmd
		if cs.formField == 0 {
			cs.formName, cmd = cs.formName.Update(key)
		} else {
			cs.formTraits, cmd = cs.formTraits.Update(key)
		}
		return true, cmd
	}
}

func considerHandleTemplate(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	cs := &m.config.consider

	switch key.String() {
	case "esc":
		// Save and return focus to aspects.
		val := cs.templateTA.Value()
		_ = m.db.Config.Set(config.KeyConsiderTemplate, val)
		cs.templateTA.Blur()
		cs.focus = conFocusAspects
		configFlash(m, "template saved")
		return true, nil
	default:
		var cmd tea.Cmd
		cs.templateTA, cmd = cs.templateTA.Update(key)
		return true, cmd
	}
}

func considerStartAdd(m *model) {
	cs := &m.config.consider
	cs.focus = conFocusForm
	cs.addMode = true
	cs.formField = 0
	cs.formName.SetValue("")
	cs.formTraits.SetValue("")
	cs.formName.Focus()
	cs.formTraits.Blur()
}

func considerStartEdit(m *model) {
	cs := &m.config.consider
	a := cs.aspects[cs.aspIdx]
	cs.focus = conFocusForm
	cs.addMode = false
	cs.formField = 0
	cs.formName.SetValue(a.Name)
	cs.formTraits.SetValue(a.Traits)
	cs.formName.Focus()
	cs.formTraits.Blur()
}

// considerRenderExtra renders the Aspects table + Template below the
// standard field rows in the Consider section.
func considerRenderExtra(m *model, width int) string {
	cs := &m.config.consider
	accent := lipgloss.NewStyle().Foreground(colAccentBlue).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	muted := lipgloss.NewStyle().Foreground(colTextMuted)
	sep := lipgloss.NewStyle().Foreground(colBorderThin).
		Render(strings.Repeat("─", max(0, width-4)))

	var b strings.Builder
	b.WriteString("  " + sep + "\n")
	b.WriteString("  " + accent.Render("Aspects") + "\n")

	// Add hint.
	var addHint string
	if cs.focus == conFocusAspects || cs.focus == conFocusForm || cs.focus == conFocusTemplate {
		addHint = "  " + dim.Render("[ctrl+a] add  [enter] edit  [space] toggle  [ctrl+d] delete") + "\n"
	} else {
		addHint = "  " + dim.Render("[tab] → aspects  [ctrl+a] add") + "\n"
	}
	b.WriteString(addHint)

	// Inline form (when active).
	if cs.focus == conFocusForm {
		formStyle := lipgloss.NewStyle().Foreground(colVoiceSelene)
		labelStyle := lipgloss.NewStyle().Foreground(colText)
		b.WriteString("\n")
		nameLabel := labelStyle.Render("  Name:   ")
		nameField := formStyle.Render(cs.formName.View())
		b.WriteString(nameLabel + nameField + "\n")
		traitsLabel := labelStyle.Render("  Traits: ")
		traitsField := formStyle.Render(cs.formTraits.View())
		b.WriteString(traitsLabel + traitsField + "\n")
		b.WriteString("  " + dim.Render("[enter] save  [tab] next field  [esc] cancel") + "\n")
		b.WriteString("\n")
	}

	// Delete confirmation.
	if cs.deleteConfirm && cs.aspIdx < len(cs.aspects) {
		warn := lipgloss.NewStyle().Foreground(colErr).Bold(true)
		b.WriteString("  " + warn.Render(fmt.Sprintf("Delete %q? [y] yes  [any] cancel", cs.aspects[cs.aspIdx].Name)) + "\n")
	}

	// Aspects table.
	if len(cs.aspects) == 0 {
		b.WriteString("  " + dim.Render("(no aspects — press [ctrl+a] to add)") + "\n")
	} else {
		// Column widths.
		nameW := 12
		for _, a := range cs.aspects {
			if l := len(a.Name); l > nameW {
				nameW = l
			}
		}
		traitsW := max(10, width-nameW-12)

		checkStyle := lipgloss.NewStyle().Foreground(colAccentBlue)
		selStyle := lipgloss.NewStyle().Foreground(colAccentBlue).Bold(true)
		normalStyle := lipgloss.NewStyle().Foreground(colText)

		for i, a := range cs.aspects {
			isSel := i == cs.aspIdx && (cs.focus == conFocusAspects || cs.focus == conFocusForm || cs.focus == conFocusTemplate)

			check := "[ ]"
			if a.Enabled {
				check = checkStyle.Render("[x]")
			}

			name := a.Name
			if len(name) > nameW {
				name = name[:nameW-1] + "…"
			}
			traits := a.Traits
			if len(traits) > traitsW {
				traits = traits[:traitsW-1] + "…"
			}

			var row string
			if isSel {
				cursor := selStyle.Render("▸ ")
				row = "  " + cursor + check + " " + selStyle.Render(pad(name, nameW)) + "  " + muted.Render(traits)
			} else {
				row = "    " + check + " " + normalStyle.Render(pad(name, nameW)) + "  " + muted.Render(traits)
			}
			b.WriteString(row + "\n")
		}
	}

	// Aspect body section (the editable fragment that gets substituted into
	// the locked scaffolding shown in the Compiled Preview below).
	b.WriteString("  " + sep + "\n")
	b.WriteString("  " + accent.Render("Aspect Body") + "  " +
		muted.Render("(editable; {name} and {traits} are substituted per aspect)") + "\n")

	var taFocus string
	if cs.focus == conFocusTemplate {
		taFocus = dim.Render("  [esc] save & exit") + "\n"
	} else {
		taFocus = dim.Render("  [tab from aspects ↓] to edit") + "\n"
	}
	b.WriteString(taFocus)

	cs.templateTA.SetWidth(max(20, width-6))
	b.WriteString("  " + cs.templateTA.View() + "\n")

	// Compiled preview — full assembled prompt for the currently-selected
	// aspect. Locked scaffolding is dim; the substituted body stands out so
	// the user can see exactly what their edits land in.
	b.WriteString("\n  " + sep + "\n")
	b.WriteString("  " + accent.Render("Compiled Preview") + "\n")

	var previewName, previewTraits, caption string
	if cs.aspIdx >= 0 && cs.aspIdx < len(cs.aspects) {
		a := cs.aspects[cs.aspIdx]
		previewName, previewTraits = a.Name, a.Traits
		caption = fmt.Sprintf("preview against: %s", a.Name)
	} else {
		previewName, previewTraits = "{name}", "{traits}"
		caption = "no aspect selected — placeholders shown"
	}
	b.WriteString("  " + dim.Render(caption) + "\n\n")

	prefix, bodyOut, suffix := consider.BuildSystemPromptParts(
		previewName, previewTraits, cs.templateTA.Value())

	lockedStyle := lipgloss.NewStyle().Foreground(colTextDim)
	bodyStyle := lipgloss.NewStyle().Foreground(colVoiceSelene)

	indent := func(s string, style lipgloss.Style) string {
		lines := strings.Split(s, "\n")
		for i, ln := range lines {
			lines[i] = "  " + style.Render(ln)
		}
		return strings.Join(lines, "\n")
	}

	b.WriteString(indent(prefix, lockedStyle))
	b.WriteString(indent(bodyOut, bodyStyle))
	b.WriteString(indent(suffix, lockedStyle))
	b.WriteString("\n")

	return b.String()
}

// pad right-pads s to exactly width runes.
func pad(s string, width int) string {
	l := len([]rune(s))
	if l >= width {
		return s
	}
	return s + strings.Repeat(" ", width-l)
}
