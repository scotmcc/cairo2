package main

import (
	"sync"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// App is the dependency container for an interactive cairo run. It is built
// inline by main() following cairo's linear init sequence; Phase 1.6+ may
// introduce a newApp(ctx, opts) factory.
type App struct {
	DB          *sqliteopen.DB
	LLM         *llm.Client
	OllamaURL   string
	EmbedModel  string
	Model       string
	Session     *sessions.Session
	Agent       *agent.Agent
	Choices     chan tools.ChoiceRequest // nil except in TUI mode
	RegistryURL string
	RegistryWG  *sync.WaitGroup
}
