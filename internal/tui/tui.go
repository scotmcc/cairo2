package tui

// tui.go — Bubble Tea model, update, view. v1: conversation viewport,
// single-line input, status bar footer, role-tinted prompt glyph, streaming
// tokens from the agent event bus. Overlays and drawers come in v2.

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"

	"log"
	"path/filepath"
)

// Run starts the Bubble Tea program. Blocks until the user quits. Drains the
// agent's background goroutines (summarizer, etc.) on the way out.
// choiceRequests is the channel the choose tool sends ChoiceRequest values on;
// may be nil if the choose tool was not registered.
// Run starts the Bubble Tea program. Returns:
//   - reload=true when the user invoked /reload — main re-execs cairo with
//     the same args.
//   - newSession=true when the user invoked /new — main re-execs with -new
//     appended so a fresh session is created (and the previous session's
//     unsummarized backlog drains via SummarizeAll on the way through).
func Run(a *agent.Agent, database *sqliteopen.DB, session *sessions.Session, choiceRequests chan tools.ChoiceRequest) (bool, bool, error) {
	// Redirect Go's default logger to a file. Background workers (summarizer,
	// memory_search, etc.) call log.Printf for warnings/errors; in alt-screen
	// mode those writes leak into the rendered display and corrupt it. The
	// log file lives next to the DB so users can tail it for debugging.
	if logPath := filepath.Join(sqliteopen.DefaultDataDir(), "cairo.log"); os.Getenv("HOME") != "" {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			prev := log.Writer()
			log.SetOutput(f)
			defer func() {
				log.SetOutput(prev)
				f.Close()
			}()
		}
	}

	// Pin color profile so lipgloss doesn't need to probe the terminal.
	lipgloss.DefaultRenderer().SetColorProfile(termenv.TrueColor)

	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	a.StartWatcher(watchCtx)

	m := newModel(a, database, session, choiceRequests)
	seedTranscript(&m)
	injectDreamContext(a, database, session)
	unsub := m.unsub
	p := tea.NewProgram(m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		// Even with the profile pinned, bubbletea's package-init fires
		// lipgloss.HasDarkBackground() which sends an OSC 11 ("query
		// background color") to the terminal. Emulators that respond slowly
		// (Waveterm has been observed to take longer than the probe's 5s
		// timeout) end up replying while our input reader is already live,
		// so the raw escape — "]11;rgb:0000/0000/0000\" and friends — gets
		// injected into the textinput as if the user typed it. Bubbletea
		// parses OSC sequences as alt+']' followed by plain runes (it only
		// knows CSI responses), so we filter them explicitly here.
		tea.WithFilter(oscFilter()),
	)
	finalModel, err := p.Run()
	unsub()
	// TUI is torn down at this point. Run the phased shutdown with stdout
	// progress so the user can see what's happening (summarizer drain,
	// session feedback LLM call, hooks) and Ctrl-C out cleanly if it's
	// taking too long. See internal/agent/shutdown.go.
	a.Shutdown(os.Stdout)
	reload := false
	newSession := false
	if fm, ok := finalModel.(model); ok {
		reload = fm.reload
		newSession = fm.newSession
	}
	return reload, newSession, err
}

// injectDreamContext fires once at TUI startup. If a dream ran since the
// current session was created, it queues a one-turn <ui-context> block so
// the agent surfaces it on the user's first message.
func injectDreamContext(a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) {
	lastDreamRaw, err := database.Config.Get(config.KeyLastDreamAt)
	if err != nil || lastDreamRaw == "" {
		return
	}
	var lastDreamUnix int64
	if _, err := fmt.Sscan(lastDreamRaw, &lastDreamUnix); err != nil || lastDreamUnix == 0 {
		return
	}
	lastDream := time.Unix(lastDreamUnix, 0)
	if !lastDream.After(session.CreatedAt) {
		return
	}

	dreams, err := database.Dreams.List(1)
	if err != nil || len(dreams) == 0 {
		return
	}
	d := dreams[0]

	entries, _ := database.DreamLog.List(d.ID)

	var sb strings.Builder
	fmt.Fprintf(&sb, "A dream-pass ran since this session began. Today's dream is at:\n\n  %s\n\n", d.NarrativePath)
	if len(entries) > 0 {
		sb.WriteString("The dream-pass made these mutations (read dream_log if you need details):\n")
		for _, e := range entries {
			fmt.Fprintf(&sb, "- %s: %s ids=%s — %s\n", e.Action, e.TargetTable, e.TargetIDs, e.Note)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Before answering the user's first message, consider whether to read tonight's dream. The narrative may carry useful framing for today's work.")

	a.SetUIContext(sb.String())
}

// oscBodyRe matches the characters that appear inside an OSC 10/11 response
// body — digit/hex triples like "rgb:0000/0000/0000", the OSC number itself,
// separators. Conservative by design: we'd rather let a real keypress
// through than swallow one.
var oscBodyRe = regexp.MustCompile(`^[0-9a-fA-F;:/rgbRGB()\\\s,.]+$`)

// oscFilter returns a message filter that drops OSC 10/11 (and CSI-shaped)
// responses that leak into the input stream when the terminal emulator
// replies late to a background-color probe. It keeps state across
// invocations via closure.
//
// Note: with internal/tuisetup pinning lipgloss before bubbletea's init()
// runs, the probe is skipped in the first place and this filter should
// rarely see a response. It stays as defence-in-depth for terminals that
// also emit unsolicited OSC 10/11 responses, or for cases where another
// Charm library (e.g. glamour) triggers its own probe.
//
// Shape of what we see after the terminal responds with ESC ] 11 ; rgb:... :
//  1. a KeyMsg with Alt=true and Runes=[']'] — ESC followed by ']'. Some
//     terminals/parsers present CSI-shaped replies as Alt+'[' instead.
//     Either variant opens sink mode.
//  2. a KeyMsg with Type=KeyRunes containing the rest: "11;rgb:0000/0000/0000"
//     (sometimes split across several messages).
//  3. a trailing backslash or KeyEscape from the ST terminator (ESC \).
//
// We enter osc-sink mode on (1) and stay there while consecutive messages
// look like response bodies — up to a small cap so a runaway state can't
// swallow real input. Anything that doesn't match ends sink mode and
// passes through.
func oscFilter() func(tea.Model, tea.Msg) tea.Msg {
	const maxSinkMessages = 3
	inOSC := false
	sunk := 0
	return func(_ tea.Model, msg tea.Msg) tea.Msg {
		key, ok := msg.(tea.KeyMsg)
		if !ok {
			return msg
		}
		if !inOSC {
			// Detect start: alt-modified ']' (OSC) or '[' (CSI-shaped)
			// is how bubbletea presents the ESC prefix of a response.
			if key.Alt && len(key.Runes) == 1 && (key.Runes[0] == ']' || key.Runes[0] == '[') {
				inOSC = true
				sunk = 0
				return nil
			}
			return msg
		}
		// In sink mode. Drop messages while they look OSC-ish OR are
		// the ST terminator. Bail out otherwise so real keys pass through.
		if sunk >= maxSinkMessages {
			inOSC = false
			return msg
		}
		sunk++
		if key.Type == tea.KeyRunes && oscBodyRe.MatchString(string(key.Runes)) {
			return nil
		}
		if key.Type == tea.KeyEscape {
			// ST terminator (ESC \) arriving as plain escape — closes
			// sink mode. Drop this one, then pass subsequent messages.
			inOSC = false
			return nil
		}
		// Doesn't look like a body — exit sink mode and let it through.
		inOSC = false
		return msg
	}
}

// --- update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tickMsg:
		return m.handleTick(msg)
	case tickRefreshMsg:
		return m.handleTickRefresh(msg)
	case pasteWrittenMsg:
		return m.handlePasteWritten(msg)
	case taskCancelledMsg:
		handleTaskCancelled(msg, &m)
		return m, nil
	case eventMsg:
		return m.handleAgentEvent(msg)
	case promptErrMsg:
		return m.handlePromptErr(msg)
	case turnCompleteMsg:
		return m.handleTurnComplete(msg)
	case MsgJobApprove:
		return m.handleJobApprove(msg)
	case MsgJobReject:
		return m.handleJobReject(msg)
	case dreamDoneMsg:
		if msg.err != nil {
			m.addToast("dream-pass failed: "+msg.err.Error(), toastError)
		} else {
			m.addToast("dream-pass complete", toastSuccess)
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd
	}
	return m, nil
}

// openSlash puts the model into slash-drawer mode. Called when the user
// types '/' as the first char of an otherwise-empty input. Relayouts so
// the viewport shrinks by the drawer's row budget.
func (m *model) openSlash() {
	if m.slashOpen {
		return
	}
	m.slashOpen = true
	m.slashIndex = 0
	m.slashMatches = filterCommands(m.commands, "")
	m.relayout()
}

// closeSlash tears down drawer state and relayouts. Without the relayout
// the viewport stays sized as if the drawer were still occupying its rows
// — closing the drawer would leave a phantom gap above the input.
func (m *model) closeSlash() {
	if !m.slashOpen {
		return
	}
	m.slashOpen = false
	m.slashMatches = nil
	m.slashIndex = 0
	m.relayout()
}

// refreshSlash re-filters the drawer based on the current input. If the
// leading '/' has been erased (input no longer starts with /), closes.
//
// Only the first whitespace-delimited token is used for filtering, so
// "/learn ~/foo" still matches the "learn" command — the rest is treated
// as args and consumed at dispatch time by HandlerWithArgs.
func (m *model) refreshSlash() {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") {
		m.closeSlash()
		return
	}
	query := strings.TrimPrefix(v, "/")
	if i := strings.IndexAny(query, " \t"); i >= 0 {
		query = query[:i]
	}
	m.slashMatches = filterCommands(m.commands, query)
	if m.slashIndex >= len(m.slashMatches) {
		m.slashIndex = 0
	}
}

// relayout recomputes the viewport height based on the current window size
// and which overlays are open. Called from WindowSizeMsg and whenever a
// drawer/panel opens or closes.
func (m *model) relayout() {
	// Reserve rows (non-viewport, always present):
	//   title(1) + heavy-rule(1) + thin-top(1) + input(N) + thin-bot(1) + status(1)
	// Input height is dynamic — starts at 1 and grows as the user adds
	// explicit line breaks (see syncInputHeight).
	inputH := m.input.Height()
	if inputH < 1 {
		inputH = 1
	}
	reserved := 5 + inputH
	if m.slashOpen {
		// Drawer eats up to drawerHeight rows between transcript and input.
		reserved += drawerHeight(m)
	}
	// Global progress bars sit between bottom panels and the input frame.
	// Each in-flight task with a label/total takes one row, capped by
	// progressMaxRows with a "+N more" overflow line.
	reserved += progressRowCount(m.progressTasks)
	// Tool-call toasts sit above the progress bars (between bottom
	// panels and progress). Each active or lingering tool call takes
	// one row; the toast region prunes itself on tick once a toast's
	// linger window expires.
	reserved += toolToastRowCount(m.toolToasts)
	// Open top/bottom panels consume vertical space too. Without this the
	// viewport thought it still had the full terminal height and the total
	// output would overflow the terminal — the top of the screen (header
	// + panel title) would scroll off and vanish. Left/right panels carve
	// horizontal space, handled separately in renderTranscriptWithSides.
	for _, s := range registeredPanels {
		if !m.openPanels[s.ID] {
			continue
		}
		if s.Position == posTop || s.Position == posBottom {
			h := s.PreferredHeight
			if h == 0 {
				h = 8
			}
			reserved += h
		}
	}
	vpHeight := max(m.height-reserved, 3)

	// Compute viewport width accounting for any open side panels.
	vpWidth := m.width
	for _, s := range registeredPanels {
		if !m.openPanels[s.ID] {
			continue
		}
		if s.Position == posLeft || s.Position == posRight {
			w := s.PreferredWidth
			if s.DynamicWidth != nil {
				w = s.DynamicWidth(m)
			}
			if w == 0 {
				w = 32
			}
			vpWidth -= w
		}
	}
	vpWidth = max(10, vpWidth)

	wasAtBottom := m.viewport.AtBottom()
	oldOffset := m.viewport.YOffset

	if !m.ready {
		m.viewport = viewport.New(vpWidth, vpHeight)
		m.ready = true
	} else {
		m.viewport.Width = vpWidth
		m.viewport.Height = vpHeight
	}
	// Only call SetContent when the transcript or the viewport width has
	// changed since the last push. pushViewport() is the authoritative path
	// for content changes; here we only need to re-render when the wrap
	// width changed (terminal resize / panel open-close) or the content
	// version is newer than what the viewport last saw.
	if m.transcriptSeq != m.viewportContentSeq || vpWidth != m.viewportContentWidth {
		m.viewport.SetContent(m.composeTranscript())
		m.viewportContentSeq = m.transcriptSeq
		m.viewportContentWidth = vpWidth
	}
	if wasAtBottom {
		m.viewport.GotoBottom()
	} else {
		m.viewport.SetYOffset(oldOffset)
	}
	// Leave room for the role-tinted glyph ("▸ " = 2 cells) + a small
	// right margin so long lines don't kiss the terminal edge.
	m.input.SetWidth(max(10, m.width-4))
}

// preGrowInput temporarily expands the textarea to its MaxHeight before a
// keystroke is processed. The textarea's internal repositionView only runs
// inside Update and uses the *current* height to decide whether to scroll
// — so if we leave it at one row when the user types past the wrap point,
// it shifts YOffset to keep the cursor visible and the first row vanishes.
// Pre-growing means there's always room, so YOffset stays at 0;
// syncInputHeight then trims the height back down to what's actually used.
func (m *model) preGrowInput() {
	if m.input.MaxHeight > 0 && m.input.Height() < m.input.MaxHeight {
		m.input.SetHeight(m.input.MaxHeight)
	}
}

// clearInput empties the input box and shrinks it back to one row, freeing
// the rows up for the transcript viewport. Use this instead of bare
// SetValue("") so the auto-grow stays in sync.
func (m *model) clearInput() {
	m.input.SetValue("")
	if m.syncInputHeight() {
		m.relayout()
	}
}

// Smart-paste thresholds: pastes that exceed *either* go to a tempfile and
// the input gets an @paste:N reference instead of the raw text. Below the
// thresholds, paste falls through to the textarea normally. Numbers picked
// so a one-line shell command stays inline but a 30-line code snippet or
// stack trace doesn't fill the input.
const (
	smartPasteMinChars = 800
	smartPasteMinLines = 6
)

// shouldDivertPaste reports whether a pasted rune slice is large enough to
// be diverted to a tempfile attachment.
func shouldDivertPaste(runes []rune) bool {
	if len(runes) >= smartPasteMinChars {
		return true
	}
	lines := 1
	for _, r := range runes {
		if r == '\n' {
			lines++
			if lines > smartPasteMinLines {
				return true
			}
		}
	}
	return false
}

// pasteWrittenMsg carries the result of an async smart-paste file write.
type pasteWrittenMsg struct {
	id    string
	path  string
	bytes int
	lines int
	err   error
}

// handleSmartPaste schedules a tea.Cmd that writes the paste content to a
// tempfile off the render loop, then returns a pasteWrittenMsg. The input
// field stays clear while the write is in flight.
func (m model) handleSmartPaste(runes []rune) (model, tea.Cmd) {
	text := string(runes)
	m.pasteCounter++
	id := strconv.Itoa(m.pasteCounter)
	lines := strings.Count(text, "\n") + 1
	bytes := len(text)

	cmd := func() tea.Msg {
		tmp, err := os.CreateTemp("", "cairo-paste-*.txt")
		if err != nil {
			return pasteWrittenMsg{id: id, err: err}
		}
		if _, err := tmp.WriteString(text); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return pasteWrittenMsg{id: id, err: err}
		}
		tmp.Close()
		return pasteWrittenMsg{id: id, path: tmp.Name(), bytes: bytes, lines: lines}
	}
	return m, cmd
}

// handlePasteWritten applies the result of a smart-paste write: registers the
// ref, inserts the token into the input, and shows a toast.
func (m model) handlePasteWritten(msg pasteWrittenMsg) (model, tea.Cmd) {
	if msg.err != nil {
		m.addToast("paste: "+msg.err.Error(), toastError)
		return m, nil
	}
	m.pasteRefs[msg.id] = &PasteRef{
		Path:  msg.path,
		Bytes: msg.bytes,
		Lines: msg.lines,
	}
	val := m.input.Value()
	token := "@paste:" + msg.id
	if val != "" && !strings.HasSuffix(val, " ") && !strings.HasSuffix(val, "\n") {
		token = " " + token
	}
	token += " "
	m.input.InsertString(token)
	if m.syncInputHeight() {
		m.relayout()
	}
	m.addToast(fmt.Sprintf("📎 pasted %s (%d lines) → @paste:%s",
		humanBytes(msg.bytes), msg.lines, msg.id), toastSuccess)
	return m, nil
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// syncInputHeight resizes the input to fit its current visual line count,
// clamped to [1, MaxHeight]. Returns true when the height changed so the
// caller can call relayout() to give the freed/used rows back to the
// transcript viewport. Visual lines account for soft-wrap — a long
// single-line message that wraps to three rows grows the box to three.
func (m *model) syncInputHeight() bool {
	want := visualLineCount(m.input.Value(), m.input.Width())
	if want < 1 {
		want = 1
	}
	if m.input.MaxHeight > 0 && want > m.input.MaxHeight {
		want = m.input.MaxHeight
	}
	if want == m.input.Height() {
		return false
	}
	m.input.SetHeight(want)
	return true
}

// visualLineCount estimates how many terminal rows the given text will
// occupy in a textarea of the given width, counting both explicit "\n"
// breaks and soft-wraps. Mirrors the textarea's internal wrap math
// (which is unexported) closely enough that the box matches what's drawn.
func visualLineCount(s string, width int) int {
	if width <= 0 {
		return 1
	}
	total := 0
	for _, line := range strings.Split(s, "\n") {
		if line == "" {
			total++
			continue
		}
		w := lipgloss.Width(line)
		n := (w + width - 1) / width // ceil
		if n < 1 {
			n = 1
		}
		total += n
	}
	if total < 1 {
		return 1
	}
	return total
}

// drawerHeight computes how many rows the slash drawer should consume —
// proportional to the number of matches, clamped to a sensible range.
func drawerHeight(m *model) int {
	h := len(m.slashMatches)
	if h == 0 {
		h = 1
	}
	// +1 for a thin rule above and a short footer hint row
	return min(h+2, 10)
}

// submit sends a prompt to the agent on a background goroutine. The actual
// streaming / tool events arrive via the event bus. Each submission gets a
// fresh cancel handle; Ctrl-C while streaming calls it to abort the turn.
func (m *model) submit(text string) tea.Cmd {
	return m.submitWithOpts(text, false)
}

// submitWithOpts is like submit but accepts a forceConsider flag for the /c prefix.
func (m *model) submitWithOpts(text string, forceConsider bool) tea.Cmd {
	a := m.agent
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel

	// Drain the UI event queue into the agent before this turn fires.
	if len(m.uiEvents) > 0 {
		note := "No action required — recent UI activity:\n"
		for _, e := range m.uiEvents {
			note += "- " + e + "\n"
		}
		m.uiEvents = m.uiEvents[:0]
		a.SetUIContext(strings.TrimRight(note, "\n"))
	}

	return func() tea.Msg {
		if err := a.PromptWithOpts(ctx, text, agent.PromptOpts{ForceConsider: forceConsider}); err != nil {
			return promptErrMsg{err: err}
		}
		return turnCompleteMsg{}
	}
}

// trackChangedFile appends path to changedFiles if not already present.
func (m *model) trackChangedFile(path string) {
	for _, existing := range m.changedFiles {
		if existing == path {
			return
		}
	}
	m.changedFiles = append(m.changedFiles, path)
}

// runWatchdog checks all running tasks for dead processes and hung processes
// (no log output growth for >10 minutes). Dead processes are marked failed;
// hung processes generate a warning toast.
func (m *model) runWatchdog() {
	tasks, err := m.db.Tasks.Running()
	if err != nil || len(tasks) == 0 {
		return
	}
	now := time.Now()
	for _, t := range tasks {
		// Check process liveness.
		if t.PID != nil {
			proc, err := os.FindProcess(*t.PID)
			alive := err == nil && proc.Signal(syscall.Signal(0)) == nil
			if !alive {
				m.db.Tasks.SetStatusAndResult(t.ID, "failed", "reaped: process died")
				m.db.Jobs.ResolveAndUpdateJobStatus(t.JobID)
				continue
			}
		}

		// Check for hung task (running >10m with no log output growth).
		if t.StartedAt != nil && now.Sub(*t.StartedAt) > 10*time.Minute {
			var currentSize int64
			if t.LogPath != "" {
				if fi, err := os.Stat(t.LogPath); err == nil {
					currentSize = fi.Size()
				}
			}
			lastSize, seen := m.taskLogSizes[t.ID]
			if seen && currentSize == lastSize {
				// No growth — fire a hung toast if not already shown.
				toastKey := -t.ID // negative to avoid collision with toastedTaskIDs
				_ = toastKey
				title := t.Title
				if len(title) > 30 {
					title = title[:27] + "..."
				}
				m.addToast(
					fmt.Sprintf("Task '%s' may be hung (10m, no output) — check agent(action=\"log\")", title),
					toastWarn,
				)
			}
			m.taskLogSizes[t.ID] = currentSize
		}
	}
}

// enqueueUIEvent records a TUI activity note for the agent. At most 10 entries
// are kept; oldest are dropped. Call this from Update handlers to give the
// agent passive awareness of what the user has been doing.
func (m *model) enqueueUIEvent(desc string) {
	const maxUIEvents = 10
	m.uiEvents = append(m.uiEvents, desc)
	if len(m.uiEvents) > maxUIEvents {
		m.uiEvents = m.uiEvents[len(m.uiEvents)-maxUIEvents:]
	}
}
