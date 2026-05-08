package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
)

type bashTool struct{}

func Bash() agent.Tool { return bashTool{} }

func (bashTool) Name() string { return "bash" }
func (bashTool) Description() string {
	return "Run a shell command. Returns combined stdout and stderr. " +
		"The subprocess working directory is always set to the session CWD (unsafe_mode=false). " +
		"Relative paths resolve from there."
}
func (bashTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": prop("string", "Shell command to execute"),
			"timeout": prop("integer", "Timeout in seconds (default 30, max 120)"),
		},
		"required": []string{"command"},
	}
}

func (bashTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
	if r, refused := checkDiscipline(ctx, "bash", "", 3); refused {
		return r
	}
	command := strArg(args, "command")
	if command == "" {
		return agent.ToolResult{Content: "error: command is required", IsError: true}
	}

	timeoutSec := intArg(args, "timeout", 30)
	if timeoutSec > 120 {
		timeoutSec = 120
	}
	timeout := time.Duration(timeoutSec) * time.Second

	cmdCtx, cancel := context.WithTimeout(ctx.Ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", command)
	cmd.Dir = ctx.WorkDir
	configureProcAttr(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: stdout pipe: %v", err), IsError: true}
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: stderr pipe: %v", err), IsError: true}
	}

	if err := cmd.Start(); err != nil {
		return agent.ToolResult{Content: fmt.Sprintf("error: start: %v", err), IsError: true}
	}

	var (
		mu  sync.Mutex
		buf strings.Builder
		wg  sync.WaitGroup
	)

	readLines := func(r io.Reader, prefix string) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			buf.WriteString(line)
			buf.WriteByte('\n')
			mu.Unlock()
			if ctx.Bus != nil {
				ctx.Bus.Publish(agent.Event{
					Type:    agent.EventToolUpdate,
					Payload: agent.PayloadToolUpdate{Name: "bash", Output: prefix + line},
				})
			}
		}
	}

	wg.Add(2)
	go readLines(stdoutPipe, "[stdout] ")
	go readLines(stderrPipe, "[stderr] ")

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-cmdCtx.Done():
		killProcessGroup(cmdCtx, cmd)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}

	waitErr := cmd.Wait()
	killProcessGroup(cmdCtx, cmd)

	output := buf.String()

	if cmdCtx.Err() != nil {
		msg := fmt.Sprintf("\n[timed out after %s]", timeout)
		if ctx.Ctx.Err() != nil {
			msg = "\n[cancelled]"
		}
		return agent.ToolResult{Content: output + msg, IsError: true}
	}
	if waitErr != nil {
		exitCode := -1
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		return agent.ToolResult{
			Content: fmt.Sprintf("[exit %d]\n%s", exitCode, output),
			IsError: true,
		}
	}

	return agent.ToolResult{Content: captureAndTruncate(output)}
}

// configureProcAttr puts bash and all its children in a new process group so
// the whole tree can be killed on timeout (catches grandchildren like git
// credential helpers).
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGKILL to the entire process group when the context
// has expired, cleaning up any grandchildren.
func killProcessGroup(ctx context.Context, cmd *exec.Cmd) {
	if ctx.Err() != nil && cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// captureAndTruncate caps output at 65536 bytes and appends a truncation note
// when the original was larger.
func captureAndTruncate(output string) string {
	const maxBytes = 65536
	if len(output) <= maxBytes {
		return output
	}
	originalSize := len(output)
	return string([]byte(output)[:maxBytes]) +
		fmt.Sprintf("\n[truncated: original size was %d bytes]", originalSize)
}
