package agent

// events_test.go — verifies that the v0.3.0 job-review event types
// (EventJobApprove, EventJobReject) compile, publish, and are received
// correctly by a subscriber. No LLM or DB required.

import (
	"testing"
)

// Test_Bus_JobApprovePublish verifies that EventJobApprove with a
// PayloadJobAction is published to and received by a subscriber.
func Test_Bus_JobApprovePublish(t *testing.T) {
	var b Bus
	ch, unsub := b.Subscribe()
	defer unsub()

	b.Publish(Event{
		Type:    EventJobApprove,
		Payload: PayloadJobAction{JobID: 42},
	})

	ev := <-ch
	if ev.Type != EventJobApprove {
		t.Fatalf("expected EventJobApprove, got %q", ev.Type)
	}
	p, ok := ev.Payload.(PayloadJobAction)
	if !ok {
		t.Fatalf("expected PayloadJobAction, got %T", ev.Payload)
	}
	if p.JobID != 42 {
		t.Errorf("expected JobID=42, got %d", p.JobID)
	}
}

// Test_Bus_JobRejectPublish verifies that EventJobReject with a
// PayloadJobAction is published to and received by a subscriber.
func Test_Bus_JobRejectPublish(t *testing.T) {
	var b Bus
	ch, unsub := b.Subscribe()
	defer unsub()

	b.Publish(Event{
		Type:    EventJobReject,
		Payload: PayloadJobAction{JobID: 99},
	})

	ev := <-ch
	if ev.Type != EventJobReject {
		t.Fatalf("expected EventJobReject, got %q", ev.Type)
	}
	p, ok := ev.Payload.(PayloadJobAction)
	if !ok {
		t.Fatalf("expected PayloadJobAction, got %T", ev.Payload)
	}
	if p.JobID != 99 {
		t.Errorf("expected JobID=99, got %d", p.JobID)
	}
}
