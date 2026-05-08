package agent

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/providers"
)

type agentQueues struct {
	steer  []llm.Message
	follow []llm.Message
}

type pendingAnnotations struct {
	inboxNote string
	uiContext string
}

// SummarizerStatus holds the last-run outcome for the background summarizer.
// ranAt is zero when the summarizer has never run this session.
type SummarizerStatus struct {
	RanAt time.Time
	Err   error
}

// Agent is the stateful wrapper around the agent loop.
type Agent struct {
	db         *db.DB
	llm        *llm.Client
	model      string
	session    *db.Session
	tools      []Tool
	bus        *Bus
	registry   *providers.Registry
	background bool // true when running as a background task worker
	maxTurns   int  // outer-loop turn limit; set from Config or DB config

	lastActiveBeforeTurn time.Time // captured before Touch() each turn — used for temporal awareness

	mu              sync.Mutex
	history         []llm.Message // user/assistant/tool only — system prompt is NOT stored here
	streaming       bool
	queues          agentQueues
	annotations     pendingAnnotations
	wg              sync.WaitGroup // tracks background goroutines (summarizer)
	summCtx         context.Context
	summCancel      context.CancelFunc
	summarizerRanAt time.Time // zero until first summarizer run
	summarizerErr   error     // nil on success, last error on failure

	watcherOnce sync.Once // ensures StartWatcher goroutine launches at most once
}

// Config is passed to New.
type Config struct {
	DB           *db.DB
	LLM          *llm.Client
	Model        string
	Session      *db.Session
	Tools        []Tool
	Registry     *providers.Registry // nil falls back to providers.Default()
	IsBackground bool                // set to true for background task workers
	MaxTurns     int                 // outer-loop turn limit; 0 means use default (50)
	// SystemPrompt removed — the prompt is now rebuilt dynamically each turn
}

// New creates an Agent and loads the session's message history from the DB.
func New(cfg Config) (*Agent, error) {
	reg := cfg.Registry
	if reg == nil {
		reg = providers.Default()
	}

	// Resolve max turns: caller > DB config > default (50).
	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		if s, _ := cfg.DB.Config.Get(db.KeyMaxTurns); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				maxTurns = n
			}
		}
	}
	if maxTurns <= 0 {
		maxTurns = 50
	}

	summCtx, summCancel := context.WithCancel(context.Background())
	a := &Agent{
		db:         cfg.DB,
		llm:        cfg.LLM,
		model:      cfg.Model,
		session:    cfg.Session,
		tools:      cfg.Tools,
		bus:        &Bus{},
		registry:   reg,
		background: cfg.IsBackground,
		maxTurns:   maxTurns,
		summCtx:    summCtx,
		summCancel: summCancel,
	}
	if err := a.loadHistory(); err != nil {
		return nil, err
	}
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		RunHooks(a.db, "session_start", "", nil)
	}()
	return a, nil
}

// Bus returns the event bus. Subscribe before calling Prompt.
func (a *Agent) Bus() *Bus { return a.bus }

// SummarizerStatus returns the last-run timestamp and error for the background
// summarizer. RanAt is zero when the summarizer has not yet run this session.
// Safe to call from any goroutine.
func (a *Agent) SummarizerStatus() SummarizerStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return SummarizerStatus{RanAt: a.summarizerRanAt, Err: a.summarizerErr}
}

// Tools returns the agent's registered tool list. Read-only — callers
// must not mutate the slice. Used by the prompt panel to estimate the
// request-side cost of tool schemas (separate from the system prompt).
func (a *Agent) Tools() []Tool { return a.tools }

// Model returns the resolved Ollama model name this agent is using.
// Already accounts for per-role overrides and the global config fallback.
// Surfaced in the header so the user can see which model their turn
// will actually run against.
func (a *Agent) Model() string { return a.model }

// Session returns the session this agent is associated with.
func (a *Agent) Session() *db.Session { return a.session }

// History returns a snapshot of the in-memory conversation history that
// gets sent to Ollama on every turn. Used by the prompt panel to count
// the tokens contributed by prior turns.
func (a *Agent) History() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]llm.Message, len(a.history))
	copy(out, a.history)
	return out
}

// PromptOpts carries per-turn options for Prompt. Zero value is safe (no consider forced).
type PromptOpts struct {
	ForceConsider bool   // run consider even when consider.enabled=false
	TriggerSource string // audit tag for consider_activations (default "tui")
}

// Prompt submits a user message and runs the agent loop to completion.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	return a.PromptWithOpts(ctx, text, PromptOpts{})
}

// PromptWithOpts is like Prompt but accepts per-turn options.
func (a *Agent) PromptWithOpts(ctx context.Context, text string, opts PromptOpts) error {
	a.mu.Lock()
	if a.streaming {
		a.queues.steer = append(a.queues.steer, llm.Message{Role: "user", Content: text})
		a.mu.Unlock()
		return nil
	}
	a.streaming = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.streaming = false
		a.mu.Unlock()
		// After the turn completes, check if we need to summarize.
		// Tracked via WaitGroup so Close() can drain it before process exit.
		a.wg.Add(1)
		go func() {
			defer a.wg.Done()
			err := Summarize(a.summCtx, a.db, a.llm, a.session.ID, "auto")
			a.mu.Lock()
			a.summarizerRanAt = time.Now()
			a.summarizerErr = err
			a.mu.Unlock()
			if err != nil {
				log.Printf("summarize session %d: %v", a.session.ID, err)
				return
			}
			// Evict summarized messages from in-memory history. Without this,
			// a.history grows unbounded even though the DB has already marked
			// those messages summarized and injected their digest into the
			// system prompt — keeping the raw messages too causes double-counting.
			a.syncHistory()
		}()
	}()

	// Drain the background inbox: any tasks that completed while we were idle
	// get surfaced once, appended to the system prompt for this turn only.
	// Background task workers skip this entirely — they must not consume
	// completion notifications intended for the parent session.
	if !a.background {
		if note := a.drainBackgroundInbox(); note != "" {
			a.mu.Lock()
			a.annotations.inboxNote = note
			a.mu.Unlock()
		}
	}

	// Persist the user message first (empty inner_voice), then route through
	// the canonical ConsiderInput entry point, which UPDATEs the row's
	// inner_voice and links activations. The user message has to land before
	// consider so all four trigger paths (tui/cli/api/tool) share one ordering.
	//
	// Consider fires when: not a background worker AND (caller forced it via
	// opts.ForceConsider OR the trigger source is "tui" AND the global
	// consider.enabled flag is true). CLI/API/tool sources must opt in
	// explicitly via ForceConsider — the consider.enabled flag is the
	// interactive-TUI auto-fire toggle, not a global on-switch.
	userMsg, err := a.db.Messages.AddWithInnerVoice(a.session.ID, "user", text, "", "", "", "")
	if err != nil {
		return err
	}
	innerVoice := ""
	if !a.background {
		triggerSource := opts.TriggerSource
		if triggerSource == "" {
			triggerSource = "tui"
		}
		considerEnabled, _ := a.db.Config.Get(db.KeyConsiderEnabled)
		// consider.enabled auto-fires only on the interactive TUI path.
		// CLI one-shot, API, and model-tool entries must opt in explicitly
		// (`/c ` prefix or model invocation) — otherwise tool-using roles
		// pay 30-40s/turn for off-target consider work.
		autoFire := triggerSource == "tui" && considerEnabled == "true"
		if opts.ForceConsider || autoFire {
			considerPub := &considerBusAdapter{bus: a.bus}
			considerStart := time.Now()
			a.bus.Publish(Event{Type: EventStepStart, Payload: PayloadStepStart{Step: "consider", StartedAt: considerStart}})
			_, iv, considerErr := ConsiderInput(ctx, a.db, a.llm, considerPub, a.session.ID, a.session.Role, userMsg.ID, text, triggerSource)
			a.bus.Publish(Event{Type: EventStepEnd, Payload: PayloadStepEnd{Step: "consider", Duration: time.Since(considerStart)}})
			if considerErr != nil {
				log.Printf("consider: %v", considerErr)
			} else {
				innerVoice = iv
			}
		}
	}
	a.mu.Lock()
	a.history = append(a.history, llm.Message{Role: "user", Content: wrapUserMessage(text, innerVoice)})
	a.mu.Unlock()
	a.lastActiveBeforeTurn = a.session.LastActive
	_ = a.db.Sessions.Touch(a.session.ID)

	return runLoop(ctx, loopConfig{
		model:          a.model,
		history:        a.history,
		tools:          a.toolsForSkill(),
		llm:            a.llm,
		bus:            a.bus,
		db:             a.db,
		session:        a.session,
		registry:       a.registry,
		persist:        a.persistMessage,
		persistTool:    a.persistToolMessage,
		workDir:        a.session.CWD,
		buildPrompt:    a.buildSystemPrompt,
		drainSteering:  a.drainSteering,
		drainFollowUp:  a.drainFollowUp,
		maxTurns:       a.maxTurns,
		background:     a.background,
		disciplineMode: a.session.DisciplineMode,
	})
}

// Steer injects a message at the next turn boundary; if idle, runs it immediately.
func (a *Agent) Steer(ctx context.Context, text string) error {
	a.mu.Lock()
	if a.streaming {
		a.queues.steer = append(a.queues.steer, llm.Message{Role: "user", Content: text})
		a.mu.Unlock()
		return nil
	}
	a.mu.Unlock()
	return a.Prompt(ctx, text)
}

// FollowUp queues a message to run only after the agent is fully idle.
func (a *Agent) FollowUp(text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.queues.follow = append(a.queues.follow, llm.Message{Role: "user", Content: text})
}

// Close waits for all background goroutines (summarizer etc.) to finish.
// Use this only when you don't need the visible-progress / abortable
// shutdown flow — e.g., headless CLI invocations where there's no user
// to look at progress. Interactive TUI sessions should call Shutdown
// (defined in shutdown.go) instead, which prints phased progress to
// stdout and exits cleanly on Ctrl-C.
func (a *Agent) Close() {
	a.wg.Wait()
	if a.session != nil {
		RunSessionFeedback(context.Background(), a.db, a.llm, a.session.ID)
	}
	RunHooks(a.db, "session_end", "", nil)
}

// drainBackgroundInbox formats any unreported completed background tasks as
// a single "[background]" note and marks them reported so they surface only
// once. Returns "" if the inbox is empty.
func (a *Agent) drainBackgroundInbox() string {
	tasks, err := a.db.Tasks.UnreportedCompleted()
	if err != nil || len(tasks) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("[background] while you were idle, these tasks reached a terminal state:\n")
	ids := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
		result := t.Result
		// Trim long results — the full text is still in the DB for anyone
		// who wants to read it via task(action="artifacts") or agent(action="log").
		if len(result) > 300 {
			result = result[:300] + "…"
		}
		// Reaped tasks have a distinctive result prefix — surface them with
		// explicit action options so the user knows what to do.
		isReaped := t.Status == db.StatusFailed && strings.HasPrefix(result, "reaped:")
		if isReaped {
			fmt.Fprintf(&b,
				"- Background task %q (id: %d) failed unexpectedly (process died).\n"+
					"  Options: delete it with task(action=\"delete\"), re-spawn it with agent(action=\"spawn\"), or defer for later.\n",
				t.Title, t.ID)
		} else if result == "" {
			fmt.Fprintf(&b, "- task %d [%s] %q (role: %s)\n", t.ID, t.Status, t.Title, t.AssignedRole)
		} else {
			fmt.Fprintf(&b, "- task %d [%s] %q (role: %s): %s\n", t.ID, t.Status, t.Title, t.AssignedRole, result)
		}
	}
	b.WriteString("\nWeave into your response if relevant, or just acknowledge and continue.")

	if err := a.db.Tasks.MarkReported(ids); err != nil {
		// If we can't mark reported, don't surface the note — otherwise it'd
		// repeat on every turn. Log so a permanently-broken MarkReported
		// (FK or schema drift) doesn't silently swallow the inbox forever.
		log.Printf("drainBackgroundInbox: MarkReported ids=%v: %v", ids, err)
		return ""
	}
	return b.String()
}

// StartWatcher launches a background goroutine that polls for completed
// background tasks every 3 seconds and calls Steer with a context-rich
// message when any are found. Safe to call multiple times — only one watcher
// goroutine ever runs per Agent. No-op for background task workers (a.background).
func (a *Agent) StartWatcher(ctx context.Context) {
	if a.background {
		return
	}
	a.watcherOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					tasks, err := a.db.Tasks.UnreportedCompleted()
					if err != nil || len(tasks) == 0 {
						continue
					}
					ids := make([]int64, 0, len(tasks))
					for _, t := range tasks {
						ids = append(ids, t.ID)
					}
					if err := a.db.Tasks.MarkReported(ids); err != nil {
						continue
					}
					_ = a.Steer(ctx, formatCompletionSteer(tasks))
				}
			}
		}()
	})
}

// formatCompletionSteer builds a context-rich Steer message for one or more
// completed background tasks. Includes task identity, status, result snippet,
// and an orientation note for the agent in case it was mid-turn when the
// notification arrived.
func formatCompletionSteer(tasks []*db.Task) string {
	var b strings.Builder
	b.WriteString("[background task completed]\n")
	for _, t := range tasks {
		result := t.Result
		if len(result) > 300 {
			result = result[:300] + "…"
		}
		if result == "" {
			fmt.Fprintf(&b, "Task #%d (role: %s) %q finished with status: %s.\n",
				t.ID, t.AssignedRole, t.Title, t.Status)
		} else {
			fmt.Fprintf(&b, "Task #%d (role: %s) %q finished with status: %s.\nResult snippet: %s\n",
				t.ID, t.AssignedRole, t.Title, t.Status, result)
		}
	}
	b.WriteString("\nYou may have been mid-response when this notification arrived. ")
	b.WriteString("Finish your current thought naturally if appropriate, then ")
	b.WriteString("acknowledge or act on this completion.")
	return b.String()
}

// Embed returns a vector embedding for text using the session's configured
// embed model. Exposed so UI surfaces (e.g. the TUI memory spotlight) can
// reuse the same embedder the summarizer and memory_search tool use, without
// needing their own llm.Client handle. Returns an empty slice + error if
// no embed_model is configured.
func (a *Agent) Embed(text string) ([]float32, error) {
	model, _ := a.db.Config.Get(db.KeyEmbedModel)
	if model == "" {
		return nil, nil
	}
	return a.llm.Embed(context.Background(), model, text)
}

// LastAssistantText returns the most recent assistant message's content from
// the in-memory history. Used by background task workers to capture a task's
// canonical output without a second DB round-trip — the in-memory history
// is the authoritative view of what the loop just produced, avoiding the
// stale-read that LastAssistantMessage exposed on partial failure.
// Returns "" when the turn produced only tool calls or errored before emitting text.
func (a *Agent) LastAssistantText() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.history) - 1; i >= 0; i-- {
		m := a.history[i]
		if m.Role == "assistant" && m.Content != "" {
			return m.Content
		}
	}
	return ""
}

// IsStreaming reports whether the agent is mid-turn.
func (a *Agent) IsStreaming() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.streaming
}

// toolsForSkill applies the session_skill_tools config key as a second filter
// on top of the role allowlist already baked into a.tools. Returns a.tools
// unchanged when the key is absent or empty (back-compat, no filtering).
func (a *Agent) toolsForSkill() []Tool {
	raw, err := a.db.Config.Get("session_skill_tools")
	if err != nil || raw == "" {
		return a.tools
	}
	parts := strings.Split(raw, ",")
	allowed := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			allowed = append(allowed, s)
		}
	}
	return FilterByAllowlist(a.tools, allowed)
}

// buildSystemPrompt is the closure passed to loopConfig.buildPrompt.
// Called fresh at the start of every outer loop iteration.
func (a *Agent) buildSystemPrompt() (llm.Message, error) {
	// Wire fact semantic search only when an embed model is configured.
	// Background workers skip it (no embed model available in that path).
	var factSearch FactSearchFn
	if embedModel, _ := a.db.Config.Get(db.KeyEmbedModel); embedModel != "" {
		limit := 5
		if lstr, _ := a.db.Config.Get(db.KeyMemoryLimit); lstr != "" {
			if n, err := strconv.Atoi(lstr); err == nil && n > 0 {
				limit = n
			}
		}
		factSearch = func(query string) ([]*db.Fact, error) {
			if query == "" {
				return nil, nil
			}
			vec, err := a.llm.Embed(context.Background(), embedModel, query)
			if err != nil || len(vec) == 0 {
				return nil, nil // silent: embed service may be unavailable
			}
			return a.db.Facts.Search(vec, embedModel, limit)
		}
	}

	// Consider (inner-dialogue) is no longer wired into the system prompt.
	// It runs in agent.Prompt() before the user message is persisted, and
	// the resulting summary is attached to that message via inner_voice.
	// The system prompt's permanent inner-voice meta block (in BuildSystemPrompt)
	// teaches Selene how to read those embedded sections.

	msg, err := BuildSystemPrompt(context.Background(), a.db, a.session.ID, a.session.Role, a.session.CWD, a.tools, a.lastActiveBeforeTurn, a.registry, factSearch)
	if err != nil {
		return msg, err
	}
	// Drain one-shot annotations: inbox notes and UI context are appended to
	// the system prompt for this turn only, then cleared.
	a.mu.Lock()
	note := a.annotations.inboxNote
	a.annotations.inboxNote = ""
	uiCtx := a.annotations.uiContext
	a.annotations.uiContext = ""
	a.mu.Unlock()
	if note != "" {
		msg.Content += "\n\n" + note
	}
	if uiCtx != "" {
		msg.Content += "\n\n<ui-context>\n" + uiCtx + "\n</ui-context>"
	}
	return msg, nil
}

// SetUIContext queues a UI activity note to be injected into the system prompt
// on the next turn. Callers should describe what the user did in plain text;
// the agent receives it as passive context, not a task. Multiple calls before
// a turn fires are joined with newlines.
func (a *Agent) SetUIContext(text string) {
	if text == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.annotations.uiContext == "" {
		a.annotations.uiContext = text
	} else {
		a.annotations.uiContext += "\n" + text
	}
}

func (a *Agent) drainSteering() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := a.queues.steer
	a.queues.steer = nil
	return msgs
}

func (a *Agent) drainFollowUp() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := a.queues.follow
	a.queues.follow = nil
	return msgs
}
