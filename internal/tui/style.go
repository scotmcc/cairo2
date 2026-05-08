package tui

import "github.com/charmbracelet/lipgloss"

// Moonlight palette — the story: Selene is moon-named. The interface is a night
// garden — cool air, soft light, a focused companion. Background stack is cool
// blue-black (moonlight depth). Selene's voice is moonlight-blue (cool, clear).
// User's voice is warm parchment (candlelight — the human is the warmth). Memory
// is deep amber (preserved, like embers). Threads are violet-twilight (parallel
// at the edge of attention). Everything else is departure from the cool dominant.

var (
	// Surfaces — three levels of depth + one elevated modal layer
	colBg          = lipgloss.Color("#0e1014") // base: near-black, cool
	colSurface     = lipgloss.Color("#151820") // panels, drawers
	colSurfaceHi   = lipgloss.Color("#1e2230") // overlays, focus rings
	colSurfaceElev = lipgloss.Color("#252838") // command palette, modals — elevated layer

	// Borders — moonlight-tinted structural lines
	colBorder     = lipgloss.Color("#3f4a60") // heavy rules
	colBorderThin = lipgloss.Color("#2a3142") // thin offset rules
	colBorderElev = lipgloss.Color("#4a3d6a") // command palette border — warm-purple, reserved

	// Text hierarchy
	colText      = lipgloss.Color("#e8e8ef") // primary body text
	colTextMuted = lipgloss.Color("#a8abbb") // secondary
	colTextDim   = lipgloss.Color("#6a6f7e") // hints, metadata, timestamps

	// Voices — who's speaking. Cool/warm split: Selene is moonlight, user is candlelight.
	colVoiceSelene  = lipgloss.Color("#a0c8e0") // moonlight-blue
	colVoiceSelene2 = lipgloss.Color("#7ca8be") // dim echo — session labels, meta (brightened for readability)
	colVoiceUser    = lipgloss.Color("#d8c890") // warm parchment
	colVoiceSystem  = lipgloss.Color("#9080c8") // cool purple — background/system notes (split from threads)

	// Semantic colors — each color owns exactly one concept. Peripheral recognition
	// builds over time: amber=memory, teal=activity, violet=parallel threads.
	colMemory = lipgloss.Color("#c8983c") // deep amber — stored knowledge, preserved warmth
	colTool   = lipgloss.Color("#6ab89a") // sea-glass teal — activity, verbs, in-progress
	colThread = lipgloss.Color("#c4a8e8") // violet-twilight — parallel threads (now split from system voice)

	// States
	colOK        = lipgloss.Color("#7fd4a0") // bright green — success, ✓ markers, coder role
	colWarn      = lipgloss.Color("#e8b84a") // warm yellow-amber — attention (now distinct from memory)
	colErr       = lipgloss.Color("#d77070") // soft red — errors
	colFocus     = lipgloss.Color("#ffffff")
	colStreaming = lipgloss.Color("#5a7898") // deep muted blue — AI mid-generation, liminal state

	// Role accents — prompt glyph and mode label color per role.
	colAccentSilver  = lipgloss.Color("#a8b8cc") // thinking_partner — blue-silver (more distinct from body text)
	colAccentGreen   = lipgloss.Color("#7bc47e") // coder
	colAccentBlue    = lipgloss.Color("#6b9bd2") // planner
	colAccentAmber   = lipgloss.Color("#d4a656") // reviewer (role accent ≠ colMemory by design)
	colAccentMagenta = lipgloss.Color("#c677c7") // orchestrator
)

// isBaselineRole reports whether the given role is Selene's baseline state,
// where no explicit mode label should be drawn — she's just "being Selene."
func isBaselineRole(role string) bool {
	return role == "thinking_partner" || role == ""
}

// roleAccent maps a role name to its accent color. Unknown roles fall back to
// silver (the default thinking-partner tint) so peripheral vision never shows
// an unrecognized color.
func roleAccent(role string) lipgloss.Color {
	switch role {
	case "coder":
		return colAccentGreen
	case "planner":
		return colAccentBlue
	case "reviewer":
		return colAccentAmber
	case "orchestrator":
		return colAccentMagenta
	default:
		return colAccentSilver
	}
}

// styles collects the lipgloss styles used across the TUI. Built once at Run
// time and attached to the model so they can be re-used without re-allocating
// on every View() call.
type styles struct {
	// Top header
	headerName lipgloss.Style // "Selene" — soft white, bold (label, not voice)
	headerMeta lipgloss.Style // "session 1" — dim echo of her voice
	headerRule lipgloss.Style // heavy horizontal — "heading" divider
	thinRule   lipgloss.Style // light horizontal — offset around the input

	// Conversation
	voiceUser   lipgloss.Style
	voiceSelene lipgloss.Style
	voiceSystem lipgloss.Style
	body        lipgloss.Style
	toolLine    lipgloss.Style // soft green — tool-call lines
	toolOK      lipgloss.Style // brighter green — ✓ marker
	toolErr     lipgloss.Style // red — ✗ marker

	// Input
	inputGlyph lipgloss.Style // role-tinted ▸ (silver in baseline)
	input      lipgloss.Style

	// Status bar — semantic colors for the concepts they name
	statusMode   lipgloss.Style // role accent — only drawn when non-baseline
	statusMemNum lipgloss.Style // amber — memory count digits
	statusMemLbl lipgloss.Style // dim — "mem" label
	statusThrNum lipgloss.Style // lavender — thread count digits
	statusThrLbl lipgloss.Style // dim — "◇" marker for threads
	statusHint   lipgloss.Style
	statusRule   lipgloss.Style
	statusLeft   lipgloss.Style // model name in status bar left zone

	// Input glyph variants — pre-built so renderInput doesn't allocate
	// a new style on every frame.
	glyphStreaming lipgloss.Style // ● in Selene-blue, bold
	glyphSlash     lipgloss.Style // / in hint-dim, bold
	glyphShell     lipgloss.Style // ! and @ in user voice, bold

	// Inline transcript styles on hot paths
	toolUpdateDim lipgloss.Style // dimmed indent for streaming tool output
	errorLine     lipgloss.Style // red italic for EventError lines
	drawerSel     lipgloss.Style // selected row in slash drawer

	// Toast border styles — one per kind, so View() doesn't allocate on each frame
	toastDefault lipgloss.Style
	toastSuccess lipgloss.Style
	toastWarn    lipgloss.Style
	toastError   lipgloss.Style

	// Activity indicator styles — tick-driven but struct-level to avoid alloc
	activityStreaming  lipgloss.Style // ● Selene-blue bold
	activityStreamDim  lipgloss.Style // ● Selene-blue2 (dim tick)
	activityStreamName lipgloss.Style // aiName label
	activityThinking   lipgloss.Style // ❋ thinking dim
}

func newStyles(role string) styles {
	accent := roleAccent(role)
	return styles{
		headerName: lipgloss.NewStyle().
			// Name-as-label is soft-white bold — what felt right the first
			// time. The moonlight-blue voice color lives on "Selene:" when
			// she actually speaks, not on the name tag.
			Foreground(colText).Bold(true),
		headerMeta: lipgloss.NewStyle().
			Foreground(colVoiceSelene2),
		headerRule: lipgloss.NewStyle().
			Foreground(colBorder),
		thinRule: lipgloss.NewStyle().
			// Dimmer than the heavy rules — they're offsets, not section
			// breaks. The hierarchy reads weight→importance twice: thin
			// characters (─) and darker color.
			Foreground(colBorderThin),

		voiceUser: lipgloss.NewStyle().
			Foreground(colVoiceUser).Bold(true),
		voiceSelene: lipgloss.NewStyle().
			Foreground(colVoiceSelene).Bold(true),
		voiceSystem: lipgloss.NewStyle().
			Foreground(colVoiceSystem).Italic(true),
		body: lipgloss.NewStyle().
			Foreground(colText),
		toolLine: lipgloss.NewStyle().
			Foreground(colTool),
		toolOK: lipgloss.NewStyle().
			Foreground(colOK).Bold(true),
		toolErr: lipgloss.NewStyle().
			Foreground(colErr).Bold(true),

		inputGlyph: lipgloss.NewStyle().
			Foreground(accent).Bold(true),
		input: lipgloss.NewStyle().
			Foreground(colText),

		statusMode: lipgloss.NewStyle().
			Foreground(accent).Bold(true),
		// Linked colors: number and label share the concept's color so the
		// pair reads as one unit. Number is bold to punch, label is not.
		statusMemNum: lipgloss.NewStyle().
			Foreground(colMemory).Bold(true),
		statusMemLbl: lipgloss.NewStyle().
			Foreground(colMemory),
		statusThrNum: lipgloss.NewStyle().
			Foreground(colThread).Bold(true),
		statusThrLbl: lipgloss.NewStyle().
			Foreground(colThread),
		statusHint: lipgloss.NewStyle().
			Foreground(colTextDim),
		statusRule: lipgloss.NewStyle().
			Foreground(colBorder),
		statusLeft: lipgloss.NewStyle().
			Foreground(colTextDim),

		glyphStreaming: lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true),
		glyphSlash:     lipgloss.NewStyle().Foreground(colTextDim).Bold(true),
		glyphShell:     lipgloss.NewStyle().Foreground(colVoiceUser).Bold(true),

		toolUpdateDim: lipgloss.NewStyle().Foreground(colTextDim),
		errorLine:     lipgloss.NewStyle().Foreground(colErr).Italic(true),
		drawerSel: lipgloss.NewStyle().
			Foreground(colFocus).
			Background(colSurfaceHi).
			Bold(true),

		toastDefault: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colTextDim).
			Foreground(colText).
			Background(colSurfaceElev).
			Padding(0, 1).
			MaxWidth(50),
		toastSuccess: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colOK).
			Foreground(colText).
			Background(colSurfaceElev).
			Padding(0, 1).
			MaxWidth(50),
		toastWarn: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#D4A017")).
			Foreground(colText).
			Background(colSurfaceElev).
			Padding(0, 1).
			MaxWidth(50),
		toastError: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colErr).
			Foreground(colText).
			Background(colSurfaceElev).
			Padding(0, 1).
			MaxWidth(50),

		activityStreaming:  lipgloss.NewStyle().Foreground(colVoiceSelene).Bold(true),
		activityStreamDim:  lipgloss.NewStyle().Foreground(colVoiceSelene2),
		activityStreamName: lipgloss.NewStyle().Foreground(colVoiceSelene2),
		activityThinking:   lipgloss.NewStyle().Foreground(colVoiceSelene2),
	}
}
