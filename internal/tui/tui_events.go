package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/scotmcc/cairo2/internal/agent"
)

// --- msg types ---

type eventMsg struct {
	event agent.Event
}

type promptErrMsg struct {
	err error
}

type turnCompleteMsg struct{}

// MsgJobApprove is sent by the diff panel when the user presses 'a' while a
// job is selected in job mode. The TUI Update loop receives it and calls the
// stub approve handler (sets jobs.status = merged, emits confirmation).
type MsgJobApprove struct{ JobID int64 }

// MsgJobReject is sent by the diff panel when the user presses 'r' while a
// job is selected in job mode. The TUI Update loop receives it and calls the
// stub reject handler (sets jobs.status = cancelled, emits confirmation).
type MsgJobReject struct{ JobID int64 }

// listenEvents returns a tea.Cmd that blocks on the event channel and returns
// a tea.Msg wrapping the event. The Update loop re-issues it after each
// event to keep the pump going. If the channel closes, the cmd returns nil
// (which terminates the pump — intentional on shutdown).
func listenEvents(ch <-chan agent.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return eventMsg{event: ev}
	}
}

// --- event handling ---

func (m *model) handleEvent(ev agent.Event) {
	switch ev.Type {
	case agent.EventAgentStart:
		// Outermost run start — reset per-run counters so the status bar
		// shows fresh "0 tools · 0m" for this exchange.
		m.activity.BeginTurn()
		// Reset per-turn tool aggregates so the end-of-turn summary
		// line covers only this exchange.
		m.turnToolCount = 0
		m.turnToolBytes = 0
		m.turnToolNames = nil

	case agent.EventTokens:
		p := ev.Payload.(agent.PayloadTokens)
		// First token of a streaming block transitions us out of thinking/
		// tool state — the model is now actually saying something.
		m.activity.SetStreaming()
		m.streamingChars += len(p.Token)
		m.appendAssistantToken(p.Token)

	case agent.EventThinking:
		// We do NOT render thinking tokens in the transcript (that would
		// uglify it), but we DO Tick() so the status bar can distinguish
		// "actively reasoning" from "waiting on Ollama". The bar reads
		// activity.Awaiting() to flip the label between ❋ thinking and
		// ⋯ awaiting model. IncThinkEvent gives us a count we can show
		// as a liveness signal — if it's climbing, the model is alive.
		if m.activity.State() != activityTool {
			m.activity.SetThinking()
		}
		m.activity.Tick()
		m.activity.IncThinkEvent()

	case agent.EventToolStart:
		p := ev.Payload.(agent.PayloadToolStart)
		if p.Name == "write" || p.Name == "edit" {
			if pathVal, ok := p.Args["path"]; ok {
				if filePath, ok := pathVal.(string); ok && filePath != "" {
					m.trackChangedFile(filePath)
				}
			}
		}
		fam := familyOf(p.Name)
		m.activity.SetTool(p.Name, fam)
		m.activity.IncTool()
		m.stampFlash(fam)
		m.maybeWarnLoop(p.Name)
		// Push a new toast for this tool. Replaces the old inline
		// transcript line so Selene's voice is the only thing the
		// transcript scrolls.
		m.toolToasts = append(m.toolToasts, toolToast{
			name:       p.Name,
			family:     fam,
			argPreview: summarizeArgs(p.Name, p.Args),
			startedAt:  time.Now(),
		})
		m.relayout()

	case agent.EventToolUpdate:
		// Live progress from a long-running tool (currently only
		// agent.wait emits these). Update the most recent active
		// toast's preview text so the user sees what's happening
		// without the transcript churning.
		p := ev.Payload.(agent.PayloadToolUpdate)
		for i := len(m.toolToasts) - 1; i >= 0; i-- {
			if m.toolToasts[i].endedAt.IsZero() {
				m.toolToasts[i].argPreview = p.Output
				break
			}
		}

	case agent.EventToolEnd:
		p := ev.Payload.(agent.PayloadToolEnd)
		dur := m.activity.Duration()
		// Mark the most recent active toast as ended; it will linger
		// briefly with the success/error tail then prune away.
		for i := len(m.toolToasts) - 1; i >= 0; i-- {
			if m.toolToasts[i].endedAt.IsZero() {
				m.toolToasts[i].endedAt = time.Now()
				m.toolToasts[i].duration = dur
				m.toolToasts[i].resultBytes = len(p.Result)
				m.toolToasts[i].resultPreview = toolResultPreview(p.Result, 80)
				m.toolToasts[i].isError = p.IsError
				// Aggregate for the end-of-turn summary line.
				m.turnToolCount++
				m.turnToolBytes += len(p.Result)
				m.turnToolNames = append(m.turnToolNames, p.Name)
				break
			}
		}
		// Between tool end and the next event the model has to re-prefill
		// the (now larger) message stack before generating. SetThinking
		// PostTool flags the state so the renderer can label this gap as
		// "⤓ processing tool result" — distinct from cold start AND from
		// generic thinking-while-emitting-tokens.
		m.activity.SetThinkingPostTool()

	case agent.EventConsiderAspectStart:
		p := ev.Payload.(agent.PayloadConsiderAspectStart)
		m.toolToasts = append(m.toolToasts, toolToast{
			name:      "Consider: " + p.Name,
			family:    familyAdmin,
			startedAt: time.Now(),
		})
		m.relayout()

	case agent.EventConsiderAspectEnd:
		p := ev.Payload.(agent.PayloadConsiderAspectEnd)
		target := "Consider: " + p.Name
		for i := len(m.toolToasts) - 1; i >= 0; i-- {
			if m.toolToasts[i].endedAt.IsZero() && m.toolToasts[i].name == target {
				m.toolToasts[i].endedAt = time.Now()
				m.toolToasts[i].resultPreview = toolResultPreview(p.Output, 80)
				m.toolToasts[i].resultBytes = len(p.Output)
				m.toolToasts[i].isError = p.IsError
				break
			}
		}

	case agent.EventConsiderInjected:
		p := ev.Payload.(agent.PayloadConsiderInjected)
		now := time.Now()
		t := toolToast{
			name:          "Consider: injected",
			family:        familyAdmin,
			startedAt:     now,
			endedAt:       now,
			duration:      time.Duration(p.ElapsedMs) * time.Millisecond,
			resultBytes:   len(p.Summary),
			resultPreview: toolResultPreview(p.Summary, 80),
		}
		m.toolToasts = append(m.toolToasts, t)
		m.relayout()

	case agent.EventError:
		p := ev.Payload.(agent.PayloadError)
		if p.Err != nil {
			m.appendErrorLine(p.Err.Error())
		}

	case agent.EventAgentEnd:
		m.activity.SetIdle()
		m.finishAssistant()
		// One-line summary of the tool work this turn so the transcript
		// retains a record now that individual tool start/end lines
		// don't render inline. Skipped when no tools fired.
		m.appendTurnSummary()

	case agent.EventStallDetected:
		// The agent stopped mid-intent: forward-looking text with no tool calls.
		// Flag it so the status bar renders a banner prompting the user to continue.
		m.stalledMidIntent = true

	case agent.EventStepStart:
		p := ev.Payload.(agent.PayloadStepStart)
		m.currentStep = &activeStep{
			Name:      p.Step,
			Detail:    p.Detail,
			StartedAt: p.StartedAt,
		}

	case agent.EventStepEnd:
		m.currentStep = nil
	}
}

// maybeWarnLoop appends the just-started tool call to the recent ring,
// drops anything older than loopWarnWindow, and fires a toast if the
// same tool has been called ≥ loopWarnThreshold times in the window.
// Rate-limited by loopWarnCooldown so a sustained loop warns once, then
// goes quiet for 30s.
func (m *model) maybeWarnLoop(name string) {
	now := time.Now()
	cutoff := now.Add(-loopWarnWindow)

	// Drop expired entries from the head; we keep the slice sorted by
	// time because we only ever append.
	keep := m.recentTools[:0]
	for _, e := range m.recentTools {
		if e.at.After(cutoff) {
			keep = append(keep, e)
		}
	}
	keep = append(keep, recentToolCall{name: name, at: now})
	if len(keep) > loopRingMax {
		keep = keep[len(keep)-loopRingMax:]
	}
	m.recentTools = keep

	count := 0
	for _, e := range keep {
		if e.name == name {
			count++
		}
	}
	if count < loopWarnThreshold {
		return
	}
	if !m.lastLoopWarnAt.IsZero() && now.Sub(m.lastLoopWarnAt) < loopWarnCooldown {
		return
	}
	m.lastLoopWarnAt = now
	m.addToast(
		fmt.Sprintf("⚠ %s called %d× in %s — possibly looping?", name, count, shortDuration(loopWarnWindow)),
		toastWarn)
}

// stampFlash records that the given tool family just fired, so the
// matching stats label in the status bar brightens for flashFor. Only
// memory and threads have visible stats; other families are no-ops.
func (m *model) stampFlash(f toolFamily) {
	now := time.Now()
	switch f {
	case familyMemory, familyKnowledge:
		m.memFlashAt = now
	case familyThreads:
		m.threadFlashAt = now
	}
}

// summarizeArgs renders a compact one-line preview of a tool call. The
// dispatch is tool-aware so meta-tools like task/job/agent surface what
// matters (the task title, the spawned id) instead of just "create" or
// "spawn". Falls back to a generic field-priority scan for unknown tools.
func summarizeArgs(name string, args map[string]any) string {
	pick := func(keys ...string) string {
		for _, k := range keys {
			if s := argStr(args, k); s != "" {
				return s
			}
		}
		return ""
	}
	action := argStr(args, "action")
	idStr := func() string {
		if s := argStr(args, "id"); s != "" {
			return "#" + s
		}
		return ""
	}

	var s string
	switch name {
	case "bash":
		s = pick("command")
	case "read", "write", "edit":
		s = pick("path")
	case "fetch":
		s = pick("url")
	case "search":
		s = pick("query")
	case "say":
		s = pick("text")

	// Meta-tools: <action> <key-detail>
	case "task", "job":
		switch action {
		case "create":
			s = action + " " + pick("title")
		case "update":
			s = action + " " + idStr() + " " + pick("status")
		case "list", "ready":
			s = action
			if jobID := argStr(args, "job_id"); jobID != "" {
				s += " job#" + jobID
			}
		default:
			s = action + " " + idStr()
		}
	case "agent":
		s = action + " " + idStr()
	case "memory_tool", "skill":
		switch action {
		case "search":
			s = action + " " + pick("query")
		case "add", "create":
			s = action + " " + pick("title", "name", "content")
		case "read", "update", "delete":
			s = action + " " + pick("name", "title", "id")
		default:
			s = action
		}

	default:
		// Generic fallback for tools we haven't special-cased: prefer the
		// most-informative arg available, then fold in action/id if present.
		s = pick("path", "url", "query", "command", "pattern", "text", "title", "name")
		if s == "" && action != "" {
			s = action
			if id := idStr(); id != "" {
				s += " " + id
			}
		}
	}

	return tidyArgPreview(s)
}

// argStr returns the named arg as a one-line string ("" if missing/nil).
func argStr(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", v))
}

// tidyArgPreview collapses whitespace and truncates so the tool row stays
// a single line of reasonable width.
func tidyArgPreview(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Collapse newlines/tabs/runs of spaces so multi-line bash commands or
	// pasted content render as one tidy line.
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	const max = 80
	if len(s) > max {
		s = s[:max-1] + "…"
	}
	return s
}
