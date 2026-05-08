package tui

// panel_config.go — fullscreen settings panel. Two-pane layout: a left rail of
// sections (Identity, LLM Backend, ...) and a right pane showing the keys and
// values of the selected section. Tab moves focus between rail and fields.
// Enter on a field opens an inline editor (text rows) or a centered modal
// picker (model rows). Opened via Ctrl+G or /config.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const panelConfigID panelID = "config"

// configURLFields lists config keys that require http:// or https:// prefixes
// when the value is non-empty.
var configURLFields = map[string]bool{
	"kokoro_url":  true,
	"searxng_url": true,
}

// urlValidFor returns true if val is empty (optional field) or starts with
// http:// or https://.
func urlValidFor(key, val string) bool {
	if !configURLFields[key] {
		return true
	}
	if val == "" {
		return true
	}
	return strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://")
}

// configFocus tracks which pane has keyboard focus when no overlay
// (editing / picking) is active.
type configFocus int

const (
	cfgFocusRail configFocus = iota
	cfgFocusFields
)

type configState struct {
	sectionIdx int
	fieldIdx   int
	focus      configFocus
	fieldsVP   viewport.Model

	editing   bool
	editInput textinput.Model
	editKey   string

	picking         bool
	pickerOptions   []string
	pickerSelected  int
	pickerWindowTop int

	// Brief flash message shown after a save action.
	flash        string
	flashExpires time.Time

	lastWidth  int
	lastHeight int

	// Extended state for the Consider section.
	consider considerState
}

// configSectionDef describes one display group in the rail.
type configSectionDef struct {
	title  string
	accent lipgloss.Color
	keys   []configKeyDef
}

type configKeyDef struct {
	key   string
	label string
	hint  string // optional hint shown beneath the field row when selected
}

// configLayout drives the section order, accents, and the keys shown per
// section. Adding a new section/key here is the only change needed.
var configLayout = []configSectionDef{
	{
		title:  "Identity",
		accent: colVoiceSelene,
		keys: []configKeyDef{
			{key: "ai_name", label: "ai_name"},
			{key: "user_name", label: "user_name"},
		},
	},
	{
		title:  "LLM Backend",
		accent: colTool,
		keys: []configKeyDef{
			{key: "ollama_url", label: "ollama_url"},
			{key: "model", label: "model", hint: "Enter opens the Ollama model picker"},
			{key: "embed_model", label: "embed_model", hint: "Enter opens the Ollama model picker"},
			{key: "think", label: "think", hint: "true / false — global default for chain-of-thought"},
			{key: "think_budget", label: "think_budget", hint: "token budget for thinking (integer)"},
		},
	},
	{
		title:  "Voice",
		accent: colAccentMagenta,
		keys: []configKeyDef{
			{key: "kokoro_url", label: "kokoro_url"},
			{key: "kokoro_voice", label: "kokoro_voice",
				hint: "blend example: af_heart(8)+af_nicole(2) · speed via say() arg"},
		},
	},
	{
		title:  "Memory",
		accent: colMemory,
		keys: []configKeyDef{
			{key: "memory_limit", label: "memory_limit"},
			{key: "summary_model", label: "summary_model"},
			{key: "summary_threshold", label: "summary_threshold",
				hint: "summarizer fires when unsummarized turn count > this (default 8)"},
			{key: "summary_batch_size", label: "summary_batch_size",
				hint: "user/assistant turns folded per fire (default 4)"},
			{key: "summary_context", label: "summary_context",
				hint: "latest N summaries surfaced in the prompt (default 4)"},
			{key: "memory_dedup_threshold", label: "dedup_threshold", hint: "cosine sim threshold for near-duplicate memory detection — range 0–1 (default 0.85)"},
			{key: "learn_max_chunk_tokens", label: "learn_max_chunk_tokens", hint: "max tokens per indexed chunk before splitting at line boundary (default 400)"},
			{key: "summary_token_threshold", label: "token_pressure_thresh", hint: "unsummarized token count triggering secondary summarizer (default 8000)"},
		},
	},
	{
		title:  "Display",
		accent: colTextMuted,
		keys: []configKeyDef{
			{key: "glamour_style", label: "glamour_style",
				hint: "dark / light / notty / auto — controls markdown rendering style"},
		},
	},
	{
		title:  "Limits",
		accent: colWarn,
		keys: []configKeyDef{
			{key: "max_turns", label: "max_turns"},
		},
	},
	{
		title:  "Search",
		accent: colAccentBlue,
		keys: []configKeyDef{
			{key: "searxng_url", label: "searxng_url"},
		},
	},
	{
		title:  "Safety",
		accent: colErr,
		keys: []configKeyDef{
			{key: "unsafe_mode", label: "unsafe_mode"},
		},
	},
	{
		title:  "Consider",
		accent: colAccentBlue,
		keys: []configKeyDef{
			{key: "consider.enabled", label: "consider.enabled",
				hint: "true / false — enable pre-turn inner-dialogue before each response"},
			{key: "consider.model", label: "consider.model",
				hint: "Ollama model used for the consider pass · Enter opens the model picker"},
			{key: "consider.summary_model", label: "consider.summary_model",
				hint: "Ollama model used to summarize the consider output · Enter opens the model picker"},
		},
	},
	{
		title:  "Roles",
		accent: colAccentGreen,
		keys: []configKeyDef{
			{key: "role:orchestrator:model", label: "orchestrator.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:orchestrator:think", label: "orchestrator.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:researcher:model", label: "researcher.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:researcher:think", label: "researcher.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:planner:model", label: "planner.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:planner:think", label: "planner.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:coder:model", label: "coder.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:coder:think", label: "coder.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:reviewer:model", label: "reviewer.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:reviewer:think", label: "reviewer.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:thinking_partner:model", label: "thinking_partner.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:thinking_partner:think", label: "thinking_partner.think",
				hint: "empty / true / false — empty inherits global"},
			{key: "role:dream:model", label: "dream.model",
				hint: "per-role Ollama model · empty = inherit global"},
			{key: "role:dream:think", label: "dream.think",
				hint: "empty / true / false — empty inherits global"},
		},
	},
}

// roleRowKey parses a config row key of the form "role:<name>:<field>" into
// its parts. ok is false for non-role keys.
func roleRowKey(key string) (roleName, field string, ok bool) {
	if !strings.HasPrefix(key, "role:") {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, "role:")
	idx := strings.LastIndex(rest, ":")
	if idx <= 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

func roleRowGet(m *model, key string) string {
	name, field, ok := roleRowKey(key)
	if !ok {
		return ""
	}
	r, err := m.db.Roles.Get(name)
	if err != nil || r == nil {
		return ""
	}
	switch field {
	case "model":
		return r.Model
	case "think":
		return r.Think
	}
	return ""
}

// roleRowSet writes a new value for a role:<name>:<field> row. Returns the
// canonical stored value and a non-nil error if rejected.
func roleRowSet(m *model, key, val string) (string, error) {
	name, field, ok := roleRowKey(key)
	if !ok {
		return val, fmt.Errorf("not a role row key: %q", key)
	}
	switch field {
	case "model":
		if err := m.db.Roles.SetModel(name, val); err != nil {
			return "", err
		}
		return val, nil
	case "think":
		if err := m.db.Roles.SetThink(name, val); err != nil {
			return "", err
		}
		return val, nil
	}
	return val, fmt.Errorf("unknown role field: %q", field)
}

// configValueOf reads the current value for a key — role-aware.
func configValueOf(m *model, key string) string {
	if _, _, ok := roleRowKey(key); ok {
		return roleRowGet(m, key)
	}
	v, _ := m.db.Config.Get(key)
	return v
}

// configHintFor returns the hint string for a given config key, or "".
func configHintFor(key string) string {
	for _, section := range configLayout {
		for _, kd := range section.keys {
			if kd.key == key {
				return kd.hint
			}
		}
	}
	return ""
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelConfigID,
		Position:    posFullscreen,
		Accent:      colTool,
		Title:       "config",
		Description: "Browse and edit configuration (Ctrl+G or /config).",
		ToggleKey:   "ctrl+g",
		ShowInHelp:  true,
		OnOpen:      configOpen,
		OnClose:     configClose,
		Update:      configUpdate,
		View:        configView,
	})
}

// --- lifecycle ---

func configOpen(m *model) tea.Cmd {
	m.config.fieldsVP = viewport.New(0, 0)
	ti := textinput.New()
	ti.CharLimit = 512
	m.config.editInput = ti
	m.config.focus = cfgFocusRail
	if m.config.sectionIdx < 0 || m.config.sectionIdx >= len(configLayout) {
		m.config.sectionIdx = 0
	}
	curr := configLayout[m.config.sectionIdx]
	if m.config.fieldIdx < 0 || m.config.fieldIdx >= len(curr.keys) {
		m.config.fieldIdx = 0
	}
	considerOpen(m)
	return nil
}

func configClose(m *model) {
	m.config.editing = false
	m.config.picking = false
	m.config.editKey = ""
	m.config.pickerOptions = nil
}

// --- update ---

func configUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		// View() is called next render with fresh dims; nothing to do here.
		return false, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		if m.config.editing {
			newTi, cmd := m.config.editInput.Update(msg)
			m.config.editInput = newTi
			return true, cmd
		}
		// Route non-key messages to consider template textarea when it has focus.
		if isConsiderSection(m) && m.config.consider.focus == conFocusTemplate {
			var cmd tea.Cmd
			m.config.consider.templateTA, cmd = m.config.consider.templateTA.Update(msg)
			return true, cmd
		}
		return false, nil
	}

	if m.config.picking {
		return configUpdatePicking(key, m)
	}
	if m.config.editing {
		return configUpdateEditing(key, m)
	}
	if m.config.focus == cfgFocusRail {
		return configUpdateRail(key, m)
	}
	// Route to consider-specific handler when that section is active and
	// the consider sub-focus is not on the plain field rows.
	if isConsiderSection(m) {
		cs := &m.config.consider
		if cs.focus != conFocusFields {
			if consumed, cmd := considerHandleKey(key, m); consumed {
				return true, cmd
			}
		} else {
			// In conFocusFields, still offer consider extras (e.g. 'a' to add).
			if consumed, cmd := considerHandleKey(key, m); consumed {
				return true, cmd
			}
		}
	}
	return configUpdateFields(key, m)
}

func configUpdateRail(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.closePanel(panelConfigID)
		return true, nil
	case "up", "k":
		if m.config.sectionIdx > 0 {
			m.config.sectionIdx--
		} else {
			m.config.sectionIdx = len(configLayout) - 1
		}
		m.config.fieldIdx = 0
		m.config.fieldsVP.GotoTop()
		if isConsiderSection(m) {
			considerOpen(m)
		}
		return true, nil
	case "down", "j":
		m.config.sectionIdx = (m.config.sectionIdx + 1) % len(configLayout)
		m.config.fieldIdx = 0
		m.config.fieldsVP.GotoTop()
		if isConsiderSection(m) {
			considerOpen(m)
		}
		return true, nil
	case "tab", "right", "l", "enter":
		m.config.focus = cfgFocusFields
		return true, nil
	}
	return true, nil
}

func configUpdateFields(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	section := configLayout[m.config.sectionIdx]
	n := len(section.keys)
	if n == 0 {
		// Empty section — only allow leaving.
		if key.String() == "esc" || key.String() == "tab" || key.String() == "left" || key.String() == "h" {
			m.config.focus = cfgFocusRail
		}
		return true, nil
	}
	switch key.String() {
	case "esc":
		m.config.focus = cfgFocusRail
		m.config.consider.focus = conFocusFields
		return true, nil
	case "tab", "shift+tab", "left", "h":
		m.config.focus = cfgFocusRail
		m.config.consider.focus = conFocusFields
		return true, nil
	case "up", "k":
		if m.config.fieldIdx > 0 {
			m.config.fieldIdx--
		} else {
			m.config.fieldIdx = n - 1
		}
		return true, nil
	case "down", "j":
		m.config.fieldIdx = (m.config.fieldIdx + 1) % n
		return true, nil
	case "enter":
		kd := section.keys[m.config.fieldIdx]
		val := configValueOf(m, kd.key)
		if configKeyUsesPicker(kd.key) && configEnterPicker(m, kd.key, val) {
			return true, nil
		}
		m.config.editing = true
		m.config.editKey = kd.key
		m.config.editInput.SetValue(val)
		m.config.editInput.CursorEnd()
		m.config.editInput.Focus()
		return true, textinput.Blink
	case "pgup", "pgdown", "home", "end":
		newVp, cmd := m.config.fieldsVP.Update(key)
		m.config.fieldsVP = newVp
		return true, cmd
	}
	return true, nil
}

func configUpdateEditing(key tea.KeyMsg, m *model) (bool, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.config.editing = false
		m.config.editKey = ""
		m.config.editInput.Blur()
		return true, nil
	case "enter":
		newVal := strings.TrimSpace(m.config.editInput.Value())
		savedKey := m.config.editKey
		if !urlValidFor(savedKey, newVal) {
			m.config.editing = false
			m.config.editKey = ""
			m.config.editInput.Blur()
			configFlash(m, "URL must start with http:// or https://")
			return true, nil
		}
		if _, _, ok := roleRowKey(m.config.editKey); ok {
			_, _ = roleRowSet(m, m.config.editKey, newVal)
		} else {
			_ = m.db.Config.Set(m.config.editKey, newVal)
		}
		m.config.editing = false
		m.config.editKey = ""
		m.config.editInput.Blur()
		configFlash(m, "saved "+savedKey)
		return true, nil
	default:
		newTi, cmd := m.config.editInput.Update(key)
		m.config.editInput = newTi
		return true, cmd
	}
}

// --- view ---

func configView(width, height int, m *model) string {
	m.config.lastWidth = width
	m.config.lastHeight = height

	if width < 40 || height < 6 {
		return lipgloss.NewStyle().Foreground(colTextDim).
			Render("  terminal too small for /config")
	}

	footer := configFooter(m, width)
	footerH := lipgloss.Height(footer)
	bodyH := height - footerH
	if bodyH < 3 {
		bodyH = 3
	}

	railW := configRailWidth()
	if railW > width/2 {
		railW = width / 2
	}
	sepW := 1
	fieldsW := width - railW - sepW
	if fieldsW < 20 {
		fieldsW = 20
	}

	rail := configRenderRail(m, railW, bodyH)
	sep := configRenderSeparator(bodyH)
	fields := configRenderFields(m, fieldsW, bodyH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, rail, sep, fields)

	view := lipgloss.JoinVertical(lipgloss.Left, body, footer)

	if m.config.picking {
		return configRenderPickerModal(m, width, height, view)
	}
	return view
}

// configRailWidth returns the rail width sized to fit the longest section
// title with comfortable padding.
func configRailWidth() int {
	maxLen := 0
	for _, s := range configLayout {
		if l := lipgloss.Width(s.title); l > maxLen {
			maxLen = l
		}
	}
	// padding: "▌ " (2) + title + trailing space (1) + buffer (3)
	w := maxLen + 6
	if w < 18 {
		w = 18
	}
	return w
}

func configRenderRail(m *model, width, height int) string {
	bg := lipgloss.NewStyle().Background(colSurface).Foreground(colTextMuted)
	dimTitle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextDim).Bold(true)

	var b strings.Builder
	// Header: "Settings"
	header := dimTitle.Width(width).Padding(0, 1).Render("Settings")
	b.WriteString(header)
	b.WriteByte('\n')

	usedLines := 1
	for i, s := range configLayout {
		isCurrent := i == m.config.sectionIdx
		bar := " "
		if isCurrent {
			bar = "▌"
		}
		titleStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextMuted)
		barStyle := lipgloss.NewStyle().Background(colSurface).Foreground(s.accent)
		if isCurrent {
			if m.config.focus == cfgFocusRail {
				titleStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(s.accent).Bold(true)
				barStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(s.accent).Bold(true)
			} else {
				titleStyle = lipgloss.NewStyle().Background(colSurface).Foreground(s.accent).Bold(true)
				barStyle = lipgloss.NewStyle().Background(colSurface).Foreground(s.accent).Bold(true)
			}
		}
		// Build a line of exactly `width` cells, all with the right background.
		title := s.title
		// Reserve: bar(1) + space(1) + title + trailing pad
		inner := width - 2
		if inner < lipgloss.Width(title) {
			title = truncate(title, inner)
		}
		pad := inner - lipgloss.Width(title)
		if pad < 0 {
			pad = 0
		}
		rowBg := lipgloss.NewStyle().Background(colSurface)
		if isCurrent && m.config.focus == cfgFocusRail {
			rowBg = lipgloss.NewStyle().Background(colSurfaceHi)
		}
		line := barStyle.Render(bar) +
			rowBg.Render(" ") +
			titleStyle.Render(title) +
			rowBg.Render(strings.Repeat(" ", pad))
		b.WriteString(line)
		b.WriteByte('\n')
		usedLines++
	}

	// Pad remaining lines with surface-colored blanks so the rail looks like
	// a solid column.
	blank := bg.Width(width).Render("")
	for usedLines < height {
		b.WriteString(blank)
		b.WriteByte('\n')
		usedLines++
	}
	return strings.TrimRight(b.String(), "\n")
}

func configRenderSeparator(height int) string {
	style := lipgloss.NewStyle().Foreground(colBorderThin)
	var b strings.Builder
	for i := 0; i < height; i++ {
		b.WriteString(style.Render("│"))
		if i < height-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func configRenderFields(m *model, width, height int) string {
	section := configLayout[m.config.sectionIdx]

	titleStyle := lipgloss.NewStyle().Foreground(section.accent).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)

	header := "  " + titleStyle.Render(section.title)
	subHeader := descStyle.Render("  " + configSectionTagline(section.title))
	rule := lipgloss.NewStyle().Foreground(colBorderThin).
		Render("  " + strings.Repeat("─", max(0, width-4)))

	headerBlock := header + "\n" + subHeader + "\n" + rule + "\n"
	headerH := lipgloss.Height(headerBlock)

	// Reserve room for the inline edit field's hint (when present).
	bodyH := height - headerH
	if bodyH < 3 {
		bodyH = 3
	}

	body := configBuildFieldsBody(m, width)

	m.config.fieldsVP.Width = width
	m.config.fieldsVP.Height = bodyH
	m.config.fieldsVP.SetContent(body)
	configScrollToFieldSelection(m)

	return headerBlock + m.config.fieldsVP.View()
}

// configSectionTagline returns a one-line description shown under the section
// title in the right pane. Returns "" if no tagline is defined; the line still
// renders blank to preserve layout stability.
func configSectionTagline(title string) string {
	switch title {
	case "Identity":
		return "Who Selene is and who you are to her."
	case "LLM Backend":
		return "Ollama URL, primary chat model, and reasoning settings."
	case "Voice":
		return "Kokoro TTS endpoint and voice blend."
	case "Memory":
		return "How much Selene remembers and when she summarizes."
	case "Limits":
		return "Hard limits on what a turn or run can consume."
	case "Search":
		return "External search backends."
	case "Display":
		return "Rendering and visual preferences."
	case "Safety":
		return "Permissioning and unsafe-mode toggles."
	case "Consider":
		return "Pre-turn inner-dialogue: model, aspects, and summarization."
	case "Roles":
		return "Per-role overrides for model and reasoning settings."
	}
	return ""
}

// configBuildFieldsBody produces the scrollable content for the right pane.
func configBuildFieldsBody(m *model, width int) string {
	section := configLayout[m.config.sectionIdx]

	labelStyle := lipgloss.NewStyle().Foreground(colText)
	valueStyle := lipgloss.NewStyle().Foreground(colTextMuted)
	emptyStyle := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
	selLabelStyle := lipgloss.NewStyle().Foreground(section.accent).Bold(true)
	selValueStyle := lipgloss.NewStyle().Foreground(colText).Bold(true)
	editStyle := lipgloss.NewStyle().Foreground(colVoiceSelene)
	hintStyle := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
	cursorStyle := lipgloss.NewStyle().Foreground(section.accent).Bold(true)

	labelW := configLabelWidth(section)
	if labelW > width/2 {
		labelW = width / 2
	}
	valueW := width - labelW - 6 // 2 left margin + 2 cursor + 2 spacing
	if valueW < 8 {
		valueW = 8
	}

	var b strings.Builder
	b.WriteByte('\n') // breathing room under the rule

	for i, kd := range section.keys {
		isSel := i == m.config.fieldIdx && m.config.focus == cfgFocusFields
		isEdit := isSel && m.config.editing && m.config.editKey == kd.key

		cursor := "  "
		if isSel {
			cursor = cursorStyle.Render("▸ ")
		}

		var lStyle, vStyle lipgloss.Style
		if isSel {
			lStyle = selLabelStyle
			vStyle = selValueStyle
		} else {
			lStyle = labelStyle
			vStyle = valueStyle
		}

		labelText := truncate(kd.label, labelW)
		labelCell := lStyle.Render(labelText) + strings.Repeat(" ", max(0, labelW-lipgloss.Width(labelText)))

		var valueCell string
		if isEdit {
			valueCell = editStyle.Render(m.config.editInput.View())
		} else {
			val := configValueOf(m, kd.key)
			if val == "" {
				valueCell = emptyStyle.Render("(empty)")
			} else {
				display := truncate(val, valueW)
				valueCell = vStyle.Render(display)
			}
		}

		b.WriteString("  " + cursor + labelCell + "  " + valueCell)
		b.WriteByte('\n')

		if isSel {
			hint := kd.hint
			if isEdit {
				hint = "enter save · esc cancel"
			}
			if hint != "" {
				b.WriteString("        " + hintStyle.Render(hint))
				b.WriteByte('\n')
			}
		}
	}
	// Append consider-specific widgets when the Consider section is active.
	if section.title == "Consider" {
		b.WriteString(considerRenderExtra(m, width))
	}

	return b.String()
}

// configLabelWidth returns the column width to use for labels in a section.
func configLabelWidth(s configSectionDef) int {
	maxLen := 0
	for _, kd := range s.keys {
		if l := lipgloss.Width(kd.label); l > maxLen {
			maxLen = l
		}
	}
	if maxLen < 12 {
		maxLen = 12
	}
	return maxLen + 2
}

// configScrollToFieldSelection nudges the viewport so the selected field is
// visible. Each row is one or two lines (selected rows with a hint take two).
func configScrollToFieldSelection(m *model) {
	if m.config.focus != cfgFocusFields {
		return
	}
	section := configLayout[m.config.sectionIdx]
	// Approximate line offset: 1 (top blank) + sum of row heights up to fieldIdx.
	line := 1
	for i := 0; i < m.config.fieldIdx; i++ {
		line++
		// Non-selected rows above don't have a hint line.
	}
	// Pull selected row a bit further down if the row has a hint, so the
	// hint stays visible too.
	rowHeight := 1
	if i := m.config.fieldIdx; i < len(section.keys) {
		if section.keys[i].hint != "" {
			rowHeight = 2
		}
	}

	vp := m.config.fieldsVP
	if line < vp.YOffset {
		m.config.fieldsVP.SetYOffset(line)
	} else if line+rowHeight-1 >= vp.YOffset+vp.Height {
		m.config.fieldsVP.SetYOffset(line + rowHeight - vp.Height)
	}
}

// configFlash sets a brief flash message in the config panel footer.
func configFlash(m *model, msg string) {
	m.config.flash = msg
	m.config.flashExpires = time.Now().Add(3 * time.Second)
}

func configFooter(m *model, width int) string {
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	accent := lipgloss.NewStyle().Foreground(colTool).Bold(true)

	var hint string
	switch {
	case m.config.picking:
		hint = "↑↓ select · enter confirm · esc cancel"
	case m.config.editing:
		hint = "enter save · esc cancel"
	case m.config.focus == cfgFocusRail:
		hint = "↑↓ section · tab/→ fields · esc close"
	default:
		hint = "↑↓ field · enter edit · pgup/pgdn scroll · tab/← sections · esc back"
	}

	rule := lipgloss.NewStyle().Foreground(colBorderThin).
		Render(strings.Repeat("─", max(0, width)))
	footer := "  " + accent.Render("config") + dim.Render("  ·  ") + dim.Render(hint)
	if m.config.flash != "" && time.Now().Before(m.config.flashExpires) {
		flash := lipgloss.NewStyle().Foreground(colVoiceSelene).Italic(true).
			Render("  " + m.config.flash)
		return rule + "\n" + footer + "\n" + flash
	}
	return rule + "\n" + footer
}

// truncate shortens s to fit width, appending an ellipsis if cut.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	// Trim by runes to avoid splitting multibyte chars.
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > width {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}
