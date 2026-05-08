package tui

// panel_prompt.go — fullscreen view of the assembled system prompt, broken
// out by section so the user can see *what* Selene reads in the order she
// reads it. Most sections are read-only (Soul / Roles / Tools belong to the
// AI). Two slots are user-owned and editable: Steering (top, frames the
// turn) and Context (after Soul, persistent identity/preferences).
//
// Editable sections open in the host editor (VS Code, WaveTerm, $EDITOR)
// via internal/hostedit — the user edits in their real editor, presses 'r'
// when done, and cairo reloads the content from the tempfile and saves it
// back to the corresponding config key.

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/hostedit"
	"github.com/scotmcc/cairo2/internal/providers"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
)

const panelPromptID panelID = "prompt"

// promptSection describes one row in the rail and the right-pane content
// it produces. Editable sections bind to a config key; their content can
// be written back via the host-editor flow.
//
// requestSide marks sections that are NOT part of the system prompt — they
// represent other parts of the /api/chat request body (tool schemas, prior
// conversation history). Cairo prefills them on every turn just like the
// system prompt, so they belong in the cost picture, but separating them
// in the rail keeps the user's mental model honest about what is "Selene's
// standing identity" vs "this turn's contextual baggage."
type promptSection struct {
	key         string
	title       string
	accent      lipgloss.Color
	editable    bool
	configKey   string // set when editable
	headingHint string // how this section appears to the AI ("## Steering")
	requestSide bool   // true = synthetic, computed from request body not system prompt
	build       func(m *model) string
}

// promptLayout drives the rail order — must match the order BuildSystemPrompt
// assembles sections so the user's mental model lines up with what Selene reads.
var promptLayout = []promptSection{
	{
		key: "steering", title: "Steering", accent: colVoiceUser, editable: true,
		configKey: config.KeyUserSteering, headingHint: "## Steering",
		build: promptBuildSteering,
	},
	{
		key: "base", title: "Base", accent: colTool,
		build: promptBuildBase,
	},
	{
		key: "env", title: "Environment", accent: colAccentBlue,
		build: promptBuildEnv,
	},
	{
		key: "soul", title: "Soul", accent: colVoiceSelene,
		headingHint: "## My character",
		build:       promptBuildSoul,
	},
	{
		key: "context", title: "Context", accent: colVoiceUser, editable: true,
		configKey: config.KeyUserContext, headingHint: "## About the user",
		build: promptBuildContext,
	},
	{
		key: "role", title: "Role", accent: colAccentMagenta,
		build: promptBuildRole,
	},
	{
		key: "tools", title: "Tools", accent: colTool,
		build: promptBuildTools,
	},
	{
		key: "summaries", title: "Summaries", accent: colMemory,
		build: promptBuildSummaries,
	},
	{
		key: "memories", title: "Memories", accent: colMemory,
		build: promptBuildMemories,
	},
	// ---- request-side synthetic sections ----
	// These don't appear inside the system-prompt string but they DO get
	// sent to Ollama on every /api/chat call and DO contribute to prefill
	// cost. Listing them here makes the "why is prefill slow" diagnosis
	// honest — the prompt total alone undercounts the real load.
	{
		key: "tool_schemas", title: "Tool schemas", accent: colTool, requestSide: true,
		headingHint: "request body · tools[]",
		build:       promptBuildToolSchemas,
	},
	{
		key: "history", title: "History", accent: colVoiceUser, requestSide: true,
		headingHint: "request body · messages[]",
		build:       promptBuildHistory,
	},
}

type promptState struct {
	sectionIdx int
	fieldsVP   viewport.Model

	// External-edit tracking. editing=true between `e` (open editor) and
	// `r` (reload) or `c` (cancel). The tempfile is the bridge.
	editing       bool
	editConfigKey string
	editTempfile  string

	flash        string
	flashExpires time.Time

	lastWidth, lastHeight int

	// Section content cache + token estimates. Built once on panel open
	// and refreshed manually via `r` (when not editing). Avoids re-running
	// every section's build() — which calls into the DB and providers —
	// on every 300ms render tick.
	built        map[string]string
	tokens       map[string]int
	promptTokens int // system-prompt sections only — "what Selene reads"
	totalTokens  int // full request — system prompt + tool schemas + history
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelPromptID,
		Position:    posFullscreen,
		Accent:      colVoiceSelene,
		Title:       "prompt",
		Description: "Show the assembled system prompt section by section. Edit Steering and Context.",
		ToggleKey:   "ctrl+p",
		ShowInHelp:  true,
		OnOpen:      promptOpen,
		OnClose:     promptClose,
		Update:      promptUpdate,
		View:        promptView,
	})
}

// --- lifecycle ---

func promptOpen(m *model) tea.Cmd {
	m.prompt.fieldsVP = viewport.New(0, 0)
	if m.prompt.sectionIdx < 0 || m.prompt.sectionIdx >= len(promptLayout) {
		m.prompt.sectionIdx = 0
	}
	promptRefreshAll(m)
	return nil
}

// promptRefreshAll rebuilds every section's content and token estimate.
// Called on open and on `r` (when not editing). The cost-budget motive
// is to NOT do this on every render — we touch the DB and probe the
// environment for some sections, and the panel renders on every tick.
//
// Tracks two totals: promptTokens (system-prompt sections only — what
// "Selene reads") and totalTokens (the full request body cost — what
// Ollama actually prefills, including tool schemas and history).
func promptRefreshAll(m *model) {
	m.prompt.built = make(map[string]string, len(promptLayout))
	m.prompt.tokens = make(map[string]int, len(promptLayout))
	promptTotal := 0
	requestExtra := 0
	for _, sec := range promptLayout {
		content := sec.build(m)
		m.prompt.built[sec.key] = content
		// For the synthetic request-side sections we want the *content*
		// shown in the right pane to be the human-readable readout, but
		// the *token estimate* in the rail to match what Ollama actually
		// prefills. Recompute via dedicated cost helpers for those.
		var t int
		switch sec.key {
		case "tool_schemas":
			t = toolSchemasCost(m)
		case "history":
			t = historyCost(m)
		default:
			t = tokenEstimate(content)
		}
		m.prompt.tokens[sec.key] = t
		if sec.requestSide {
			requestExtra += t
		} else {
			promptTotal += t
		}
	}
	m.prompt.promptTokens = promptTotal
	m.prompt.totalTokens = promptTotal + requestExtra
}

// toolSchemasCost serializes every registered tool to its wire form and
// estimates tokens against the marshaled JSON. This matches what Ollama
// receives in the request's tools[] array.
func toolSchemasCost(m *model) int {
	tools := m.agent.Tools()
	total := 0
	for _, t := range tools {
		if js, err := json.Marshal(agent.ToLLM(t)); err == nil {
			total += tokenEstimate(string(js))
		}
	}
	return total
}

// historyCost sums the content size of every in-memory history message.
// Ignores tool-call payloads in the assistant rows (small relative to
// content); good enough for the diagnosis use case.
func historyCost(m *model) int {
	hist := m.agent.History()
	total := 0
	for _, msg := range hist {
		total += tokenEstimate(msg.Content)
	}
	return total
}

// tokenEstimate returns an approximate token count for s using the
// chars/4 heuristic — close enough for "is my prompt 2k or 30k?" and
// matches the same shape used by the old prompt panel and the agent
// loop's memory budget code. Not a real tokenizer.
func tokenEstimate(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

// formatTokenCount formats a token estimate compactly: "320", "1.2k",
// "22.4k". Right-aligns nicely in fixed-width slots.
func formatTokenCount(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%d", n)
	case n < 10000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	}
}

// formatTokens formats a token count for the live status-bar meter.
// Always shows one decimal place for values ≥ 1000 for a stable-width
// display: "42", "999", "1.2k", "9.9k", "10.4k", "99.9k".
func formatTokens(n int) string {
	switch {
	case n <= 0:
		return "0"
	case n < 1000:
		return fmt.Sprintf("%d", n)
	default:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
}

func promptClose(m *model) {
	// Discard any in-progress edit. The user pressed Esc without reloading;
	// honor that as "cancel my edit" so the tempfile doesn't leak.
	if m.prompt.editing && m.prompt.editTempfile != "" {
		_ = os.Remove(m.prompt.editTempfile)
	}
	m.prompt.editing = false
	m.prompt.editTempfile = ""
	m.prompt.editConfigKey = ""
}

// --- update ---

func promptUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		// View() recomputes dims on next render; no work here.
		return false, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		newVp, cmd := m.prompt.fieldsVP.Update(msg)
		m.prompt.fieldsVP = newVp
		return false, cmd
	}

	switch key.String() {
	case "esc":
		m.closePanel(panelPromptID)
		return true, nil

	case "up", "k":
		if m.prompt.sectionIdx > 0 {
			m.prompt.sectionIdx--
		} else {
			m.prompt.sectionIdx = len(promptLayout) - 1
		}
		m.prompt.fieldsVP.GotoTop()
		return true, nil

	case "down", "j":
		m.prompt.sectionIdx = (m.prompt.sectionIdx + 1) % len(promptLayout)
		m.prompt.fieldsVP.GotoTop()
		return true, nil

	case "ctrl+e", "enter":
		return promptHandleEdit(m), nil

	case "ctrl+r":
		if m.prompt.editing {
			promptHandleReload(m)
		} else {
			promptRefreshAll(m)
			promptFlash(m, fmt.Sprintf("refreshed · ~%s tokens total", formatTokenCount(m.prompt.totalTokens)))
		}
		return true, nil

	case "ctrl+x":
		if m.prompt.editing {
			promptHandleCancelEdit(m)
		}
		return true, nil

	case "pgup":
		m.prompt.fieldsVP.PageUp()
		return true, nil
	case "pgdown":
		m.prompt.fieldsVP.PageDown()
		return true, nil
	case "home":
		m.prompt.fieldsVP.GotoTop()
		return true, nil
	case "end":
		m.prompt.fieldsVP.GotoBottom()
		return true, nil
	}
	return true, nil
}

// promptHandleEdit starts an external edit on the selected section's
// config key. Returns true if the key was claimed.
func promptHandleEdit(m *model) bool {
	if m.prompt.editing {
		promptFlash(m, "already editing — press ctrl+r to reload or ctrl+x to cancel")
		return true
	}
	sec := promptLayout[m.prompt.sectionIdx]
	if !sec.editable {
		promptFlash(m, sec.title+" is read-only — Selene owns this section")
		return true
	}
	if hostedit.WantsTUISuspend() {
		promptFlash(m, "no GUI editor host detected (VS Code / WaveTerm) — edit via config tool")
		return true
	}

	// Write current value to a tempfile.
	current, _ := m.db.Config.Get(sec.configKey)
	tmp, err := os.CreateTemp("", "cairo-"+sec.key+"-*.md")
	if err != nil {
		promptFlash(m, "tempfile: "+err.Error())
		return true
	}
	if _, err := tmp.WriteString(current); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		promptFlash(m, "write tempfile: "+err.Error())
		return true
	}
	tmp.Close()

	if err := hostedit.Open(tmp.Name(), 0); err != nil {
		_ = os.Remove(tmp.Name())
		promptFlash(m, "open editor: "+err.Error())
		return true
	}

	m.prompt.editing = true
	m.prompt.editConfigKey = sec.configKey
	m.prompt.editTempfile = tmp.Name()
	promptFlash(m, "editing in "+hostedit.Detect().String()+" — press ctrl+r when done")
	return true
}

// promptHandleReload reads the tempfile back, saves to the config key,
// cleans up, and refreshes the view.
func promptHandleReload(m *model) {
	if m.prompt.editTempfile == "" || m.prompt.editConfigKey == "" {
		return
	}
	data, err := os.ReadFile(m.prompt.editTempfile)
	if err != nil {
		promptFlash(m, "reload: "+err.Error())
		return
	}
	newVal := string(data)
	current, _ := m.db.Config.Get(m.prompt.editConfigKey)
	if newVal == current {
		_ = os.Remove(m.prompt.editTempfile)
		m.prompt.editing = false
		m.prompt.editTempfile = ""
		m.prompt.editConfigKey = ""
		promptFlash(m, "no change")
		return
	}
	if err := m.db.Config.Set(m.prompt.editConfigKey, newVal); err != nil {
		promptFlash(m, "save: "+err.Error())
		return
	}
	_ = os.Remove(m.prompt.editTempfile)
	key := m.prompt.editConfigKey
	m.prompt.editing = false
	m.prompt.editTempfile = ""
	m.prompt.editConfigKey = ""
	promptFlash(m, "saved "+key)
}

// promptHandleCancelEdit discards the tempfile and exits edit mode without
// touching the config value.
func promptHandleCancelEdit(m *model) {
	if m.prompt.editTempfile != "" {
		_ = os.Remove(m.prompt.editTempfile)
	}
	m.prompt.editing = false
	m.prompt.editTempfile = ""
	m.prompt.editConfigKey = ""
	promptFlash(m, "edit cancelled")
}

func promptFlash(m *model, msg string) {
	m.prompt.flash = msg
	m.prompt.flashExpires = time.Now().Add(4 * time.Second)
}

// --- view ---

func promptView(width, height int, m *model) string {
	m.prompt.lastWidth = width
	m.prompt.lastHeight = height

	if width < 40 || height < 6 {
		return lipgloss.NewStyle().Foreground(colTextDim).
			Render("  terminal too small for /prompt")
	}

	footer := promptFooter(m, width)
	footerH := lipgloss.Height(footer)
	bodyH := height - footerH
	if bodyH < 3 {
		bodyH = 3
	}

	railW := promptRailWidth()
	if railW > width/2 {
		railW = width / 2
	}
	sepW := 1
	fieldsW := width - railW - sepW
	if fieldsW < 20 {
		fieldsW = 20
	}

	rail := promptRenderRail(m, railW, bodyH)
	sep := configRenderSeparator(bodyH)
	fields := promptRenderFields(m, fieldsW, bodyH)

	body := lipgloss.JoinHorizontal(lipgloss.Top, rail, sep, fields)
	return lipgloss.JoinVertical(lipgloss.Left, body, footer)
}

// tokenCountWidth is how many cells we reserve for the right-aligned
// per-section count in the rail. "32.4k" is 5 chars, plus a leading space
// of breathing room.
const tokenCountWidth = 6

func promptRailWidth() int {
	maxLen := 0
	for _, s := range promptLayout {
		if l := lipgloss.Width(s.title); l > maxLen {
			maxLen = l
		}
	}
	// chrome (bar+space+mark+space) = 4, plus title, plus token-count slot.
	w := maxLen + 4 + tokenCountWidth + 2
	if w < 22 {
		w = 22
	}
	return w
}

func promptRenderRail(m *model, width, height int) string {
	bg := lipgloss.NewStyle().Background(colSurface).Foreground(colTextMuted)
	dimTitle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextDim).Bold(true)
	totalStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colVoiceSelene).Bold(true)

	var b strings.Builder

	// Header row: "Request" total — the full cost Ollama prefills on each
	// turn (system prompt + tool schemas + history). The pure system-prompt
	// total appears on the divider below; together they let the user see
	// "what Selene reads" vs "what Ollama actually crunches."
	totalStr := formatTokenCount(m.prompt.totalTokens)
	leftLabel := " Request"
	leftW := lipgloss.Width(leftLabel)
	totalW := lipgloss.Width(totalStr)
	gap := width - leftW - totalW - 1
	if gap < 1 {
		gap = 1
	}
	bgFill := lipgloss.NewStyle().Background(colSurface)
	b.WriteString(dimTitle.Render(leftLabel))
	b.WriteString(bgFill.Render(strings.Repeat(" ", gap)))
	b.WriteString(totalStyle.Render(totalStr))
	b.WriteString(bgFill.Render(" "))
	b.WriteByte('\n')
	usedLines := 1

	// Track whether we've crossed from prompt-side to request-side so we
	// can drop a subtle divider line between the two regions.
	emittedDivider := false

	for i, s := range promptLayout {
		if s.requestSide && !emittedDivider {
			// Sub-header for the request-side region: short label on the
			// left + system-prompt subtotal on the right.
			subLabel := " Prompt"
			subVal := formatTokenCount(m.prompt.promptTokens)
			subW := lipgloss.Width(subLabel)
			subValW := lipgloss.Width(subVal)
			gapSub := width - subW - subValW - 1
			if gapSub < 1 {
				gapSub = 1
			}
			subStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextDim).Italic(true)
			subValStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextMuted)
			b.WriteString(subStyle.Render(subLabel))
			b.WriteString(bgFill.Render(strings.Repeat(" ", gapSub)))
			b.WriteString(subValStyle.Render(subVal))
			b.WriteString(bgFill.Render(" "))
			b.WriteByte('\n')
			usedLines++
			emittedDivider = true
		}
		isCurrent := i == m.prompt.sectionIdx

		barStyle := lipgloss.NewStyle().Background(colSurface).Foreground(s.accent)
		titleStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextMuted)
		markStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colVoiceUser)
		tokStyle := lipgloss.NewStyle().Background(colSurface).Foreground(colTextDim)
		rowBg := lipgloss.NewStyle().Background(colSurface)

		bar := " "
		if isCurrent {
			bar = "▌"
			titleStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(s.accent).Bold(true)
			barStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(s.accent).Bold(true)
			markStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(colVoiceUser)
			tokStyle = lipgloss.NewStyle().Background(colSurfaceHi).Foreground(colTextMuted)
			rowBg = lipgloss.NewStyle().Background(colSurfaceHi)
		}

		mark := " "
		if s.editable {
			mark = "✎"
		}

		// Layout: bar(1) + space(1) + mark(1) + space(1) + title + pad + tokens
		// Total chrome on a row = 4 (bar + spaces + mark) + tokenCountWidth + 1 (trailing space)
		titleSlot := width - 4 - tokenCountWidth - 1
		if titleSlot < 4 {
			titleSlot = 4
		}
		title := s.title
		if titleSlot < lipgloss.Width(title) {
			title = truncate(title, titleSlot)
		}
		pad := titleSlot - lipgloss.Width(title)
		if pad < 0 {
			pad = 0
		}

		tokStr := formatTokenCount(m.prompt.tokens[s.key])
		// Right-align the token count in tokenCountWidth cells.
		tokPad := tokenCountWidth - lipgloss.Width(tokStr)
		if tokPad < 0 {
			tokPad = 0
		}
		tokCell := strings.Repeat(" ", tokPad) + tokStr

		line := barStyle.Render(bar) +
			rowBg.Render(" ") +
			markStyle.Render(mark) +
			rowBg.Render(" ") +
			titleStyle.Render(title) +
			rowBg.Render(strings.Repeat(" ", pad)) +
			tokStyle.Render(tokCell) +
			rowBg.Render(" ")
		b.WriteString(line)
		b.WriteByte('\n')
		usedLines++
	}

	blank := bg.Width(width).Render("")
	for usedLines < height {
		b.WriteString(blank)
		b.WriteByte('\n')
		usedLines++
	}
	return strings.TrimRight(b.String(), "\n")
}

func promptRenderFields(m *model, width, height int) string {
	sec := promptLayout[m.prompt.sectionIdx]

	titleStyle := lipgloss.NewStyle().Foreground(sec.accent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)

	tokenStyle := lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true)

	headerLine := "  " + titleStyle.Render(sec.title)
	if sec.headingHint != "" {
		headerLine += "  " + dim.Render(sec.headingHint)
	}
	if sec.editable {
		headerLine += "  " + lipgloss.NewStyle().Foreground(colVoiceUser).Render("· editable")
	} else {
		headerLine += "  " + dim.Render("· read-only")
	}
	headerLine += "  " + dim.Render("· ~") + tokenStyle.Render(formatTokenCount(m.prompt.tokens[sec.key])) + dim.Render(" tok")

	rule := lipgloss.NewStyle().Foreground(colBorderThin).
		Render("  " + strings.Repeat("─", max(0, width-4)))

	headerBlock := headerLine + "\n" + rule + "\n"
	headerH := lipgloss.Height(headerBlock)

	bodyH := height - headerH
	if bodyH < 3 {
		bodyH = 3
	}

	var content string
	if m.prompt.editing && m.prompt.editConfigKey == sec.configKey {
		content = promptRenderEditingNotice(width)
	} else {
		// Use the cached content rather than re-running sec.build(m) on
		// every render — most builders touch the DB or environment.
		raw := m.prompt.built[sec.key]
		if strings.TrimSpace(raw) == "" {
			if sec.editable {
				content = "\n  " + dim.Render("(empty — press ctrl+e to add)")
			} else {
				content = "\n  " + dim.Render("(empty)")
			}
		} else {
			content = "\n" + promptIndentWrap(raw, width-4, "  ")
		}
	}

	m.prompt.fieldsVP.Width = width
	m.prompt.fieldsVP.Height = bodyH
	m.prompt.fieldsVP.SetContent(content)

	return headerBlock + m.prompt.fieldsVP.View()
}

// promptRenderEditingNotice is what the right pane shows while an edit is
// in progress externally — instructions for completing the round-trip.
func promptRenderEditingNotice(width int) string {
	accent := lipgloss.NewStyle().Foreground(colVoiceUser).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	body := lipgloss.NewStyle().Foreground(colText)

	var b strings.Builder
	b.WriteString("\n  " + accent.Render("✎ editing in "+hostedit.Detect().String()))
	b.WriteString("\n\n")
	b.WriteString("  " + body.Render("When you're done in your editor, come back here and:"))
	b.WriteString("\n\n")
	b.WriteString("    " + accent.Render("r") + body.Render("  reload from disk and save"))
	b.WriteByte('\n')
	b.WriteString("    " + accent.Render("c") + body.Render("  cancel and discard the edit"))
	b.WriteString("\n\n")
	b.WriteString("  " + dim.Render("(Cairo doesn't auto-detect when you save — press ctrl+r yourself.)"))
	return b.String()
}

func promptFooter(m *model, width int) string {
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	accent := lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true)

	var hint string
	switch {
	case m.prompt.editing:
		hint = "ctrl+r reload & save · ctrl+x cancel edit · esc close"
	default:
		sec := promptLayout[m.prompt.sectionIdx]
		if sec.editable {
			hint = "↑↓ section · ctrl+e edit · ctrl+r refresh · pgup/pgdn scroll · esc close"
		} else {
			hint = "↑↓ section · ctrl+r refresh · pgup/pgdn scroll · esc close"
		}
	}

	rule := lipgloss.NewStyle().Foreground(colBorderThin).
		Render(strings.Repeat("─", max(0, width)))

	footer := "  " + accent.Render("prompt") + dim.Render("  ·  ") + dim.Render(hint)
	if m.prompt.flash != "" && time.Now().Before(m.prompt.flashExpires) {
		flash := lipgloss.NewStyle().Foreground(colVoiceUser).Italic(true).
			Render("  " + m.prompt.flash)
		return rule + "\n" + footer + "\n" + flash
	}
	return rule + "\n" + footer
}

// promptIndentWrap word-wraps content to width and prefixes every line with
// indent. Skips empty leading/trailing newlines that wrap can introduce.
func promptIndentWrap(content string, width int, indent string) string {
	if width < 4 {
		width = 4
	}
	wrapped := wordwrap.String(content, width)
	lines := strings.Split(wrapped, "\n")
	var b strings.Builder
	for i, ln := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(indent + ln)
	}
	return b.String()
}

// --- per-section content builders ---

func promptBuildSteering(m *model) string {
	v, _ := m.db.Config.Get(config.KeyUserSteering)
	return v
}

func promptBuildContext(m *model) string {
	v, _ := m.db.Config.Get(config.KeyUserContext)
	return v
}

// promptConfigVars returns the current config key→value map for template
// substitution. Returns an empty map on error so callers can proceed safely.
func promptConfigVars(m *model) map[string]string {
	vars, _ := m.db.Config.All()
	if vars == nil {
		vars = map[string]string{}
	}
	return vars
}

func promptBuildSoul(m *model) string {
	v, _ := m.db.Config.Get("soul_prompt")
	return agent.ApplyTemplates(v, promptConfigVars(m))
}

func promptBuildBase(m *model) string {
	parts, err := m.db.Prompts.Base("")
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	if len(parts) == 0 {
		return ""
	}
	vars := promptConfigVars(m)
	var b strings.Builder
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n%s", p.Key, agent.ApplyTemplates(p.Content, vars))
	}
	return b.String()
}

func promptBuildEnv(m *model) string {
	reg := providers.Default()
	return reg.GetContext(m.session.CWD)
}

func promptBuildRole(m *model) string {
	role := m.session.Role
	if role == "" {
		return "(no role on this session)"
	}
	parts, err := m.db.Prompts.ForTrigger("role:" + role)
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	if len(parts) == 0 {
		return fmt.Sprintf("(no prompt parts for role:%s)", role)
	}
	vars := promptConfigVars(m)
	var b strings.Builder
	fmt.Fprintf(&b, "Active role: %s\n\n", role)
	for i, p := range parts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n%s", p.Key, agent.ApplyTemplates(p.Content, vars))
	}
	return b.String()
}

func promptBuildTools(m *model) string {
	// Pull every prompt part with a tool:* trigger plus enabled custom tools'
	// addenda. The actual prompt only includes tools that are wired into the
	// current agent; for the panel preview we show the universe of tool-related
	// prompt content so the user can see what's seeded.
	all, err := m.db.Prompts.All()
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	var toolParts []*identity.PromptPart
	for _, p := range all {
		if strings.HasPrefix(p.Trigger, "tool:") {
			toolParts = append(toolParts, p)
		}
	}

	customTools, _ := m.db.Tools.Enabled()

	if len(toolParts) == 0 && len(customTools) == 0 {
		return ""
	}

	var b strings.Builder
	if len(toolParts) > 0 {
		b.WriteString("Built-in tool addenda:\n\n")
		for i, p := range toolParts {
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "[%s · %s]\n%s", p.Trigger, p.Key, p.Content)
		}
	}
	if len(customTools) > 0 {
		if len(toolParts) > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("Custom tool addenda:\n\n")
		for i, t := range customTools {
			if t.PromptAddendum == "" {
				continue
			}
			if i > 0 {
				b.WriteString("\n\n")
			}
			fmt.Fprintf(&b, "[%s]\n%s", t.Name, t.PromptAddendum)
		}
	}
	return b.String()
}

func promptBuildSummaries(m *model) string {
	count := 4
	if cstr, _ := m.db.Config.Get(config.KeySummaryCtx); cstr != "" {
		if n, err := strconv.Atoi(cstr); err == nil && n > 0 {
			count = n
		}
	}
	summaries, err := m.db.Summaries.LatestForSession(m.session.ID, count)
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	if len(summaries) == 0 {
		return "(no summaries for this session yet)"
	}
	var b strings.Builder
	for i, s := range summaries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%s]\n%s", s.CreatedAt.Format("Jan 2 15:04"), s.Content)
	}
	return b.String()
}

// promptBuildToolSchemas serializes every registered tool to the same
// llm.ToolDef shape Ollama receives in the request's tools[] array, then
// renders a per-tool readout: name, char/token sizes, description first
// line. The token estimate then matches what Ollama actually prefills.
func promptBuildToolSchemas(m *model) string {
	tools := m.agent.Tools()
	if len(tools) == 0 {
		return "(no tools registered)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d tools sent on every request.\n\n", len(tools))
	for _, t := range tools {
		def := agent.ToLLM(t)
		js, err := json.Marshal(def)
		if err != nil {
			fmt.Fprintf(&b, "%-22s (marshal err: %v)\n", t.Name(), err)
			continue
		}
		bytes := len(js)
		toks := tokenEstimate(string(js))
		desc := t.Description()
		// First line of description for at-a-glance recognition.
		if i := strings.IndexByte(desc, '\n'); i > 0 {
			desc = desc[:i]
		}
		if len(desc) > 80 {
			desc = desc[:79] + "…"
		}
		fmt.Fprintf(&b, "%-22s  %4d B  ~%s tok\n  %s\n\n",
			t.Name(), bytes, formatTokenCount(toks), desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

// promptBuildHistory shows the in-memory conversation history that gets
// sent on each turn. Per-message line includes role, char count, token
// estimate, and a short content preview.
func promptBuildHistory(m *model) string {
	hist := m.agent.History()
	if len(hist) == 0 {
		return "(no prior turns yet)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d messages in the in-memory history.\n", len(hist))
	fmt.Fprintf(&b, "(Older messages may be folded into Summaries via the background summarizer.)\n\n")
	for i, msg := range hist {
		content := msg.Content
		if content == "" && len(msg.ToolCalls) > 0 {
			content = fmt.Sprintf("(%d tool call(s))", len(msg.ToolCalls))
		}
		preview := strings.ReplaceAll(content, "\n", " ")
		if len(preview) > 90 {
			preview = preview[:89] + "…"
		}
		toks := tokenEstimate(content)
		role := msg.Role
		if msg.Name != "" {
			role = fmt.Sprintf("%s/%s", msg.Role, msg.Name)
		}
		fmt.Fprintf(&b, "%3d  %-12s  %5d B  ~%s tok\n     %s\n\n",
			i+1, role, len(content), formatTokenCount(toks), preview)
	}
	return strings.TrimRight(b.String(), "\n")
}

func promptBuildMemories(m *model) string {
	role := m.session.Role
	if role != "" && role != identity.RoleThinkingPartner {
		return fmt.Sprintf("(memories not auto-injected for role:%s — searched on demand via memory tool)", role)
	}
	limit := 15
	if lstr, _ := m.db.Config.Get(config.KeyMemoryLimit); lstr != "" {
		if n, err := strconv.Atoi(lstr); err == nil && n > 0 {
			limit = n
		}
	}
	memories, err := m.db.Memories.RecentContent(limit)
	if err != nil {
		return "(error: " + err.Error() + ")"
	}
	if len(memories) == 0 {
		return "(no memories yet)"
	}
	var b strings.Builder
	for i, c := range memories {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("- " + c)
	}
	total, _ := m.db.Memories.Count()
	if overflow := total - len(memories); overflow > 0 {
		fmt.Fprintf(&b, "\n\n(%d more memories available via memory tool)", overflow)
	}
	return b.String()
}
