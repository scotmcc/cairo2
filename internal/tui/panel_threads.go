package tui

// panel_threads.go — left drawer showing a collapsible jobs/tasks tree.
// Jobs are top-level rows; tasks are indented beneath. When a task is
// selected the panel auto-grows wider and shows a detail pane on the right
// with the task's metadata and a live tail of its log file.

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/scotmcc/cairo2/internal/hostedit"
	"github.com/scotmcc/cairo2/internal/store/jobs"
)

const panelThreadsID panelID = "threads"

// Width budget for the threads drawer. Narrow when only the tree is visible;
// wider when a task is selected and the detail pane is showing.
const (
	threadsListWidth   = 46   // tree-only width
	threadsDetailWidth = 53   // additional cols for the detail pane
	threadsLogTailMax  = 2000 // in-memory log tail cap; user can scroll within this window
)

// treeRow is a flattened entry in the rendered tree — either a job header
// or an indented task row.
type treeRow struct {
	isJob  bool
	job    *jobs.Job  // set when isJob==true
	task   *jobs.Task // set when isJob==false
	jobIdx int        // index into threadsState.jobs — used for expand/collapse
}

// threadsState is the panel's per-model state.
type threadsState struct {
	jobs     []*jobs.Job
	tasks    map[int64][]*jobs.Task // keyed by job ID
	expanded map[int64]bool         // true = job's tasks are visible

	rows     []treeRow // flattened view — rebuilt on load/expand/collapse
	selected int       // index into rows

	// Log tail state for the currently-selected task.
	logLines     []string
	logFollow    bool   // true: stick to bottom; false: freeze on toggle
	logPath      string // path the current logLines were read from (cache key)
	detailScroll int    // lines scrolled back from the end of logLines (0 = pinned to latest)

	// Toast-style flash for one-shot actions ("cancelled", "log opened").
	flash        string
	flashExpires time.Time
}

func init() {
	registerPanel(&panelSpec{
		ID:             panelThreadsID,
		Position:       posLeft,
		Accent:         colThread,
		Title:          "threads",
		Description:    "Jobs and tasks tree — running and recent background work.",
		ToggleKey:      "ctrl+t",
		ShowInHelp:     true,
		PreferredWidth: threadsListWidth,
		DynamicWidth:   threadsWidth,
		OnOpen:         threadsOpen,
		Update:         threadsUpdate,
		View:           threadsView,
	})
}

// threadsWidth returns the panel's current preferred width — narrow when
// the selection is on a job row, wide when a task row is selected so the
// detail pane has room to render.
func threadsWidth(m *model) int {
	if threadsSelectedTask(m) != nil {
		return threadsListWidth + threadsDetailWidth
	}
	return threadsListWidth
}

// threadsSelectedTask returns the currently-selected task, or nil if the
// selection is on a job row or the list is empty.
func threadsSelectedTask(m *model) *jobs.Task {
	s := &m.threads
	if s.selected < 0 || s.selected >= len(s.rows) {
		return nil
	}
	row := s.rows[s.selected]
	if row.isJob {
		return nil
	}
	return row.task
}

// threadsOpen loads jobs+tasks and starts the live-refresh cycle.
func threadsOpen(m *model) tea.Cmd {
	m.threads.logFollow = true
	threadsRefresh(m)
	return nil
}

// threadsRefresh loads jobs + tasks from the DB and rebuilds the flat tree.
// Called on open, on 'r' refresh, and on every tick while the panel is open.
func threadsRefresh(m *model) tea.Cmd {
	jobList, err := m.db.Jobs.List()
	if err != nil {
		m.threads.jobs = nil
		m.threads.tasks = nil
		m.threads.rows = nil
		return nil
	}

	if len(jobList) > 20 {
		jobList = jobList[:20]
	}
	m.threads.jobs = jobList

	if m.threads.tasks == nil {
		m.threads.tasks = make(map[int64][]*jobs.Task)
	}
	if m.threads.expanded == nil {
		m.threads.expanded = make(map[int64]bool)
	}

	for _, j := range jobList {
		tasks, err := m.db.Tasks.ForJob(j.ID)
		if err == nil {
			m.threads.tasks[j.ID] = tasks
		}
		if j.Status == jobs.StatusRunning {
			m.threads.expanded[j.ID] = true
		}
	}

	threadsRebuild(&m.threads)
	if m.threads.selected >= len(m.threads.rows) {
		m.threads.selected = max(0, len(m.threads.rows)-1)
	}

	// If the selected task has a log file and we're following, refresh the tail.
	if t := threadsSelectedTask(m); t != nil && t.LogPath != "" && m.threads.logFollow {
		m.threads.logLines = readLastLines(t.LogPath, threadsLogTailMax)
		m.threads.logPath = t.LogPath
	}
	return nil
}

// threadsRebuild flattens jobs+tasks into the rows slice used for display
// and navigation. Must be called after any change to jobs, tasks, or expanded.
func threadsRebuild(s *threadsState) {
	s.rows = s.rows[:0]
	for i, j := range s.jobs {
		s.rows = append(s.rows, treeRow{isJob: true, job: j, jobIdx: i})
		if s.expanded[j.ID] {
			for _, t := range s.tasks[j.ID] {
				s.rows = append(s.rows, treeRow{isJob: false, task: t, jobIdx: i})
			}
		}
	}
}

// threadsLoadLogForSelection pulls a fresh tail of the selected task's log,
// independent of the follow toggle. Called on selection change so the detail
// pane shows the right file immediately. Resets the scroll offset so the
// new selection starts pinned to its latest output.
func threadsLoadLogForSelection(m *model) {
	t := threadsSelectedTask(m)
	if t == nil || t.LogPath == "" {
		m.threads.logLines = nil
		m.threads.logPath = ""
		m.threads.detailScroll = 0
		return
	}
	if t.LogPath == m.threads.logPath && !m.threads.logFollow {
		return // cached
	}
	m.threads.logLines = readLastLines(t.LogPath, threadsLogTailMax)
	m.threads.logPath = t.LogPath
	m.threads.detailScroll = 0
}

func threadsUpdate(msg tea.Msg, m *model) (bool, tea.Cmd) {
	if _, ok := msg.(tickMsg); ok {
		threadsRefresh(m)
		return false, nil // don't claim — let the tick reach other handlers
	}

	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}

	switch key.String() {
	case "up", "k":
		if m.threads.selected > 0 {
			m.threads.selected--
			threadsLoadLogForSelection(m)
		}
		return true, nil
	case "down", "j":
		if m.threads.selected < len(m.threads.rows)-1 {
			m.threads.selected++
			threadsLoadLogForSelection(m)
		}
		return true, nil
	case " ":
		// Space toggles expand/collapse on a job row.
		if row, ok := threadsCurrentRow(m); ok && row.isJob {
			id := row.job.ID
			m.threads.expanded[id] = !m.threads.expanded[id]
			threadsRebuild(&m.threads)
			if m.threads.selected >= len(m.threads.rows) {
				m.threads.selected = max(0, len(m.threads.rows)-1)
			}
		}
		return true, nil
	case "enter":
		// Enter on a job row: expand. Enter on a task row: open log in editor.
		row, ok := threadsCurrentRow(m)
		if !ok {
			return true, nil
		}
		if row.isJob {
			id := row.job.ID
			m.threads.expanded[id] = !m.threads.expanded[id]
			threadsRebuild(&m.threads)
			return true, nil
		}
		threadsOpenInEditor(m, row.task)
		return true, nil
	case "ctrl+f":
		m.threads.logFollow = !m.threads.logFollow
		if m.threads.logFollow {
			threadsLoadLogForSelection(m)
			threadsFlash(m, "follow ON")
		} else {
			threadsFlash(m, "follow OFF (frozen)")
		}
		return true, nil
	case "ctrl+o":
		if t := threadsSelectedTask(m); t != nil {
			threadsOpenInEditor(m, t)
		}
		return true, nil
	case "ctrl+x":
		if t := threadsSelectedTask(m); t != nil {
			return true, threadsCancelTask(m, t)
		}
		return true, nil
	case "ctrl+r":
		threadsRefresh(m)
		return true, nil
	case "pgup":
		// Scroll the detail pane back through the log. Freezes follow so
		// the auto-refresh doesn't fight the user's position.
		if threadsSelectedTask(m) != nil {
			m.threads.detailScroll += 10
			m.threads.logFollow = false
			threadsClampScroll(m)
		}
		return true, nil
	case "pgdown":
		if threadsSelectedTask(m) != nil {
			m.threads.detailScroll -= 10
			if m.threads.detailScroll <= 0 {
				m.threads.detailScroll = 0
				m.threads.logFollow = true
				threadsLoadLogForSelection(m)
			}
		}
		return true, nil
	case "home", "g":
		if threadsSelectedTask(m) != nil {
			m.threads.detailScroll = len(m.threads.logLines) // clamped below
			m.threads.logFollow = false
			threadsClampScroll(m)
		}
		return true, nil
	case "end", "G":
		if threadsSelectedTask(m) != nil {
			m.threads.detailScroll = 0
			m.threads.logFollow = true
			threadsLoadLogForSelection(m)
		}
		return true, nil
	case "esc":
		m.closePanel(panelThreadsID)
		return true, nil
	}
	return false, nil
}

// threadsClampScroll caps detailScroll so it never points past the start of
// the log buffer. Called after every scroll-up so the user can't scroll
// into negative territory.
func threadsClampScroll(m *model) {
	maxBack := len(m.threads.logLines)
	if maxBack < 0 {
		maxBack = 0
	}
	if m.threads.detailScroll > maxBack {
		m.threads.detailScroll = maxBack
	}
}

func threadsCurrentRow(m *model) (treeRow, bool) {
	if m.threads.selected < 0 || m.threads.selected >= len(m.threads.rows) {
		return treeRow{}, false
	}
	return m.threads.rows[m.threads.selected], true
}

// taskCancelledMsg carries the result of an async threadsCancelTask operation.
type taskCancelledMsg struct {
	taskID  int64
	flash   string
	refresh bool
}

// threadsCancelTask schedules a tea.Cmd that SIGTERMs the task's worker (if a
// PID is recorded) and marks the task as cancelled in the DB off the render
// loop. Returns immediately; the result arrives as a taskCancelledMsg.
func threadsCancelTask(m *model, t *jobs.Task) tea.Cmd {
	switch t.Status {
	case jobs.StatusDone, jobs.StatusFailed, jobs.StatusCancelled:
		threadsFlash(m, fmt.Sprintf("task %d already %s", t.ID, t.Status))
		return nil
	}
	taskID := t.ID
	pid := t.PID
	database := m.db
	return func() tea.Msg {
		msg := taskCancelledMsg{taskID: taskID}
		if pid != nil && *pid > 0 {
			if err := syscall.Kill(*pid, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
				msg.flash = fmt.Sprintf("kill %d: %v", *pid, err)
				return msg
			}
		}
		if err := database.Tasks.SetStatusAndResult(taskID, jobs.StatusCancelled, "cancelled by user"); err != nil {
			msg.flash = fmt.Sprintf("cancel: %v", err)
			return msg
		}
		msg.flash = fmt.Sprintf("task %d cancelled", taskID)
		msg.refresh = true
		return msg
	}
}

// handleTaskCancelled applies the result of an async cancel operation.
func handleTaskCancelled(msg taskCancelledMsg, m *model) tea.Cmd {
	if msg.flash != "" {
		threadsFlash(m, msg.flash)
	}
	if msg.refresh {
		threadsRefresh(m)
	}
	return nil
}

// threadsOpenInEditor launches the host's editor on the task's log path.
// Falls back to a flash message if no log is recorded.
func threadsOpenInEditor(m *model, t *jobs.Task) {
	if t.LogPath == "" {
		threadsFlash(m, "no log file for this task")
		return
	}
	if hostedit.WantsTUISuspend() {
		// Pure-terminal fallback — opening vi/nano would clobber the TUI.
		// Skip for now and surface the path so the user can open it manually.
		threadsFlash(m, "no GUI editor host detected — log: "+t.LogPath)
		return
	}
	if err := hostedit.Open(t.LogPath, 0); err != nil {
		threadsFlash(m, fmt.Sprintf("open: %v", err))
		return
	}
	threadsFlash(m, "opened in "+hostedit.Detect().String())
}

// threadsFlash shows a short toast inside the panel for ~3 seconds.
func threadsFlash(m *model, msg string) {
	m.threads.flash = msg
	m.threads.flashExpires = time.Now().Add(3 * time.Second)
}

// --- view ---

func threadsView(width, height int, m *model) string {
	// Decide layout: tree-only or tree + detail.
	hasDetail := threadsSelectedTask(m) != nil
	listW := width
	detailW := 0
	if hasDetail && width > threadsListWidth+8 {
		listW = threadsListWidth
		detailW = width - listW - 1 // -1 for divider
	}

	tree := threadsRenderTree(m, listW, height)
	if !hasDetail || detailW <= 0 {
		return tree
	}
	divider := strings.Repeat(m.styles.thinRule.Render("│")+"\n", max(1, height))
	detail := threadsRenderDetail(m, detailW, height)
	return lipgloss.JoinHorizontal(lipgloss.Top, tree, divider, detail)
}

func threadsRenderTree(m *model, width, height int) string {
	var b strings.Builder
	title := lipgloss.NewStyle().Foreground(colThread).Bold(true).Render("threads")
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(m.styles.thinRule.Render(strings.Repeat("─", max(0, width))))
	b.WriteByte('\n')
	b.WriteString(renderSummarizerStatus(m, width))
	b.WriteByte('\n')

	if len(m.threads.jobs) == 0 {
		b.WriteString(m.styles.statusHint.Render("  (no jobs yet — start one with /learn or have Selene spawn a task)\n"))
		b.WriteString(m.styles.statusHint.Render("\n  ctrl+r refresh · esc close"))
		return b.String()
	}

	// Reserve: title(1) + rule(1) + summarizerStatus(1) + footer(2) = 5
	listHeight := max(1, height-5)
	rows := m.threads.rows
	sel := m.threads.selected

	start := 0
	if sel >= listHeight {
		start = sel - listHeight + 1
	}
	end := start + listHeight
	if end > len(rows) {
		end = len(rows)
	}

	for i := start; i < end; i++ {
		row := rows[i]
		var plain, styled string
		if row.isJob {
			plain, styled = formatJobRow(row.job, m.threads.expanded[row.job.ID], width, m.tickCounter)
		} else {
			plain, styled = formatTaskRow(row.task, width, m.tickCounter)
		}
		if i == sel {
			selStyle := lipgloss.NewStyle().
				Foreground(colFocus).
				Background(colSurfaceHi).
				Bold(true)
			b.WriteString(selStyle.Render(padRight(plain, width)))
		} else {
			b.WriteString(styled)
		}
		b.WriteByte('\n')
	}

	// Footer: hints, with a flash overlay when active.
	var hint string
	if threadsSelectedTask(m) != nil {
		hint = "  ↑↓ nav · pgup/pgdn scroll log · ctrl+o edit · ctrl+x cancel · ctrl+f follow · esc close"
	} else {
		hint = "  ↑↓ nav · space expand · ctrl+r refresh · esc close"
	}
	if m.threads.flash != "" && time.Now().Before(m.threads.flashExpires) {
		flash := lipgloss.NewStyle().Foreground(colVoiceSelene).Italic(true).
			Render("  " + m.threads.flash)
		b.WriteString("\n" + flash)
	} else {
		b.WriteString("\n" + m.styles.statusHint.Render(truncateStr(hint, width)))
	}
	return b.String()
}

func threadsRenderDetail(m *model, width, height int) string {
	t := threadsSelectedTask(m)
	if t == nil {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Foreground(taskStatusColor(t.Status)).Bold(true)
	labelStyle := m.styles.statusMemLbl
	valStyle := m.styles.body
	dim := lipgloss.NewStyle().Foreground(colTextDim)

	var b strings.Builder
	b.WriteString("  " + titleStyle.Render(truncateStr(t.Title, width-4)))
	b.WriteByte('\n')
	b.WriteString(m.styles.thinRule.Render(strings.Repeat("─", max(0, width))))
	b.WriteByte('\n')

	// Metadata block.
	statusGlyph := taskStatusGlyph(t.Status, m.tickCounter)
	statusLine := fmt.Sprintf("%s %s", statusGlyph, t.Status)
	rows := []struct{ k, v string }{
		{"id", fmt.Sprintf("%d", t.ID)},
		{"job", fmt.Sprintf("%d", t.JobID)},
		{"role", t.AssignedRole},
		{"status", statusLine},
		{"elapsed", humanElapsed(t.CreatedAt, t.CompletedAt)},
	}
	if t.PID != nil && *t.PID > 0 {
		rows = append(rows, struct{ k, v string }{"pid", fmt.Sprintf("%d", *t.PID)})
	}
	if t.LogPath != "" {
		rows = append(rows, struct{ k, v string }{"log", truncateStr(t.LogPath, width-12)})
	}
	for _, ln := range rows {
		b.WriteString("  ")
		b.WriteString(labelStyle.Render(padRight(ln.k, 8)))
		b.WriteString(valStyle.Render(ln.v))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')

	// Reserve: title(1) + rule(1) + meta(len(rows)) + blank(1) + log header(1) = 4 + len(rows)
	usedTop := 4 + len(rows)
	logHeight := max(2, height-usedTop)

	// Compute the visible window so the header can show pos / total.
	total := len(m.threads.logLines)
	logEnd := total - m.threads.detailScroll
	if logEnd < 0 {
		logEnd = 0
	}
	logStart := logEnd - logHeight
	if logStart < 0 {
		logStart = 0
	}

	// Log tail header — shows mode + position when scrolled.
	logHdr := "log tail"
	if m.threads.detailScroll > 0 {
		logHdr += fmt.Sprintf(" · %d–%d of %d (pgdn to catch up)", logStart+1, logEnd, total)
	} else if m.threads.logFollow {
		logHdr += " · following"
	} else {
		logHdr += " · frozen (f to follow)"
	}
	b.WriteString("  " + dim.Render(logHdr))
	b.WriteByte('\n')

	if t.LogPath == "" {
		b.WriteString("  " + dim.Italic(true).Render("(no log file)"))
		return b.String()
	}
	if total == 0 {
		b.WriteString("  " + dim.Italic(true).Render("(log empty)"))
		return b.String()
	}

	logStyle := lipgloss.NewStyle().Foreground(colTextMuted)
	for _, line := range m.threads.logLines[logStart:logEnd] {
		b.WriteString("  ")
		b.WriteString(logStyle.Render(truncateStr(line, width-2)))
		b.WriteByte('\n')
	}
	return b.String()
}

// renderSummarizerStatus returns a single compact status line for the
// background summarizer: "summarizer: idle", "summarizer: OK · 12s ago",
// or "summarizer: FAILED · 3m ago". Color: red on failure, dim default otherwise.
func renderSummarizerStatus(m *model, width int) string {
	st := m.agent.SummarizerStatus()
	var label string
	var style lipgloss.Style
	if st.RanAt.IsZero() {
		label = "summarizer: idle"
		style = lipgloss.NewStyle().Foreground(colTextDim)
	} else {
		age := humanAge(st.RanAt)
		if st.Err != nil {
			label = "summarizer: FAILED · " + age + " ago"
			style = lipgloss.NewStyle().Foreground(colErr)
		} else {
			label = "summarizer: OK · " + age + " ago"
			style = lipgloss.NewStyle().Foreground(colTextDim)
		}
	}
	return "  " + style.Render(truncateStr(label, max(0, width-2)))
}

// --- row formatters ---

func formatJobRow(j *jobs.Job, expanded bool, width, tick int) (plain, styled string) {
	chevron := "►"
	if expanded {
		chevron = "▼"
	}
	glyph := jobStatusGlyph(j.Status, tick)
	elapsed := humanElapsed(j.CreatedAt, j.CompletedAt)
	title := j.Title
	maxTitle := width - 13
	if maxTitle < 8 {
		maxTitle = 8
	}
	if len(title) > maxTitle {
		title = title[:maxTitle-1] + "…"
	}

	plain = fmt.Sprintf("%s %s %-*s  %s", chevron, glyph, maxTitle, title, elapsed)

	chevStyle := lipgloss.NewStyle().Foreground(colTextMuted)
	glyphStyle := lipgloss.NewStyle().Foreground(jobStatusColor(j.Status)).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(colText).Bold(true)
	ageStyle := lipgloss.NewStyle().Foreground(colTextDim)

	styled = fmt.Sprintf("%s %s %s  %s",
		chevStyle.Render(chevron),
		glyphStyle.Render(glyph),
		titleStyle.Render(padRight(title, maxTitle)),
		ageStyle.Render(elapsed))
	return
}

func formatTaskRow(t *jobs.Task, width, tick int) (plain, styled string) {
	glyph := taskStatusGlyph(t.Status, tick)
	elapsed := humanElapsed(t.CreatedAt, t.CompletedAt)
	title := t.Title
	maxTitle := width - 14
	if maxTitle < 8 {
		maxTitle = 8
	}
	if len(title) > maxTitle {
		title = title[:maxTitle-1] + "…"
	}

	plain = fmt.Sprintf("    %s %-*s  %s", glyph, maxTitle, title, elapsed)

	glyphStyle := lipgloss.NewStyle().Foreground(taskStatusColor(t.Status))
	titleStyle := lipgloss.NewStyle().Foreground(colTextMuted)
	ageStyle := lipgloss.NewStyle().Foreground(colTextDim)

	styled = fmt.Sprintf("    %s %s  %s",
		glyphStyle.Render(glyph),
		titleStyle.Render(padRight(title, maxTitle)),
		ageStyle.Render(elapsed))
	return
}

// --- status glyphs and colors ---

func jobStatusGlyph(status string, tick int) string {
	switch status {
	case "running":
		frames := []string{"↻", "↺"}
		return frames[tick%len(frames)]
	case "done":
		return "✓"
	case "failed":
		return "✗"
	case "cancelled":
		return "⊘"
	default:
		return "·"
	}
}

func taskStatusGlyph(status string, tick int) string {
	switch status {
	case "running":
		frames := []string{"↻", "↺"}
		return frames[tick%len(frames)]
	case "done":
		return "✓"
	case "failed":
		return "✗"
	case "cancelled":
		return "⊘"
	default:
		return "·"
	}
}

func jobStatusColor(status string) lipgloss.Color {
	switch status {
	case "running":
		return colThread
	case "done":
		return colOK
	case "failed":
		return colErr
	case "cancelled":
		return colTextDim
	default:
		return colTextDim
	}
}

func taskStatusColor(status string) lipgloss.Color {
	switch status {
	case "running":
		return colThread
	case "done":
		return colOK
	case "failed":
		return colErr
	case "cancelled":
		return colTextDim
	default:
		return colTextDim
	}
}

// --- helpers ---

// humanElapsed returns a compact "Xs" / "Xm" / "Xh" / "Xd" duration.
// Uses completedAt if set, otherwise measures from createdAt to now.
func humanElapsed(start time.Time, end *time.Time) string {
	if start.IsZero() {
		return "—"
	}
	var d time.Duration
	if end != nil {
		d = end.Sub(start)
	} else {
		d = time.Since(start)
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// humanAge is kept for other panel files that use it.
func humanAge(t time.Time) string {
	return humanElapsed(t, nil)
}

// truncateStr truncates s to at most n runes, appending "…" if needed.
func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

// readLastLines opens a file and returns at most n lines from the end.
// Returns an empty slice (not an error) if the file can't be opened.
func readLastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
