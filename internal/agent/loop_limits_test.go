package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
)

// TestRunLoop_MaxTurnsGuard verifies that when steering keeps forcing more
// outer-loop iterations, the max-turns guard kicks in at the start of the
// (maxTurns+1)th turn, persists a "max turns" message, and returns cleanly.
//
// The guard fires at the start of an outer iteration when turns >= maxTurns.
// We drive the outer loop forward using drainSteering (which forces another
// outer iteration). Each turn yields a final text response (no inner-loop
// looping required). With maxTurns=2 we expect 2 actual turns + the guard
// firing on the third iteration before the LLM is called.
func TestRunLoop_MaxTurnsGuard(t *testing.T) {
	script := []scriptedResponse{
		{text: "step 1"},
		{text: "step 2"},
		{text: "step 3"}, // should never be consumed if guard works
	}

	steeringCount := 0
	cfg, state, rec, _ := newLoopHarness(t, script, harnessOpts{
		maxTurns: 2,
		drainSteering: func() []llm.Message {
			// Always queue a user steering message so the outer loop keeps going.
			steeringCount++
			return []llm.Message{{Role: "user", Content: "keep going"}}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := runLoop(ctx, cfg); err != nil {
		t.Fatalf("runLoop returned error: %v", err)
	}

	// Exactly two LLM calls should have happened (turn 1, turn 2). On the
	// third outer iteration the guard fires before StreamOnce is invoked.
	if got := state.CallCount(); got != 2 {
		t.Errorf("expected exactly 2 LLM calls (maxTurns=2), got %d", got)
	}

	var sawGuard bool
	for _, m := range rec.Messages() {
		if m.Role == "assistant" && strings.Contains(m.Content, "max turns") {
			sawGuard = true
		}
	}
	if !sawGuard {
		t.Errorf("expected 'max turns' assistant guard message in persist log; got %+v", rec.Messages())
	}
}

// TestRunLoop_StallDetection covers the EventStallDetected branch.
//
//   - "fires": forward-looking text + zero tool calls → event fires.
//   - "no_text_no_fire": empty finalText → event must NOT fire.
//   - "with_tools_no_fire": forward-looking text but tool calls present this
//     turn → event must NOT fire.
func TestRunLoop_StallDetection(t *testing.T) {
	t.Run("fires", func(t *testing.T) {
		script := []scriptedResponse{
			{text: "Now let me try a different approach"},
		}
		cfg, _, _, bus := newLoopHarness(t, script, harnessOpts{})

		events := drainEvents(bus, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := runLoop(ctx, cfg); err != nil {
				t.Fatalf("runLoop: %v", err)
			}
		})

		var n int
		for _, e := range events {
			if e.Type == EventStallDetected {
				n++
			}
		}
		if n != 1 {
			t.Errorf("expected exactly 1 EventStallDetected, got %d", n)
		}
	})

	t.Run("no_text_no_fire", func(t *testing.T) {
		script := []scriptedResponse{
			{text: ""},
		}
		cfg, _, _, bus := newLoopHarness(t, script, harnessOpts{})

		events := drainEvents(bus, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := runLoop(ctx, cfg); err != nil {
				t.Fatalf("runLoop: %v", err)
			}
		})

		for _, e := range events {
			if e.Type == EventStallDetected {
				t.Errorf("EventStallDetected should NOT fire on empty text")
			}
		}
	})

	t.Run("with_tools_no_fire", func(t *testing.T) {
		ft := &fakeTool{}
		script := []scriptedResponse{
			{toolCalls: []toolCallSpec{{id: "t1", name: "fake_tool", args: "{}"}}},
			{text: "Now let me try a different approach"},
		}
		cfg, _, _, bus := newLoopHarness(t, script, harnessOpts{
			tools: []Tool{ft},
		})

		events := drainEvents(bus, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := runLoop(ctx, cfg); err != nil {
				t.Fatalf("runLoop: %v", err)
			}
		})

		// toolsThisTurn > 0 in the only outer iteration → no stall event.
		for _, e := range events {
			if e.Type == EventStallDetected {
				t.Errorf("EventStallDetected should NOT fire when tool calls happened this turn")
			}
		}
	})
}
