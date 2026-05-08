package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunLoop_RecoveryRetry verifies that when StreamOnce returns a transient
// 5xx error, the loop retries once with the same context and persists a
// recovery note when both attempts fail. With one failure followed by a
// success, the recovery note must NOT be persisted but the retry must occur.
func TestRunLoop_RecoveryRetry(t *testing.T) {
	t.Run("retry_succeeds", func(t *testing.T) {
		script := []scriptedResponse{
			{httpStatus: 503},
			{text: "recovered"},
		}
		cfg, state, rec, _ := newLoopHarness(t, script, harnessOpts{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := runLoop(ctx, cfg); err != nil {
			t.Fatalf("runLoop returned error: %v", err)
		}

		if got := state.CallCount(); got != 2 {
			t.Errorf("expected 2 LLM calls (initial+retry), got %d", got)
		}

		msgs := rec.Messages()
		// No recovery note expected on successful retry.
		for _, m := range msgs {
			if strings.Contains(m.Content, "[recovery note]") {
				t.Errorf("recovery note should NOT be persisted when retry succeeds; got %q", m.Content)
			}
		}
		// Final assistant text should be "recovered".
		var sawFinal bool
		for _, m := range msgs {
			if m.Role == "assistant" && m.Content == "recovered" {
				sawFinal = true
			}
		}
		if !sawFinal {
			t.Errorf("expected final assistant message %q in persist log; got %+v", "recovered", msgs)
		}
	})

	t.Run("both_fail_persists_note", func(t *testing.T) {
		script := []scriptedResponse{
			{httpStatus: 503},
			{httpStatus: 503},
		}
		cfg, state, rec, _ := newLoopHarness(t, script, harnessOpts{})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Both fail → loop returns the error.
		if err := runLoop(ctx, cfg); err == nil {
			t.Fatalf("runLoop expected error after two 5xx, got nil")
		}

		if got := state.CallCount(); got != 2 {
			t.Errorf("expected 2 LLM calls, got %d", got)
		}

		var sawNote bool
		for _, m := range rec.Messages() {
			if m.Role == "system" && strings.Contains(m.Content, "[recovery note]") {
				sawNote = true
			}
		}
		if !sawNote {
			t.Errorf("expected recovery note system message, got %+v", rec.Messages())
		}
	})
}

// TestRunLoop_MidToolCancellation verifies that cancelling the context
// during the LLM call causes runLoop to return cleanly (no error) and
// publish EventTurnEnd.
func TestRunLoop_MidToolCancellation(t *testing.T) {
	// Use a long server-side delay so cancellation lands while StreamOnce is
	// still waiting on the response body. The httptest handler honors the
	// request context, returning early when the client cancels.
	script := []scriptedResponse{
		{text: "partial answer", delayMs: 500},
	}
	cfg, _, rec, bus := newLoopHarness(t, script, harnessOpts{})

	ctx, cancel := context.WithCancel(context.Background())

	events := drainEvents(bus, func() {
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()
		// runLoop must not panic and should return nil on context cancel.
		if err := runLoop(ctx, cfg); err != nil {
			t.Errorf("runLoop returned error on cancel: %v", err)
		}
	})

	// After cancel, EventTurnEnd should be published.
	var sawTurnEnd bool
	for _, e := range events {
		if e.Type == EventTurnEnd {
			sawTurnEnd = true
		}
	}
	if !sawTurnEnd {
		t.Errorf("expected EventTurnEnd after cancellation; events=%v", eventTypes(events))
	}

	// If any assistant text was persisted, it must be tagged "(interrupted)".
	for _, m := range rec.Messages() {
		if m.Role == "assistant" && m.Content != "" {
			if !strings.Contains(m.Content, "(interrupted)") {
				t.Errorf("persisted assistant text after cancel should contain '(interrupted)' suffix; got %q", m.Content)
			}
		}
	}
}

func eventTypes(evs []Event) []EventType {
	out := make([]EventType, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}
