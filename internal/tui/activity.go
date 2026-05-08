// activity.go — activityTracker: tracks what the agent is doing and for how long, driving status-bar display.
package tui

import "time"

// activityTracker bundles the fields that describe what the agent is doing
// right now and how long it's been doing it. Lives on model as `activity`.
//
// Two clocks:
//   - start: when the current state began. Drives Duration() — used by the
//     status bar so the user sees "❋ thinking · 14s" growing.
//   - lastEvent: when we last received a streaming token or tool update.
//     Drives Awaiting() — when the state says "thinking" but no token has
//     arrived in over awaitingThreshold, the model is really *waiting* on
//     Ollama (cold load, prompt prefill), not reasoning. The status bar
//     uses this distinction so a 60s prefill doesn't masquerade as 60s of
//     deep thought.
type activityTracker struct {
	state     activityState
	tool      string
	family    toolFamily
	start     time.Time
	lastEvent time.Time

	// seenAnyActivity flips to true the moment Ollama produces something
	// in this run — a thinking token, a content token, or a tool call.
	// Awaiting() reads this so "⋯ awaiting model" is reserved for the
	// genuine cold-start case (model load + first prefill) and never
	// fires during a between-tools gap, when we know the model is hot.
	seenAnyActivity bool

	// cameFromTool flips true when EventToolEnd transitions us back to
	// thinking. The renderer uses it to label the silent gap as
	// "⤓ processing tool result" rather than "❋ thinking" — Ollama is
	// re-prefilling the (now larger) message stack before it generates,
	// and that's a different cost than fresh reasoning. Cleared by Tick()
	// because once a real token arrives, prefill is over.
	cameFromTool bool

	// thinkEvents counts EventThinking events received this turn. Pure
	// liveness signal — when it's incrementing, the model is producing
	// reasoning tokens. When it's frozen during a long silence, that's
	// our cue to escalate the label to a stuck warning.
	thinkEvents int

	// Per-agent-run counters. Reset on TurnStart (the outermost
	// EventAgentStart) and frozen on TurnEnd. Drives the "● Selene · 14
	// tools · 8m" footer in the status bar so the user can see the cost
	// of an in-flight turn at a glance.
	turnStart time.Time
	toolCount int
}

// awaitingThreshold is how long the activity has to go without a token
// arrival before we consider the model "awaiting" rather than "thinking".
// 1.5s is conservative — fast enough to flip during a 5s+ prefill, slow
// enough that a model briefly pausing between tokens doesn't flicker.
const awaitingThreshold = 1500 * time.Millisecond

// staleThreshold is how long the model has to be silent before we
// surface a stuck warning. Crosses the line from "slow but working" to
// "may actually be hung — Ctrl+C cancels". Conservative enough that a
// big prefill doesn't trigger it spuriously.
const staleThreshold = 30 * time.Second

func (a *activityTracker) SetIdle() {
	a.state = activityIdle
	a.tool = ""
	a.family = 0
}

func (a *activityTracker) SetStreaming() {
	if a.state != activityStreaming {
		a.start = time.Now()
	}
	a.state = activityStreaming
	a.lastEvent = time.Now()
	a.seenAnyActivity = true
}

func (a *activityTracker) SetThinking() {
	if a.state != activityThinking {
		a.start = time.Now()
	}
	a.state = activityThinking
	// NOTE: no lastEvent stamp here — entering "thinking" might be the
	// caller flipping us between tool end and the next token (the gap is
	// what we want to detect). Real lastEvent stamps happen via Tick.
	// Also no seenAnyActivity stamp — being told "we're thinking now" by
	// the caller is not proof the model produced anything; only Tick is.
}

func (a *activityTracker) SetTool(name string, fam toolFamily) {
	a.state = activityTool
	a.tool = name
	a.family = fam
	a.start = time.Now()
	a.lastEvent = time.Now()
	a.seenAnyActivity = true
}

// Tick is called when an EventTokens or EventThinking arrives — proves
// the model is actually emitting something. The status bar reads
// time.Since(lastEvent) to decide whether to label as thinking vs awaiting.
// Clears cameFromTool — once we have a real token, the post-tool
// prefill is done and "thinking" is the right word again.
func (a *activityTracker) Tick() {
	a.lastEvent = time.Now()
	a.seenAnyActivity = true
	a.cameFromTool = false
}

// SetThinkingPostTool is called from the EventToolEnd handler. Behaves
// like SetThinking but flags the gap as "post-tool prefill" so the
// renderer can label it accurately — distinct from cold start AND from
// generic thinking.
func (a *activityTracker) SetThinkingPostTool() {
	a.SetThinking()
	a.cameFromTool = true
}

// IncThinkEvent counts each EventThinking arrival. The status bar shows
// the count as a positive liveness signal — if the number is climbing,
// the model is actually working through a chain of thought even when
// no content has streamed yet.
func (a *activityTracker) IncThinkEvent()     { a.thinkEvents++ }
func (a *activityTracker) ThinkEvents() int   { return a.thinkEvents }
func (a *activityTracker) CameFromTool() bool { return a.cameFromTool }

// SecondsSinceEvent is the elapsed seconds since the last EventTokens or
// EventThinking. Used by the renderer to decide whether to flip into
// the stuck-warning label.
func (a *activityTracker) SecondsSinceEvent() time.Duration {
	if a.lastEvent.IsZero() {
		return 0
	}
	return time.Since(a.lastEvent)
}

// Stale reports whether we've been silent long enough to warn the user
// that something might be wrong. False during streaming or actively
// arriving tokens, true when state is thinking AND no event has arrived
// in staleThreshold.
func (a *activityTracker) Stale() bool {
	if a.state != activityThinking {
		return false
	}
	if a.lastEvent.IsZero() {
		return false
	}
	return time.Since(a.lastEvent) > staleThreshold
}

// BeginTurn resets the per-run counters and stamps the turn start. Called
// from the EventAgentStart handler.
func (a *activityTracker) BeginTurn() {
	a.turnStart = time.Now()
	a.toolCount = 0
	a.seenAnyActivity = false
	a.cameFromTool = false
	a.thinkEvents = 0
}

// IncTool bumps the per-turn tool counter. Called from EventToolStart.
func (a *activityTracker) IncTool() { a.toolCount++ }

// TurnElapsed returns how long the current run has been going. Zero when
// no run has been started.
func (a *activityTracker) TurnElapsed() time.Duration {
	if a.turnStart.IsZero() {
		return 0
	}
	return time.Since(a.turnStart)
}

// ToolCount returns how many tool calls have started in this run.
func (a *activityTracker) ToolCount() int { return a.toolCount }

func (a *activityTracker) State() activityState   { return a.state }
func (a *activityTracker) ToolName() string       { return a.tool }
func (a *activityTracker) ToolFamily() toolFamily { return a.family }

// Duration is how long we've been in the current state.
func (a *activityTracker) Duration() time.Duration {
	if a.start.IsZero() {
		return 0
	}
	return time.Since(a.start)
}

// Awaiting reports whether we're genuinely waiting on Ollama to produce
// its first sign of life this turn — model load + initial prefill. True
// means the bar should render as ⋯ awaiting model rather than ❋ thinking.
//
// Once any activity (a thinking token, a content token, a tool call) has
// occurred this turn we know the model is hot, so the gap-between-tools
// case stays labeled as plain "thinking" — the model is still working,
// we're just between events.
func (a *activityTracker) Awaiting() bool {
	if a.state != activityThinking {
		return false
	}
	if a.seenAnyActivity {
		return false
	}
	if a.lastEvent.IsZero() {
		return true
	}
	return time.Since(a.lastEvent) > awaitingThreshold
}
