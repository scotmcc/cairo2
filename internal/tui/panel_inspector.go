package tui

// panel_inspector.go — top slide-in showing current prompt composition and
// token budget. Ctrl+I toggles it. Useful for seeing what's in the context
// window at a glance without having to read the full prompt (Ctrl+P does that).
//
// The panel reads from DB state directly so it stays accurate without coupling
// to BuildSystemPrompt — it shows counts and estimates, not the raw text.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const panelInspectorID panelID = "inspector"

type inspectorState struct {
	viewport viewport.Model
	content  string
	tokens   int
}

func init() {
	registerPanel(&panelSpec{
		ID:          panelInspectorID,
		Position:    posTop,
		Accent:      colTextDim,
		Title:       "inspector",
		Description: "Show current prompt composition and token budget (Ctrl+Y).",
		ToggleKey:   "ctrl+y", // ctrl+i collides with Tab in most terminals

		ShowInHelp:      true,
		PreferredHeight: 18,
		OnOpen:          inspectorOpen,
		OnClose:         inspectorClose,
		Update:          inspectorUpdate,
		View:            inspectorView,
	})
}

func inspectorOpen(m *model) tea.Cmd {
	vp := viewport.New(0, 0)
	m.inspector.viewport = vp
	inspectorResize(m)
	inspectorRefresh(m)
	return nil
}

func inspectorResize(m *model) {
	w := m.width
	if w <= 0 {
		w = 80
	}
	// Panel height = 18. Reserve 3 rows: title + rule + hint. Viewport gets 15.
	h := 18 - 3
	m.inspector.viewport.Width = w
	m.inspector.viewport.Height = h
}

func inspectorClose(m *model) {
	m.inspector.content = ""
	m.inspector.tokens = 0
}

// inspectorRefresh assembles the context breakdown from DB state and loads it
// into the viewport. Called on open and on 'r'.
func inspectorRefresh(m *model) {
	m.inspector.content = buildInspectorContent(m)
	m.inspector.tokens = len(m.inspector.content) / 4
	m.inspector.viewport.SetContent(m.inspector.content)
	m.inspector.viewport.GotoTop()
}

func buildInspectorContent(m *model) string {
	var b strings.Builder

	// Read config values needed for the breakdown.
	modelName, _ := m.db.Config.Get("model")
	if modelName == "" {
		modelName = "(not set)"
	}

	// Context window size: read from config key "model_ctx" (canonical key per config_keys.go).
	contextLenStr, _ := m.db.Config.Get("model_ctx")
	contextLen := 4096
	if n, err := strconv.Atoi(contextLenStr); err == nil && n > 0 {
		contextLen = n
	}

	memoryLimitStr, _ := m.db.Config.Get("memory_limit")
	memoryLimit := 15
	if n, err := strconv.Atoi(memoryLimitStr); err == nil && n > 0 {
		memoryLimit = n
	}

	summaryContextStr, _ := m.db.Config.Get("summary_context")
	summaryContext := 4
	if n, err := strconv.Atoi(summaryContextStr); err == nil && n > 0 {
		summaryContext = n
	}

	// --- header ---
	rule := strings.Repeat("─", 50)
	fmt.Fprintf(&b, "%s\n", rule)
	fmt.Fprintf(&b, "Model:      %-24s Context: %d tokens\n", modelName, contextLen)
	fmt.Fprintf(&b, "%s\n", rule)

	// --- counts ---
	memTotal := m.memoryCount
	fmt.Fprintf(&b, "Memories:   %d stored  ·  injecting top %d\n", memTotal, memoryLimit)

	summaries, _ := m.db.Summaries.LatestForSession(m.session.ID, summaryContext)
	summaryCount := len(summaries)
	fmt.Fprintf(&b, "Summaries:  %d in context\n", summaryCount)

	facts, _ := m.db.Facts.ForSession(m.session.ID)
	factCount := len(facts)
	fmt.Fprintf(&b, "Facts:      %d in session\n", factCount)

	fmt.Fprintf(&b, "%s\n", rule)

	// --- recent summaries (preview) ---
	if summaryCount > 0 {
		fmt.Fprintf(&b, "Recent summaries:\n")
		for _, s := range summaries {
			preview := s.Content
			if len(preview) > 60 {
				preview = preview[:60] + "..."
			}
			fmt.Fprintf(&b, "  [%s] %s\n", s.CreatedAt.Format("Jan 2 15:04"), preview)
		}
		fmt.Fprintf(&b, "%s\n", rule)
	}

	// --- token budget breakdown ---
	// Estimate each section by fetching the actual content length where
	// cheap, otherwise use representative counts × average chars.

	// base prompt: load base prompt parts
	baseParts, _ := m.db.Prompts.Base("")
	baseChars := 0
	for _, p := range baseParts {
		baseChars += len(p.Content)
	}
	// soul
	soul, _ := m.db.Config.Get("soul_prompt")
	baseChars += len(soul)
	// env/date stamp: rough constant
	baseChars += 100

	// memories: fetch recent content strings up to the limit
	memContents, _ := m.db.Memories.RecentContent(memoryLimit)
	memChars := 0
	for _, c := range memContents {
		memChars += len(c) + 4 // "- " prefix + newline
	}

	// summaries
	summaryChars := 0
	for _, s := range summaries {
		summaryChars += len(s.Content) + 30 // timestamp overhead
	}

	baseTok := baseChars / 4
	memTok := memChars / 4
	sumTok := summaryChars / 4
	totalTok := baseTok + memTok + sumTok

	pct := 0
	if contextLen > 0 {
		pct = (totalTok * 100) / contextLen
	}

	innerRule := strings.Repeat("─", 30)
	fmt.Fprintf(&b, "Token budget (estimated):\n")
	fmt.Fprintf(&b, "  base prompt:  ~%d tok\n", baseTok)
	fmt.Fprintf(&b, "  memories:     ~%d tok\n", memTok)
	fmt.Fprintf(&b, "  summaries:    ~%d tok\n", sumTok)
	fmt.Fprintf(&b, "  %s\n", innerRule)
	fmt.Fprintf(&b, "  total input:  ~%d tok  (%d%% of %d)\n", totalTok, pct, contextLen)
	fmt.Fprintf(&b, "%s\n", rule)

	fmt.Fprintf(&b, "Press ctrl+r to refresh · Esc to close\n")

	// Stamp refresh time so user knows when the data was last fetched.
	fmt.Fprintf(&b, "Last refreshed: %s\n", time.Now().Format("15:04:05"))

	return b.String()
}

func inspectorUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		inspectorResize(m)
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		newVp, cmd := m.inspector.viewport.Update(msg)
		m.inspector.viewport = newVp
		return false, cmd
	}
	switch key.String() {
	case "esc":
		m.closePanel(panelInspectorID)
		return true, nil
	case "ctrl+r":
		inspectorRefresh(m)
		return true, nil
	case "up", "down", "pgup", "pgdown", "home", "end":
		newVp, cmd := m.inspector.viewport.Update(msg)
		m.inspector.viewport = newVp
		return true, cmd
	}
	return true, nil
}

func inspectorView(width, _ int, m *model) string {
	accent := lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	title := accent.Render("inspector") +
		dim.Render(fmt.Sprintf("  ·  ~%d tokens  ·  %d chars",
			m.inspector.tokens, len(m.inspector.content)))

	rule := m.styles.thinRule.Render(strings.Repeat("─", max(0, width)))
	body := m.inspector.viewport.View()
	hint := m.styles.statusHint.Render("  ↑↓ PgUp/PgDn scroll · ctrl+r refresh · esc close")

	return title + "\n" + rule + "\n" + body + "\n" + hint
}
