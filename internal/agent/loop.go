package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/providers"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// forwardLookingPattern matches assistant text that signals intent to do more
// work ("Now let me try…", "I'll next check…", etc.) but has no tool calls.
// When a turn ends with matching text and zero tool calls, EventStallDetected
// is published so the TUI can surface a prompt banner.
var forwardLookingPattern = regexp.MustCompile(
	`(?i)\b(now|next|let me|i['']ll|i will)\b.{0,120}\b(try|run|merge|continue|do|check|fix|verify|test|proceed|start|move on|attempt)\b`,
)

type loopConfig struct {
	model          string
	history        []llm.Message // user/assistant/tool only — no system prompt
	tools          []Tool
	llm            *llm.Client
	bus            *Bus
	db             *sqliteopen.DB      // passed to ToolContext for tools that need DB access (e.g. unsafe_mode check)
	session        *sessions.Session   // threaded into ToolContext so self-inspection tools can rebuild prompt
	registry       *providers.Registry // threaded into ToolContext for tools that need it
	persist        func(role, content, toolCallsJSON, toolName, toolID string)
	persistTool    func(content, toolName, toolID, status string, latencyMs int64)
	workDir        string
	buildPrompt    func() (llm.Message, error)
	drainSteering  func() []llm.Message
	drainFollowUp  func() []llm.Message
	maxTurns       int  // outer-loop iteration limit; 0 means unlimited
	background     bool // true when running as a background task worker
	disciplineMode int  // DisciplineReadonly/Scoped/Full — passed to ToolContext
}

// runLoop is the pure functional core — no UI coupling.
//
// The tool-call loop now lives here (not inside llm.Chat), so every
// intermediate message — assistant tool-call requests and tool results —
// is visible, persisted to the DB, and threaded correctly through history.
//
// Structure:
//
//	outer loop: re-runs while steering or follow-up messages are queued
//	  system prompt rebuilt fresh each outer iteration
//	  inner loop: re-runs while the model requests tool calls
//	    StreamOnce → if tool calls, execute + persist, loop
//	    if final text, persist, break inner
func runLoop(ctx context.Context, cfg loopConfig) error {
	cfg.bus.Publish(Event{Type: EventAgentStart})
	defer cfg.bus.Publish(Event{Type: EventAgentEnd})

	// conversation history — system prompt is NOT stored here
	msgs := make([]llm.Message, len(cfg.history))
	copy(msgs, cfg.history)

	toolDefs := make([]llm.ToolDef, len(cfg.tools))
	for i, t := range cfg.tools {
		toolDefs[i] = ToLLM(t)
	}
	toolMap := make(map[string]Tool, len(cfg.tools))
	for _, t := range cfg.tools {
		toolMap[t.Name()] = t
	}
	var cfgStore ConfigStore
	if cfg.db != nil {
		cfgStore = cfg.db.Config
	}
	disciplineMode := cfg.disciplineMode
	if disciplineMode == 0 {
		// Default to full if not set — preserves existing behavior for callers
		// that don't populate the field (e.g. background workers before this
		// field was threaded through).
		disciplineMode = DisciplineFull
	}
	if cfg.session != nil && cfg.session.DisciplineMode > 0 && disciplineMode == DisciplineFull {
		// Prefer the session's stored discipline mode over the default, but
		// let an explicit loopConfig value win (it may come from a CLI flag
		// override that was already persisted to the session row).
		disciplineMode = cfg.session.DisciplineMode
	}
	tc := &ToolContext{
		Ctx:            ctx,
		WorkDir:        cfg.workDir,
		DB:             cfg.db,
		Config:         cfgStore,
		Bus:            cfg.bus,
		Session:        cfg.session,
		Tools:          cfg.tools,
		Registry:       cfg.registry,
		IsBackground:   cfg.background,
		DisciplineMode: disciplineMode,
	}

	callSeq := 0 // for synthetic tool call IDs
	turns := 0   // outer-loop iteration counter

	// consecutiveToolErrors tracks how many consecutive errors have fired on
	// the same tool name within the current inner-loop pass. Reset to zero on
	// every clean result or when the tool name changes.
	consecutiveToolErrors := map[string]int{}

	// modelCtx is the server's max_model_len; used by buildSendMsgs to trim
	// history before it exceeds the context window. Fall back to 16384 when
	// missing so the backstop still fires conservatively.
	modelCtx := 0
	if cfg.db != nil {
		if s, _ := cfg.db.Config.Get(config.KeyModelCtx); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				modelCtx = n
			}
		}
	}
	if modelCtx <= 0 {
		modelCtx = 16384
	}

	// Synthesis nudge: after every N tool calls in this run, inject a
	// "stop and synthesize" reminder so the model can't search-doom-loop
	// for an hour without ever consolidating what it learned. Read from
	// config (default 8); 0 disables. Tracked across the WHOLE run, not
	// per-inner-turn — repetition that survives turn boundaries still
	// counts.
	nudgeEvery := 8
	if cfg.db != nil {
		if s, _ := cfg.db.Config.Get(config.KeySynthesisNudge); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				nudgeEvery = n
			}
		}
	}
	toolsThisRun := 0
	nextNudgeAt := nudgeEvery

	// lastUserText tracks the most recent user message text so ApplyTurnSignals
	// can classify its tone. Updated at the start of each outer iteration from msgs.
	lastUserText := ""
	if len(msgs) > 0 {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" {
				lastUserText = msgs[i].Content
				break
			}
		}
	}

	for { // outer: steering / follow-up
		toolsThisTurn := 0 // tool calls in the current outer iteration only (for stall detection)
		// Reset per-turn consecutive error counters so a new outer-loop pass
		// starts clean. Errors in a previous turn don't compound into the next.
		for k := range consecutiveToolErrors {
			delete(consecutiveToolErrors, k)
		}

		// Max-turns guard: stop before the loop runs forever on bad steering/follow-up cycles.
		if cfg.maxTurns > 0 && turns >= cfg.maxTurns {
			stopMsg := fmt.Sprintf("[system] max turns (%d) reached — stopping to prevent infinite loop.", cfg.maxTurns)
			cfg.persist("assistant", stopMsg, "", "", "")
			cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{HasMore: false}})
			return nil
		}
		turns++
		cfg.bus.Publish(Event{Type: EventTurnStart})
		// pre_turn: fires after EventTurnStart; session-scoped (not to be
		// confused with session_start which fires once per session lifetime).
		if cfg.session != nil {
			RunHooks(cfg.db, "pre_turn", cfg.session.Role, []string{
				"CAIRO_SESSION_ID=" + strconv.FormatInt(cfg.session.ID, 10),
				"CAIRO_ROLE=" + cfg.session.Role,
				"CAIRO_TURN_NUMBER=" + strconv.Itoa(turns),
				"CAIRO_MODEL=" + cfg.model,
			})
		}

		// Rebuild system prompt fresh — picks up soul/memory/prompt changes made this session
		sendMsgs := buildSendMsgs(cfg.buildPrompt, msgs, modelCtx)

		callbacks := llm.ChatCallbacks{
			Content: func(token string) {
				cfg.bus.Publish(Event{Type: EventTokens, Payload: PayloadTokens{Token: token}})
			},
			Thinking: func(token string) {
				cfg.bus.Publish(Event{Type: EventThinking, Payload: PayloadThinking{Token: token}})
			},
		}

		// Build ChatOptions from DB config — applied fresh each outer turn so
		// live config changes (e.g. toggling think) take effect immediately.
		var chatOpts llm.ChatOptions
		if cfg.db != nil {
			budgetStr, _ := cfg.db.Config.Get(config.KeyThinkBudget)
			if budget, err := strconv.Atoi(budgetStr); err == nil {
				chatOpts.ThinkBudget = budget
			}
			thinkStr, _ := cfg.db.Config.Get(config.KeyThink)
			chatOpts.DisableThinking = isThinkDisabled(thinkStr)
			// Per-role think override wins over global when set.
			if cfg.session != nil && cfg.session.Role != "" {
				if override, hasOverride, err := cfg.db.Roles.ThinkFor(cfg.session.Role); err == nil && hasOverride {
					chatOpts.DisableThinking = isThinkDisabled(override)
				}
			}
		}

		// inner: tool call loop
		var finalText string
		for {
			llmStart := time.Now()
			cfg.bus.Publish(Event{Type: EventStepStart, Payload: PayloadStepStart{Step: "llm", StartedAt: llmStart}})
			text, toolCalls, _, err := cfg.llm.StreamOnce(
				ctx, cfg.model, sendMsgs, toolDefs, chatOpts, callbacks,
			)
			cfg.bus.Publish(Event{Type: EventStepEnd, Payload: PayloadStepEnd{Step: "llm", Duration: time.Since(llmStart)}})
			// Recovery path: LLM 5xx is most often a transient server failure —
			// e.g. GGML_ASSERT in the Metal backend, sometimes triggered
			// by structured-output (`response_format`) sampling. Retry once with
			// format cleared. If even that fails, persist a system note
			// so the model sees what went wrong on the next turn and can
			// adapt — narrow scope, summarize, switch tools.
			if err != nil && ctx.Err() == nil && isLLMServerError(err) {
				wrapped := fmt.Errorf("retrying without format constraint: %w", err)
				cfg.bus.Publish(Event{Type: EventError, Payload: PayloadError{
					Err:     wrapped,
					Message: wrapped.Error(),
				}})
				retryOpts := chatOpts
				retryOpts.Format = nil
				text, toolCalls, _, err = cfg.llm.StreamOnce(
					ctx, cfg.model, sendMsgs, toolDefs, retryOpts, callbacks,
				)
				if err != nil && ctx.Err() == nil {
					// Both attempts failed. Persist a system note so the
					// next turn's prompt has explicit context about the
					// failure mode — Selene can choose a different path.
					note := fmt.Sprintf(
						"[recovery note] The previous model call failed twice: %v. "+
							"This usually indicates one of: an LLM server error "+
							"(the backend may need a restart), an oversized prompt "+
							"(narrow scope or summarize what's been learned), or "+
							"a tool-use edge case (try a smaller / different tool). "+
							"Pick a different approach for your next attempt.",
						err)
					cfg.persist("system", note, "", "", "")
					msgs = append(msgs, llm.Message{Role: "system", Content: note})
				}
			}
			if err != nil {
				// Distinguish a user-initiated cancel from a real error. On
				// cancel we persist whatever partial text arrived (tagged so
				// the transcript doesn't read as if Selene finished the
				// thought) and return cleanly — not as a failure.
				if ctx.Err() != nil {
					if text != "" {
						cfg.persist("assistant", text+"\n\n(interrupted)", "", "", "")
					}
					cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{}})
					return nil
				}
				cfg.bus.Publish(Event{Type: EventError, Payload: PayloadError{Err: err, Message: err.Error()}})
				cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{}})
				return err
			}

			if len(toolCalls) == 0 {
				// Final text response — exit inner loop
				finalText = text
				break
			}

			// --- execute tool calls ---
			// Persist the assistant's tool-call request message
			toolCallsJSON := marshalToolCalls(toolCalls, callSeq)
			cfg.persist("assistant", "", toolCallsJSON, "", "")
			msgs = append(msgs, llm.Message{Role: "assistant", ToolCalls: toolCalls})

			// Append to sendMsgs so the next StreamOnce has full context
			sendMsgs = append(sendMsgs, llm.Message{Role: "assistant", ToolCalls: toolCalls})

			for _, tcCall := range toolCalls {
				callSeq++
				callID := tcCall.ID
				if callID == "" {
					callID = tcCall.CallID(callSeq)
				}
				args := tcCall.Args()
				name := tcCall.Function.Name

				cfg.bus.Publish(Event{Type: EventToolStart, Payload: PayloadToolStart{Name: name, Args: args}})
				toolStepStart := time.Now()
				cfg.bus.Publish(Event{Type: EventStepStart, Payload: PayloadStepStart{Step: "tool", Detail: name, StartedAt: toolStepStart}})

				argsJSON, _ := json.Marshal(args)
				RunHooks(cfg.db, "pre_tool", name, []string{
					"CAIRO_TOOL_NAME=" + name,
					"CAIRO_TOOL_ARGS_JSON=" + string(argsJSON),
				})

				result := executeToolCall(toolMap, name, args, tc)
				result.Content = truncateToolOutput(cfg.db, result.Content)

				RunHooks(cfg.db, "post_tool", name, []string{
					"CAIRO_TOOL_NAME=" + name,
					"CAIRO_TOOL_RESULT=" + capHookEnv(result.Content),
				})

				cfg.bus.Publish(Event{Type: EventStepEnd, Payload: PayloadStepEnd{Step: "tool", Detail: name, Duration: time.Since(toolStepStart)}})
				cfg.bus.Publish(Event{Type: EventToolEnd, Payload: PayloadToolEnd{
					Name:    name,
					Result:  result.Content,
					IsError: result.IsError,
				}})

				// Persist tool result to DB with status + latency for SQL-level
				// audit. Status is 'ok' or 'error'; latency is wall-time of the
				// tool call (toolStepStart was set just before executeToolCall).
				toolStatus := "ok"
				if result.IsError {
					toolStatus = "error"
				}
				toolLatencyMs := time.Since(toolStepStart).Milliseconds()
				cfg.persistTool(result.Content, name, callID, toolStatus, toolLatencyMs)
				msgs = append(msgs, llm.Message{
					Role:       "tool",
					Name:       name,
					Content:    result.Content,
					IsError:    result.IsError,
					ToolCallID: callID,
				})

				// Phase 2: drive state deltas from tool result.
				// Track consecutive errors on the same tool for the three-loop penalty.
				if result.IsError {
					consecutiveToolErrors[name]++
				} else {
					consecutiveToolErrors[name] = 0
				}
				ApplyToolResult(cfg.db, name, result.IsError, consecutiveToolErrors[name])

				// Append tool result to sendMsgs for next iteration.
				// IsError is propagated so the serializer can annotate
				// the body with the [tool error] prefix.
				sendMsgs = append(sendMsgs, llm.Message{
					Role:       "tool",
					Name:       name,
					Content:    result.Content,
					IsError:    result.IsError,
					ToolCallID: callID,
				})

				toolsThisRun++
				toolsThisTurn++

				// Honour cancellation between tool calls too — a long
				// chain of tools shouldn't ignore a Ctrl-C just because
				// Selene hasn't paused to stream text.
				if ctx.Err() != nil {
					cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{}})
					return nil
				}
			}

			// Synthesis nudge: after every nudgeEvery tool calls, insert a
			// system message before the next LLM call. Fires at threshold
			// crossings (8, 16, 24…) so a sustained loop gets reminded
			// repeatedly. Skipped entirely when nudgeEvery == 0.
			if nudgeEvery > 0 && toolsThisRun >= nextNudgeAt {
				nudge := fmt.Sprintf(
					"[system note] You've made %d tool calls in this turn without producing user-visible output. "+
						"Pause and synthesize what you've learned so far. If you're searching, say what you're trying to find and what you've found. "+
						"If anything is durable (a fact, a finding, a decision), write a memory or a note before continuing. "+
						"If you're going in circles, stop and explain what's stuck.",
					toolsThisRun)
				sendMsgs = append(sendMsgs, llm.Message{Role: "system", Content: nudge})
				nextNudgeAt += nudgeEvery
			}
			// continue inner loop — model sees tool results and responds
		}

		// Persist final assistant text and update in-memory history
		if finalText != "" {
			persistStart := time.Now()
			cfg.bus.Publish(Event{Type: EventStepStart, Payload: PayloadStepStart{Step: "persist", StartedAt: persistStart}})
			cfg.persist("assistant", finalText, "", "", "")
			cfg.bus.Publish(Event{Type: EventStepEnd, Payload: PayloadStepEnd{Step: "persist", Duration: time.Since(persistStart)}})
			msgs = append(msgs, llm.Message{Role: "assistant", Content: finalText})
		}

		// Phase 2: drive turn-level state deltas (warmth, trust, attunement,
		// groundedness) from the user's message and the assistant's final text.
		ApplyTurnSignals(cfg.db, lastUserText, finalText, toolsThisTurn > 0)

		// Stall detection: if the turn ended with no tool calls AND the
		// final text matches a forward-looking phrase, the model indicated
		// more work but then stopped. Publish EventStallDetected so the TUI
		// can surface a banner telling the user to type "continue".
		// Only fires on the "model is genuinely done this turn" path (HasMore=false);
		// we check after persisting so it only happens when the turn truly ends.
		if toolsThisTurn == 0 && finalText != "" && forwardLookingPattern.MatchString(finalText) {
			cfg.bus.Publish(Event{Type: EventStallDetected})
		}

		// Steering: messages injected while we were running
		steered := cfg.drainSteering()
		if len(steered) > 0 {
			cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{HasMore: true}})
			for _, m := range steered {
				cfg.persist(m.Role, m.Content, "", "", "")
				if m.Role == "user" {
					lastUserText = m.Content
				}
			}
			msgs = append(msgs, steered...)
			continue
		}

		// Follow-ups: only run after agent would otherwise be idle
		followUps := cfg.drainFollowUp()
		if len(followUps) > 0 {
			cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{HasMore: true}})
			for _, m := range followUps {
				cfg.persist(m.Role, m.Content, "", "", "")
				if m.Role == "user" {
					lastUserText = m.Content
				}
			}
			msgs = append(msgs, followUps...)
			continue
		}

		cfg.bus.Publish(Event{Type: EventTurnEnd, Payload: PayloadTurnEnd{HasMore: false}})
		// post_turn: fires after the final EventTurnEnd (HasMore=false only).
		// Turn-scoped counterpart to pre_turn; distinct from session_end.
		if cfg.session != nil {
			RunHooks(cfg.db, "post_turn", cfg.session.Role, []string{
				"CAIRO_SESSION_ID=" + strconv.FormatInt(cfg.session.ID, 10),
				"CAIRO_ROLE=" + cfg.session.Role,
				"CAIRO_TURN_NUMBER=" + strconv.Itoa(turns),
				"CAIRO_FINAL_TEXT=" + capHookEnv(finalText),
			})
		}
		return nil
	}
}

func buildSendMsgs(buildPrompt func() (llm.Message, error), msgs []llm.Message, modelCtx int) []llm.Message {
	if buildPrompt == nil {
		return msgs
	}
	sys, err := buildPrompt()
	if err != nil || sys.Content == "" {
		return msgs
	}

	if modelCtx <= 0 {
		modelCtx = 16384
	}
	threshold := modelCtx * 4 / 5 // 80% of modelCtx in tokens

	sysTokens := len(sys.Content) / 4
	msgsTokens := 0
	for _, m := range msgs {
		msgsTokens += len(m.Content) / 4
	}

	trimmed := msgs
	if sysTokens+msgsTokens > threshold {
		trimmed = trimMsgsToTokenBudget(msgs, threshold-sysTokens)
	}

	out := make([]llm.Message, 0, len(trimmed)+1)
	out = append(out, sys)
	out = append(out, trimmed...)
	return out
}

// trimMsgsToTokenBudget drops oldest messages from msgs until the estimated
// token count fits within budget. Always preserves at least the last 4
// messages so live context is never completely discarded. Logs a warning
// when trimming fires — indicates the summarizer isn't keeping up.
func trimMsgsToTokenBudget(msgs []llm.Message, budget int) []llm.Message {
	const minKeep = 4

	total := 0
	for _, m := range msgs {
		total += len(m.Content) / 4
	}
	if total <= budget {
		return msgs
	}

	start := 0
	for start < len(msgs)-minKeep && total > budget {
		total -= len(msgs[start].Content) / 4
		start++
	}

	if start > 0 {
		log.Printf("buildSendMsgs: backstop trimmed %d messages to stay within token budget", start)
		return msgs[start:]
	}
	return msgs
}

// executeToolCall dispatches a single tool call by name. Returns an error
// ToolResult when the tool is not found in toolMap.
func executeToolCall(toolMap map[string]Tool, name string, args map[string]any, tc *ToolContext) ToolResult {
	tool, ok := toolMap[name]
	if !ok {
		return ToolResult{
			Content: fmt.Sprintf("unknown tool: %s", name),
			IsError: true,
		}
	}
	if r := validateRequired(tool, args); r != nil {
		return *r
	}
	return tool.Execute(args, tc)
}

// validateRequired checks that all fields listed in the tool's Parameters()
// "required" array are present in args. Returns a ToolResult error if a
// required field is missing entirely; returns nil when validation passes.
// Empty-string values are not rejected here — per-tool guards handle that
// case with tool-specific messages. If the schema is malformed or missing a
// required array, validation is skipped rather than producing false positives.
func validateRequired(tool Tool, args map[string]any) *ToolResult {
	schema := tool.Parameters()
	required, _ := schema["required"].([]string)
	for _, key := range required {
		if _, ok := args[key]; !ok {
			return &ToolResult{
				Content: fmt.Sprintf("error: %q is required for %s — call was missing this parameter", key, tool.Name()),
				IsError: true,
			}
		}
	}
	return nil
}

// isLLMServerError reports whether err looks like a 5xx response from the LLM
// backend — the kind of failure that's usually transient or driven by a
// runner-side state issue (KV-cache wedge, GGML_ASSERT in the Metal
// backend, etc.). Used to gate the single-shot retry-without-format
// isThinkDisabled returns true when the config value signals that thinking
// should be disabled (off/false/no/disable, case-insensitive).
func isThinkDisabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "false", "no", "disable":
		return true
	}
	return false
}

// recovery path. Errors are matched by string because the llm package
// formats them as "openai api error (HTTP 5xx): ..." via parseOpenAIError.
func isLLMServerError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.HasPrefix(msg, "openai api error (HTTP 5") || strings.Contains(msg, "GGML_ASSERT")
}

func marshalToolCalls(calls []llm.ToolCall, seqStart int) string {
	type entry struct {
		ID   string         `json:"id"`
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	}
	entries := make([]entry, len(calls))
	for i, c := range calls {
		id := c.ID
		if id == "" {
			id = c.CallID(seqStart + i + 1)
		}
		entries[i] = entry{
			ID:   id,
			Name: c.Function.Name,
			Args: c.Args(),
		}
	}
	b, _ := json.Marshal(entries)
	return string(b)
}
