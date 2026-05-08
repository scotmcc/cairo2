package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bashCtx returns a ToolContext wired for bash (DisciplineFull, real workdir).
func bashCtx(t *testing.T) *agent.ToolContext {
	t.Helper()
	tmp := t.TempDir()
	d := openTestDB(t)
	return &agent.ToolContext{
		Ctx:            context.Background(),
		WorkDir:        tmp,
		DB:             d,
		DisciplineMode: agent.DisciplineFull,
	}
}

func Test_BashTool_RunsCommandAndReturnsStdout(t *testing.T) {
	ctx := bashCtx(t)
	result := Bash().Execute(map[string]any{"command": "echo hello"}, ctx)
	require.False(t, result.IsError, "unexpected error: %s", result.Content)
	assert.Contains(t, result.Content, "hello")
}

func Test_BashTool_CapturesStderr(t *testing.T) {
	ctx := bashCtx(t)
	// bash merges stdout+stderr into the output buffer
	result := Bash().Execute(map[string]any{"command": "echo err >&2"}, ctx)
	require.False(t, result.IsError, "unexpected error: %s", result.Content)
	assert.Contains(t, result.Content, "err")
}

func Test_BashTool_NonZeroExitIsError(t *testing.T) {
	ctx := bashCtx(t)
	result := Bash().Execute(map[string]any{"command": "exit 1"}, ctx)
	assert.True(t, result.IsError, "exit 1 should be an error")
	assert.Contains(t, result.Content, "[exit 1]")
}

func Test_BashTool_EmptyCommandIsError(t *testing.T) {
	ctx := bashCtx(t)
	result := Bash().Execute(map[string]any{"command": ""}, ctx)
	assert.True(t, result.IsError)
}

func Test_BashTool_WorkDirIsRespected(t *testing.T) {
	ctx := bashCtx(t)
	// pwd should return the temp workdir, not the process cwd
	result := Bash().Execute(map[string]any{"command": "pwd"}, ctx)
	require.False(t, result.IsError, "unexpected error: %s", result.Content)
	assert.Contains(t, strings.TrimSpace(result.Content), ctx.WorkDir)
}

func Test_BashTool_RefusedUnderScoped(t *testing.T) {
	ctx := bashCtx(t)
	ctx.DisciplineMode = agent.DisciplineScoped
	result := Bash().Execute(map[string]any{"command": "echo hi"}, ctx)
	assert.True(t, result.IsError)
	assert.Contains(t, result.Content, "refused")
}
