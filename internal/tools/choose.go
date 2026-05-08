package tools

import "github.com/scotmcc/cairo2/internal/agent"

// ChoiceRequest is sent on the channel when the AI calls choose().
// The TUI receives it, presents the overlay, and sends the selected option
// back on Result.
type ChoiceRequest struct {
	Title   string
	Options []string
	Result  chan<- string // the tool writes the selected option here
}

type chooseTool struct {
	requests chan<- ChoiceRequest // nil when running headless
}

// Choose constructs the choose tool. Pass the channel the TUI will drain;
// pass nil for headless / background use (the tool will always return an error).
func Choose(requests chan<- ChoiceRequest) agent.Tool {
	return chooseTool{requests: requests}
}

func (chooseTool) Name() string { return "choose" }

func (chooseTool) Description() string {
	return "Present a choice to the user and wait for their selection. " +
		"Use when you need explicit human approval before proceeding. " +
		"Args: title (string), options (array of strings)."
}

func (chooseTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": prop("string", "Required. The question or decision to present."),
			"options": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Required. The choices to offer (2–8 items).",
			},
		},
		"required": []string{"title", "options"},
	}
}

// DeclinedSentinel is sent on the result channel when the user dismisses the
// choice overlay with esc or ctrl+c. The Execute method checks for it and
// returns an error result so the agent knows the user declined.
const DeclinedSentinel = ""

func (t chooseTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	// choose requires scoped mode (tier 2) — it interrupts the user for input.
	if r, refused := checkDiscipline(ctx, "choose", "", 2); refused {
		return r
	}
	if ctx.IsBackground || t.requests == nil {
		return agent.ToolResult{
			Content: "error: choose is unavailable in this session — there is no interactive user to prompt. " +
				"Make the decision yourself based on the context, state your choice and reasoning, then proceed. " +
				"Do not call choose again in this session.",
			IsError: true,
		}
	}

	title := strArg(args, "title")
	if title == "" {
		return agent.ToolResult{Content: "error: title is required", IsError: true}
	}

	var options []string
	if raw, ok := args["options"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok && s != "" {
				options = append(options, s)
			}
		}
	}
	if len(options) < 2 {
		return agent.ToolResult{Content: "error: at least 2 options are required", IsError: true}
	}
	if len(options) > 8 {
		options = options[:8]
	}

	result := make(chan string, 1)
	select {
	case t.requests <- ChoiceRequest{Title: title, Options: options, Result: result}:
	case <-ctx.Ctx.Done():
		return agent.ToolResult{Content: "cancelled", IsError: true}
	}

	select {
	case chosen := <-result:
		if chosen == DeclinedSentinel {
			return agent.ToolResult{Content: "(user declined)", IsError: true}
		}
		return agent.ToolResult{Content: chosen}
	case <-ctx.Ctx.Done():
		return agent.ToolResult{Content: "cancelled", IsError: true}
	}
}
