package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/store/config"
)

// TestRunLoop_SynthesisNudge verifies that after `nudgeEvery` tool calls,
// a synthesis nudge system message is appended to sendMsgs before the next
// LLM call. We lower the threshold to 2 via db config, dispatch 2 tool calls
// in the first inner iteration, then a final-text response. The third
// request body sent to the server should contain the nudge text.
func TestRunLoop_SynthesisNudge(t *testing.T) {
	ft := &fakeTool{}
	script := []scriptedResponse{
		// Call 1: two tool calls → toolsThisRun goes from 0 → 2 → triggers nudge for next call.
		{toolCalls: []toolCallSpec{
			{id: "t1", name: "fake_tool", args: "{}"},
			{id: "t2", name: "fake_tool", args: "{}"},
		}},
		// Call 2: final text — sendMsgs for THIS request must include the nudge.
		{text: "done"},
	}
	cfg, state, _, _ := newLoopHarness(t, script, harnessOpts{
		tools: []Tool{ft},
	})

	if err := cfg.db.Config.Set(config.KeySynthesisNudge, "2"); err != nil {
		t.Fatalf("set nudge threshold: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runLoop(ctx, cfg); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	if got := state.CallCount(); got != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", got)
	}

	sent := state.Sent()
	if len(sent) < 2 {
		t.Fatalf("expected at least 2 captured request bodies, got %d", len(sent))
	}

	// Inspect the 2nd request body — its messages array should contain
	// a system message with the synthesis nudge text.
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(sent[1], &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}

	var sawNudge bool
	for _, m := range body.Messages {
		// The system message gets demoted to "user" with "[harness] " prefix
		// by serializeMessages because it's not at position 0. The literal
		// "[system note]" plus "tool calls in this turn" is what we look for.
		if strings.Contains(m.Content, "[system note]") &&
			strings.Contains(m.Content, "tool calls in this turn") {
			sawNudge = true
		}
	}
	if !sawNudge {
		t.Errorf("expected synthesis nudge in 2nd request messages; got: %+v", body.Messages)
	}
}
