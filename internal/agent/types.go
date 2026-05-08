package agent

import (
	"context"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/providers"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// Tool is the interface every built-in and custom tool must satisfy.
type Tool interface {
	Name() string
	Description() string
	Parameters() map[string]any // JSON Schema object
	Execute(args map[string]any, ctx *ToolContext) ToolResult
}

// ToolResult carries both the model-visible content and UI-only detail.
type ToolResult struct {
	Content string // returned to the model as the tool result
	Details any    // structured data for the TUI renderer (never sent to model)
	IsError bool
}

// ConfigStore is the subset of db.ConfigQ that tools need.
type ConfigStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// EventSink is the subset of Bus that tools need.
type EventSink interface {
	Publish(e Event)
}

// Discipline mode constants. DisciplineReadonly is the most restrictive;
// DisciplineFull is current behavior. Tools check ctx.DisciplineMode against
// their required tier before executing. Higher mode value = more permissive.
//
// Mapping to capability surface:
//
//	DisciplineReadonly (1) — read-class tools only (read, search, fetch,
//	                         memory_tool/search, learn/list+search+describe+status,
//	                         skill/list+read+search, tool_list_builtin).
//	DisciplineScoped   (2) — readonly + file writes within CWD (write, edit,
//	                         choose, say). NOT bash, agent, task, job, or soul.
//	DisciplineFull     (3) — current behavior: all tools per the role allowlist.
//	                         unsafe_mode is only meaningful at this level.
//
// A tool's required tier is looked up in tools.ToolTier(name). Action-dispatched
// tools (memory_tool, learn, skill) enforce per-action tiers inside Execute.
// Discipline mode values: lower is more restrictive.
const DisciplineReadonly = 1
const DisciplineScoped = 2
const DisciplineFull = 3

// DisciplineModeName returns the human-readable name for a discipline mode constant.
func DisciplineModeName(mode int) string {
	switch mode {
	case DisciplineReadonly:
		return "readonly"
	case DisciplineScoped:
		return "scoped"
	default:
		return "full"
	}
}

// ToolContext is passed to every tool Execute call.
// Session and Tools are populated by the agent loop; they let tools reason
// about the being's current state. Both may be nil in contexts that don't
// have an Agent (e.g. future standalone tool invocation); tools must guard.
type ToolContext struct {
	Ctx          context.Context
	WorkDir      string
	DB           *sqliteopen.DB // keep for now — tools need too many Q-types to decompose fully
	Config       ConfigStore    // db.ConfigQ satisfies this; use for config reads
	Bus          EventSink      // *Bus satisfies this
	Session      *sessions.Session
	Tools        []Tool
	Registry     *providers.Registry // may be nil; callers should fall back to providers.Default()
	IsBackground bool                // true when running as a background task; choose returns error
	// DisciplineMode gates tool execution: 1=readonly, 2=scoped, 3=full (current behavior).
	// Populated by the agent loop from the session record; individual tool Execute methods
	// call checkDiscipline() to enforce their tier requirement.
	DisciplineMode int
}

// ToLLM converts a Tool to the llm.ToolDef wire format.
func ToLLM(t Tool) llm.ToolDef {
	var def llm.ToolDef
	def.Type = "function"
	def.Function.Name = t.Name()
	def.Function.Description = t.Description()
	def.Function.Parameters = t.Parameters()
	return def
}
