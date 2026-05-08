package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/reflow/wrap"
)

// --- transcript helpers ---

// appendTurnSummary writes a one-line dim record of the tools used in
// the just-completed turn, e.g. "[3 tools · 2.4 KB · memory, read×2]".
// Skipped when no foreground tools fired this turn (Selene replied
// without calling anything). Replaces the previous per-call inline
// rendering so the transcript stays focused on Selene's voice but
// still records what was used after the fact.
func (m *model) appendTurnSummary() {
	n := m.turnToolCount
	if n == 0 {
		return
	}
	// Dedupe + count tool names while preserving first-seen order.
	counts := make(map[string]int, len(m.turnToolNames))
	var order []string
	for _, name := range m.turnToolNames {
		if _, ok := counts[name]; !ok {
			order = append(order, name)
		}
		counts[name]++
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if counts[name] == 1 {
			parts = append(parts, name)
		} else {
			parts = append(parts, fmt.Sprintf("%s×%d", name, counts[name]))
		}
	}
	label := "tool"
	if n != 1 {
		label = "tools"
	}
	summary := fmt.Sprintf("[%d %s · %s · %s]",
		n, label, humanBytes(m.turnToolBytes), strings.Join(parts, ", "))
	dim := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
	fmt.Fprintf(m.transcript, "%s\n\n", dim.Render(summary))
	m.pushViewport()
}

func (m *model) appendUser(text string) {
	const prefix = "You: "
	width := m.width - len(prefix)
	if width < 20 {
		width = 20
	}
	fmt.Fprintf(m.transcript, "%s%s\n\n",
		m.styles.voiceUser.Render(prefix),
		m.styles.body.Width(width).Render(text))
	m.pushViewport()
}

func (m *model) appendSystem(text string) {
	width := m.width
	if width < 20 {
		width = 20
	}
	fmt.Fprintf(m.transcript, "%s\n\n",
		m.styles.voiceSystem.Width(width).Render(text))
	m.pushViewport()
}

func (m *model) startAssistant() {
	fmt.Fprintf(m.transcript, "%s",
		m.styles.voiceSelene.Render(m.aiName+": "))
	m.streaming = true
	// Between Enter and the first event from the agent there can be a
	// gap of a few seconds on big models. Seed the activity as "thinking"
	// so the status bar and input glyph light up immediately; the first
	// real event (Tokens / ToolStart / Thinking) refines from there.
	m.activity.SetThinking()
	m.streamingRaw.Reset()
	m.streamingChars = 0
	m.streamingStart = m.transcript.Len()
	m.pushViewport()
}

func (m *model) appendAssistantToken(tok string) {
	// Accumulate raw tokens only. The viewport-composition step wraps the
	// streamingRaw buffer at the available width on every push so long
	// responses don't overflow mid-stream. When the turn ends,
	// finishAssistant splices the markdown-rendered version in.
	m.streamingRaw.WriteString(tok)
	m.pushViewport()
}

func (m *model) finishAssistant() {
	raw := m.streamingRaw.String()
	if rendered, ok := renderMarkdown(raw, m.renderer); ok {
		// Splice out the streamed (raw, styled) region and replace with
		// the markdown-rendered version.
		current := m.transcript.String()
		m.transcript.Reset()
		m.transcript.WriteString(current[:m.streamingStart])
		m.transcript.WriteString(rendered)
	}
	m.transcript.WriteString("\n\n")
	m.streamingRaw.Reset()
	m.pushViewport()
}

// renderMarkdown runs the assistant's raw text through the cached glamour
// renderer. Returns ok=false if rendering fails or the text is empty —
// callers fall back to the already-streamed raw text in that case.
func renderMarkdown(text string, r *glamour.TermRenderer) (string, bool) {
	if strings.TrimSpace(text) == "" || r == nil {
		return "", false
	}
	out, err := r.Render(text)
	if err != nil {
		return "", false
	}
	// Glamour adds leading/trailing whitespace for visual breathing room;
	// we add our own \n\n separator in finishAssistant, so trim here.
	return strings.Trim(out, "\n"), true
}

// toolResultPreview returns a single-line, length-capped excerpt of a
// tool's result for inline display. Strips leading whitespace and
// collapses internal newlines so the excerpt fits on one screen row.
func toolResultPreview(result string, maxLen int) string {
	s := strings.TrimSpace(result)
	if s == "" {
		return ""
	}
	// Collapse newlines to " ⋅ " so a multi-line result still shows its
	// shape (different items separated visibly) without breaking rows.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", " ⋅ ")
	// Compact runs of spaces.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	if len(s) > maxLen {
		s = s[:maxLen-1] + "…"
	}
	return s
}

// appendErrorLine renders an italic red row for errors that arrive via
// EventError — previously silently dropped. One row, no stack trace, so
// the minimal aesthetic survives.
func (m *model) appendErrorLine(msg string) {
	fmt.Fprintf(m.transcript, "%s\n\n", m.styles.errorLine.Render("⚠ "+msg))
	m.pushViewport()
}

// shortDuration formats a duration with just enough precision to tell the
// story: sub-second to ms, otherwise 0.1s steps. Keeps the tool-line tail
// compact — "(2.3s)" not "(2.347812ms)".
func shortDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func seedTranscript(m *model) {
	history := m.agent.History()
	if len(history) == 0 {
		return
	}
	seeded := 0
	for _, msg := range history {
		switch msg.Role {
		case "user":
			if msg.Content != "" {
				m.appendUser(msg.Content)
				seeded++
			}
		case "system":
			if msg.Content != "" {
				m.appendSystem(msg.Content)
				seeded++
			}
		case "assistant":
			if len(msg.ToolCalls) > 0 || msg.Content == "" {
				continue
			}
			fmt.Fprintf(m.transcript, "%s", m.styles.voiceSelene.Render(m.aiName+": "))
			m.streamingStart = m.transcript.Len()
			m.streamingRaw.Reset()
			m.streamingRaw.WriteString(msg.Content)
			m.finishAssistant()
			seeded++
		}
	}
	if seeded > 0 {
		dim := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
		fmt.Fprintf(m.transcript, "%s\n\n", dim.Render("── resumed ──"))
		m.pushViewport()
	}
}

func (m *model) pushViewport() {
	// Only auto-scroll if the user hasn't scrolled up to read earlier turns.
	// AtBottom() reports whether we were pinned to the bottom before the
	// content grew; if so, follow the stream. Otherwise preserve their scroll
	// position — they're reading something and don't want to get yanked.
	wasAtBottom := m.viewport.AtBottom()
	m.transcriptSeq++
	m.viewport.SetContent(m.composeTranscript())
	m.viewportContentSeq = m.transcriptSeq
	m.viewportContentWidth = m.viewport.Width
	if wasAtBottom {
		m.viewport.GotoBottom()
	}
}

// composeTranscript returns what the viewport should show right now. When
// streaming, that's the committed transcript (which ends in the styled
// "<aiName>: " prefix) followed by a word-wrapped, body-styled render of
// streamingRaw. When not streaming, it's just the transcript verbatim —
// finishAssistant will have already spliced the glamour-rendered version
// in. The point is that long responses wrap cleanly mid-stream instead of
// running off the right edge until the markdown re-render catches up.
func (m *model) composeTranscript() string {
	if m.streamingRaw.Len() == 0 {
		return m.transcript.String()
	}
	// Line 1 shares space with the "<aiName>: " prefix already sitting on
	// the transcript (e.g. "Selene: " = 8 cells). Wrapping at
	// m.width - prefixCells for the whole body means subsequent lines
	// are slightly narrower than the terminal — same compromise
	// appendUser makes for the "You: " prefix. Minor cosmetic loss,
	// correct under all widths.
	prefixCells := len(m.aiName) + 2
	width := m.width - prefixCells
	if width < 20 {
		width = 20
	}
	// Two-pass wrap: word-aware first (so sentences break at spaces),
	// then a hard wrap of the result for pathological cases (no spaces —
	// think long URLs or code fences) so we still never overflow.
	wrapped := wrap.String(wordwrap.String(m.streamingRaw.String(), width), width)
	return m.transcript.String() + m.styles.body.Render(wrapped)
}
