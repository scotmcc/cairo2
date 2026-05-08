package tui

// tool_toasts.go — ephemeral one-line displays for in-flight foreground
// tool calls. Replaces the old inline-in-transcript rendering of tool
// start/end lines. Selene's voice stays the only thing the transcript
// scrolls; mechanism (tools) lives transiently above the input frame.
//
// One row per active tool. Successful tools linger ~1.5s after ✓ so the
// user has a chance to register the result; errors linger 4s so they
// don't blink past unnoticed. Cap at 3 visible rows with "+N more"
// overflow when a turn briefly fans out beyond that.

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// toolToast is one in-flight (or recently-ended) foreground tool call.
type toolToast struct {
	name       string
	family     toolFamily
	argPreview string
	startedAt  time.Time
	// endedAt is zero while running, set on EventToolEnd. Drives the
	// linger-then-fade behavior; once non-zero we re-render with the
	// success/error tail and prune after the linger window expires.
	endedAt       time.Time
	duration      time.Duration
	resultBytes   int
	resultPreview string
	isError       bool
}

const (
	toolToastSuccessLinger = 3 * time.Second
	toolToastErrorLinger   = 6 * time.Second
	toolToastsMax          = 3
)

// toolToastRowCount returns how many rows the toast region will consume
// for the current toast slice. Used by relayout to reserve space.
func toolToastRowCount(toasts []toolToast) int {
	if len(toasts) == 0 {
		return 0
	}
	n := len(toasts)
	if n > toolToastsMax {
		return toolToastsMax + 1 // +1 for the "+N more" overflow line
	}
	return n
}

// pruneToolToasts removes ended-and-lingered toasts from the slice.
// Active toasts (endedAt zero) are kept regardless. Returns true when
// anything was removed so the tick handler can relayout.
func (m *model) pruneToolToasts() bool {
	now := time.Now()
	kept := m.toolToasts[:0]
	changed := false
	for _, t := range m.toolToasts {
		if t.endedAt.IsZero() {
			kept = append(kept, t)
			continue
		}
		linger := toolToastSuccessLinger
		if t.isError {
			linger = toolToastErrorLinger
		}
		if now.Sub(t.endedAt) < linger {
			kept = append(kept, t)
		} else {
			changed = true
		}
	}
	m.toolToasts = kept
	return changed
}

// renderToolToasts builds the stack of active+lingering toast rows.
// Returns "" when the slice is empty so callers can skip composing
// the section entirely.
func (m model) renderToolToasts(width int) string {
	if len(m.toolToasts) == 0 {
		return ""
	}
	rows := make([]string, 0, toolToastsMax+1)
	visible := len(m.toolToasts)
	if visible > toolToastsMax {
		visible = toolToastsMax
	}
	for i := 0; i < visible; i++ {
		rows = append(rows, renderOneToolToast(m.toolToasts[i], width))
	}
	if extra := len(m.toolToasts) - visible; extra > 0 {
		dim := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
		rows = append(rows, "  "+dim.Render(fmt.Sprintf("+ %d more", extra)))
	}
	return strings.Join(rows, "\n")
}

// renderOneToolToast formats a single row. Active tools show family
// icon + name + arg preview + growing duration. Ended tools swap the
// arg preview for ✓/✗ + duration + size + result preview so the user
// can still see what came back even after the toast goes ephemeral.
func renderOneToolToast(t toolToast, width int) string {
	c := familyColor(t.family)
	nameStyle := lipgloss.NewStyle().Foreground(c).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	icon := familyIcon(t.family)
	head := nameStyle.Render(icon + " " + t.name)

	if t.endedAt.IsZero() {
		// Active.
		body := ""
		if t.argPreview != "" {
			arg := t.argPreview
			if len(arg) > 60 {
				arg = arg[:59] + "…"
			}
			body = "  " + dim.Render(arg)
		}
		elapsed := time.Since(t.startedAt)
		var elapsedStr string
		if elapsed >= time.Second {
			elapsedStr = "  " + dim.Render(shortDuration(elapsed))
		}
		return "  " + head + body + elapsedStr
	}

	// Ended. Successful tools use the family color for ✓; errors use
	// the global error red so a failure jumps off the page.
	var marker string
	if t.isError {
		errStyle := lipgloss.NewStyle().Foreground(colErr).Bold(true)
		marker = errStyle.Render("  ✗")
	} else {
		marker = nameStyle.Render("  ✓")
	}
	tail := fmt.Sprintf("  %s · %s",
		shortDuration(t.duration), humanBytes(t.resultBytes))
	out := "  " + head + marker + dim.Render(tail)
	if t.resultPreview != "" {
		preview := t.resultPreview
		// Crude width fit: leave room for the existing prefix.
		room := width - lipgloss.Width(out) - 4
		if room < 12 {
			room = 12
		}
		if len(preview) > room {
			preview = preview[:room-1] + "…"
		}
		out += dim.Italic(true).Render("  " + preview)
	}
	return out
}
