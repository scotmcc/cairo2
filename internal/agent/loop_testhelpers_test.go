package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
)

// scriptedResponse describes one scripted /v1/chat/completions reply.
// httpStatus != 0 returns that status with an OpenAI-format error body
// so isLLMServerError classifies it. delayMs sleeps before responding;
// the wait honors the request context (used by the cancellation test).
type scriptedResponse struct {
	text       string
	toolCalls  []toolCallSpec
	httpStatus int
	delayMs    int
}

type toolCallSpec struct {
	id   string
	name string
	args string
}

type scriptedLLM struct {
	mu     sync.Mutex
	script []scriptedResponse
	calls  int
	sent   [][]byte
}

func (s *scriptedLLM) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *scriptedLLM) Sent() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.sent))
	for i, b := range s.sent {
		c := make([]byte, len(b))
		copy(c, b)
		out[i] = c
	}
	return out
}

// newScriptedLLM spins up an httptest.Server speaking SSE on
// /v1/chat/completions and returns a real *llm.Client wired to it.
// The script is consumed in order; once exhausted the server returns
// an error, which surfaces as a non-server (HTTP 500 with non-matching
// body) failure for the test to notice.
func newScriptedLLM(t *testing.T, script []scriptedResponse) (*llm.Client, *scriptedLLM) {
	t.Helper()
	state := &scriptedLLM{script: script}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(bodyBytes)
		}

		state.mu.Lock()
		if state.calls >= len(state.script) {
			state.mu.Unlock()
			http.Error(w, `{"error":{"message":"script exhausted","type":"test_error"}}`, http.StatusInternalServerError)
			return
		}
		idx := state.calls
		resp := state.script[idx]
		state.calls++
		state.sent = append(state.sent, bodyBytes)
		state.mu.Unlock()

		if resp.delayMs > 0 {
			select {
			case <-time.After(time.Duration(resp.delayMs) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}

		if resp.httpStatus != 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.httpStatus)
			fmt.Fprintf(w, `{"error":{"message":"service unavailable","type":"server_error","code":"unavailable"}}`)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		// Build an SSE delta with content + tool_calls.
		type sseDeltaCallEnc struct {
			Index    int    `json:"index"`
			ID       string `json:"id,omitempty"`
			Type     string `json:"type"`
			Function struct {
				Name      string `json:"name,omitempty"`
				Arguments string `json:"arguments,omitempty"`
			} `json:"function"`
		}
		type sseDeltaEnc struct {
			Content   string            `json:"content,omitempty"`
			ToolCalls []sseDeltaCallEnc `json:"tool_calls,omitempty"`
		}
		type sseChoiceEnc struct {
			Delta        sseDeltaEnc `json:"delta"`
			FinishReason *string     `json:"finish_reason,omitempty"`
		}
		type sseChunkEnc struct {
			Choices []sseChoiceEnc `json:"choices"`
		}

		delta := sseDeltaEnc{Content: resp.text}
		for i, tc := range resp.toolCalls {
			var enc sseDeltaCallEnc
			enc.Index = i
			enc.ID = tc.id
			enc.Type = "function"
			enc.Function.Name = tc.name
			args := tc.args
			if args == "" {
				args = "{}"
			}
			enc.Function.Arguments = args
			delta.ToolCalls = append(delta.ToolCalls, enc)
		}
		chunk := sseChunkEnc{Choices: []sseChoiceEnc{{Delta: delta}}}
		buf, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", string(buf))
		if flusher != nil {
			flusher.Flush()
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	return llm.New(srv.URL, ""), state
}

// persistCall records one cfg.persist invocation.
type persistCall struct {
	Role          string
	Content       string
	ToolCallsJSON string
	ToolName      string
	ToolID        string
}

// persistToolCall records one cfg.persistTool invocation.
type persistToolCall struct {
	Content   string
	ToolName  string
	ToolID    string
	Status    string
	LatencyMs int64
}

type persistRecorder struct {
	mu       sync.Mutex
	messages []persistCall
	tools    []persistToolCall
}

func (r *persistRecorder) recordMsg(role, content, toolCallsJSON, toolName, toolID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, persistCall{role, content, toolCallsJSON, toolName, toolID})
}

func (r *persistRecorder) recordTool(content, toolName, toolID, status string, latencyMs int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, persistToolCall{content, toolName, toolID, status, latencyMs})
}

func (r *persistRecorder) Messages() []persistCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]persistCall, len(r.messages))
	copy(out, r.messages)
	return out
}

func (r *persistRecorder) Tools() []persistToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]persistToolCall, len(r.tools))
	copy(out, r.tools)
	return out
}

// fakeTool is a minimum-surface Tool used so toolCalls dispatched by the
// loop have a target. Execute returns "ok" and bumps an atomic counter.
// No process is spawned; no filesystem touched.
type fakeTool struct {
	calls atomic.Int64
}

func (f *fakeTool) Name() string        { return "fake_tool" }
func (f *fakeTool) Description() string { return "test fake tool" }
func (f *fakeTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (f *fakeTool) Execute(args map[string]any, ctx *ToolContext) ToolResult {
	f.calls.Add(1)
	return ToolResult{Content: "ok"}
}

// harnessOpts carries optional overrides for newLoopHarness.
type harnessOpts struct {
	tools         []Tool
	maxTurns      int
	history       []llm.Message
	drainSteering func() []llm.Message
	drainFollowUp func() []llm.Message
}

// newLoopHarness wires together a stub LLM, a real test DB, a fresh Bus,
// and a persistRecorder, returning a fully populated loopConfig ready to
// pass to runLoop. Defaults are minimal and side-effect-free; opts override.
func newLoopHarness(t *testing.T, script []scriptedResponse, opts harnessOpts) (loopConfig, *scriptedLLM, *persistRecorder, *Bus) {
	t.Helper()
	client, state := newScriptedLLM(t, script)
	d := openTestDB(t)
	rec := &persistRecorder{}
	bus := &Bus{}

	cfg := loopConfig{
		model:       "test-model",
		history:     opts.history,
		tools:       opts.tools,
		llm:         client,
		bus:         bus,
		db:          d,
		session:     nil,
		registry:    nil,
		persist:     rec.recordMsg,
		persistTool: rec.recordTool,
		workDir:     t.TempDir(),
		buildPrompt: func() (llm.Message, error) {
			return llm.Message{Role: "system", Content: "test prompt"}, nil
		},
		drainSteering:  opts.drainSteering,
		drainFollowUp:  opts.drainFollowUp,
		maxTurns:       opts.maxTurns,
		background:     false,
		disciplineMode: DisciplineFull,
	}
	if cfg.drainSteering == nil {
		cfg.drainSteering = func() []llm.Message { return nil }
	}
	if cfg.drainFollowUp == nil {
		cfg.drainFollowUp = func() []llm.Message { return nil }
	}
	return cfg, state, rec, bus
}

// drainEvents subscribes to bus and returns events buffered during fn.
// Bounds wait time so a stuck loop doesn't hang the test.
func drainEvents(bus *Bus, fn func()) []Event {
	ch, unsub := bus.Subscribe()
	defer unsub()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-done:
			// drain anything still buffered
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						return out
					}
					out = append(out, ev)
				default:
					return out
				}
			}
		case <-time.After(10 * time.Second):
			return out
		}
	}
}
