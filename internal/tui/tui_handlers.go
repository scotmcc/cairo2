package tui

// tui_handlers.go — per-message-type handler methods extracted from Update.
// Each method receives a typed message, mutates a copy of model, and returns
// (model, tea.Cmd) so Update stays a clean ~20-line dispatcher.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/jobs"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// handleWindowSize responds to terminal resize events.
func (m model) handleWindowSize(msg tea.WindowSizeMsg) (model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	// WithStandardStyle to avoid OSC 11 probe on resize — see tui_model.go.
	// Read style from config so it matches the startup setting.
	glamourStyle, _ := m.db.Config.Get("glamour_style")
	if glamourStyle == "" {
		glamourStyle = "dark"
	}
	if r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(glamourStyle),
		glamour.WithWordWrap(msg.Width),
	); err == nil {
		m.renderer = r
	}
	m.relayout()

	// Non-key routing: push resize to all open panels and viewport.
	// Non-focused panels keep stale dimensions otherwise, causing layout
	// artifacts in panels with internal viewports (inspector, diff, etc.).
	var cmds []tea.Cmd
	for _, spec := range registeredPanels {
		if m.openPanels[spec.ID] && spec.Update != nil {
			if _, cmd := spec.Update(msg, &m); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)
	return m, tea.Batch(cmds...)
}

// handleTick is the animation / watchdog heartbeat.
func (m model) handleTick(msg tickMsg) (model, tea.Cmd) {
	var cmds []tea.Cmd

	// Drain an incoming choice request (non-blocking). If one arrives and no
	// overlay is currently active, open it. We skip if activeChoice is already
	// set so a pending choice doesn't get replaced before the user responds.
	if m.choiceRequests != nil && m.activeChoice == nil {
		select {
		case req := <-m.choiceRequests:
			m.activeChoice = &choiceOverlay{
				title:    req.Title,
				options:  req.Options,
				selected: 0,
				result:   req.Result,
			}
		default:
		}
	}

	// On the very first tick, check if this is a fresh (uninitialized) DB
	// and nudge the user toward /init. Only emits once per session.
	if m.tickCounter == 0 && !m.initNudgeDone {
		m.initNudgeDone = true
		if v, err := m.db.Config.Get("init_complete"); err == nil && (v == "" || v == "false") {
			m.appendSystem("Type /init to get started — Selene will introduce herself, set up her soul, and learn about you so future sessions start informed.")
		}
	}

	// Increment the animation counter — drives spinner, breathing indicator.
	m.tickCounter++

	// Expire old toasts.
	now := time.Now()
	active := m.toasts[:0]
	for _, t := range m.toasts {
		if now.Before(t.expiresAt) {
			active = append(active, t)
		}
	}
	m.toasts = active

	// Prune tool-call toasts whose linger window has expired. Re-layout
	// when the rendered row count changes so freed rows go back to the
	// transcript viewport.
	prevToasts := toolToastRowCount(m.toolToasts)
	if m.pruneToolToasts() {
		if toolToastRowCount(m.toolToasts) != prevToasts {
			m.relayout()
		}
	}

	// Every 10th tick, check if the event bus has dropped events since the
	// last warning. One toast per drop-count increase (not per dropped event)
	// so a burst of drops produces a single notification.
	if m.tickCounter%10 == 0 {
		if drops := m.agent.Bus().DropCount(); drops > m.lastWarnedDropCount {
			m.addToast(fmt.Sprintf("event bus dropped %d event(s) — subscriber too slow", drops-m.lastWarnedDropCount), toastWarn)
			m.lastWarnedDropCount = drops
		}
	}

	// Schedule a tea.Cmd to run DB queries off the render loop every tick.
	// Every 10th tick also refreshes memory and job counts (refreshCounts).
	// Every 100th tick also runs the watchdog (check dead/hung processes).
	runFull := m.tickCounter%10 == 0
	runWatchdog := m.tickCounter%100 == 0
	db := m.db
	cmds = append(cmds, func() tea.Msg {
		return tickRefreshQuery(db, runFull, runWatchdog)
	})

	cmds = append(cmds, scheduleTick())

	// Non-key routing: push tick to focused panel and viewport.
	if spec := m.focusedPanelSpec(); spec != nil && spec.Update != nil {
		if _, cmd := spec.Update(msg, &m); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// tickRefreshQuery runs all DB reads that were previously inline in handleTick.
// It is called as a tea.Cmd so the work happens off the render loop.
func tickRefreshQuery(database *sqliteopen.DB, fullRefresh bool, runWd bool) tea.Msg {
	msg := tickRefreshMsg{}

	if n, err := database.Tasks.CountRunning(); err == nil {
		msg.threadCount = n
	}
	if tasks, err := database.Tasks.UnreportedCompleted(); err == nil {
		msg.completedTasks = tasks
	}
	if prog, err := database.Tasks.RunningWithProgress(); err == nil {
		msg.progressTasks = prog
	}
	if fullRefresh {
		// refreshCounts fields: mem and job counts are returned via extra fields
		// when needed; for now threadCount covers the animation signal. The
		// memoryCount/jobCount update happens via handleTickRefresh calling
		// refreshCounts-equivalent logic.
		if n, err := database.Jobs.CountRunning(); err == nil {
			msg.jobCount = n
			msg.hasJobCount = true
		}
		if n, err := database.Memories.Count(); err == nil {
			msg.memoryCount = n
			msg.hasMemoryCount = true
		}
	}
	if runWd {
		msg.runWatchdog = true
	}
	return msg
}

// handleTickRefresh applies the results of the off-loop DB queries.
func (m model) handleTickRefresh(msg tickRefreshMsg) (model, tea.Cmd) {
	m.threadCount = msg.threadCount
	if msg.hasJobCount {
		m.jobCount = msg.jobCount
	}
	if msg.hasMemoryCount {
		m.memoryCount = msg.memoryCount
	}

	// Apply completed-task toasts.
	for _, t := range msg.completedTasks {
		if m.toastedTaskIDs[t.ID] {
			continue
		}
		m.toastedTaskIDs[t.ID] = true
		var kind toastKind
		var icon string
		switch t.Status {
		case "done":
			kind = toastSuccess
			icon = "✓"
		case "blocked":
			kind = toastError
			icon = "⊘"
		default: // failed
			kind = toastError
			icon = "✗"
		}
		title := t.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		m.addToast(fmt.Sprintf("%s %s [%s]", icon, title, t.Status), kind)
	}

	// Apply progress bar update.
	prevBars := progressRowCount(m.progressTasks)
	m.progressTasks = msg.progressTasks
	if progressRowCount(m.progressTasks) != prevBars {
		m.relayout()
	}

	// Refresh threads panel if open. Gate to every 4th tick (~1.2s) to
	// reduce DB churn from N+1 queries on each tickRefreshMsg.
	if m.isPanelOpen(panelThreadsID) && m.tickCounter%4 == 0 {
		threadsRefresh(&m)
	}

	// Run watchdog inline here (it's already off the render loop from the
	// caller's perspective — this handler runs in Update but the heavy I/O
	// path was decided in the tea.Cmd goroutine via runWatchdog flag).
	if msg.runWatchdog {
		m.runWatchdog()
	}

	return m, nil
}

// handleAgentEvent processes a streamed agent event from the event bus.
func (m model) handleAgentEvent(msg eventMsg) (model, tea.Cmd) {
	var cmds []tea.Cmd

	m.handleEvent(msg.event)
	// Re-issue the listen command to keep the event pump going.
	cmds = append(cmds, listenEvents(m.eventCh))

	// Non-key routing: push event to focused panel and viewport.
	if spec := m.focusedPanelSpec(); spec != nil && spec.Update != nil {
		if _, cmd := spec.Update(msg, &m); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// handlePromptErr handles an error returned by the agent prompt goroutine.
func (m model) handlePromptErr(msg promptErrMsg) (model, tea.Cmd) {
	var cmds []tea.Cmd

	// Clean up any partial assistant turn before showing the error.
	// finishAssistant() splices the streamed (raw) region into the
	// transcript and resets streamingRaw — safe to call even if the
	// turn produced no tokens (streamingRaw will be empty).
	m.finishAssistant()
	m.appendSystem(fmt.Sprintf("error: %v", msg.err))
	m.streaming = false
	m.activity.SetIdle()
	m.cancel = nil

	// If this turn was triggered by /init but errored/interrupted,
	// still set init_complete to suppress the banner on next launch.
	// The user has been nudged; partial /init completion is enough.
	if m.initPending {
		m.initPending = false
		_ = m.db.Config.Set("init_complete", "true")
	}

	// Non-key routing: push to focused panel and viewport.
	if spec := m.focusedPanelSpec(); spec != nil && spec.Update != nil {
		if _, cmd := spec.Update(msg, &m); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// handleTurnComplete handles the signal that the agent turn finished cleanly.
func (m model) handleTurnComplete(msg turnCompleteMsg) (model, tea.Cmd) {
	var cmds []tea.Cmd

	m.streaming = false
	m.activity.SetIdle()
	m.cancel = nil

	// If this turn was triggered by /init, set init_complete deterministically.
	// Small models don't reliably call config(set, init_complete, true) themselves.
	if m.initPending {
		m.initPending = false
		_ = m.db.Config.Set("init_complete", "true")
	}
	// Accumulate session token spend using historyCost() as a proxy.
	// historyCost sums the content size of every in-memory history message;
	// it's already the same estimator used in the prompt panel so the
	// numbers are self-consistent.
	m.sessionTokens = historyCost(&m)
	m.refreshCounts()

	// Non-key routing: push to focused panel and viewport.
	if spec := m.focusedPanelSpec(); spec != nil && spec.Update != nil {
		if _, cmd := spec.Update(msg, &m); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	var vpCmd tea.Cmd
	m.viewport, vpCmd = m.viewport.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// handleKey is the main keyboard dispatcher. It handles all tea.KeyMsg events:
// focused-panel delegation, panel toggles, command palette, slash drawer,
// Ctrl-C, global hotkeys, and the text input.
func (m model) handleKey(msg tea.KeyMsg) (model, tea.Cmd) {
	// Choice overlay gets exclusive keyboard focus while active.
	if m.activeChoice != nil {
		return m.handleChoiceKey(msg)
	}

	var cmds []tea.Cmd
	key := msg.String()

	// Focused panel gets first crack at the key. If it claims the
	// message (returns handled=true), we stop. This is how panels
	// own up/down, enter, esc for their own navigation without the
	// main Update needing to know what each one does.
	if spec := m.focusedPanelSpec(); spec != nil && spec.Update != nil {
		if handled, cmd := spec.Update(msg, &m); handled {
			return m, cmd
		}
	}

	// Smart paste — bracketed-paste events that exceed the divert
	// thresholds get redirected to a tempfile + @paste:N token instead of
	// flooding the textarea. Below the thresholds, paste falls through to
	// the input normally so a one-line URL paste behaves as expected.
	if msg.Paste && shouldDivertPaste(msg.Runes) {
		return m.handleSmartPaste(msg.Runes)
	}

	// Panel toggle keys — opening/closing via bound keys. Runs before
	// slash-drawer and input-field handling so Ctrl-T can't get captured
	// as input. Ctrl-modified keys always toggle (they can't be typed as
	// text anyway); plain keys (like "?") only toggle when the input is
	// empty, so you can type "?" mid-message without surprise.
	if spec := panelByToggleKey(key); spec != nil {
		ctrlKey := strings.HasPrefix(key, "ctrl+")
		if ctrlKey || m.input.Value() == "" {
			return m, m.togglePanel(spec.ID)
		}
	}

	// Command palette — Ctrl+K opens it; all keys route through it
	// while open. Mutually exclusive with the slash drawer.
	if key == "ctrl+k" && !m.slashOpen {
		m.openPalette()
		return m, nil
	}
	if m.palette.open {
		handled, cmd := m.updatePalette(key)
		if handled {
			return m, cmd
		}
	}

	// Slash drawer active: drawer-specific navigation + Esc handling.
	if m.slashOpen {
		switch key {
		case "esc", "ctrl+c":
			// Ctrl-C is bound globally to /clear, but while the slash
			// drawer is open we want it to just close the drawer (like
			// Esc) — wiping the transcript mid-command-selection
			// would be jarring.
			m.clearInput()
			m.closeSlash()
			return m, nil
		case "up", "k":
			if m.slashIndex > 0 {
				m.slashIndex--
			}
			return m, nil
		case "down", "j":
			if m.slashIndex < len(m.slashMatches)-1 {
				m.slashIndex++
			}
			return m, nil
		case "enter":
			// Execute the selected command. Capture any text after the
			// command name as args BEFORE clearing the input, so commands
			// like /learn can read the path the user typed.
			if len(m.slashMatches) > 0 && m.slashIndex < len(m.slashMatches) {
				cmd := m.slashMatches[m.slashIndex]
				args := ""
				full := strings.TrimSpace(strings.TrimPrefix(m.input.Value(), "/"))
				if i := strings.IndexAny(full, " \t"); i >= 0 {
					args = strings.TrimSpace(full[i+1:])
				}
				m.clearInput()
				m.closeSlash()
				var handlerCmd tea.Cmd
				if cmd.HandlerWithArgs != nil {
					handlerCmd = cmd.HandlerWithArgs(&m, args)
				} else if cmd.Handler != nil {
					handlerCmd = cmd.Handler(&m)
				}
				return m, handlerCmd
			}
			return m, nil
		}
		// Any other key: let the input handle it, then refresh the
		// filter — if the leading '/' got erased, close the drawer.
		var cmd tea.Cmd
		m.preGrowInput()
		m.input, cmd = m.input.Update(msg)
		cmds = append(cmds, cmd)
		if m.syncInputHeight() {
			m.relayout()
		}
		m.refreshSlash()
		return m, tea.Batch(cmds...)
	}

	// Ctrl-C is context-sensitive — its meaning depends on whether
	// Selene is mid-turn, whether the input has content, and otherwise
	// falls through to clearing the transcript view. Matches the
	// intuition that Ctrl-C means "stop whatever I was doing" and the
	// "doing" part shifts with state. All three forms are non-
	// destructive relative to Selene's DB — see design rule above.
	if key == "ctrl+c" {
		switch {
		case m.streaming && m.cancel != nil:
			// Abort the in-flight turn. runLoop catches ctx.Err(),
			// persists partial text with an (interrupted) tag, and
			// fires EventTurnEnd — the UI state resets naturally.
			m.cancel()
			m.cancel = nil
			return m, nil
		case m.input.Value() != "":
			m.clearInput()
			return m, nil
		default:
			// Idle and empty: clear the transcript view. DB untouched.
			m.transcript.Reset()
			m.pushViewport()
			return m, nil
		}
	}

	// Global hotkeys (registry-bound). Checked before text input so
	// binding a key doesn't leak a character into the field.
	if bound := lookupByHotkey(m.commands, key); bound != nil {
		return m, bound.Handler(&m)
	}

	// ctrl+d is intercepted by panelByToggleKey above (diff panel).
	switch key {
	case "/":
		// First-char slash: open the drawer. Otherwise a literal char.
		if m.input.Value() == "" {
			m.openSlash()
			// Fall through so the '/' also gets typed into the input,
			// keeping the drawer's filter query aligned with what the
			// user sees.
		}
	case "@":
		// '@' at a word boundary opens the file picker so the user can
		// browse instead of typing the path. The literal '@' still gets
		// typed into the input — insertFileRef looks for a trailing '@'
		// on selection and inserts the path right after it, so the user
		// experiences it as autocomplete of what they just started.
		v := m.input.Value()
		atBoundary := v == "" || strings.HasSuffix(v, " ") || strings.HasSuffix(v, "\t")
		if atBoundary {
			// openPanel may return a cmd (filepicker.Init); batch it
			// alongside the normal input handling that follows.
			if cmd := m.openPanel(panelFilesID); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case "ctrl+enter":
		// Dispatch the current input as a background task (job+task pair)
		// instead of submitting it inline. No-op if input is empty or if
		// Selene is mid-stream (same guard as plain Enter).
		raw := strings.TrimSpace(m.input.Value())
		if raw == "" || m.streaming {
			break
		}
		m.clearInput()
		jobID, taskID, err := m.spawnBackgroundTask(raw)
		if err != nil {
			m.addToast("dispatch failed: "+err.Error(), toastError)
		} else {
			// Record the dispatch in the transcript so there is a visible
			// record of what was sent to the background.
			m.appendSystem(fmt.Sprintf("[→ background] %s", raw))
			m.addToast(
				fmt.Sprintf("task %s dispatched (job %s) — Ctrl+T to watch",
					itoa(taskID), itoa(jobID)),
				toastSuccess)
		}
		return m, tea.Batch(cmds...)
	case "enter":
		raw := strings.TrimSpace(m.input.Value())
		if raw == "" || m.streaming {
			break
		}
		m.clearInput()
		// Clear stall banner — user is responding, so the stall is resolved.
		m.stalledMidIntent = false
		// Detect /c prefix for per-message consider opt-in. Must happen before
		// expander so the marker is stripped from what the agent sees.
		forceConsider := false
		if strings.HasPrefix(raw, "/c ") {
			raw = strings.TrimPrefix(raw, "/c ")
			forceConsider = true
		}
		// Prefix expansion: !shell runs a command and uses the output
		// as the user turn. @file (added separately) injects file
		// contents into what Selene sees without cluttering the
		// transcript with the full file body.
		displayed, sent := m.expander.Expand(raw)
		m.appendUser(displayed)
		m.startAssistant()
		cmds = append(cmds, m.submitWithOpts(sent, forceConsider))
		return m, tea.Batch(cmds...)
	case "pgup", "pgdown", "up", "down":
		if m.input.Value() == "" {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			cmds = append(cmds, cmd)
			return m, tea.Batch(cmds...)
		}
	case "y":
		// y copies the last assistant message to the clipboard when input
		// is empty and no panel is focused. Falls back gracefully if the
		// clipboard is unavailable.
		if m.input.Value() == "" && m.focusedPanel == "" {
			raw := strings.TrimSpace(m.streamingRaw.String())
			if raw == "" {
				m.addToast("nothing to copy — no assistant response yet", toastInfo)
			} else if err := clipboard.WriteAll(raw); err != nil {
				m.addToast("clipboard unavailable: "+err.Error(), toastWarn)
			} else {
				m.addToast("copied last response to clipboard", toastSuccess)
			}
			return m, nil
		}
	}

	// Default: pass to the text input.
	var cmd tea.Cmd
	m.preGrowInput()
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)
	if m.syncInputHeight() {
		m.relayout()
	}
	return m, tea.Batch(cmds...)
}

// spawnBackgroundTask creates a job+task pair and launches a detached cairo
// subprocess to run the given description. Returns (jobID, taskID, error).
// This is the Ctrl+Enter dispatch path — equivalent to the agent calling
// agent(action="spawn") but triggered directly from the TUI keybind.
//
// Role is always "orchestrator" so the background process has full
// tool access. The job title is the first 60 chars of the description.
func (m *model) spawnBackgroundTask(description string) (int64, int64, error) {
	title := description
	if len(title) > 60 {
		title = title[:60] + "…"
	}
	sid := m.session.ID
	job, err := m.db.Jobs.Create(title, description, "orchestrator", &sid)
	if err != nil {
		return 0, 0, fmt.Errorf("create job: %w", err)
	}
	task, err := m.db.Tasks.Create(job.ID, "orchestrate", description, "orchestrator", "")
	if err != nil {
		return 0, 0, fmt.Errorf("create task: %w", err)
	}

	logDir := filepath.Join(sqliteopen.DefaultDataDir(), "logs")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, fmt.Sprintf("task_%d.log", task.ID))
	_ = m.db.Tasks.SetLogPath(task.ID, logPath)

	logFile, err := os.Create(logPath)
	if err != nil {
		_ = m.db.Tasks.SetStatusAndResult(task.ID, jobs.StatusFailed, fmt.Sprintf("create log: %v", err))
		return 0, 0, fmt.Errorf("create log: %w", err)
	}

	exe, err := os.Executable()
	if err != nil {
		exe = "cairo"
	}
	cmd := exec.Command(exe,
		fmt.Sprintf("-task=%d", task.ID),
		"-background",
		"-new",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = spawnDetached()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		_ = m.db.Tasks.SetStatusAndResult(task.ID, jobs.StatusFailed, fmt.Sprintf("spawn failed: %v", err))
		return 0, 0, fmt.Errorf("spawn: %w", err)
	}
	logFile.Close()
	_ = m.db.Tasks.SetPID(task.ID, cmd.Process.Pid)
	cmd.Process.Release()

	return job.ID, task.ID, nil
}

// handleJobApprove processes a MsgJobApprove from the diff panel.
// Closes the diff panel, publishes EventJobApprove to the agent bus for any
// listeners, and auto-submits a prompt so Selene immediately calls
// merge_job(action="approve", job_id=N). Without the auto-submit the queued
// UI event would sit unread until the user manually typed something.
// Status is set by merge_job — this handler does NOT write jobs.status.
func (m model) handleJobApprove(msg MsgJobApprove) (model, tea.Cmd) {
	m.closePanel(panelDiffID)
	m.appendSystem(fmt.Sprintf("[review] approving job #%d — handing off to Selene...", msg.JobID))
	m.agent.Bus().Publish(agent.Event{
		Type:    agent.EventJobApprove,
		Payload: agent.PayloadJobAction{JobID: msg.JobID},
	})
	prompt := fmt.Sprintf(
		"Job #%d was approved via the diff panel. Run merge_job(action=\"approve\", job_id=%d) and narrate each step as it progresses.",
		msg.JobID, msg.JobID,
	)
	return m, m.submit(prompt)
}

// handleJobReject processes a MsgJobReject from the diff panel.
// Closes the diff panel, publishes EventJobReject for any listeners, and
// auto-submits a prompt so Selene calls merge_job(action="reject", job_id=N)
// immediately. Status is set by merge_job — this handler does NOT write
// jobs.status.
func (m model) handleJobReject(msg MsgJobReject) (model, tea.Cmd) {
	m.closePanel(panelDiffID)
	m.appendSystem(fmt.Sprintf("[review] rejecting job #%d — handing off to Selene...", msg.JobID))
	m.agent.Bus().Publish(agent.Event{
		Type:    agent.EventJobReject,
		Payload: agent.PayloadJobAction{JobID: msg.JobID},
	})
	prompt := fmt.Sprintf(
		"Job #%d was rejected via the diff panel. Run merge_job(action=\"reject\", job_id=%d), then ask whether to keep the worktree for inspection or remove it.",
		msg.JobID, msg.JobID,
	)
	return m, m.submit(prompt)
}

// handleChoiceKey routes keyboard input while a choice overlay is active.
// It owns up/down navigation, enter to confirm, and esc/ctrl+c to cancel
// (sending the current selection so the tool doesn't block indefinitely).
func (m model) handleChoiceKey(msg tea.KeyMsg) (model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.activeChoice.selected > 0 {
			m.activeChoice.selected--
		}
	case "down", "j":
		if m.activeChoice.selected < len(m.activeChoice.options)-1 {
			m.activeChoice.selected++
		}
	case "enter":
		chosen := m.activeChoice.options[m.activeChoice.selected]
		result := m.activeChoice.result
		m.activeChoice = nil
		return m, func() tea.Msg {
			result <- chosen
			return nil
		}
	case "esc", "ctrl+c":
		// Send the declined sentinel so the choose tool returns an error to the
		// agent. Esc and ctrl+c are dismissal, not confirmation.
		result := m.activeChoice.result
		m.activeChoice = nil
		return m, func() tea.Msg {
			result <- tools.DeclinedSentinel
			return nil
		}
	}
	return m, nil
}
