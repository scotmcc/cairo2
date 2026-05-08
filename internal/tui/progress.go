package tui

// progress.go — global in-flight progress bars, rendered just above the
// input frame. Driven by the tasks table: any background task that calls
// db.Tasks.SetProgress(...) gets a bar. Disappears the moment the task
// transitions out of running.
//
// Design rule: this is purely passive UI. Cancellation lives in the threads
// panel ('c'); the bar itself is read-only ambient state.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/store/jobs"
)

const (
	// progressBarWidth is the fixed cell width of the bar graphic. Wide
	// enough that 5% increments are visually distinguishable.
	progressBarWidth = 20
	// progressMaxRows caps how many bars render at once. Beyond this we
	// show a "+N more" overflow line so the input frame doesn't get pushed
	// off-screen during a burst of background work.
	progressMaxRows = 3
)

// progressRowCount returns how many rows progress rendering will consume
// for the given task set — used by relayout to reserve space.
func progressRowCount(tasks []*jobs.Task) int {
	if len(tasks) == 0 {
		return 0
	}
	n := len(tasks)
	if n > progressMaxRows {
		// progressMaxRows bars + 1 overflow row.
		return progressMaxRows + 1
	}
	return n
}

// renderProgressBars stacks one bar per active task. Returns "" when
// nothing's in flight so the caller can skip JoinVertical-ing an empty
// section. Width is the full panel width.
func (m model) renderProgressBars(width int) string {
	if len(m.progressTasks) == 0 {
		return ""
	}
	rows := make([]string, 0, progressMaxRows+1)
	rendered := len(m.progressTasks)
	if rendered > progressMaxRows {
		rendered = progressMaxRows
	}
	for i := 0; i < rendered; i++ {
		rows = append(rows, renderProgressRow(m.progressTasks[i], width))
	}
	if extra := len(m.progressTasks) - rendered; extra > 0 {
		dim := lipgloss.NewStyle().Foreground(colTextDim).Italic(true)
		rows = append(rows, "  "+dim.Render(fmt.Sprintf("+ %d more in flight", extra)))
	}
	return strings.Join(rows, "\n")
}

// renderProgressRow renders a single progress bar line:
//
//	▰▰▰▰▰▰▱▱▱▱▱▱▱▱▱▱▱▱▱▱  62%  indexing cairo · internal/agent/loop.go
//
// For indeterminate progress (total == 0) the bar pulses a single block
// across the cells based on the current value mod width — the value is
// expected to be incremented by the caller per heartbeat.
func renderProgressRow(t *jobs.Task, width int) string {
	filledStyle := lipgloss.NewStyle().Foreground(colTool).Bold(true)
	emptyStyle := lipgloss.NewStyle().Foreground(colBorderThin)
	pctStyle := lipgloss.NewStyle().Foreground(colTextMuted).Bold(true)
	labelStyle := lipgloss.NewStyle().Foreground(colText)
	detailStyle := lipgloss.NewStyle().Foreground(colTextDim)
	sepStyle := lipgloss.NewStyle().Foreground(colTextDim)

	var bar string
	var pct string
	if t.ProgressTotal > 0 {
		filled := t.ProgressCurrent * progressBarWidth / t.ProgressTotal
		if filled < 0 {
			filled = 0
		}
		if filled > progressBarWidth {
			filled = progressBarWidth
		}
		bar = filledStyle.Render(strings.Repeat("▰", filled)) +
			emptyStyle.Render(strings.Repeat("▱", progressBarWidth-filled))
		percent := t.ProgressCurrent * 100 / t.ProgressTotal
		if percent < 0 {
			percent = 0
		}
		if percent > 100 {
			percent = 100
		}
		pct = pctStyle.Render(fmt.Sprintf("%3d%%", percent))
	} else {
		// Indeterminate: a single block walks across the bar.
		head := t.ProgressCurrent % progressBarWidth
		if head < 0 {
			head = 0
		}
		var cells strings.Builder
		for i := 0; i < progressBarWidth; i++ {
			if i == head {
				cells.WriteString(filledStyle.Render("▰"))
			} else {
				cells.WriteString(emptyStyle.Render("▱"))
			}
		}
		bar = cells.String()
		pct = pctStyle.Render(" …  ")
	}

	label := t.ProgressLabel
	if label == "" {
		label = t.Title
	}

	// Compose: "  bar  pct  label · detail"
	prefix := "  " + bar + "  " + pct + "  "
	prefixW := lipgloss.Width(prefix)
	avail := width - prefixW
	if avail < 4 {
		// Too narrow — drop detail/label entirely and let the bar speak.
		return prefix
	}

	if t.ProgressDetail == "" {
		return prefix + labelStyle.Render(truncateStr(label, avail))
	}
	// Reserve at least 8 cells for the detail; otherwise hide it.
	sep := sepStyle.Render(" · ")
	sepW := lipgloss.Width(sep)
	labelW := lipgloss.Width(label)
	if avail < labelW+sepW+8 {
		return prefix + labelStyle.Render(truncateStr(label, avail))
	}
	detail := truncateStr(t.ProgressDetail, avail-labelW-sepW)
	return prefix + labelStyle.Render(label) + sep + detailStyle.Render(detail)
}
