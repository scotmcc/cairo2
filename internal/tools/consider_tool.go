package tools

import (
	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/agent/consider"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

type considerTool struct {
	db  *sqliteopen.DB
	llm *llm.Client
}

// ConsiderTool returns a tool that runs the inner-dialogue (consider) step on demand.
// Receives database and llmClient at construction — same pattern as Fetch and MemoryTool.
func ConsiderTool(database *sqliteopen.DB, llmClient *llm.Client) agent.Tool {
	return considerTool{db: database, llm: llmClient}
}

func (considerTool) Name() string { return "consider_input" }

func (considerTool) Description() string {
	return `Run the inner-dialogue (consider) step for the current turn.

Invoke this when the user's message involves:
- A decision with real tradeoffs (not a simple factual lookup)
- An emotionally charged or interpersonally sensitive topic
- A request where multiple competing values apply (speed vs. correctness, brevity vs. completeness)
- A situation where you feel pulled toward a quick answer but sense the question is deeper than it appears

Do NOT invoke for routine requests, confirmations, or simple lookups — consider adds latency
(N parallel LLM calls) and should be reserved for turns where the pre-conscious friction is real.

Returns the inner-voice summary as a string. Use it to inform your response.`
}

func (considerTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": prop("string", "The user message to run consider on. Required."),
		},
		"required": []string{"message"},
	}
}

func (t considerTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	msg := strArg(args, "message")
	if msg == "" {
		return agent.ToolResult{Content: "error: message is required for consider_input", IsError: true}
	}

	// Discover the current turn's user message so the canonical entry can
	// attach inner_voice and link activations. Tolerate missing — early in a
	// session the row may not exist yet, in which case ConsiderInput skips
	// the row-bound side effects but still persists activation rows.
	var userMsgID int64
	if userMsg, err := t.db.Messages.LatestUserForSession(ctx.Session.ID); err == nil && userMsg != nil {
		userMsgID = userMsg.ID
	}

	result, _, err := agent.ConsiderInput(ctx.Ctx, t.db, t.llm, noopPublisher{}, ctx.Session.ID, ctx.Session.Role, userMsgID, msg, "tool")
	if err != nil {
		return agent.ToolResult{Content: "error: consider_input failed: " + err.Error(), IsError: true}
	}
	if result.Summary == "" {
		return agent.ToolResult{Content: "(consider_input returned no output — no aspects are enabled or no model is configured)"}
	}
	return agent.ToolResult{Content: result.Summary}
}

// noopPublisher satisfies consider.EventPublisher without publishing anywhere.
type noopPublisher struct{}

func (noopPublisher) Publish(_ consider.PublishEvent) {}
