package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
)

// TestRunLoop_SteeringDrain verifies that steering messages queued mid-turn
// are persisted, appended to history, and reach the next LLM request body.
func TestRunLoop_SteeringDrain(t *testing.T) {
	script := []scriptedResponse{
		{text: "step 1"},
		{text: "step 2"},
	}

	steeringFires := 0
	cfg, state, rec, bus := newLoopHarness(t, script, harnessOpts{
		drainSteering: func() []llm.Message {
			steeringFires++
			if steeringFires == 1 {
				return []llm.Message{{Role: "user", Content: "steer me"}}
			}
			return nil
		},
	})

	events := drainEvents(bus, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runLoop(ctx, cfg); err != nil {
			t.Fatalf("runLoop: %v", err)
		}
	})

	if got := state.CallCount(); got != 2 {
		t.Fatalf("expected 2 LLM calls (one per outer turn), got %d", got)
	}

	// Steering message must have been persisted as a user message.
	var sawSteerPersist bool
	for _, m := range rec.Messages() {
		if m.Role == "user" && m.Content == "steer me" {
			sawSteerPersist = true
		}
	}
	if !sawSteerPersist {
		t.Errorf("expected steering message persisted; got %+v", rec.Messages())
	}

	// Inspect 2nd request body — it should carry the steering message.
	sent := state.Sent()
	if len(sent) < 2 {
		t.Fatalf("expected at least 2 sent bodies, got %d", len(sent))
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(sent[1], &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var sawSteerWire bool
	for _, m := range body.Messages {
		if m.Content == "steer me" {
			sawSteerWire = true
		}
	}
	if !sawSteerWire {
		t.Errorf("expected steering message in 2nd request wire; got %+v", body.Messages)
	}

	// We expect at least one EventTurnEnd with HasMore=true (mid-cycle) and
	// one with HasMore=false (final).
	var sawHasMore, sawFinal bool
	for _, e := range events {
		if e.Type == EventTurnEnd {
			if p, ok := e.Payload.(PayloadTurnEnd); ok {
				if p.HasMore {
					sawHasMore = true
				} else {
					sawFinal = true
				}
			}
		}
	}
	if !sawHasMore {
		t.Errorf("expected at least one EventTurnEnd{HasMore:true} after steering drain")
	}
	if !sawFinal {
		t.Errorf("expected final EventTurnEnd{HasMore:false}")
	}
}

// TestRunLoop_FollowUpDrain verifies that follow-up messages forced via
// drainFollowUp are persisted and reach the next LLM request body, mirroring
// the steering branch but on the follow-up path.
//
// drainFollowUp messages are persisted with their literal Role. Because the
// system prompt is at position 0 of every send, a non-position-0 system
// message is demoted to user with "[harness] " prefix during serialization.
// We use Role="user" to keep the assertion clean.
func TestRunLoop_FollowUpDrain(t *testing.T) {
	script := []scriptedResponse{
		{text: "step 1"},
		{text: "step 2"},
	}

	followFires := 0
	cfg, state, rec, _ := newLoopHarness(t, script, harnessOpts{
		drainFollowUp: func() []llm.Message {
			followFires++
			if followFires == 1 {
				return []llm.Message{{Role: "user", Content: "follow-up nudge"}}
			}
			return nil
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runLoop(ctx, cfg); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	if got := state.CallCount(); got != 2 {
		t.Fatalf("expected 2 LLM calls, got %d", got)
	}

	var sawPersist bool
	for _, m := range rec.Messages() {
		if m.Role == "user" && strings.Contains(m.Content, "follow-up nudge") {
			sawPersist = true
		}
	}
	if !sawPersist {
		t.Errorf("expected follow-up message persisted; got %+v", rec.Messages())
	}

	sent := state.Sent()
	if len(sent) < 2 {
		t.Fatalf("expected at least 2 sent bodies, got %d", len(sent))
	}
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(sent[1], &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var sawWire bool
	for _, m := range body.Messages {
		if strings.Contains(m.Content, "follow-up nudge") {
			sawWire = true
		}
	}
	if !sawWire {
		t.Errorf("expected follow-up message in 2nd request wire; got %+v", body.Messages)
	}
}
