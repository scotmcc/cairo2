package llm

import "fmt"

// Message is a single turn in a chat conversation.
type Message struct {
	Role       string     `json:"role"` // system | user | assistant | tool
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"` // tool function name on role=tool messages — required by some model templates
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // required on role=tool messages (OpenAI spec)
	// IsError marks a tool result message as an error. OpenAI has no native
	// error role, so the serializer prefixes the content with "[tool error] "
	// when this flag is set, letting the model detect and handle failures.
	IsError bool `json:"-"` // not sent to LLM; handled in serialization
}

// ToolCall is a single tool invocation requested by the model.
type ToolCall struct {
	Type     string `json:"type"`         // Always "function" for OpenAI-spec tool calls. Required by spec on outbound; populated from server on inbound.
	ID       string `json:"id,omitempty"` // server-emitted call ID; empty for backends that don't emit one
	Function struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"` // map[string]any or raw JSON string
	} `json:"function"`
}

// Args parses tool call arguments — handles both map and JSON-string forms.
func (tc *ToolCall) Args() map[string]any {
	return normalizeArgs(tc.Function.Arguments)
}

// CallID returns a stable synthetic ID for this tool call.
// Used as fallback when the server did not emit an ID.
func (tc *ToolCall) CallID(seq int) string {
	return fmt.Sprintf("call_%s_%d", tc.Function.Name, seq)
}

// ToolDef is an OpenAI-compatible tool definition.
type ToolDef struct {
	Type     string `json:"type"` // always "function"
	Function struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Parameters  any    `json:"parameters"` // JSON Schema object
	} `json:"function"`
}

// ChatOptions controls optional model behaviour.
type ChatOptions struct {
	ThinkBudget int // max thinking chars before budget-exceeded retry
	// Format opts into OpenAI's structured-output mode. Pass the literal
	// string "json" for best-effort JSON, or a JSON Schema (as a
	// map[string]any) to constrain sampling to an exact shape. nil disables
	// structured output — the default free-form chat behaviour.
	Format any
	// When true, sends chat_template_kwargs.enable_thinking=false to disable
	// model thinking. Zero value (false) = use model's default (typically
	// thinking on for Qwen3 family).
	DisableThinking bool
}
