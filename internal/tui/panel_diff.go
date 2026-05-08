package tui

// panel_diff.go — Ctrl+D diff panel.
//
// Two modes, one panel:
//
//   Job mode (active jobs exist): two-pane layout — job list on the left,
//   diff detail (briefing + git diff) on the right. ↑↓/j/k move selection.
//   Esc closes. No approve/reject in 4a — those are chunk 4b.
//
//   Session mode (no active jobs): single-pane session diff — shows git diff
//   HEAD for every file changed in the current session. Same as the original
//   panel_diff behaviour. Preserves muscle memory when no jobs are queued.
//
// Changed files are tracked via EventToolStart on write/edit tools. On open
// (or 'r' to refresh), session mode runs `git diff HEAD -- <file>` for each
// tracked path and colorizes the output: added lines green, removed lines
// red, hunk headers cyan. Line cap is 200 for session diffs; 500 for job
// diffs where the context is typically richer.
//
// Accent: green (colOK / colToolBright) — "what changed" signal color.

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/store/jobs"
)

const panelDiffID panelID = "diff"

// colDiffAccent is the accent color for the diff panel — green, because diffs
// are about changes and additions, not errors or structural chrome.
var colDiffAccent = lipgloss.Color("#4CAF50")

// diffState holds all per-frame state for the diff panel.
type diffState struct {
	viewport  viewport.Model
	content   string // rendered diff body (colorized, line-capped)
	lastWidth int    // width content was rendered at; re-render on change

	// Job mode — populated when ListActiveJobs returns > 0 results.
	jobs        []*jobs.ActiveJob // cached list, refreshed on open + tick
	selectedIdx int               // index into jobs for the highlighted row
}

// MsgOpenDiffPanel is an exported tea.Msg. Send it via program.Send() from
// outside the TUI loop (e.g. from the agent loop) to open the diff panel
// with a specific job pre-selected. JobID == 0 selects the first job.
type MsgOpenDiffPanel struct{ JobID int64 }

func init() {
	registerPanel(&panelSpec{
		ID:          panelDiffID,
		Position:    posFullscreen,
		Accent:      colDiffAccent,
		Title:       "diff",
		Description: "Show git diff of changed files or active job diffs.",
		ToggleKey:   "ctrl+d",
		ShowInHelp:  true,
		OnOpen:      diffOpen,
		OnClose:     diffClose,
		Update:      diffUpdate,
		View:        diffView,
	})
}

// OpenDiffPanel returns a tea.Cmd that, when executed, fires a
// MsgOpenDiffPanel. Use program.Send(MsgOpenDiffPanel{JobID: id}) to call
// from outside the Bubble Tea loop.
func OpenDiffPanel(jobID int64) tea.Cmd {
	return func() tea.Msg { return MsgOpenDiffPanel{JobID: jobID} }
}

func diffOpen(m *model) tea.Cmd {
	vp := viewport.New(0, 0)
	m.diff.viewport = vp
	diffLoadJobs(m)
	diffResize(m)
	diffRefreshAndLoad(m)
	return nil
}

// diffLoadJobs queries ListActiveJobs and stores the result in m.diff.jobs.
// On error (or nil DB) it clears the list so the panel falls back to
// session-diff mode. The nil-DB guard is primarily for unit tests that
// build minimal models without a real database.
func diffLoadJobs(m *model) {
	if m.db == nil {
		m.diff.jobs = nil
		return
	}
	jobs, err := m.db.Jobs.ListActiveJobs()
	if err != nil {
		m.diff.jobs = nil
		return
	}
	m.diff.jobs = jobs
}

// diffResize sets viewport dimensions from current model state. Called on
// open and on WindowSizeMsg. Fullscreen panel: width = full screen, height
// = full screen minus chrome (title, rule, hint = 3 lines). The View
// function recomputes per-frame using its `height` argument too, but
// having sensible values here avoids a flash of zero-sized viewport on
// first render.
func diffResize(m *model) {
	w := m.width
	if w <= 0 {
		w = 80
	}
	h := m.height - 3
	if h < 5 {
		h = 5
	}
	m.diff.viewport.Width = w
	m.diff.viewport.Height = h
}

func diffClose(m *model) {
	m.diff.content = ""
	m.diff.jobs = nil
}

// diffRefreshAndLoad rebuilds the diff content and loads it into the
// viewport. Called on open, on 'r', on tick, and on resize.
func diffRefreshAndLoad(m *model) {
	if len(m.diff.jobs) == 0 {
		m.diff.content = diffRefreshSession(m)
	} else {
		m.diff.content = diffRefreshJob(m)
	}
	m.diff.lastWidth = m.width
	m.diff.viewport.SetContent(m.diff.content)
	// Only go to top when content changes or panel first opens; keep scroll
	// position on tick refreshes when the selected job hasn't changed.
	m.diff.viewport.GotoTop()
}

// --- colorizer helpers shared by both modes ---

func diffStyles() (styleAdd, styleDel, styleHunk, styleSep lipgloss.Style) {
	styleAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("#4CAF50"))
	styleDel = lipgloss.NewStyle().Foreground(lipgloss.Color("#d77070"))
	styleHunk = lipgloss.NewStyle().Foreground(lipgloss.Color("#64b5f6"))
	styleSep = lipgloss.NewStyle().Foreground(colTextDim)
	return
}

func colorizeDiff(raw string, maxLines int) ([]string, int) {
	styleAdd, styleDel, styleHunk, styleSep := diffStyles()
	var out []string
	totalLines := 0
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	for _, line := range lines {
		if totalLines >= maxLines {
			out = append(out, styleSep.Render(fmt.Sprintf("... output capped at %d lines", maxLines)))
			break
		}
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			out = append(out, styleSep.Render(line))
		case strings.HasPrefix(line, "+"):
			out = append(out, styleAdd.Render(line))
		case strings.HasPrefix(line, "-"):
			out = append(out, styleDel.Render(line))
		case strings.HasPrefix(line, "@@"):
			out = append(out, styleHunk.Render(line))
		default:
			out = append(out, line)
		}
		totalLines++
	}
	return out, totalLines
}

// --- session-diff mode (fallback when no active jobs) ---

// diffRefreshSession builds the colorized diff content for all session-changed
// files. Returns a plain string with ANSI escape codes ready for the viewport.
func diffRefreshSession(m *model) string {
	if len(m.changedFiles) == 0 {
		return "No files changed this session."
	}

	_, _, _, styleSep := diffStyles()

	const maxLines = 200
	var out []string
	totalLines := 0

	for i, file := range m.changedFiles {
		if totalLines >= maxLines {
			out = append(out, styleSep.Render(fmt.Sprintf("... output capped at %d lines", maxLines)))
			break
		}

		if i > 0 {
			sep := strings.Repeat("─", 40)
			out = append(out, styleSep.Render(sep))
			totalLines++
		}

		var cwd string
		if m.session != nil {
			cwd = m.session.CWD
		}
		cmd := exec.Command("git", "diff", "HEAD", "--", file)
		if cwd != "" {
			cmd.Dir = cwd
		}
		raw, err := cmd.Output()

		if err != nil || len(raw) == 0 {
			if len(raw) == 0 {
				out = append(out, styleSep.Render(fmt.Sprintf("%s: no diff available", file)))
			} else {
				out = append(out, styleSep.Render(fmt.Sprintf("%s: %v", file, err)))
			}
			totalLines++
			continue
		}

		lines, n := colorizeDiff(string(raw), maxLines-totalLines)
		out = append(out, lines...)
		totalLines += n
	}

	if len(out) == 0 {
		return "No diff available."
	}
	return strings.Join(out, "\n")
}

// --- job-diff mode (two-pane: left=list, right=detail) ---

// diffRefreshJob builds the right-pane content for the currently selected job.
func diffRefreshJob(m *model) string {
	if len(m.diff.jobs) == 0 {
		return "No active jobs."
	}
	if m.diff.selectedIdx >= len(m.diff.jobs) {
		m.diff.selectedIdx = 0
	}
	j := m.diff.jobs[m.diff.selectedIdx]

	_, _, _, styleSep := diffStyles()
	styleBriefing := lipgloss.NewStyle().Foreground(colTextDim)
	styleBold := lipgloss.NewStyle().Bold(true)

	var out []string

	// Briefing section.
	out = append(out, styleBold.Render("briefing"))
	out = append(out, styleSep.Render(strings.Repeat("─", 40)))
	if j.Briefing != nil && *j.Briefing != "" {
		for _, line := range strings.Split(*j.Briefing, "\n") {
			out = append(out, styleBriefing.Render(line))
		}
	} else {
		out = append(out, styleBriefing.Render("(no briefing)"))
	}

	out = append(out, "")
	out = append(out, styleBold.Render("diff"))
	out = append(out, styleSep.Render(strings.Repeat("─", 40)))

	// Git diff — run from repo root (not worktree) per design decision.
	const maxLines = 500
	cmd := exec.Command("git", "diff", j.ParentBranch+"..."+j.Branch)
	// Use the worktree path as cwd so git can find the repo; the triple-dot
	// diff still targets the branch tips regardless of working directory.
	if j.WorktreePath != "" {
		cmd.Dir = j.WorktreePath
	} else if m.session.CWD != "" {
		cmd.Dir = m.session.CWD
	}
	raw, err := cmd.Output()
	if err != nil {
		out = append(out, styleSep.Render(fmt.Sprintf("git diff error: %v", err)))
	} else if len(raw) == 0 {
		out = append(out, styleSep.Render("(no diff)"))
	} else {
		lines, _ := colorizeDiff(string(raw), maxLines)
		out = append(out, lines...)
	}

	return strings.Join(out, "\n")
}

// diffSelectedJobID returns the ID of the currently selected job, or 0.
func diffSelectedJobID(m *model) int64 {
	if len(m.diff.jobs) == 0 || m.diff.selectedIdx >= len(m.diff.jobs) {
		return 0
	}
	return m.diff.jobs[m.diff.selectedIdx].ID
}

// diffSelectJobByID sets selectedIdx to the job with the given ID. If not
// found, index stays at 0.
func diffSelectJobByID(m *model, jobID int64) {
	if jobID == 0 || len(m.diff.jobs) == 0 {
		m.diff.selectedIdx = 0
		return
	}
	for i, j := range m.diff.jobs {
		if j.ID == jobID {
			m.diff.selectedIdx = i
			return
		}
	}
	m.diff.selectedIdx = 0
}

func diffUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	// Handle the external open+select message.
	if open, ok := msg.(MsgOpenDiffPanel); ok {
		var cmd tea.Cmd
		if !m.isPanelOpen(panelDiffID) {
			cmd = m.openPanel(panelDiffID)
		}
		diffSelectJobByID(m, open.JobID)
		diffRefreshAndLoad(m)
		return true, cmd
	}

	if _, ok := msg.(tea.WindowSizeMsg); ok {
		diffResize(m)
		m.diff.viewport.SetContent(m.diff.content)
	}

	// On tick: refresh job list while panel is open.
	if _, ok := msg.(tickMsg); ok && m.isPanelOpen(panelDiffID) {
		prevID := diffSelectedJobID(m)
		diffLoadJobs(m)
		diffSelectJobByID(m, prevID)
		diffRefreshAndLoad(m)
		return false, nil
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		newVp, cmd := m.diff.viewport.Update(msg)
		m.diff.viewport = newVp
		return false, cmd
	}

	switch key.String() {
	case "esc":
		m.closePanel(panelDiffID)
		return true, nil

	case "ctrl+a":
		// Approve: job mode only. Session mode has no job to approve.
		if len(m.diff.jobs) > 0 {
			jobID := diffSelectedJobID(m)
			return true, func() tea.Msg { return MsgJobApprove{JobID: jobID} }
		}
		return true, nil

	case "ctrl+r":
		if len(m.diff.jobs) > 0 {
			// Job mode: reject the selected job.
			jobID := diffSelectedJobID(m)
			return true, func() tea.Msg { return MsgJobReject{JobID: jobID} }
		}
		// Session mode: refresh as before.
		diffLoadJobs(m)
		diffRefreshAndLoad(m)
		return true, nil

	case "up", "k":
		if len(m.diff.jobs) > 0 {
			if m.diff.selectedIdx > 0 {
				m.diff.selectedIdx--
				diffRefreshAndLoad(m)
			}
			return true, nil
		}
		// Session mode: pass scroll to viewport.
		newVp, cmd := m.diff.viewport.Update(msg)
		m.diff.viewport = newVp
		return true, cmd

	case "down", "j":
		if len(m.diff.jobs) > 0 {
			if m.diff.selectedIdx < len(m.diff.jobs)-1 {
				m.diff.selectedIdx++
				diffRefreshAndLoad(m)
			}
			return true, nil
		}
		newVp, cmd := m.diff.viewport.Update(msg)
		m.diff.viewport = newVp
		return true, cmd

	case "pgup", "pgdown", "home", "end":
		newVp, cmd := m.diff.viewport.Update(msg)
		m.diff.viewport = newVp
		return true, cmd

	}
	return true, nil
}

// diffView renders the full panel content for one frame.
func diffView(width, height int, m *model) string {
	accent := lipgloss.NewStyle().Foreground(colDiffAccent).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	// Fullscreen: claim all available rows. Reserve 3 for title + rule + hint.
	bodyH := height - 3
	if bodyH < 5 {
		bodyH = 5
	}
	if m.diff.viewport.Width != width || m.diff.viewport.Height != bodyH {
		m.diff.viewport.Width = width
		m.diff.viewport.Height = bodyH
		m.diff.viewport.SetContent(m.diff.content)
	}

	if len(m.diff.jobs) == 0 {
		// --- session-diff mode (single pane) ---
		fileCount := len(m.changedFiles)
		var subtitle string
		switch fileCount {
		case 0:
			subtitle = "  ·  no files changed"
		case 1:
			subtitle = "  ·  1 file"
		default:
			subtitle = fmt.Sprintf("  ·  %d files", fileCount)
		}
		title := accent.Render("session diff") + dim.Render(subtitle)
		rule := dim.Render(strings.Repeat("─", max(0, width)))
		body := m.diff.viewport.View()
		hint := dim.Render("  ↑↓ PgUp/PgDn scroll · ctrl+r refresh · esc close")
		return title + "\n" + rule + "\n" + body + "\n" + hint
	}

	// --- job-diff mode (two panes) ---
	jobCount := len(m.diff.jobs)
	title := accent.Render("job diff") + dim.Render(fmt.Sprintf("  ·  %d job(s)", jobCount))
	rule := dim.Render(strings.Repeat("─", max(0, width)))

	// Left pane: job list (~30 cols or 25% of width, whichever is bigger).
	leftWidth := max(28, width/4)
	if leftWidth > width-20 {
		leftWidth = width - 20
	}
	leftLines := diffRenderJobList(m, leftWidth)

	// Right pane: diff detail (remaining width minus separator).
	rightWidth := width - leftWidth - 1
	rightLines := strings.Split(m.diff.viewport.View(), "\n")

	// Merge left and right side by side.
	nRows := max(len(leftLines), len(rightLines))
	var rows []string
	sep := dim.Render("│")
	for i := 0; i < nRows; i++ {
		var left, right string
		if i < len(leftLines) {
			left = leftLines[i]
		}
		if i < len(rightLines) {
			right = rightLines[i]
		}
		// Pad left cell to leftWidth.
		left = padRight(left, leftWidth)
		// Truncate right if too wide.
		if len(right) > rightWidth {
			right = right[:rightWidth]
		}
		rows = append(rows, left+sep+right)
	}

	body := strings.Join(rows, "\n")
	hint := dim.Render("  ↑↓/j/k select · ctrl+a approve · ctrl+r reject · esc close")
	return title + "\n" + rule + "\n" + body + "\n" + hint
}

// diffRenderJobList renders the left-pane job list. Each job occupies two
// lines: status+id+title and diff stats. The selected row is highlighted.
func diffRenderJobList(m *model, width int) []string {
	accent := lipgloss.NewStyle().Foreground(colDiffAccent)
	dim := lipgloss.NewStyle().Foreground(colTextDim)
	sel := lipgloss.NewStyle().Background(lipgloss.Color("#1e3a2b")).Foreground(lipgloss.Color("#ffffff"))
	normal := lipgloss.NewStyle().Foreground(colText)

	var lines []string
	for i, j := range m.diff.jobs {
		isSelected := i == m.diff.selectedIdx

		statusLabel := statusBadge(j.Status)
		idStr := fmt.Sprintf("#%d", j.ID)

		title := j.Title
		if j.Summary != nil && *j.Summary != "" {
			title = *j.Summary
		}
		// Truncate title to fit.
		maxTitle := width - len(statusLabel) - len(idStr) - 3
		if maxTitle < 4 {
			maxTitle = 4
		}
		if len(title) > maxTitle {
			title = title[:maxTitle-1] + "…"
		}

		row1 := fmt.Sprintf("%s %s %s", statusLabel, idStr, title)
		row1 = padRight(row1, width)

		var stats string
		if j.DiffFiles != nil {
			ins := int64(0)
			del := int64(0)
			if j.DiffInsertions != nil {
				ins = *j.DiffInsertions
			}
			if j.DiffDeletions != nil {
				del = *j.DiffDeletions
			}
			stats = fmt.Sprintf("  %df +%d -%d", *j.DiffFiles, ins, del)
		} else {
			stats = "  (no stats)"
		}
		row2 := padRight(stats, width)

		if isSelected {
			lines = append(lines, sel.Render(row1))
			lines = append(lines, sel.Render(row2))
		} else {
			_ = accent
			_ = normal
			lines = append(lines, normal.Render(row1))
			lines = append(lines, dim.Render(row2))
		}
	}
	return lines
}

// statusBadge returns a short bracketed label for a job status.
func statusBadge(status string) string {
	switch status {
	case jobs.StatusAwaitingReview:
		return "[review]"
	case jobs.StatusRunning:
		return "[running]"
	case jobs.StatusPending:
		return "[pending]"
	case jobs.StatusConflict:
		return "[conflict]"
	default:
		if len(status) > 8 {
			return "[" + status[:8] + "]"
		}
		return "[" + status + "]"
	}
}

// padRight is defined in tui_view.go — reused here without redeclaration.
