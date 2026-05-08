package server

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sessions"
)

// fakeAgent implements Prompter for tests. Prompt() publishes EventTokens
// for each item in tokens, then EventAgentEnd, simulating a real agent turn.
type fakeAgent struct {
	mu       sync.Mutex
	bus      agent.Bus
	response string
	tokens   []string
	delay    time.Duration // optional artificial delay before completing
	session  *sessions.Session
}

func newFakeAgent(response string, tokens []string) *fakeAgent {
	return &fakeAgent{
		response: response,
		tokens:   tokens,
		session:  &sessions.Session{ID: 1},
	}
}

func (f *fakeAgent) Prompt(ctx context.Context, text string) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
		}
	}
	for _, tok := range f.tokens {
		f.bus.Publish(agent.Event{Type: agent.EventTokens, Payload: agent.PayloadTokens{Token: tok}})
	}
	f.bus.Publish(agent.Event{Type: agent.EventAgentEnd})
	return nil
}

func (f *fakeAgent) PromptWithOpts(ctx context.Context, text string, opts agent.PromptOpts) error {
	return f.Prompt(ctx, text)
}

func (f *fakeAgent) Bus() *agent.Bus            { return &f.bus }
func (f *fakeAgent) LastAssistantText() string  { return f.response }
func (f *fakeAgent) Model() string              { return "fake-model" }
func (f *fakeAgent) Session() *sessions.Session { return f.session }
func (f *fakeAgent) IsStreaming() bool          { return false }

func newTestBridge(t *testing.T, fa *fakeAgent) *SessionBridge {
	t.Helper()
	b := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.Start(ctx)
	return b
}

// TestBridge_Send_ReturnsResponse verifies a basic non-streaming send.
func TestBridge_Send_ReturnsResponse(t *testing.T) {
	fa := newFakeAgent("hello", []string{"hel", "lo"})
	b := newTestBridge(t, fa)

	resp, _, err := b.Send(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if resp != "hello" {
		t.Errorf("expected %q, got %q", "hello", resp)
	}
}

// TestBridge_Send_Serializes verifies that two concurrent calls do not race.
// The race detector will catch any unsynchronized access if present.
func TestBridge_Send_Serializes(t *testing.T) {
	// Use a delay to make the overlap window wide enough for the race detector.
	fa := newFakeAgent("done", nil)
	fa.delay = 20 * time.Millisecond
	b := newTestBridge(t, fa)

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, _, err := b.Send(context.Background(), "ping")
			if err != nil {
				t.Errorf("Send: %v", err)
			}
			if resp != "done" {
				t.Errorf("expected %q, got %q", "done", resp)
			}
		}()
	}
	wg.Wait()
}

// TestBridge_SendStream_ForwardsTokens verifies that SSE tokens arrive in order.
func TestBridge_SendStream_ForwardsTokens(t *testing.T) {
	wantTokens := []string{"tok1", "tok2", "tok3"}
	fa := newFakeAgent("tok1tok2tok3", wantTokens)
	b := newTestBridge(t, fa)

	tokensCh := make(chan string, 16)
	resp, _, err := b.SendStream(context.Background(), "stream me", tokensCh)
	if err != nil {
		t.Fatalf("SendStream: %v", err)
	}
	if resp != "tok1tok2tok3" {
		t.Errorf("response mismatch: got %q", resp)
	}

	var got []string
	for tok := range tokensCh {
		got = append(got, tok)
	}
	if len(got) != len(wantTokens) {
		t.Fatalf("expected %d tokens, got %d: %v", len(wantTokens), len(got), got)
	}
	for i, tok := range wantTokens {
		if got[i] != tok {
			t.Errorf("token[%d]: want %q, got %q", i, tok, got[i])
		}
	}
}

// TestBridge_CtxCancellation_ReturnsPromptly verifies that cancelling the
// caller context causes Send to return (even if the agent is still running).
func TestBridge_CtxCancellation_ReturnsPromptly(t *testing.T) {
	fa := newFakeAgent("delayed", nil)
	fa.delay = 2 * time.Second // much longer than the test timeout
	b := newTestBridge(t, fa)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := b.Send(ctx, "take your time")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Send took too long after cancel: %v", elapsed)
	}
}

// TestBridge_Stop_ExitsCleanly verifies that Stop() does not block or panic.
func TestBridge_Stop_ExitsCleanly(t *testing.T) {
	fa := newFakeAgent("ok", nil)
	b := NewBridge(fa)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b.Start(ctx)

	done := make(chan struct{})
	go func() {
		b.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within timeout")
	}
}
