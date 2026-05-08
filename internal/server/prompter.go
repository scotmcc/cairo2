package server

import (
	"context"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sessions"
)

// Prompter is the narrow interface the server needs from an agent.
// *agent.Agent satisfies this interface. Tests use a fakeAgent.
type Prompter interface {
	Prompt(ctx context.Context, text string) error
	PromptWithOpts(ctx context.Context, text string, opts agent.PromptOpts) error
	Bus() *agent.Bus
	LastAssistantText() string
	Model() string
	Session() *sessions.Session
	IsStreaming() bool
}
