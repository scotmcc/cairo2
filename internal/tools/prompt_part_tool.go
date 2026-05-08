package tools

import (
	"fmt"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/db"
)

// promptPartTool wraps db.PromptQ, giving programmatic access to system prompt parts.
// Tier 3 — identity-layer tool.
type promptPartTool struct{ db *db.DB }

func PromptPartTool(database *db.DB) agent.Tool { return promptPartTool{db: database} }

func (promptPartTool) Name() string { return "prompt_part" }

func (promptPartTool) Description() string {
	return `Manage system prompt parts — reusable behaviors, role definitions, and procedural guidance that shape every session.
- prompt_part(action="add", key="...", content="...", trigger="...", load_order=0)
  Creates a new always-on prompt part (trigger="", load_order=0).
  Pass trigger="role:thinking_partner" for role-scoped parts, or "tool:bash" for tool-specific guidance.
- prompt_part(action="list") — list all prompt parts as numbered items with key, trigger, and enabled status.
- prompt_part(action="read", id=<int>) — read the full content of a prompt part by ID.
- prompt_part(action="update", id=<int>, content="...") — update the content of an existing prompt part.
- prompt_part(action="delete", id=<int>) — delete a prompt part by ID (permanent, no undo).
- prompt_part(action="set_enabled", id=<int>, enabled=true/false) — enable or disable a prompt part.`
}

func (promptPartTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"add", "list", "read", "update", "delete", "set_enabled"},
				"description": "Operation: add, list, read, update, delete, or set_enabled. Required.",
			},
			"id":         propOptional("integer", "Prompt part ID. Required for read, update, delete, set_enabled.", ""),
			"key":        propOptional("string", "Identifier/key for the prompt part. Required for add.", ""),
			"content":    prop("string", "Content text for the prompt part. Required for add and update."),
			"trigger":    propOptional("string", "Trigger: empty='' for always-on, 'role:thinking_partner' for role-scoped, 'tool:bash' for tool-specific. Optional for add.", ""),
			"load_order": propOptional("integer", "Load order (0-based). Lower loads first. Optional for add (default 0).", "0"),
			"enabled":    propOptional("boolean", "Whether the prompt part is enabled. Required for set_enabled.", "true"),
		},
		"required": []string{"action"},
	}
}

func (t promptPartTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "prompt_part", "", 3); refused {
		return r
	}
	switch strArg(args, "action") {
	case "add":
		return t.doAdd(args)
	case "list":
		return t.doList()
	case "read":
		return t.doRead(args)
	case "update":
		return t.doUpdate(args)
	case "delete":
		return t.doDelete(args)
	case "set_enabled":
		return t.doSetEnabled(args)
	case "":
		return agent.ToolResult{Content: "error: action is required (add|list|read|update|delete|set_enabled)", IsError: true}
	default:
		return agent.ToolResult{
			Content: fmt.Sprintf("error: unknown action %q — valid: add|list|read|update|delete|set_enabled", strArg(args, "action")),
			IsError: true,
		}
	}
}

func (t promptPartTool) doAdd(args map[string]any) agent.ToolResult {
	key := strArg(args, "key")
	content := strArg(args, "content")
	trigger := strArg(args, "trigger")
	loadOrder := intArg(args, "load_order", 0)

	if key == "" || content == "" {
		return agent.ToolResult{Content: "error: key and content are required for add", IsError: true}
	}

	err := t.db.Prompts.Add(key, content, trigger, loadOrder)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}

	triggerDesc := ""
	if trigger != "" {
		triggerDesc = fmt.Sprintf(", trigger=%q", trigger)
	}
	return agent.ToolResult{Content: fmt.Sprintf("prompt part added (key=%q, load_order=%d%s)", key, loadOrder, triggerDesc)}
}

func (t promptPartTool) doList() agent.ToolResult {
	parts, err := t.db.Prompts.All()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}

	if len(parts) == 0 {
		return agent.ToolResult{Content: "(no prompt parts found)"}
	}

	var result string
	for _, p := range parts {
		status := "disabled"
		if p.IsEnabled {
			status = "enabled"
		}
		triggerDisplay := "<always-on>"
		if p.Trigger != "" {
			triggerDisplay = p.Trigger
		}
		result += fmt.Sprintf("[%d] %s (trigger=%s, order=%d, %s)\n", p.ID, p.Key, triggerDisplay, p.LoadOrder, status)
		if len(p.Content) > 80 {
			result += "    " + truncateMemory(p.Content, 80) + "\n"
		} else {
			result += "    " + p.Content + "\n"
		}
	}
	return agent.ToolResult{Content: result}
}

func (t promptPartTool) doRead(args map[string]any) agent.ToolResult {
	id := intArg(args, "id", 0)
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for read", IsError: true}
	}

	part, err := t.db.Prompts.Get(int64(id))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}

	status := "disabled"
	if part.IsEnabled {
		status = "enabled"
	}
	triggerDisplay := "<always-on>"
	if part.Trigger != "" {
		triggerDisplay = part.Trigger
	}

	return agent.ToolResult{Content: fmt.Sprintf("Prompt part [%d] %s\n  trigger=%s, load_order=%d, status=%s\n\n%s",
		part.ID, part.Key, triggerDisplay, part.LoadOrder, status, part.Content)}
}

func (t promptPartTool) doUpdate(args map[string]any) agent.ToolResult {
	id := intArg(args, "id", 0)
	content := strArg(args, "content")

	if id == 0 || content == "" {
		return agent.ToolResult{Content: "error: id and content are required for update", IsError: true}
	}

	err := t.db.Prompts.Update(int64(id), content)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("prompt part %d updated", id)}
}

func (t promptPartTool) doDelete(args map[string]any) agent.ToolResult {
	id := intArg(args, "id", 0)
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for delete", IsError: true}
	}

	err := t.db.Prompts.Delete(int64(id))
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("prompt part %d deleted", id)}
}

func (t promptPartTool) doSetEnabled(args map[string]any) agent.ToolResult {
	id := intArg(args, "id", 0)
	enabled := true
	if v, ok := args["enabled"].(bool); ok {
		enabled = v
	}
	if id == 0 {
		return agent.ToolResult{Content: "error: id is required for set_enabled", IsError: true}
	}

	err := t.db.Prompts.SetEnabled(int64(id), enabled)
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: %v", err), IsError: true}
	}
	status := "disabled"
	if enabled {
		status = "enabled"
	}
	return agent.ToolResult{Content: fmt.Sprintf("prompt part %d %s", id, status)}
}
