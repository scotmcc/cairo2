package tools

// dbtools.go holds tool_list_builtin (the registry mirror).

import (
	"fmt"
	"sort"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
)

// --- tool_list_builtin ---

// toolListBuiltinTool returns the names of all built-in tools.
// The list is captured from the live registry at construction time so it
// stays in lockstep with tools.Default() without a hardcoded duplicate.
type toolListBuiltinTool struct {
	names []string
}

// ToolListBuiltin constructs the tool. Pass the list of registered built-in
// names — typically derived by iterating tools.Default() and reading Name().
func ToolListBuiltin(names []string) agent.Tool {
	sorted := make([]string, len(names))
	copy(sorted, names)
	sort.Strings(sorted)
	return toolListBuiltinTool{names: sorted}
}

func (toolListBuiltinTool) Name() string        { return "tool_list_builtin" }
func (toolListBuiltinTool) Description() string { return "List all built-in tools (not custom tools)." }
func (toolListBuiltinTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t toolListBuiltinTool) Execute(_ map[string]any, _ *agent.ToolContext) agent.ToolResult {
	if len(t.names) == 0 {
		return agent.ToolResult{Content: "no built-in tools"}
	}
	var b strings.Builder
	for _, name := range t.names {
		fmt.Fprintf(&b, "%s\n", name)
	}
	return agent.ToolResult{Content: strings.TrimSpace(b.String()), Details: t.names}
}
