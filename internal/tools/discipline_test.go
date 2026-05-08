package tools

import (
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
)

// TestDiscipline_WriteClassToolsRefuseUnderReadonly asserts that every
// write-class tool (tier 2 or 3) returns IsError with "refused" when the
// discipline mode is DisciplineReadonly. Covers both direct tools and
// action-dispatched tools with their write-only actions.
func TestDiscipline_WriteClassToolsRefuseUnderReadonly(t *testing.T) {
	d := openTestDB(t)
	embed := &EmbedClient{Embedder: stubEmbedder{}, Model: "stub"}

	tests := []struct {
		tool   agent.Tool
		args   map[string]any
		action string
	}{
		{Write(), map[string]any{"path": "/tmp/x", "content": "y"}, ""},
		{Edit(), map[string]any{"path": "/tmp/x", "old_string": "a", "new_string": "b"}, ""},
		{Bash(), map[string]any{"command": "echo hi"}, ""},
		{Say(d), map[string]any{"text": "hi"}, ""},
		{Choose(nil), map[string]any{"title": "q", "options": []any{"a", "b"}}, ""},
		{MemoryTool(d, embed), map[string]any{"action": "add", "content": "x"}, "add"},
		{Learn(d, embed), map[string]any{"action": "add", "path": "."}, "add"},
		{Learn(d, embed), map[string]any{"action": "forget", "project": "x"}, "forget"},
		{Soul(d), map[string]any{"action": "set", "content": "x"}, "set"},
	}

	ctx := &agent.ToolContext{
		DB:             d,
		WorkDir:        t.TempDir(),
		DisciplineMode: agent.DisciplineReadonly,
	}

	for _, tc := range tests {
		name := tc.tool.Name()
		if tc.action != "" {
			name = name + "/" + tc.action
		}
		t.Run(name, func(t *testing.T) {
			result := tc.tool.Execute(tc.args, ctx)
			if !result.IsError {
				t.Errorf("%s: expected discipline refusal, got success: %q", name, result.Content)
				return
			}
			if !strings.Contains(result.Content, "refused") {
				t.Errorf("%s: expected 'refused' in error content, got: %q", name, result.Content)
			}
		})
	}
}

// TestDiscipline_ReadonlyToolsPermittedUnderReadonly asserts that tier-1
// tools and read-only actions on action-dispatched tools are not refused
// under DisciplineReadonly. A "refused" error from any of these is a bug.
func TestDiscipline_ReadonlyToolsPermittedUnderReadonly(t *testing.T) {
	d := openTestDB(t)
	embed := &EmbedClient{Embedder: stubEmbedder{}, Model: "stub"}
	ctx := &agent.ToolContext{
		DB:             d,
		WorkDir:        t.TempDir(),
		DisciplineMode: agent.DisciplineReadonly,
	}
	readonlyTools := []struct {
		tool agent.Tool
		args map[string]any
	}{
		{MemoryTool(d, embed), map[string]any{"action": "search", "query": "x"}},
		{Learn(d, embed), map[string]any{"action": "list"}},
	}
	for _, tc := range readonlyTools {
		result := tc.tool.Execute(tc.args, ctx)
		if result.IsError && strings.Contains(result.Content, "refused") {
			t.Errorf("%s: readonly tool should not be refused under DisciplineReadonly: %q",
				tc.tool.Name(), result.Content)
		}
	}
}

// TestDiscipline_AllDefaultToolsHaveTierEntry asserts that every tool in
// Default() maps to a tier in [1,3]. Unknown tools default to tier 3 via
// ToolTier(), so this guards against invalid tier values being introduced.
func TestDiscipline_AllDefaultToolsHaveTierEntry(t *testing.T) {
	d := openTestDB(t)
	tools := Default(d, nil, "", nil)
	for _, tool := range tools {
		tier := ToolTier(tool.Name())
		if tier < 1 || tier > 3 {
			t.Errorf("tool %q has invalid tier %d", tool.Name(), tier)
		}
	}
}
