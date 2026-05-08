package tools

import (
	"fmt"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/llm"
)

// toolTiers maps each built-in tool name to the minimum discipline mode
// required to call it. Tools not in this map default to DisciplineFull (3).
//
// Tier semantics (matching agent.Discipline* constants):
//
//	1 = DisciplineReadonly — observable, no state change
//	2 = DisciplineScoped   — file writes within CWD; no shell, no identity layer
//	3 = DisciplineFull     — current behavior; identity-layer tools also here
//
// Action-dispatched tools (memory_tool, learn, skill) have per-action tiers
// enforced inside their Execute methods; their map entry is their lowest
// required tier (the most permissive action's requirement).
var toolTiers = map[string]int{
	// Tier 1: readonly — observable local state, no writes, no network.
	"read":              1,
	"memory_tool":       1, // per-action enforcement inside Execute; search = tier 1
	"learn":             1, // per-action enforcement; list/search/describe/status = tier 1
	"skill":             1, // per-action enforcement; list/read/search = tier 1
	"tool_list_builtin": 1,
	// Tier 2: scoped side effects — local file writes within CWD, outbound HTTP,
	// or DB writes that are bounded and non-identity. No shell, no identity layer.
	"write":          2,
	"edit":           2,
	"say":            2, // outbound HTTP to TTS service + audio playback
	"choose":         2,
	"search":         2, // outbound HTTP to SearXNG / configured search backend
	"fetch":          2, // arbitrary outbound HTTP (URL fetch)
	"consider_input": 2, // writes consider_activations rows + N parallel LLM calls
	// Tier 3: full — current behavior; identity-layer tools included.
	"bash":        3,
	"agent":       3,
	"job":         3,
	"task":        3,
	"soul":        3,
	"worktree":    3,
	"merge_job":   3,
	"config":      3,
	"prompt_part": 3,
}

// ToolTier returns the minimum discipline mode required to call the named tool.
// Unknown tools default to DisciplineFull (3) so unregistered or custom tools
// are not silently granted wider access than intended.
func ToolTier(name string) int {
	if t, ok := toolTiers[name]; ok {
		return t
	}
	return agent.DisciplineFull
}

// DisciplineRefusal returns a ToolResult that signals the tool was refused
// because the current discipline mode is too restrictive. The message is
// visible to both Selene (in the tool result) and the user (in the transcript).
func DisciplineRefusal(mode int, toolName, action string) agent.ToolResult {
	modeName := agent.DisciplineModeName(mode)
	var msg string
	if action != "" {
		msg = fmt.Sprintf("(refused: discipline=%s does not allow %s(%s))", modeName, toolName, action)
	} else {
		msg = fmt.Sprintf("(refused: discipline=%s does not allow %s)", modeName, toolName)
	}
	return agent.ToolResult{Content: msg, IsError: true}
}

// Default returns the full set of built-in tools wired to the given DB.
// embedder and embedModel are optional — pass nil/empty to skip embedding.
// choiceRequests is the channel the TUI drains for choose() calls; pass nil
// for headless/background use (the tool will always return an error then).
//
// tool_list_builtin is appended last and receives the derived name list so
// it stays in sync automatically when tools are added or removed above.
func Default(database *db.DB, embedder Embedder, embedModel string, choiceRequests chan<- ChoiceRequest) []agent.Tool {
	embed := &EmbedClient{Embedder: embedder, Model: embedModel}
	// Extract the concrete LLM client for tools that need full LLM access
	// (summarize+embed). The Embedder interface is always *llm.Client in
	// practice; nil is safe — tools guard for missing deps.
	var llmClient *llm.Client
	if lc, ok := embedder.(*llm.Client); ok {
		llmClient = lc
	}
	tools := []agent.Tool{
		// filesystem
		Read(),
		Write(),
		Edit(),
		Bash(),
		// memory_tool (v0.3.0 consolidation — add + search across memories/facts/summaries)
		MemoryTool(database, embed),
		// skills (consolidated — list/read/create/update/delete/search)
		Skill(database, embed),
		// jobs + tasks (consolidated — create/list/update/delete; task also has ready, artifacts)
		Job(database),
		Task(database),
		// background agents (consolidated — spawn/wait/log)
		Agent(database),
		// soul (consolidated — get/set)
		Soul(database),
		// config (get/set/list key-value settings)
		ConfigTool(database),
		// prompt_part (manage system prompt parts)
		PromptPartTool(database),
		// web tools
		Search(database),
		Fetch(database, llmClient, embed),
		// voice output
		Say(database),
		// project map: per-project summarized + embedded file index
		Learn(database, embed),
		// interactive choice — nil channel in headless/background mode
		Choose(choiceRequests),
		// agent-invokable inner-dialogue step
		ConsiderTool(database, llmClient),
	}

	names := make([]string, 0, len(tools)+1)
	for _, t := range tools {
		names = append(names, t.Name())
	}
	names = append(names, "tool_list_builtin")
	tools = append(tools, ToolListBuiltin(names))
	return tools
}

// FilterByAllowlist is re-exported from the agent package so existing callers
// (tests, downstream code) keep working. New code should call agent.FilterByAllowlist
// directly. Living here would force agent → tools imports for the same logic.
func FilterByAllowlist(tools []agent.Tool, allowed []string) []agent.Tool {
	return agent.FilterByAllowlist(tools, allowed)
}

// LoadCustom loads enabled custom tools from the DB and returns them as agent.Tools.
// Each custom tool wraps its implementation script as an executable.
func LoadCustom(database *db.DB) ([]agent.Tool, error) {
	customs, err := database.Tools.Enabled()
	if err != nil {
		return nil, err
	}
	out := make([]agent.Tool, 0, len(customs))
	for _, ct := range customs {
		out = append(out, newCustomTool(ct, database))
	}
	return out, nil
}
