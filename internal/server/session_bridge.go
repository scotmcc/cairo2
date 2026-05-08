package server

import (
	"context"
	"sync"

	"github.com/scotmcc/cairo2/internal/agent"
)

// bridgeReq is a single request queued to the bridge worker.
type bridgeReq struct {
	ctx           context.Context
	text          string
	tokens        chan<- string // nil for non-streaming
	replyCh       chan bridgeResp
	forceConsider bool
	triggerSource string
}

// bridgeResp is the result returned by the bridge worker for each request.
type bridgeResp struct {
	response string
	turnID   int64
	err      error
}

// SessionBridge serializes external HTTP requests into the agent loop.
// The agent's Prompt() is not safe for concurrent callers, so the bridge
// queues requests and processes them one at a time via a single goroutine.
type SessionBridge struct {
	agent  Prompter
	queue  chan *bridgeReq
	worker sync.Once
	done   chan struct{}
}

// NewBridge creates a bridge for the given Prompter. Call Start() before
// sending any requests.
func NewBridge(a Prompter) *SessionBridge {
	return &SessionBridge{
		agent: a,
		queue: make(chan *bridgeReq, BridgeQueueDepth),
		done:  make(chan struct{}),
	}
}

// Start launches the single worker goroutine. It exits when the bridge's
// done channel is closed (via Stop). Start is idempotent — only the first
// call creates the goroutine.
func (b *SessionBridge) Start(ctx context.Context) {
	b.worker.Do(func() {
		go b.run(ctx)
	})
}

// Stop closes the bridge. No further requests will be processed.
// In-flight requests drain to completion before the worker exits.
func (b *SessionBridge) Stop() {
	close(b.done)
}

// Send submits a message and blocks until the agent has finished its turn.
// Returns the agent's response text and the session's current turn ID.
// Cancelling ctx causes Send to return early; the worker continues draining
// the agent turn to keep agent state consistent.
func (b *SessionBridge) Send(ctx context.Context, text string) (string, int64, error) {
	return b.send(ctx, text, nil, false, "")
}

// SendWithOpts is like Send but lets the caller force the consider step and
// tag activation rows with a trigger source ("api", "tool", etc.).
func (b *SessionBridge) SendWithOpts(ctx context.Context, text string, forceConsider bool, triggerSource string) (string, int64, error) {
	return b.send(ctx, text, nil, forceConsider, triggerSource)
}

// SendStream submits a message and streams response tokens to the tokens
// channel as they arrive. The caller must not close tokens; the worker closes
// it when the turn ends. Cancelling ctx causes early return; the worker keeps
// draining until EventAgentEnd to preserve agent state consistency.
func (b *SessionBridge) SendStream(ctx context.Context, text string, tokens chan<- string) (string, int64, error) {
	return b.send(ctx, text, tokens, false, "")
}

// SendStreamWithOpts is like SendStream but lets the caller force the consider
// step and tag activation rows with a trigger source.
func (b *SessionBridge) SendStreamWithOpts(ctx context.Context, text string, tokens chan<- string, forceConsider bool, triggerSource string) (string, int64, error) {
	return b.send(ctx, text, tokens, forceConsider, triggerSource)
}

func (b *SessionBridge) send(ctx context.Context, text string, tokens chan<- string, forceConsider bool, triggerSource string) (string, int64, error) {
	req := &bridgeReq{
		ctx:           ctx,
		text:          text,
		tokens:        tokens,
		replyCh:       make(chan bridgeResp, 1),
		forceConsider: forceConsider,
		triggerSource: triggerSource,
	}

	select {
	case b.queue <- req:
	case <-ctx.Done():
		return "", 0, ctx.Err()
	case <-b.done:
		return "", 0, context.Canceled
	}

	select {
	case resp := <-req.replyCh:
		return resp.response, resp.turnID, resp.err
	case <-ctx.Done():
		// Return early to the caller. The worker goroutine will keep draining
		// until EventAgentEnd so the agent is left in a clean idle state.
		return "", 0, ctx.Err()
	}
}

// run is the single worker goroutine. It processes one request at a time.
func (b *SessionBridge) run(ctx context.Context) {
	for {
		select {
		case req := <-b.queue:
			b.process(req)
		case <-b.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// process handles a single bridgeReq: subscribes to the bus, fires Prompt in
// a goroutine, collects events, and sends the reply.
func (b *SessionBridge) process(req *bridgeReq) {
	ch, unsub := b.agent.Bus().Subscribe()

	// Fire Prompt in its own goroutine. It blocks until the turn completes.
	go func() {
		_ = b.agent.PromptWithOpts(req.ctx, req.text, agent.PromptOpts{
			ForceConsider: req.forceConsider,
			TriggerSource: req.triggerSource,
		})
	}()

	var turnID int64
	for ev := range ch {
		switch ev.Type {
		case agent.EventTokens:
			if req.tokens != nil {
				p := ev.Payload.(agent.PayloadTokens)
				// Non-blocking send: if the tokens channel buffer is full, drop
				// the token rather than stalling the event loop. The full response
				// is still available via LastAssistantText() on turn end.
				select {
				case req.tokens <- p.Token:
				default:
				}
			}
		case agent.EventAgentEnd:
			// Turn is complete. Capture response and exit the event loop.
			response := b.agent.LastAssistantText()
			if b.agent.Session() != nil {
				turnID = b.agent.Session().ID
			}
			unsub()
			if req.tokens != nil {
				close(req.tokens)
			}
			resp := bridgeResp{response: response, turnID: turnID}
			select {
			case req.replyCh <- resp:
			default:
			}
			return
		}
	}
}
