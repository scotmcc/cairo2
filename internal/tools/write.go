package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/scotmcc/cairo2/internal/agent"
)

type writeTool struct{}

func Write() agent.Tool { return writeTool{} }

func (writeTool) Name() string { return "write" }
func (writeTool) Description() string {
	return "Write content to a file, creating it and any parent directories if needed."
}
func (writeTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    prop("string", "Absolute or relative path to the file"),
			"content": prop("string", "Full content to write"),
		},
		"required": []string{"path", "content"},
	}
}

func (writeTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	// Discipline check: write requires scoped mode (tier 2).
	if r, refused := checkDiscipline(ctx, "write", "", 2); refused {
		return r
	}
	if strArg(args, "path") == "" {
		return agent.ToolResult{Content: "error: path is required for write", IsError: true}
	}
	path := resolvePath(strArg(args, "path"), ctx.WorkDir)
	content := strArg(args, "content")

	if err := checkWritePermission(ctx, path); err != nil {
		return agent.ToolResult{Content: err.Error(), IsError: true}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error creating directories: %v", err), IsError: true}
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error writing file: %v", err), IsError: true}
	}
	return agent.ToolResult{Content: fmt.Sprintf("wrote %d bytes to %s", len(content), path)}
}
