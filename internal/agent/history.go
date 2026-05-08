package agent

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/identity"
)

// namedAspectThreshold is the minimum alignment score for an aspect to appear
// by name in the inner_voice block. Hardcoded by design — see
// .claude/jobs/consider-role-gate/briefing.md.
const namedAspectThreshold = 0.3

// namedAspectThoughtMax caps each rendered thought to keep prompt cost bounded.
const namedAspectThoughtMax = 200

// formatNamedAspects renders a "Aspects that rose:" block from per-aspect
// activations. Aspects below namedAspectThreshold are dropped; survivors are
// sorted by alignment desc and each thought is truncated to
// namedAspectThoughtMax chars. Returns "" when nothing survives the filter.
func formatNamedAspects(acts []identity.ConsiderActivation) string {
	filtered := make([]identity.ConsiderActivation, 0, len(acts))
	for _, a := range acts {
		if a.Alignment >= namedAspectThreshold {
			filtered = append(filtered, a)
		}
	}
	if len(filtered) == 0 {
		return ""
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Alignment > filtered[j].Alignment
	})
	var b strings.Builder
	b.WriteString("### Aspects that rose:\n")
	for _, a := range filtered {
		thought := strings.TrimSpace(a.Thought)
		if len(thought) > namedAspectThoughtMax {
			thought = thought[:namedAspectThoughtMax] + "…"
		}
		fmt.Fprintf(&b, "- %s (alignment %.2f): %q\n", a.AspectName, a.Alignment, thought)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (a *Agent) loadHistory() error {
	// Only load unsummarized messages as active context.
	// Summarized messages are represented in the context via summary blocks
	// injected into the system prompt by BuildSystemPrompt.
	msgs, err := a.db.Messages.UnsummarizedForSession(a.session.ID)
	if err != nil {
		return err
	}
	a.history = make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if m.ToolCalls != "" {
				// Reconstruct assistant tool-call request messages from persisted JSON.
				toolCalls, err := unmarshalToolCalls(m.ToolCalls)
				if err == nil && len(toolCalls) > 0 {
					a.history = append(a.history, llm.Message{Role: "assistant", ToolCalls: toolCalls})
					continue
				}
			}
			if m.Content != "" {
				a.history = append(a.history, llm.Message{Role: "assistant", Content: m.Content})
			}
			// skip empty-content assistant rows with no tool calls (shouldn't happen, but defensive)
		case "tool":
			a.history = append(a.history, llm.Message{Role: "tool", Name: m.ToolName, Content: m.Content, ToolCallID: m.ToolID})
		case "user":
			// Wrap with the consider summary that framed this message at
			// the time it arrived, when one was recorded.
			a.history = append(a.history, llm.Message{Role: "user", Content: wrapUserMessage(m.Content, m.InnerVoice)})
		default:
			// system, etc.
			a.history = append(a.history, llm.Message{Role: m.Role, Content: m.Content})
		}
	}

	// Detect incomplete turns: if the process crashed mid-turn, the DB may
	// contain an assistant tool-call row followed by fewer tool-result rows
	// than there are tool calls. This produces an invalid message sequence
	// for the LLM (mismatched call/result counts). Strip the incomplete turn
	// and inject a system note so the resumed session starts from a clean state.
	repaired, _, _ := repairIncompleteTurn(a.history)
	a.history = repaired

	return nil
}

// syncHistory reloads a.history from DB, keeping only unsummarized messages.
// Called after a successful Summarize() so in-memory history stays bounded —
// the summarizer marks old messages as summarized and injects digests into the
// system prompt, so keeping raw messages in a.history would double-count them.
//
// Safe to call from any goroutine. Skips the rebuild when streaming is active:
// a new turn owns a.history, and the next summarizer run will pick this up.
// The DB read happens before acquiring the lock; if a new Prompt() call adds
// a message after the read, it appends under a.mu and will not be lost.
func (a *Agent) syncHistory() {
	msgs, err := a.db.Messages.UnsummarizedForSession(a.session.ID)
	if err != nil {
		log.Printf("syncHistory: query failed: %v", err)
		return
	}
	rebuilt := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "assistant":
			if m.ToolCalls != "" {
				toolCalls, tErr := unmarshalToolCalls(m.ToolCalls)
				if tErr == nil && len(toolCalls) > 0 {
					rebuilt = append(rebuilt, llm.Message{Role: "assistant", ToolCalls: toolCalls})
					continue
				}
			}
			if m.Content != "" {
				rebuilt = append(rebuilt, llm.Message{Role: "assistant", Content: m.Content})
			}
		case "tool":
			rebuilt = append(rebuilt, llm.Message{Role: "tool", Name: m.ToolName, Content: m.Content, ToolCallID: m.ToolID})
		case "user":
			rebuilt = append(rebuilt, llm.Message{Role: "user", Content: wrapUserMessage(m.Content, m.InnerVoice)})
		default:
			rebuilt = append(rebuilt, llm.Message{Role: m.Role, Content: m.Content})
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	// A new turn started while we were reading DB — don't clobber a.history
	// that runLoop is actively appending to.
	if a.streaming {
		return
	}
	a.history = rebuilt
}

// repairIncompleteTurn scans the tail of history for a partially-executed
// tool-call turn and strips it if found. A turn is incomplete when the last
// assistant message has N tool calls but is followed by fewer than N tool
// results. In that case the incomplete turn is removed and a system note is
// appended so the LLM knows the previous session was interrupted.
// Returns the (possibly repaired) history, a descriptive note, and whether a
// repair was performed — without modifying the input slice.
func repairIncompleteTurn(history []llm.Message) (repaired []llm.Message, note string, didRepair bool) {
	n := len(history)
	if n == 0 {
		return history, "", false
	}

	// Count trailing tool-result messages.
	trailingTools := 0
	for i := n - 1; i >= 0; i-- {
		if history[i].Role == "tool" {
			trailingTools++
		} else {
			break
		}
	}

	// The message immediately before the trailing tool results must be an
	// assistant message with tool calls for there to be anything to repair.
	assistantIdx := n - 1 - trailingTools
	if assistantIdx < 0 {
		return history, "", false
	}
	asst := history[assistantIdx]
	if asst.Role != "assistant" || len(asst.ToolCalls) == 0 {
		return history, "", false
	}

	// If all tool calls have corresponding results, the turn is complete.
	if trailingTools >= len(asst.ToolCalls) {
		return history, "", false
	}

	// Incomplete: strip the assistant tool-call row and any partial results,
	// then append a system note so the LLM resumes with clean context.
	const repairNote = "[system] Note: the previous session was interrupted mid-turn. The last tool call sequence did not complete. Please acknowledge and ask how to proceed."
	out := make([]llm.Message, assistantIdx, assistantIdx+1)
	copy(out, history[:assistantIdx])
	out = append(out, llm.Message{
		Role:    "system",
		Content: repairNote,
	})
	return out, repairNote, true
}

// persistMessage is called by runLoop for every message produced during a turn.
// a.history is the canonical log; runLoop also appends to its local msgs copy at each call site.
// Harness-injected nudges (synthesis nudge) are sendMsgs-only by design — they must not replay on resume.
func (a *Agent) persistMessage(role, content, toolCallsJSON, toolName, toolID string) {
	if _, err := a.db.Messages.Add(a.session.ID, role, content, toolCallsJSON, toolName, toolID); err != nil {
		log.Printf("persistMessage: Add session=%d role=%s: %v", a.session.ID, role, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch role {
	case "assistant":
		if toolCallsJSON != "" {
			if toolCalls, err := unmarshalToolCalls(toolCallsJSON); err == nil && len(toolCalls) > 0 {
				a.history = append(a.history, llm.Message{Role: "assistant", ToolCalls: toolCalls})
				return
			}
		}
		if content != "" {
			a.history = append(a.history, llm.Message{Role: "assistant", Content: content})
		}
	case "tool":
		a.history = append(a.history, llm.Message{Role: "tool", Name: toolName, Content: content, ToolCallID: toolID})
	case "system":
		// System notes surface mid-conversation context (recovery
		// notes after Ollama crashes, for example). Threading them
		// through history means the next turn sees the note and can
		// adapt without us having to surface it via EventError alone.
		if content != "" {
			a.history = append(a.history, llm.Message{Role: "system", Content: content})
		}
	}
}

// persistToolMessage is the role='tool' counterpart to persistMessage. It
// routes through MessageQ.AddTool so tool_status and tool_latency_ms are
// stamped at insert time — the audit columns that make tool error rate and
// latency queryable from SQL instead of grepping logs.
func (a *Agent) persistToolMessage(content, toolName, toolID, status string, latencyMs int64) {
	if _, err := a.db.Messages.AddTool(a.session.ID, content, toolName, toolID, status, latencyMs); err != nil {
		log.Printf("persistToolMessage: AddTool tool=%s id=%s: %v", toolName, toolID, err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, llm.Message{Role: "tool", Name: toolName, Content: content, ToolCallID: toolID})
}

// wrapUserMessage builds the user-message body that's actually sent to the LLM.
// When innerVoice is empty (consider disabled, didn't fire, or errored), the
// user's text passes through unchanged. When non-empty, the summary is
// prepended under a `## What rose in you when you read this` heading and the
// user's text is delimited under `## What the user said`.
//
// The system prompt has a permanent meta block that teaches Selene how to
// read these embedded sections — first-person felt experience, not commentary;
// never quote; let it shape tone and pace; capability claims by inner voices
// are wrong, etc.
func wrapUserMessage(text, innerVoice string) string {
	if innerVoice == "" {
		return text
	}
	return "## What rose in you when you read this\n\n" +
		innerVoice + "\n\n" +
		"## What the user said\n\n" +
		text
}

// unmarshalToolCalls reconstructs llm.ToolCall slice from the JSON stored in the DB.
// The stored format is [{id, name, args}] — we map back to llm.ToolCall's shape.
func unmarshalToolCalls(raw string) ([]llm.ToolCall, error) {
	var stored []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Args any    `json:"args"`
	}
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}
	out := make([]llm.ToolCall, len(stored))
	for i, s := range stored {
		out[i].ID = s.ID
		out[i].Function.Name = s.Name
		out[i].Function.Arguments = s.Args
	}
	return out, nil
}
