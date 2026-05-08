// tui_view.go — Bubble Tea View() implementation; lays out header, transcript, side panels, status bar, and modal overlays.
package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	reflowtrunc "github.com/muesli/reflow/truncate"
)

// --- view ---

func (m model) View() string {
	if !m.ready {
		return "initializing…"
	}

	// Fullscreen panel (help) replaces transcript + side panels; header and
	// status-bar still render so the user's location in the program stays
	// legible even while reading a modal.
	if fs := m.panelsAt(posFullscreen); len(fs) > 0 {
		return m.renderWithFullscreen(fs[0])
	}

	header := m.renderHeader()

	// Top panels (soul, prompt-preview) slot between header and transcript.
	topRegion := m.renderStackedPanels(posTop)

	// Transcript + left/right side panels compose horizontally.
	transcript := m.renderTranscriptWithSides()

	// Bottom-position panels + the slash drawer share the "above input"
	// strip. The slash drawer is still bespoke (text-triggered not key-
	// toggled); bottom panels stack below it.
	var bottomSections []string
	if m.slashOpen {
		bottomSections = append(bottomSections, m.renderSlashDrawer())
	}
	if bp := m.renderStackedPanels(posBottom); bp != "" {
		bottomSections = append(bottomSections, bp)
	}

	thinTop := m.styles.thinRule.Render(strings.Repeat("─", max(0, m.width)))
	inputLine := m.renderInput()
	thinBot := m.styles.thinRule.Render(strings.Repeat("─", max(0, m.width)))
	status := m.renderStatus()
	progressBars := m.renderProgressBars(m.width)
	toolToasts := m.renderToolToasts(m.width)

	sections := []string{header}
	if topRegion != "" {
		sections = append(sections, topRegion)
	}
	sections = append(sections, transcript)
	sections = append(sections, bottomSections...)
	if toolToasts != "" {
		sections = append(sections, toolToasts)
	}
	if progressBars != "" {
		sections = append(sections, progressBars)
	}
	sections = append(sections, thinTop, inputLine, thinBot, status)

	view := lipgloss.JoinVertical(lipgloss.Left, sections...)

	// Choice overlay — centered modal, exclusive keyboard focus. Return early
	// so no other overlay competes for attention.
	if m.activeChoice != nil {
		return m.renderChoiceOverlay(view)
	}

	// Command palette overlay — full centered modal, rendered on top of
	// everything else. When open it takes full-screen focus; return early
	// so the toast overlay doesn't compete for attention.
	if m.palette.open {
		return m.renderPalette(view)
	}

	// Toast overlay — placed at bottom-right, above the status bar. Purely
	// visual; the user can keep typing and the conversation continues.
	if len(m.toasts) > 0 {
		t := m.toasts[len(m.toasts)-1] // most recent toast
		var ts lipgloss.Style
		switch t.kind {
		case toastSuccess:
			ts = m.styles.toastSuccess
		case toastWarn:
			ts = m.styles.toastWarn
		case toastError:
			ts = m.styles.toastError
		default:
			ts = m.styles.toastDefault
		}
		rendered := ts.Render(t.message)
		// Reserve the full bottom frame: thinTop(1) + input(N) + thinBot(1) +
		// status(1) = 3 + inputH. The input grows when the user wraps onto
		// new rows; without tracking that, a multi-row input would let the
		// toast land on the input area and the user would see a broken
		// border around their cursor.
		inputH := m.input.Height()
		if inputH < 1 {
			inputH = 1
		}
		bottomReserve := 3 + inputH
		view = overlayBottomRight(view, rendered, m.width, m.height, bottomReserve)
	}

	return view
}

// overlayBottomRight places the overlay string in the bottom-right corner of
// the background string, leaving bottomReserve rows untouched at the very
// bottom. Use bottomReserve to keep the overlay clear of the input frame /
// status bar — pass 0 to anchor flush with the bottom edge.
//
// It splits both into lines, then replaces the right-hand portion of the
// reserved-region's preceding lines with the overlay lines. Lines that are
// too short are padded with spaces before splicing. The result is always
// the same line count as background — no extra lines are added.
func overlayBottomRight(background, overlay string, w, _ int, bottomReserve int) string {
	bgLines := strings.Split(background, "\n")
	ovLines := strings.Split(overlay, "\n")

	ovH := len(ovLines)
	ovW := 0
	for _, l := range ovLines {
		if cw := lipgloss.Width(l); cw > ovW {
			ovW = cw
		}
	}
	if ovW > w {
		ovW = w
	}

	if bottomReserve < 0 {
		bottomReserve = 0
	}
	startRow := len(bgLines) - bottomReserve - ovH
	if startRow < 0 {
		startRow = 0
	}

	result := make([]string, len(bgLines))
	copy(result, bgLines)

	for i, ovLine := range ovLines {
		row := startRow + i
		if row >= len(result) {
			break
		}
		bg := result[row]
		bgW := lipgloss.Width(bg)
		// Pad background line to full terminal width if short.
		if bgW < w {
			bg += strings.Repeat(" ", w-bgW)
		}
		// Splice in the overlay at the right edge.
		colStart := w - ovW
		if colStart < 0 {
			colStart = 0
		}
		// Use rune-aware truncation for the left portion.
		left := truncateToWidth(bg, colStart)
		result[row] = left + ovLine
	}

	return strings.Join(result, "\n")
}

// truncateToWidth returns a prefix of s that is exactly targetWidth display
// cells wide, padding with spaces if s is shorter. ANSI escape sequences in
// s are preserved without being counted toward the visible width — critical
// for splicing overlays into already-styled background lines, where naive
// rune-counting would consume column budget on escape bytes and shift the
// overlay one or more cells left, producing visibly broken borders.
func truncateToWidth(s string, targetWidth int) string {
	if targetWidth <= 0 {
		return ""
	}
	cut := reflowtrunc.String(s, uint(targetWidth))
	w := lipgloss.Width(cut)
	if w < targetWidth {
		cut += strings.Repeat(" ", targetWidth-w)
	}
	return cut
}

// renderWithFullscreen replaces the transcript region with a fullscreen
// panel's view. Header and status-bar stay visible.
func (m model) renderWithFullscreen(spec *panelSpec) string {
	header := m.renderHeader()
	status := m.renderStatus()
	// Reserve: header(2: title + heavy rule) + input-frame(5: thin-top + 3 input rows
	// + thin-bot) + status(1) = 8 total.
	reserved := 2 + 5 + 1
	contentHeight := max(1, m.height-reserved)
	content := spec.View(m.width, contentHeight, &m)
	thinTop := m.styles.thinRule.Render(strings.Repeat("─", max(0, m.width)))
	inputLine := m.renderInput()
	thinBot := m.styles.thinRule.Render(strings.Repeat("─", max(0, m.width)))
	return lipgloss.JoinVertical(lipgloss.Left,
		header, content, thinTop, inputLine, thinBot, status)
}

// renderStackedPanels returns the rendered panels at a given position
// stacked vertically, or "" if none. Used for top and bottom regions.
func (m model) renderStackedPanels(pos panelPosition) string {
	specs := m.panelsAt(pos)
	if len(specs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(specs))
	for _, s := range specs {
		h := s.PreferredHeight
		if h == 0 {
			h = 8
		}
		parts = append(parts, s.View(m.width, h, &m))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderTranscriptWithSides composes the transcript with any left/right
// panels. If no side panels are open, it's just the transcript viewport.
func (m model) renderTranscriptWithSides() string {
	left := m.panelsAt(posLeft)
	right := m.panelsAt(posRight)

	if len(left) == 0 && len(right) == 0 {
		return m.viewport.View()
	}

	// Allocate widths. Side panels take their PreferredWidth (default 32);
	// transcript takes the remainder.
	leftW := 0
	if len(left) > 0 {
		leftW = left[0].PreferredWidth
		if left[0].DynamicWidth != nil {
			leftW = left[0].DynamicWidth(&m)
		}
		if leftW == 0 {
			leftW = 32
		}
	}
	rightW := 0
	if len(right) > 0 {
		rightW = right[0].PreferredWidth
		if right[0].DynamicWidth != nil {
			rightW = right[0].DynamicWidth(&m)
		}
		if rightW == 0 {
			rightW = 32
		}
	}
	h := m.viewport.Height

	parts := []string{}
	if len(left) > 0 {
		leftContent := left[0].View(leftW, h, &m)
		// Vertical divider between panel and transcript.
		divider := strings.Repeat(m.styles.thinRule.Render("│")+"\n", max(1, h))
		parts = append(parts, leftContent, divider)
	}
	parts = append(parts, m.viewport.View())
	if len(right) > 0 {
		rightContent := right[0].View(rightW, h, &m)
		divider := strings.Repeat(m.styles.thinRule.Render("│")+"\n", max(1, h))
		parts = append(parts, divider, rightContent)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

// renderSlashDrawer draws the filtered command list above the input. Each
// row shows the slash-prefixed name, any hotkey, and the description.
// Selected row is highlighted.
func (m model) renderSlashDrawer() string {
	var b strings.Builder
	// Light rule above the drawer so it feels attached to the input frame
	// rather than hanging off the transcript.
	b.WriteString(m.styles.thinRule.Render(strings.Repeat("─", max(0, m.width))))
	b.WriteByte('\n')

	if len(m.slashMatches) == 0 {
		b.WriteString(m.styles.statusHint.Render("  no matching commands"))
		b.WriteByte('\n')
		b.WriteString(m.styles.statusHint.Render("  esc cancel"))
		return b.String()
	}

	maxRows := drawerHeight(&m) - 2 // minus rule + footer
	if maxRows < 1 {
		maxRows = 1
	}
	if maxRows > len(m.slashMatches) {
		maxRows = len(m.slashMatches)
	}

	// Window around the selected index so selection is always visible.
	start := 0
	if m.slashIndex >= maxRows {
		start = m.slashIndex - maxRows + 1
	}
	end := start + maxRows
	if end > len(m.slashMatches) {
		end = len(m.slashMatches)
	}

	for i := start; i < end; i++ {
		c := m.slashMatches[i]
		row := fmt.Sprintf("  /%-10s  %s", c.Name, c.Description)
		if c.Hotkey != "" {
			row = fmt.Sprintf("  /%-10s  [%s]  %s", c.Name, c.Hotkey, c.Description)
		}
		if i == m.slashIndex {
			// Selected row — invert with surface-hi background.
			b.WriteString(m.styles.drawerSel.Render(padRight(row, m.width)))
		} else {
			b.WriteString(m.styles.body.Render(row))
		}
		b.WriteByte('\n')
	}
	b.WriteString(m.styles.statusHint.Render("  ↑↓/jk navigate · enter run · esc cancel"))
	return b.String()
}

// renderHelp moved to panel_help.go as a fullscreen panel. Kept as a
// reference point so the help overlay still exists even if Update's
// routing changes — but this function is no longer called.

func prependSlash(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = "/" + s
	}
	return out
}

// padRight space-pads s to width characters for full-row highlighting.
func padRight(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

func (m model) renderHeader() string {
	sessionLabel := m.session.Name
	if sessionLabel == "" {
		sessionLabel = fmt.Sprintf("session %d", m.session.ID)
	}
	// LEFT — identity zone: name + session label + (only when non-baseline)
	// the active mode + active model. Soft-white bold for the name, dim
	// moonlight for meta, role accent for the mode tag, dim teal for
	// the model. Surfacing the model here directly answers "is the slow
	// turn the model's fault?" without leaving the transcript.
	parts := []string{m.styles.headerName.Render(m.aiName)}
	parts = append(parts, m.styles.headerMeta.Render("  ·  "+sessionLabel))
	if !isBaselineRole(m.session.Role) {
		parts = append(parts,
			m.styles.headerMeta.Render("  ·  mode: "),
			m.styles.statusMode.Render(m.session.Role))
	}
	if m.agent != nil {
		if mn := m.agent.Model(); mn != "" {
			parts = append(parts,
				m.styles.headerMeta.Render("  ·  "),
				lipgloss.NewStyle().Foreground(colTool).Render(mn))
		}
	}
	// Discipline badge — only shown when not full (full is the default; no badge
	// keeps the header clean for normal usage). Orange for readonly (limited),
	// neutral dim for scoped.
	if dm := m.session.DisciplineMode; dm > 0 && dm < 3 {
		var badgeStyle lipgloss.Style
		var badgeText string
		switch dm {
		case 1: // readonly
			badgeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#D97706")).Bold(true)
			badgeText = "[mode: readonly]"
		case 2: // scoped
			badgeStyle = lipgloss.NewStyle().Foreground(colTextMuted)
			badgeText = "[mode: scoped]"
		}
		parts = append(parts,
			m.styles.headerMeta.Render("  ·  "),
			badgeStyle.Render(badgeText))
	}
	left := strings.Join(parts, "")

	// RIGHT — stats zone: cumulative DB-backed counters (memories, jobs,
	// last dream, context size). Moved up from the status bar so the
	// bottom row stays focused on session help / live activity. All muted
	// so they read as ambient state, not as primary content.
	right := renderHeaderStats(m)

	// Compose: pad the gap so right zone hugs the right edge.
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW
	if gap < 2 {
		gap = 2
	}
	title := left + strings.Repeat(" ", gap) + right

	// Rule on its own line, heavy horizontal (━), faintly moonlight-tinted.
	// Separates the header from the conversation like a panel divider.
	rule := m.styles.headerRule.Render(strings.Repeat("━", max(0, m.width)))
	return title + "\n" + rule
}

// renderHeaderStats builds the right-side stats string for the header:
// memories count, in-flight jobs, last dream age, and context size. Empty
// fields are skipped so the line stays tidy on small DBs / fresh installs.
func renderHeaderStats(m model) string {
	var parts []string

	memNumStyle, memLblStyle := m.styles.statusMemNum, m.styles.statusMemLbl
	if m.flashing(m.memFlashAt) {
		memNumStyle = memNumStyle.Bold(true).Underline(true)
		memLblStyle = memLblStyle.Bold(true)
	}
	parts = append(parts,
		memNumStyle.Render(fmt.Sprintf("%d", m.memoryCount))+memLblStyle.Render(" mem"))

	if m.jobCount > 0 {
		parts = append(parts,
			m.styles.statusThrNum.Render(fmt.Sprintf("%d", m.jobCount))+
				m.styles.statusThrLbl.Render(" jobs"))
	}
	if m.dreamAgeStr != "" {
		parts = append(parts, m.styles.statusHint.Render(m.dreamAgeStr))
	}
	if m.contextLen > 0 {
		k := (m.contextLen + 512) / 1024
		var ctxStr string
		if m.sessionTokens > 0 {
			sk := float64(m.sessionTokens) / 1000
			ctxStr = fmt.Sprintf("%.1fk / %dk ctx", sk, k)
		} else {
			ctxStr = fmt.Sprintf("%dk ctx", k)
		}
		parts = append(parts, m.styles.statusHint.Render(ctxStr))
	}

	sep := m.styles.statusHint.Render("  ·  ")
	return strings.Join(parts, sep)
}

func (m model) renderInput() string {
	// Context-aware glyph. Same slot, same width — the glyph just
	// changes its shape and color to reflect what pressing Enter will
	// actually do right now. Minimal feedback, strong story.
	//
	//   streaming        → ● in Selene-blue (presence, matches status bar)
	//   slash drawer     → / in the hint dim
	//   ! shell prefix   → ! in warm parchment (user's voice)
	//   @ file prefix    → @ in warm parchment
	//   idle             → ▸ in role accent (current behaviour)
	val := m.input.Value()
	var glyph string
	switch {
	case m.activity.State() != activityIdle:
		glyph = m.styles.glyphStreaming.Render("● ")
	case m.slashOpen:
		glyph = m.styles.glyphSlash.Render("/ ")
	case strings.HasPrefix(val, "!"):
		glyph = m.styles.glyphShell.Render("! ")
	case strings.HasPrefix(val, "@"):
		glyph = m.styles.glyphShell.Render("@ ")
	default:
		glyph = m.styles.inputGlyph.Render("▸ ")
	}
	return glyph + m.input.View()
}

func (m model) renderStatus() string {
	// Status bar focuses on session help + live activity now that cumulative
	// stats (memories, jobs, dream age, context size) live in the header.
	// LEFT — running activity (mode tag, live thread spinner). RIGHT —
	// help hint. Activity indicator stays on the left so the breathing
	// animation never collides with the static help text.

	var leftB strings.Builder
	if tok := m.renderActivity(); tok != "" {
		leftB.WriteString(tok)
		// Per-run cost: tool count + elapsed minutes. Visible the whole
		// time the agent is working so you can see "this turn has done 14
		// things over 8 minutes" instead of just "thinking" forever.
		if cost := renderTurnCost(m); cost != "" {
			leftB.WriteString(m.styles.statusHint.Render("  ·  "))
			leftB.WriteString(cost)
		}
		// Live token meter — chars/4 approximation updated on every
		// streaming chunk. Shows current-turn tokens; when the context
		// window is known, appends " / Nk" so pressure is visible at a
		// glance. Color-coded: default < 50%, yellow 50-80%, red > 80%.
		if meter := renderTokenMeter(m); meter != "" {
			leftB.WriteString(m.styles.statusHint.Render("  ·  "))
			leftB.WriteString(meter)
		}
		leftB.WriteString(m.styles.statusHint.Render("  ·  "))
	}
	if !isBaselineRole(m.session.Role) {
		leftB.WriteString(m.styles.statusMode.Render("mode: " + m.session.Role))
		leftB.WriteString(m.styles.statusHint.Render("  ·  "))
	}
	if m.threadCount > 0 {
		thrNumStyle, thrLblStyle := m.styles.statusThrNum, m.styles.statusThrLbl
		if m.flashing(m.threadFlashAt) {
			thrNumStyle = thrNumStyle.Bold(true).Underline(true)
			thrLblStyle = thrLblStyle.Bold(true)
		}
		leftB.WriteString(thrLblStyle.Render(threadSpinnerFrame(m.tickCounter) + " "))
		leftB.WriteString(thrNumStyle.Render(fmt.Sprintf("%d thread", m.threadCount)))
	}
	// Per-step heartbeat: shows the current execution phase + elapsed time.
	// Renders as "⟳ consider · 1.2s" so the user can see what cairo is
	// doing rather than wondering if it's hung. Mutually exclusive with the
	// stall banner — heartbeat means work is in progress.
	if m.currentStep != nil {
		if leftB.Len() > 0 {
			leftB.WriteString(m.styles.statusHint.Render("  ·  "))
		}
		leftB.WriteString(m.renderHeartbeat())
	}

	left := strings.TrimRight(leftB.String(), " ·")

	// Stall banner: agent stopped mid-intent (forward-looking text, no tool
	// calls). Overrides normal idle display — amber background, full-width
	// banner so it's impossible to miss. Clears when the user sends any input.
	if m.stalledMidIntent && m.activity.State() == activityIdle {
		banner := lipgloss.NewStyle().
			Background(lipgloss.Color("#7c4a00")).
			Foreground(lipgloss.Color("#ffd080")).
			Bold(true).
			Width(m.width).
			Render("⚠  agent stopped while indicating more work — type \"continue\" and press Enter")
		return banner
	}

	// RIGHT — context-sensitive help hint.
	var hintText string
	if m.activity.State() != activityIdle {
		hintText = "^c cancel"
	} else {
		hintText = "?  help  ·  /  commands  ·  ^k  palette  ·  ^q quit"
	}
	right := m.styles.statusHint.Render(hintText)

	// Compose: pad the gap so right hugs the right edge.
	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)
	gap := m.width - leftW - rightW
	if gap < 2 {
		gap = 2
	}
	line := left + strings.Repeat(" ", gap) + right

	// Apply a per-state background tint to the whole status bar so the
	// active mode is legible at a glance without changing the text colors.
	bg := activityBg(
		m.activity.State(),
		m.activity.Awaiting(),
		m.activity.CameFromTool(),
		m.activity.SecondsSinceEvent(),
		m.activity.Stale(),
	)
	if bg != "" {
		line = lipgloss.NewStyle().Background(bg).Width(m.width).Render(line)
	}
	return line
}

// renderLastDream reads last_dream_at from config and returns a short
// human-readable "dream Xh ago" / "dream Xm ago" string, or "" if unset.
// Called from refreshCounts (tick handler) to populate m.dreamAgeStr; keep
// this out of View() so the render path stays pure (no DB I/O).
func (m model) renderLastDream() string {
	raw, err := m.db.Config.Get("last_dream_at")
	if err != nil || raw == "" {
		return ""
	}
	var ts int64
	if _, err := fmt.Sscan(raw, &ts); err != nil || ts == 0 {
		return ""
	}
	ago := time.Since(time.Unix(ts, 0))
	switch {
	case ago < time.Minute:
		return "dream just now"
	case ago < time.Hour:
		return fmt.Sprintf("dream %dm ago", int(ago.Minutes()))
	default:
		return fmt.Sprintf("dream %dh ago", int(ago.Hours()))
	}
}

// flashing reports whether the given stamp is within the flash window.
// Used to brighten stats labels for ~flashFor after their tool family fires.
func (m model) flashing(stamp time.Time) bool {
	if stamp.IsZero() {
		return false
	}
	return time.Since(stamp) < flashFor
}

// renderHeartbeat returns the per-step heartbeat indicator for the status bar.
// Format: "⟳ consider · 1.2s" / "⟳ tool: bash · 0.3s" / "⟳ llm · 4.7s".
// Returns "" when no step is active.
func (m model) renderHeartbeat() string {
	s := m.currentStep
	if s == nil {
		return ""
	}
	elapsed := time.Since(s.StartedAt)
	label := s.Name
	if s.Detail != "" {
		label = s.Name + ": " + s.Detail
	}
	secs := elapsed.Seconds()
	var elapsedStr string
	if secs < 10 {
		elapsedStr = fmt.Sprintf("%.1fs", secs)
	} else {
		elapsedStr = fmt.Sprintf("%ds", int(secs))
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#5fa8d3")).
		Render("⟳ " + label + " · " + elapsedStr)
}

// renderTurnCost returns "14 tools · 8m" for the in-flight run, or "" if
// nothing's running. Both fields elide when zero so a fresh streaming
// turn doesn't read "0 tools · 0m" — by the time you'd want the readout
// at least one tool has fired or the clock has ticked past a minute.
func renderTurnCost(m model) string {
	tools := m.activity.ToolCount()
	elapsed := m.activity.TurnElapsed()
	if tools == 0 && elapsed < time.Minute {
		return ""
	}
	dim := m.styles.statusHint
	num := lipgloss.NewStyle().Foreground(colTextMuted).Bold(true)

	var parts []string
	if tools > 0 {
		label := " tools"
		if tools == 1 {
			label = " tool"
		}
		parts = append(parts, num.Render(fmt.Sprintf("%d", tools))+dim.Render(label))
	}
	if elapsed >= time.Minute {
		parts = append(parts, dim.Render(shortDuration(elapsed)))
	}
	return strings.Join(parts, dim.Render(" · "))
}

// renderTokenMeter returns a live "tok: N" or "tok: N / Wk" string for the
// status bar, updated on every streaming chunk. The token count is the current
// turn's chars/4 estimate. Color-coded against the configured context window:
//   - < 50% of window: default dim text
//   - 50–80%: amber/yellow
//   - > 80%: red
//
// Returns "" when the agent is idle and streamingChars is zero — no clutter
// when Selene is not speaking.
func renderTokenMeter(m model) string {
	if m.streamingChars == 0 {
		return ""
	}
	currentToks := m.streamingChars / 4
	label := "tok: " + formatTokens(currentToks)

	var col lipgloss.Color
	if m.contextLen > 0 {
		windowToks := m.contextLen
		ratio := float64(currentToks) / float64(windowToks)
		switch {
		case ratio >= 0.80:
			col = colErr
		case ratio >= 0.50:
			col = colWarn
		default:
			col = colTextDim
		}
		windowK := (windowToks + 512) / 1024
		label += " / " + fmt.Sprintf("%dk", windowK)
	} else {
		col = colTextDim
	}
	return lipgloss.NewStyle().Foreground(col).Render(label)
}

// activityBg returns the background color for the status bar given the current
// activity state. Returns "" (no background) when idle.
func activityBg(state activityState, awaiting, cameFromTool bool, sinceEvent time.Duration, stale bool) lipgloss.Color {
	switch state {
	case activityStreaming:
		return lipgloss.Color("#0d2a3d") // soft blue — Selene is talking
	case activityTool:
		return lipgloss.Color("#1e1040") // purple — tool in flight
	case activityThinking:
		switch {
		case awaiting:
			return lipgloss.Color("#0d2a3d") // soft blue — waiting for model
		case cameFromTool && sinceEvent > time.Second:
			return lipgloss.Color("#1e1040") // purple — processing tool result
		case stale:
			return lipgloss.Color("#3d1a00") // orange/red — silent/stuck
		default:
			return lipgloss.Color("#2a1e00") // amber — thinking
		}
	}
	return ""
}

// renderActivity returns the state-aware activity token for the status bar,
// or "" when idle. One glyph + one label, always family-tinted so color
// continuity carries the story from the transcript up to the status line.
//
// Every active state now shows elapsed time — a 60s prefill no longer
// looks identical to a 2s thought. And when the state says "thinking"
// but no token has arrived recently, we flip to ⋯ awaiting model so the
// user can tell the difference between actual reasoning and Ollama
// loading weights / processing a long prompt.
func (m model) renderActivity() string {
	dur := m.activity.Duration()
	switch m.activity.State() {
	case activityStreaming:
		// Selene is actually talking — her voice color, bullet breathes.
		var bulletStyle lipgloss.Style
		if m.tickCounter%2 == 1 {
			bulletStyle = m.styles.activityStreamDim
		} else {
			bulletStyle = m.styles.activityStreaming
		}
		head := bulletStyle.Render("● ") + m.styles.activityStreamName.Render(m.aiName)
		if dur >= time.Second {
			head += m.styles.activityStreamName.Faint(true).Render("  " + shortDuration(dur))
		}
		return head
	case activityThinking:
		// "thinking" is actually three states masquerading as one. Pick
		// the right label based on what we know about the silence:
		//   - never seen any activity yet → cold start (Ollama loading)
		//   - just came from a tool → re-prefilling the new message stack
		//   - otherwise → genuine reasoning
		//
		// Then layer on a stale warning if we've been silent past the
		// threshold — distinguishes "slow but working" from "may be hung".
		var glyph, word string
		switch {
		case m.activity.Awaiting():
			glyph, word = "⋯", "awaiting model"
		case m.activity.CameFromTool() && m.activity.SecondsSinceEvent() > time.Second:
			glyph, word = "⤓", "processing tool result"
		default:
			glyph, word = "❋", "thinking"
		}
		label := m.styles.activityThinking.Render(glyph + " " + word)
		if dur >= time.Second {
			label += m.styles.activityThinking.Faint(true).Render("  " + shortDuration(dur))
		}
		// Liveness signal: if thinking events are flowing, show the
		// count. Climbing = model is alive even when no content has
		// streamed yet.
		if n := m.activity.ThinkEvents(); n > 0 {
			label += m.styles.activityThinking.Faint(true).
				Render(fmt.Sprintf("  · %d think", n))
		}
		// Stuck warning: long silence overrides the friendly framing
		// with a yellow ⚠ + the duration of silence + a cancel hint.
		if m.activity.Stale() {
			warn := lipgloss.NewStyle().Foreground(colWarn).Bold(true)
			label += "  " + warn.Render(fmt.Sprintf("⚠ silent %s — ^c cancels",
				shortDuration(m.activity.SecondsSinceEvent())))
		}
		return label
	case activityTool:
		// Family color + family icon + tool name + elapsed.
		fam := m.activity.ToolFamily()
		c := familyColor(fam)
		head := lipgloss.NewStyle().Foreground(c).Bold(true).
			Render(fmt.Sprintf("%s %s", familyIcon(fam), m.activity.ToolName()))
		if dur >= time.Second {
			tail := lipgloss.NewStyle().Foreground(c).Faint(true).
				Render(" " + shortDuration(dur))
			return head + tail
		}
		return head
	}
	return ""
}

// threadSpinnerFrame returns one frame of a four-step rotation synced to
// the tick counter. The forms are all diamond-family unicode so the
// spinner aligns visually with the idle ◇ form.
func threadSpinnerFrame(tick int) string {
	frames := []string{"◇", "◈", "◆", "◈"}
	return frames[tick%len(frames)]
}

// renderChoiceOverlay draws a centered bordered box over the current view
// showing the choice title, navigable options, and a key hint footer.
// The background view is passed in so the overlay composites on top of it.
func (m model) renderChoiceOverlay(background string) string {
	c := m.activeChoice
	if c == nil {
		return background
	}

	var b strings.Builder
	b.WriteString(c.title + "\n\n")
	for i, opt := range c.options {
		cursor := "  "
		if i == c.selected {
			cursor = "▸ "
		}
		b.WriteString(cursor + opt + "\n")
	}
	b.WriteString("\n↑↓ navigate · enter select · esc cancel")

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#7C3AED")).
		Padding(1, 3).
		Width(50).
		Render(b.String())

	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}
