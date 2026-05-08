package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

type vscodeEvent struct {
	Type     string         `json:"type"`
	At       string         `json:"at"`
	Sequence int64          `json:"sequence"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type vscodeInput struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type vscodeWriter struct {
	mu  sync.Mutex
	seq int64
	w   io.Writer
}

func newVSCodeWriter(w io.Writer) *vscodeWriter {
	return &vscodeWriter{w: w}
}

func (w *vscodeWriter) Emit(eventType string, payload map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.seq++
	ev := vscodeEvent{
		Type:     eventType,
		At:       time.Now().Format(time.RFC3339Nano),
		Sequence: w.seq,
		Payload:  payload,
	}
	_ = json.NewEncoder(w.w).Encode(ev)
}

// VSCodeRenderer subscribes to the agent bus and emits line-delimited JSON.
// It is meant for editor integrations: no ANSI, no prompt marker, and all tool
// arguments/results are preserved in structured payloads.
func VSCodeRenderer(bus *agent.Bus, w io.Writer) (stop func()) {
	return vscodeRenderer(bus, newVSCodeWriter(w))
}

func vscodeRenderer(bus *agent.Bus, out *vscodeWriter) (stop func()) {
	ch, unsub := bus.Subscribe()
	go func() {
		for event := range ch {
			switch event.Type {
			case agent.EventAgentStart:
				out.Emit("agent_start", nil)

			case agent.EventTurnStart:
				out.Emit("turn_start", nil)

			case agent.EventTokens:
				p := event.Payload.(agent.PayloadTokens)
				out.Emit("tokens", map[string]any{"token": p.Token})

			case agent.EventThinking:
				p := event.Payload.(agent.PayloadThinking)
				out.Emit("thinking", map[string]any{"token": p.Token})

			case agent.EventToolStart:
				p := event.Payload.(agent.PayloadToolStart)
				out.Emit("tool_start", map[string]any{
					"name": p.Name,
					"args": p.Args,
				})

			case agent.EventToolUpdate:
				p := event.Payload.(agent.PayloadToolUpdate)
				out.Emit("tool_update", map[string]any{
					"name":   p.Name,
					"output": p.Output,
				})

			case agent.EventToolEnd:
				p := event.Payload.(agent.PayloadToolEnd)
				out.Emit("tool_end", map[string]any{
					"name":     p.Name,
					"result":   p.Result,
					"is_error": p.IsError,
				})

			case agent.EventTurnEnd:
				p, _ := event.Payload.(agent.PayloadTurnEnd)
				out.Emit("turn_end", map[string]any{"has_more": p.HasMore})

			case agent.EventAgentEnd:
				out.Emit("agent_end", nil)

			case agent.EventError:
				p := event.Payload.(agent.PayloadError)
				msg := ""
				if p.Err != nil {
					msg = p.Err.Error()
				}
				out.Emit("error", map[string]any{"message": msg})
			}
		}
	}()

	return unsub
}

// RunVSCode starts a stdin/stdout loop for editor integrations. It behaves like
// the line CLI from the user's point of view, but stdout is JSONL events only.
func RunVSCode(a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) error {
	out := newVSCodeWriter(os.Stdout)
	stop := vscodeRenderer(a.Bus(), out)
	defer stop()
	defer a.Close()

	out.Emit("ready", map[string]any{
		"session_id": session.ID,
		"role":       session.Role,
		"cwd":        session.CWD,
	})

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for scanner.Scan() {
		line := decodeVSCodeInput(strings.TrimSpace(scanner.Text()))
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "/") {
			if handleVSCodeCommand(line, a, database, session, out) {
				break
			}
			continue
		}

		if err := a.Prompt(context.Background(), line); err != nil {
			out.Emit("error", map[string]any{"message": err.Error()})
		}
	}

	return scanner.Err()
}

func decodeVSCodeInput(line string) string {
	if !strings.HasPrefix(line, "{") {
		return line
	}

	var input vscodeInput
	if err := json.Unmarshal([]byte(line), &input); err != nil {
		return line
	}
	if input.Type != "message" {
		return line
	}
	return strings.TrimSpace(input.Message)
}

func handleVSCodeCommand(line string, a *agent.Agent, database *sqliteopen.DB, session *sessions.Session, out *vscodeWriter) (exit bool) {
	parts := strings.Fields(line)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}

	switch cmd {
	case "/exit", "/quit", "/q":
		out.Emit("system", map[string]any{"text": "bye."})
		return true

	case "/init":
		text := buildInitPrompt(args, a, database, session)
		if err := a.Prompt(context.Background(), text); err != nil {
			out.Emit("error", map[string]any{"message": err.Error()})
		}
		return false

	case "/help":
		out.Emit("system", map[string]any{"text": helpText})

	case "/session":
		name := session.Name
		if name == "" {
			name = "(unnamed)"
		}
		out.Emit("system", map[string]any{
			"text": fmt.Sprintf("session %d: %s\nrole: %s\ncwd:  %s\n", session.ID, name, session.Role, session.CWD),
		})

	default:
		text, err := listCommandOutput(cmd, database, session)
		if err != nil {
			out.Emit("error", map[string]any{"message": err.Error()})
			return false
		}
		if text != "" {
			out.Emit("system", map[string]any{"text": text})
		} else {
			out.Emit("system", map[string]any{"text": fmt.Sprintf("unknown command: %s (try /help)\n", cmd)})
		}
	}

	out.Emit("command_end", nil)
	return false
}
