package tools

import (
	"fmt"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// configTool wraps the config key-value store, giving programmatic access to settings
// currently only available via TUI. Tier 3 — identity-layer tool.
type configTool struct{ db *sqliteopen.DB }

func ConfigTool(database *sqliteopen.DB) agent.Tool { return configTool{db: database} }

func (configTool) Name() string { return "config" }

func (configTool) Description() string {
	return `Read, set, or list Cairo configuration values.
- config(action="get", key="...") — retrieve a single config value by key.
  Returns empty string if the key is not set.
- config(action="set", key="...", value="...") — set or update a config value.
  Creates the key if it doesn't exist, updates if it does.
- config(action="all") — list all config keys and their current values as key=value pairs.`
}

func (configTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"get", "set", "all"},
				"description": "Operation: get (read a value), set (write or update), or all (list all keys). Required.",
			},
			"key":   prop("string", "Config key to read or write. Required for get and set."),
			"value": propOptional("string", "Value to set. Required for set.", ""),
		},
		"required": []string{"action"},
	}
}

func (t configTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "config", "", 3); refused {
		return r
	}
	switch strArg(args, "action") {
	case "get":
		return t.doGet(args)
	case "set":
		return t.doSet(args)
	case "all":
		return t.doAll()
	case "":
		return agent.ToolResult{Content: "error: action is required (get|set|all)", IsError: true}
	default:
		return agent.ToolResult{
			Content: fmt.Sprintf("error: unknown action %q — valid: get|set|all", strArg(args, "action")),
			IsError: true,
		}
	}
}

func (t configTool) doGet(args map[string]any) agent.ToolResult {
	key := strArg(args, "key")
	if key == "" {
		return agent.ToolResult{Content: "error: key is required for get", IsError: true}
	}
	val, err := t.db.Config.Get(key)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	if val == "" {
		return agent.ToolResult{Content: fmt.Sprintf("(key %q is not set)", key)}
	}
	return agent.ToolResult{Content: fmt.Sprintf("%s=%s", key, val)}
}

func (t configTool) doSet(args map[string]any) agent.ToolResult {
	key := strArg(args, "key")
	if key == "" {
		return agent.ToolResult{Content: "error: key is required for set", IsError: true}
	}
	value := strArg(args, "value")
	if err := t.db.Config.Set(key, value); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("config %q set to %q", key, value)}
}

func (t configTool) doAll() agent.ToolResult {
	all, err := t.db.Config.All()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	if len(all) == 0 {
		return agent.ToolResult{Content: "(no config keys set)"}
	}
	result := make([]string, 0, len(all))
	for k, v := range all {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return agent.ToolResult{Content: fmt.Sprintf("Config values:\n%s", strings.Join(result, "\n"))}
}
