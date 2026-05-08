package tui

// panel_state.go — bottom slide-in showing Cairo's seven persistent state
// variables with today's values, drift indicators, sparklines for the three
// relational vars, and last-dream info.
//
// Hotkey: ctrl+s (state). ctrl+m was the plan's original intent but it
// collides with Enter in most terminals (same issue as ctrl+i → Tab).
//
// Refresh strategy: DB is queried on panel open and on each render tick when
// the panel is visible. State changes are small and frequent (±0.001 per
// tool call) so live subscription isn't worth the complexity — polling is fine.

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/store/identity"
)

const panelStateID panelID = "state"

// statePanel holds all render state for the state panel. Kept on the main
// model (as m.statePanel) so panel hooks can access without passing extra args.
type statePanelState struct {
	today     *identity.State   // today's row; nil when DB is empty
	history   []*identity.State // last 7 rows descending (index 0 = today)
	loadedAt  time.Time         // time of last DB read
	loadError string            // non-empty when last load failed
}

// sparkChars is the set of block elements used for sparkline rendering.
// Index 0 = lowest (≈0.0), index 7 = highest (≈1.0).
var sparkChars = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// colStateAccent is the color used for the state panel title and key labels.
// Moonlight-blue, matching Selene's voice — these numbers are hers.
var colStateAccent = lipgloss.Color("#a0c8e0")

func init() {
	registerPanel(&panelSpec{
		ID:          panelStateID,
		Position:    posBottom,
		Accent:      colStateAccent,
		Title:       "state",
		Description: "Live view of Cairo's seven state variables with drift indicators and 7-day sparklines. ctrl+s to toggle.",
		ToggleKey:   "ctrl+s",
		ShowInHelp:  true,
		// Height: 7 var rows + blank + summary line + dream line + blank + sparkline header + 3 spark rows + hint = ~16
		PreferredHeight: 16,
		OnOpen:          stateOpen,
		OnClose:         stateClose,
		Update:          stateUpdate,
		View:            stateView,
	})
}

func stateOpen(m *model) tea.Cmd {
	stateLoad(m)
	return nil
}

func stateClose(m *model) {
	m.statePanel.today = nil
	m.statePanel.history = nil
	m.statePanel.loadError = ""
}

// stateLoad queries the DB for today's row and the last 7 days. Called on
// panel open and on each tick while the panel is visible.
func stateLoad(m *model) {
	today, err := m.db.State.Today()
	if err != nil {
		m.statePanel.loadError = fmt.Sprintf("state load error: %v", err)
		return
	}
	history, err := m.db.State.LastN(7)
	if err != nil {
		m.statePanel.loadError = fmt.Sprintf("history load error: %v", err)
		return
	}
	m.statePanel.today = today
	m.statePanel.history = history
	m.statePanel.loadedAt = time.Now()
	m.statePanel.loadError = ""
}

func stateUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	switch msg.(type) {
	case tickMsg:
		// Refresh on every tick (300ms) while visible.
		stateLoad(m)
		return false, nil // don't consume the tick — other handlers need it too
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch key.String() {
	case "esc":
		m.closePanel(panelStateID)
		return true, nil
	case "ctrl+r":
		stateLoad(m)
		return true, nil
	}
	return true, nil
}

// stateView renders the full panel content. width and height are the available
// pixel-columns and line-rows passed by the panel layout engine.
func stateView(width, height int, m *model) string {
	accent := lipgloss.NewStyle().Foreground(colStateAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	body := lipgloss.NewStyle().Foreground(colText)
	okStyle := lipgloss.NewStyle().Foreground(colOK)
	errStyle := lipgloss.NewStyle().Foreground(colErr)
	warnStyle := lipgloss.NewStyle().Foreground(colWarn)

	var b strings.Builder

	// Title row.
	todayLabel := ""
	if m.statePanel.today != nil {
		todayLabel = " (today: " + m.statePanel.today.Date + ")"
	}
	b.WriteString(accent.Render("state" + todayLabel))
	b.WriteByte('\n')
	b.WriteString(lipgloss.NewStyle().Foreground(colBorderThin).Render(
		strings.Repeat("─", max(0, width)),
	))
	b.WriteByte('\n')

	if m.statePanel.loadError != "" {
		b.WriteString(errStyle.Render("  " + m.statePanel.loadError))
		b.WriteByte('\n')
		b.WriteString(dim.Render("  r to retry · esc close"))
		return b.String()
	}

	if m.statePanel.today == nil {
		b.WriteString(dim.Render("  no state data yet — state is written on first tool call"))
		b.WriteByte('\n')
		b.WriteString(dim.Render("  esc close"))
		return b.String()
	}

	today := m.statePanel.today

	// yesterday is the second row in history (index 1) if it exists.
	var yesterday *identity.State
	if len(m.statePanel.history) >= 2 {
		yesterday = m.statePanel.history[1]
	}

	// stateVar describes a single row in the display table.
	type stateVar struct {
		name  string
		value float64
	}

	vars := []stateVar{
		{"confidence", today.Confidence},
		{"trust_in_user", today.TrustInUser},
		{"warmth", today.Warmth},
		{"frustration_baseline", today.FrustrationBaseline},
		{"sense_of_agency", today.SenseOfAgency},
		{"attunement", today.Attunement},
		{"groundedness", today.Groundedness},
	}

	// yesterdayValue returns yesterday's value for the named var, or nil if
	// no yesterday row exists.
	yesterdayValue := func(name string) *float64 {
		if yesterday == nil {
			return nil
		}
		var v float64
		switch name {
		case "confidence":
			v = yesterday.Confidence
		case "trust_in_user":
			v = yesterday.TrustInUser
		case "warmth":
			v = yesterday.Warmth
		case "frustration_baseline":
			v = yesterday.FrustrationBaseline
		case "sense_of_agency":
			v = yesterday.SenseOfAgency
		case "attunement":
			v = yesterday.Attunement
		case "groundedness":
			v = yesterday.Groundedness
		default:
			return nil
		}
		return &v
	}

	// Render the bar: 20 chars, filled with █ for value, ░ for remainder.
	const barLen = 20
	renderBar := func(val float64) string {
		filled := int(val * float64(barLen))
		if filled > barLen {
			filled = barLen
		}
		if filled < 0 {
			filled = 0
		}
		return strings.Repeat("█", filled) + strings.Repeat("░", barLen-filled)
	}

	// Render drift arrow and delta string.
	renderDrift := func(delta *float64) string {
		if delta == nil {
			return "·      "
		}
		d := *delta
		var arrow string
		switch {
		case d > 0.001:
			arrow = "↑"
		case d < -0.001:
			arrow = "↓"
		default:
			arrow = "·"
		}
		landmark := ""
		if d >= 0.05 || d <= -0.05 {
			landmark = " *"
		}
		if d >= 0 {
			return fmt.Sprintf("%s +%.3f%s", arrow, d, landmark)
		}
		return fmt.Sprintf("%s %.3f%s", arrow, d, landmark)
	}

	// Column widths: name (20), gap (2), value (4), gap (2), bar (20), gap (2), drift (12)
	// Total ≈ 62. Fits within typical 80-col terminal.
	const nameWidth = 20

	for _, v := range vars {
		yval := yesterdayValue(v.name)
		var delta *float64
		if yval != nil {
			d := v.value - *yval
			delta = &d
		}

		namePadded := padRight("  "+v.name, nameWidth+2)
		valStr := fmt.Sprintf("%.2f", v.value)
		bar := renderBar(v.value)
		drift := renderDrift(delta)

		// Color the drift based on direction. Landmark rows get warn color.
		var driftStyled string
		isLandmark := delta != nil && (*delta >= 0.05 || *delta <= -0.05)
		switch {
		case isLandmark:
			driftStyled = warnStyle.Render(drift)
		case delta != nil && *delta > 0.001:
			driftStyled = okStyle.Render(drift)
		case delta != nil && *delta < -0.001:
			driftStyled = errStyle.Render(drift)
		default:
			driftStyled = dim.Render(drift)
		}

		row := body.Render(namePadded) +
			body.Bold(true).Render(valStr) +
			"  " +
			dim.Render(bar) +
			"  " +
			driftStyled
		b.WriteString(row)
		b.WriteByte('\n')
	}

	// Blank line.
	b.WriteByte('\n')

	// Summary: update count + last dream date.
	updateStr := fmt.Sprintf("  updates today: %d", today.UpdateCount)
	b.WriteString(body.Render(updateStr))

	// Last dream info.
	lastDream := stateLastDream(m.statePanel.history)
	if lastDream != nil {
		dreamDateStr := "   last dream: " + lastDream.Date
		b.WriteString(dim.Render(dreamDateStr))
		dreamDelta := stateDreamDelta(lastDream)
		if dreamDelta != "" {
			b.WriteByte('\n')
			b.WriteString(dim.Render("               (post-dream Δ: " + dreamDelta + ")"))
		}
	} else {
		b.WriteString(dim.Render("   last dream: none"))
	}
	b.WriteByte('\n')

	// Blank line before sparklines.
	b.WriteByte('\n')

	// Sparklines for the three relational vars (warmth, trust_in_user, attunement).
	// We have history[0..N-1] descending by date; sparkline wants oldest→newest, so reverse.
	b.WriteString(dim.Render("  last 7 days"))
	b.WriteByte('\n')

	sparkVars := []struct {
		name  string
		label string
		get   func(*identity.State) float64
	}{
		{"warmth", "warmth     ", func(s *identity.State) float64 { return s.Warmth }},
		{"trust_in_user", "trust      ", func(s *identity.State) float64 { return s.TrustInUser }},
		{"attunement", "attunement ", func(s *identity.State) float64 { return s.Attunement }},
	}

	for _, sv := range sparkVars {
		spark := stateSparkline(m.statePanel.history, sv.get)
		trend := stateTrend(m.statePanel.history, sv.get)
		trendStr := ""
		if trend != "" {
			trendStr = "  (" + trend + ")"
		}
		b.WriteString(dim.Render("  "+sv.label) + accent.Render(spark) + dim.Render(trendStr))
		b.WriteByte('\n')
	}

	// Hint row.
	b.WriteString(dim.Render("  ctrl+r refresh · esc close"))

	return b.String()
}

// stateSparkline builds a sparkline string from the history (descending).
// Returns oldest→newest as a string of spark characters.
func stateSparkline(history []*identity.State, get func(*identity.State) float64) string {
	if len(history) == 0 {
		return "·"
	}
	// Reverse: history[0] is most recent; we want oldest first.
	n := len(history)
	var chars []rune
	for i := n - 1; i >= 0; i-- {
		val := get(history[i])
		idx := int(val*float64(len(sparkChars)-1) + 0.5)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sparkChars) {
			idx = len(sparkChars) - 1
		}
		chars = append(chars, sparkChars[idx])
	}
	return string(chars)
}

// stateTrend returns a short label describing the 7-day direction.
func stateTrend(history []*identity.State, get func(*identity.State) float64) string {
	if len(history) < 2 {
		return ""
	}
	// Compare most recent to oldest in the window.
	newest := get(history[0])
	oldest := get(history[len(history)-1])
	delta := newest - oldest
	switch {
	case delta > 0.05:
		return "consolidating"
	case delta > 0.01:
		return "rising"
	case delta < -0.05:
		return "declining"
	case delta < -0.01:
		return "drifting down"
	default:
		return "stable"
	}
}

// stateLastDream finds the most recent state row that has been dream-processed.
func stateLastDream(history []*identity.State) *identity.State {
	for _, s := range history {
		if s.DreamProcessedAt != nil {
			return s
		}
	}
	return nil
}

// stateDreamDelta returns a compact string showing the most-changed 1-2 vars
// post-dream vs live values for the given row. Empty string if no post_dream values.
func stateDreamDelta(s *identity.State) string {
	if s == nil {
		return ""
	}
	type varDelta struct {
		name  string
		delta float64
	}
	pairs := []struct {
		name    string
		live    float64
		postPtr *float64
	}{
		{"warmth", s.Warmth, s.PostDreamWarmth},
		{"frust", s.FrustrationBaseline, s.PostDreamFrustrationBaseline},
		{"trust", s.TrustInUser, s.PostDreamTrustInUser},
		{"attune", s.Attunement, s.PostDreamAttunement},
		{"conf", s.Confidence, s.PostDreamConfidence},
		{"agency", s.SenseOfAgency, s.PostDreamSenseOfAgency},
		{"ground", s.Groundedness, s.PostDreamGroundedness},
	}

	var deltas []varDelta
	for _, p := range pairs {
		if p.postPtr == nil {
			continue
		}
		d := *p.postPtr - p.live
		if d > 0.005 || d < -0.005 {
			deltas = append(deltas, varDelta{p.name, d})
		}
	}
	if len(deltas) == 0 {
		return ""
	}
	// Sort by absolute delta descending, show top 2.
	for i := 0; i < len(deltas)-1; i++ {
		for j := i + 1; j < len(deltas); j++ {
			ai, aj := deltas[i].delta, deltas[j].delta
			if ai < 0 {
				ai = -ai
			}
			if aj < 0 {
				aj = -aj
			}
			if aj > ai {
				deltas[i], deltas[j] = deltas[j], deltas[i]
			}
		}
	}
	if len(deltas) > 2 {
		deltas = deltas[:2]
	}

	parts := make([]string, len(deltas))
	for i, d := range deltas {
		if d.delta >= 0 {
			parts[i] = fmt.Sprintf("%s +%.2f", d.name, d.delta)
		} else {
			parts[i] = fmt.Sprintf("%s %.2f", d.name, d.delta)
		}
	}
	return strings.Join(parts, ", ")
}
