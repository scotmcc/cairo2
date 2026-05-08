package agent

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/scotmcc/cairo2/internal/agent/consider"
)

// EventType identifies what happened in the agent loop.
type EventType string

const (
	EventAgentStart EventType = "agent_start"
	EventTurnStart  EventType = "turn_start"
	EventTokens     EventType = "tokens"   // streaming content tokens
	EventThinking   EventType = "thinking" // streaming thinking tokens
	EventToolStart  EventType = "tool_start"
	EventToolUpdate EventType = "tool_update"
	EventToolEnd    EventType = "tool_end"
	EventTurnEnd    EventType = "turn_end"
	EventAgentEnd   EventType = "agent_end"
	EventError      EventType = "error"

	// v0.3.0 job-review events — emitted by the TUI diff panel when the user
	// presses 'a' (approve) or 'r' (reject) on a selected job. Published to
	// the agent bus so any subscriber can observe the decision.
	EventJobApprove EventType = "job_approve"
	EventJobReject  EventType = "job_reject"

	// Consider (inner-dialogue) events — emitted by consider.Run via the
	// considerBusAdapter so the TUI can surface per-aspect progress and
	// confirm injection without breaking the consider package's
	// import-independence from internal/agent.
	EventConsiderAspectStart EventType = "consider_aspect_start"
	EventConsiderAspectEnd   EventType = "consider_aspect_end"
	EventConsiderInjected    EventType = "consider_injected"

	// EventStallDetected fires when a turn ends with forward-looking text
	// ("now let me try…", "I'll next…") but zero tool calls — the model
	// indicated intent but then stopped. The TUI surfaces a banner so the
	// user knows to type "continue".
	EventStallDetected EventType = "stall_detected"

	// Per-step heartbeat events — published at the boundary of each coarse
	// execution phase so the TUI can show "⟳ consider · 1.2s" in the status bar.
	// Steps: "consider", "llm", "tool", "persist". EventStepStart fires when a
	// step begins; EventStepEnd fires when it completes (or errors out).
	EventStepStart EventType = "step_start"
	EventStepEnd   EventType = "step_end"
)

// Event carries a typed payload from the agent loop to subscribers.
type Event struct {
	Type    EventType
	Payload any
}

// Payloads — subscribers type-assert Payload based on Type.

type PayloadTokens struct{ Token string }
type PayloadThinking struct{ Token string }
type PayloadToolStart struct {
	Name string
	Args map[string]any
	PID  int // subprocess PID (non-zero for bash; zero for all other tools)
}
type PayloadToolUpdate struct {
	Name   string
	Output string
}
type PayloadToolEnd struct {
	Name    string
	Result  string
	IsError bool
}
type PayloadError struct{ Err error }
type PayloadTurnEnd struct{ HasMore bool } // HasMore = model wants another turn

// PayloadJobAction carries the job ID for EventJobApprove and EventJobReject.
type PayloadJobAction struct{ JobID int64 }

// Consider event payloads.
type PayloadConsiderAspectStart struct{ Name string }
type PayloadConsiderAspectEnd struct {
	Name    string
	Output  string
	IsError bool
}
type PayloadConsiderInjected struct {
	AspectCount int
	Summary     string
	ElapsedMs   int64
}

// PayloadStepStart is published when a coarse execution step begins.
// Step is one of: "consider", "llm", "tool", "persist".
// Detail carries extra context (tool name for "tool", otherwise "").
type PayloadStepStart struct {
	Step      string
	Detail    string
	StartedAt time.Time
}

// PayloadStepEnd is published when a coarse execution step completes.
type PayloadStepEnd struct {
	Step     string
	Detail   string
	Duration time.Duration
}

// Bus is a fan-out event publisher. Subscribers receive all events on a channel.
// Safe for concurrent use. The agent loop calls Publish; UI layers subscribe.
type Bus struct {
	mu        sync.RWMutex
	subs      []chan Event
	dropCount atomic.Int64
}

// DropCount returns the total number of events dropped across all subscribers
// since the Bus was created. Monotonically increasing.
func (b *Bus) DropCount() int64 {
	return b.dropCount.Load()
}

// Subscribe returns a receive-only channel and an unsubscribe function.
// The channel is buffered (512) so a slow subscriber doesn't stall the agent.
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 512)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subs {
			if s == ch {
				b.subs = append(b.subs[:i], b.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, unsub
}

// Publish sends an event to all subscribers. If a subscriber's channel buffer
// is full, the event is logged and dropped for that subscriber. Subscribers
// should process events promptly to avoid loss.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			b.dropCount.Add(1)
			log.Printf("event bus: dropped event type=%q total_drops=%d", e.Type, b.dropCount.Load())
		}
	}
}

// considerBusAdapter wraps a *Bus to satisfy consider.EventPublisher,
// translating consider's payload types into the matching agent payloads.
// This is the seam that lets the consider package publish progress events
// without importing internal/agent.
type considerBusAdapter struct{ bus *Bus }

func (a *considerBusAdapter) Publish(e consider.PublishEvent) {
	if a == nil || a.bus == nil {
		return
	}
	switch p := e.Payload.(type) {
	case consider.AspectStartPayload:
		a.bus.Publish(Event{Type: EventConsiderAspectStart, Payload: PayloadConsiderAspectStart(p)})
	case consider.AspectEndPayload:
		a.bus.Publish(Event{Type: EventConsiderAspectEnd, Payload: PayloadConsiderAspectEnd(p)})
	case consider.InjectedPayload:
		a.bus.Publish(Event{Type: EventConsiderInjected, Payload: PayloadConsiderInjected(p)})
	}
}
