// tui_model.go — Bubble Tea model struct and Init(); holds all TUI state and wires the agent, panels, and event subscriptions.
package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// --- model ---

type model struct {
	agent   *agent.Agent
	db      *sqliteopen.DB
	session *sessions.Session

	// Layout
	width, height int
	ready         bool

	// UI components. input is a textarea so long messages wrap across
	// multiple lines instead of scrolling horizontally off-screen. Enter
	// submits; newlines are inserted via Alt-Enter / Shift-Enter (see the
	// key handler in Update).
	viewport viewport.Model
	input    textarea.Model

	// Transcript buffer — append-only, rendered into the viewport on each
	// update. Streaming tokens land here as they arrive. Pointer so the
	// model can be passed by value (Bubble Tea idiom for Update) without
	// triggering strings.Builder's no-copy-after-write check.
	transcript *strings.Builder

	// Transcript viewport cache — avoids redundant SetContent calls.
	// transcriptSeq increments on every pushViewport() call (i.e. whenever
	// transcript content or streaming state changes). viewportContentSeq
	// records the seq at the last SetContent call; viewportContentWidth
	// records the viewport width at that call. relayout() skips SetContent
	// when both match the current state, preventing per-frame re-renders on
	// large transcripts that haven't changed.
	transcriptSeq        uint64
	viewportContentSeq   uint64
	viewportContentWidth int

	// Streaming state — whether Selene is mid-turn, plus the cancel handle
	// for the in-flight context so Ctrl-C can abort the turn without killing
	// the program.
	streaming bool
	cancel    context.CancelFunc

	// Render-on-complete: tokens stream into the transcript (styled) for
	// live feedback, but their raw text is also captured here so finish-
	// Assistant can splice the streamed region out and replace it with a
	// markdown-rendered version. streamingStart records the byte offset in
	// transcript where the current turn's body began (just after the
	// "Selene: " prefix).
	streamingRaw   *strings.Builder
	streamingStart int

	// Cached glamour renderer — recreated on WindowSizeMsg when width changes.
	renderer *glamour.TermRenderer

	// Identity / status
	aiName      string
	modelName   string // LLM model name from config, shown in status bar left zone
	memoryCount int
	jobCount    int    // running jobs, refreshed every 10 ticks
	threadCount int    // running background tasks, refreshed on each tick
	contextLen  int    // model context window (tokens), 0 if unknown
	dreamAgeStr string // cached "dream Xm ago" string, refreshed on tick

	// tickCounter increments on every tickMsg — drives subtle animations
	// (thread spinner in the status bar, breathing "thinking" indicator
	// when Selene is streaming). Wrapping at a reasonable modulus so we
	// don't drift into huge numbers over long sessions.
	tickCounter int

	// sessionTokens accumulates the estimated token spend across all turns
	// in this TUI session. Incremented at turn-end from historyCost().
	sessionTokens int

	// streamingChars counts the characters received so far in the current
	// streaming turn. Divided by 4 for a token estimate shown in the status
	// bar while Selene is speaking. Reset to 0 at each turn start.
	streamingChars int

	// initNudgeDone is set to true once the first-run /init hint has been
	// emitted. Prevents re-emission on subsequent ticks.
	initNudgeDone bool

	// initPending is set when the /init command fires. Cleared in
	// handleTurnComplete, which then persists init_complete=true. This
	// ensures the flag is set even when small models don't make the
	// config tool call themselves.
	initPending bool

	// expander handles !shell, @file, and @paste prefix expansions before submission.
	expander PrefixExpander

	// pasteRefs holds diverted-paste payloads for this session, keyed by
	// the @paste:N integer id. Writes happen in handleSmartPaste; reads
	// happen via expander.PasteRefs (same backing map).
	pasteRefs    map[string]*PasteRef
	pasteCounter int // monotonically increasing per session — never reused

	// progressTasks is the in-flight set of background tasks reporting
	// progress. Refreshed from the DB on every tick; rendered as one bar
	// per task just above the input frame.
	progressTasks []*jobs.Task

	// toolToasts is the live set of foreground tool calls being rendered
	// as transient rows above the input. Replaces the old "tools render
	// inline in the transcript" UX so Selene's voice has uninterrupted
	// scroll. Pruned on tick once a toast's linger window expires.
	toolToasts []toolToast

	// Per-turn aggregate of foreground tool calls — used for the
	// end-of-turn summary line ("[3 tools · 2.4 KB · 12s]") so the
	// transcript still has a record without the bouncy churn.
	turnToolCount int
	turnToolBytes int
	turnToolNames []string

	// Loop-detection: ring of recent tool calls (name + timestamp). On
	// each new tool start we count how many of these match within the
	// loopWarnWindow; if it crosses loopWarnThreshold, fire a warn toast.
	// lastLoopWarnAt rate-limits so a sustained loop doesn't spam.
	recentTools    []recentToolCall
	lastLoopWarnAt time.Time

	// activity tracks what the agent is doing right now. Drives the
	// state-aware token in the status bar and the input glyph. Maintained
	// by handleEvent; nothing else writes it.
	//
	//   activity.State() == activityIdle       → hidden in status bar
	//   activity.State() == activityStreaming  → "● <aiName>" in Selene-blue
	//   activity.State() == activityThinking  → "❋ thinking" in Selene-blue dim
	//   activity.State() == activityTool      → "<icon> <name>" in family color
	activity activityTracker

	// Stats flash — when a tool from a given family fires, we stamp the
	// family's key with time.Now(); renderStatus checks how recent that
	// stamp is and brightens the matching stats label for ~flashFor
	// milliseconds, then fades back. Zero cost when no flash is active.
	memFlashAt    time.Time
	threadFlashAt time.Time

	// Commands + drawers. The slash drawer opens only when the user types
	// '/' as the first char of empty input; closes when the leading '/' is
	// deleted or Esc is pressed.
	commands     []Command
	slashOpen    bool
	slashMatches []Command
	slashIndex   int // selected row in the drawer

	// Command palette — Ctrl+K full overlay that searches across commands,
	// skills, sessions, and memories. Mutually exclusive with the slash drawer.
	palette paletteState

	// Panel system — every overlay/drawer other than the slash drawer
	// routes through panelSpec registration. openPanels tracks which are
	// visible; focusedPanel gets keyboard input first (input field is
	// focused when focusedPanel == "").
	openPanels   map[panelID]bool
	focusedPanel panelID

	// Per-panel state. Kept on the main model so panel hooks can access
	// without passing state values around.
	threads    threadsState
	files      filesState
	memory     memoryState
	prompt     promptState
	sessions   sessionsState
	inspector  inspectorState
	diff       diffState
	config     configState
	help       helpState
	log        logState
	quote      quoteState
	statePanel statePanelState

	// changedFiles tracks file paths written or edited this session.
	// Populated on EventToolStart for write/edit tools; used by the diff panel.
	changedFiles []string

	// stalledMidIntent is set when the agent ended a turn with forward-looking
	// text ("Now let me try…", "I'll next check…") but no tool calls — the
	// model indicated intent and then stopped. A banner is rendered in the
	// status bar until the user sends their next message.
	stalledMidIntent bool

	// currentStep tracks the in-flight execution phase published by the agent
	// loop heartbeat. nil when the agent is idle. The status bar renders this
	// as "⟳ <step> · <elapsed>" to give the user visibility into what cairo
	// is doing right now.
	currentStep *activeStep

	// reload is set to true by the /reload command. After tea.Quit returns,
	// Run() inspects this and signals main to exec a fresh cairo process.
	reload bool

	// newSession is set to true by the /new command. main re-execs with
	// -new appended so a fresh session is created — and resolveSession
	// will drain the previous session's unsummarized backlog on the way
	// through (SummarizeAll runs on the prior session before the new
	// one is created). Mutually compatible with reload.
	newSession bool

	// Event subscription — a channel that carries agent events. The Bubble
	// Tea program polls it via listenEvents() which returns a tea.Cmd.
	eventCh <-chan agent.Event
	unsub   func()

	// Toast notifications — transient overlays that auto-dismiss after 5s.
	// toastedTaskIDs tracks which background task IDs have already had a toast
	// shown so the 300ms tick doesn't re-emit one on each poll.
	toasts         []toast
	toastedTaskIDs map[int64]bool

	// taskLogSizes tracks the last-seen log file size (bytes) for each running
	// task ID. Used by the watchdog to detect hung tasks (no output growth).
	taskLogSizes map[int64]int64

	// lastWarnedDropCount is the bus drop count at the time of the last
	// "event bus dropping" toast. Used to suppress repeat toasts when no
	// new drops have occurred since the last warning.
	lastWarnedDropCount int64

	// uiEvents is a ring buffer of recent user actions in the TUI. Drained
	// into the agent's UI context immediately before each Prompt call.
	uiEvents []string

	// choiceRequests is the channel the choose tool sends ChoiceRequest values on.
	// The model drains it in handleTick (non-blocking). nil when not wired.
	choiceRequests chan tools.ChoiceRequest

	// activeChoice holds a pending choice waiting for user input.
	// nil when no choice overlay is active.
	activeChoice *choiceOverlay

	styles styles
}

// activeStep holds the current in-flight execution phase for the heartbeat indicator.
type activeStep struct {
	Name      string    // "consider", "llm", "tool", "persist"
	Detail    string    // tool name for "tool", otherwise ""
	StartedAt time.Time // when this step began
}

// choiceOverlay holds the state for an active choose() overlay.
type choiceOverlay struct {
	title    string
	options  []string
	selected int
	result   chan<- string
}

func newModel(a *agent.Agent, database *sqliteopen.DB, sess *sessions.Session, choiceRequests chan tools.ChoiceRequest) model {
	aiName, _ := database.Config.Get("ai_name")
	if aiName == "" {
		aiName = "cairo"
	}
	modelName, _ := database.Config.Get("model")

	ti := textarea.New()
	ti.Placeholder = "message " + aiName + "…"
	ti.CharLimit = 0 // no limit
	ti.Prompt = ""   // we draw our own role-tinted glyph in View
	ti.ShowLineNumbers = false
	// Auto-growing input: starts at one row and grows up to MaxHeight as
	// you add explicit line breaks (Alt-Enter / Ctrl-J). syncInputHeight
	// in tui.go updates the height after every keystroke, and relayout
	// reads m.input.Height() so the viewport reclaims the freed rows.
	ti.SetHeight(1)
	ti.MaxHeight = 8
	// Colors: placeholder dim but legible; typed text in primary color.
	ti.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(colTextDim)
	ti.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(colTextDim)
	ti.FocusedStyle.Text = lipgloss.NewStyle().Foreground(colText)
	ti.BlurredStyle.Text = lipgloss.NewStyle().Foreground(colText)
	// Hide the built-in cursor-line background highlight — it competes
	// with the prompt glyph and makes the box look busy at rest.
	ti.FocusedStyle.CursorLine = lipgloss.NewStyle()
	// Rebind "insert newline" so it doesn't eat plain Enter — our Update
	// intercepts Enter to submit the message. Alt-Enter (and Ctrl-J as a
	// terminal-friendly fallback) are for users who want deliberate
	// line breaks within a message.
	ti.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "ctrl+j"))
	ti.Focus()

	ch, unsub := a.Bus().Subscribe()

	// Read glamour_style from config; fall back to "dark" if unset.
	// WithStandardStyle("dark") avoids glamour's OSC 11 background-color probe
	// that WithAutoStyle() triggers — the probe response leaks into the input
	// stream as "]11;rgb:..." garbage in some terminals.
	glamourStyle, _ := database.Config.Get("glamour_style")
	if glamourStyle == "" {
		glamourStyle = "dark"
	}
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(80),
	)

	m := model{
		agent:          a,
		db:             database,
		session:        sess,
		input:          ti,
		aiName:         aiName,
		modelName:      modelName,
		commands:       defaultCommands(),
		eventCh:        ch,
		unsub:          unsub,
		styles:         newStyles(sess.Role),
		transcript:     &strings.Builder{},
		streamingRaw:   &strings.Builder{},
		renderer:       renderer,
		choiceRequests: choiceRequests,
		pasteRefs:      make(map[string]*PasteRef),
		toastedTaskIDs: make(map[int64]bool),
		taskLogSizes:   make(map[int64]int64),
	}
	// Wire the expander to share the model's paste registry. Map header
	// gets copied around with model values; the underlying storage is
	// shared, so writes through m.pasteRefs are visible to Expand().
	m.expander = PrefixExpander{WorkDir: sess.CWD, PasteRefs: m.pasteRefs}
	// contextLen is fetched at startup and cached. If the user switches models
	// mid-session (via config(set, model=...)), this won't update until restart.
	if s, _ := database.Config.Get("model_ctx"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			m.contextLen = n
		}
	}
	m.refreshCounts()
	return m
}

func (m *model) refreshCounts() {
	if n, err := m.db.Memories.Count(); err == nil {
		m.memoryCount = n
	}
	if n, err := m.db.Jobs.CountRunning(); err == nil {
		m.jobCount = n
	}
	// Live thread count — drives the ◇ N marker and its pulse animation.
	if n, err := m.db.Tasks.CountRunning(); err == nil {
		m.threadCount = n
	}
	// Cache the dream-age string so View() stays pure (no DB I/O in render).
	m.dreamAgeStr = m.renderLastDream()
}

// addToast appends a transient notification. The toast will be rendered as an
// overlay in the bottom-right corner and dismissed automatically after 5s.
func (m *model) addToast(msg string, kind toastKind) {
	m.toasts = append(m.toasts, toast{
		message:   msg,
		kind:      kind,
		expiresAt: time.Now().Add(5 * time.Second),
	})
}

// --- init ---

// tickInterval governs the animation cadence: thread spinner, breathing
// streaming indicator, and live status-bar refresh. 300ms is slow enough
// to be calm (not a flicker) but fast enough to feel like presence.
const tickInterval = 300 * time.Millisecond

// flashFor is how long a status-bar stat (mem, thread) brightens after its
// matching tool family fires. Short enough to feel like a ping, long enough
// to be caught in peripheral vision.
const flashFor = 1500 * time.Millisecond

// activityState names what the agent is doing right now, feeding the
// state-aware token in the status bar and the input glyph. Kept small on
// purpose — a flat state, not a stack; transitions are driven by events.
type activityState int

const (
	activityIdle activityState = iota
	activityStreaming
	activityThinking
	activityTool
)

// toastKind distinguishes the visual treatment of a notification.
type toastKind int

const (
	toastInfo    toastKind = iota
	toastSuccess           // green border
	toastWarn              // yellow/amber border
	toastError             // red border
)

// toast is a transient on-screen notification that auto-dismisses after 5s.
type toast struct {
	message   string
	kind      toastKind
	expiresAt time.Time
}

// recentToolCall is one entry in the loop-detection ring. We keep just
// the name + timestamp — args would help disambiguate "same query 5x" vs
// "5 different queries" but cost more memory; the false-positive risk of
// name-only matching is acceptable for a soft warning.
type recentToolCall struct {
	name string
	at   time.Time
}

// Loop-detection thresholds. These are read at use time so changing the
// constants doesn't require a rebuild discipline shift.
const (
	loopWarnThreshold = 5                // same tool fired this many times
	loopWarnWindow    = 90 * time.Second // within this rolling window
	loopWarnCooldown  = 30 * time.Second // min gap between two warn toasts
	loopRingMax       = 32               // cap the ring so it doesn't grow
)

// tickMsg fires every tickInterval via a self-rescheduling tea.Cmd.
// Carries no payload — the model holds its own counter.
type tickMsg struct{}

// tickRefreshMsg carries the results of the off-render-loop DB queries that
// fire every Nth tick. Fields mirror what the tick handler previously set
// inline; the handler for this message applies them to the model.
type tickRefreshMsg struct {
	threadCount    int
	completedTasks []*jobs.Task
	progressTasks  []*jobs.Task
	jobCount       int
	hasJobCount    bool
	memoryCount    int
	hasMemoryCount bool
	runWatchdog    bool
}

// scheduleTick returns a tea.Cmd that produces a tickMsg after tickInterval.
// The Update handler re-issues it on each tick, keeping the pulse going.
func scheduleTick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		listenEvents(m.eventCh),
		textinput.Blink,
		scheduleTick(),
	)
}
