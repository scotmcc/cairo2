package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// customTool adapts a db.CustomTool to agent.Tool at runtime.
// The management tool (custom_tool) was removed in v0.3.0; custom tools
// authored before that drop continue to run via this wrapper, loaded by
// LoadCustom() in registry.go.
type customTool struct {
	name           string
	description    string
	parameters     map[string]any
	implementation string
	implType       string
	db             *sqliteopen.DB
}

func newCustomTool(ct *identity.CustomTool, database *sqliteopen.DB) agent.Tool {
	var params map[string]any
	json.Unmarshal([]byte(ct.Parameters), &params)
	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return &customTool{
		name:           ct.Name,
		description:    ct.Description,
		parameters:     params,
		implementation: ct.Implementation,
		implType:       ct.ImplType,
		db:             database,
	}
}

func (t *customTool) Name() string               { return t.name }
func (t *customTool) Description() string        { return t.description }
func (t *customTool) Parameters() map[string]any { return t.parameters }

func buildEnvironment(args map[string]any, config map[string]string) []string {
	env := []string{
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		fmt.Sprintf("HOME=%s", os.Getenv("HOME")),
		fmt.Sprintf("TMPDIR=%s", os.Getenv("TMPDIR")),
		fmt.Sprintf("SHELL=%s", os.Getenv("SHELL")),
	}
	for k, v := range args {
		env = append(env, fmt.Sprintf("CAIRO_ARG_%s=%v", strings.ToUpper(k), v))
	}
	argsJSON, _ := json.Marshal(args)
	env = append(env, fmt.Sprintf("CAIRO_ARGS=%s", argsJSON))
	if extrasConfig, ok := config["safe_env_extras"]; ok && extrasConfig != "" {
		for _, extra := range strings.Split(extrasConfig, ",") {
			extra = strings.TrimSpace(extra)
			if extra != "" {
				if val := os.Getenv(extra); val != "" {
					env = append(env, fmt.Sprintf("%s=%s", extra, val))
				}
			}
		}
	}
	return env
}

func (t *customTool) Execute(args map[string]any, tc *agent.ToolContext) agent.ToolResult {
	config, _ := t.db.Config.All()
	env := buildEnvironment(args, config)

	cmdCtx, cancel := context.WithTimeout(tc.Ctx, 60*time.Second)
	defer cancel()

	var cmd *exec.Cmd
	switch t.implType {
	case "python":
		cmd = exec.CommandContext(cmdCtx, "python3", "-c", t.implementation)
	default:
		cmd = exec.CommandContext(cmdCtx, "bash", "-c", t.implementation)
	}

	cmd.Dir = tc.WorkDir
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	err := cmd.Run()
	if cmdCtx.Err() != nil && cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	output := out.String()

	if cmdCtx.Err() == context.DeadlineExceeded {
		return agent.ToolResult{Content: output + "\n[timed out]", IsError: true}
	}
	if err != nil {
		return agent.ToolResult{Content: output, IsError: true}
	}
	return agent.ToolResult{Content: output}
}
