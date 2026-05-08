package commands

// commands.go — shared built-in command definitions.
//
// Only commands whose handler is genuinely portable between TUI and CLI live
// here. Commands that operate on TUI-only state (transcript, panels, quit) or
// that need a direct os.Exit stay in their respective frontend packages.
//
// Current shared commands:
//   - /init  — builds a prompt and submits it to the agent via env.Submit.
//              Each frontend passes its own DB-aware prompt-builder so the
//              shared handler stays free of package-level dependencies.

// NewInitCommand returns a shared /init Command bound to the given
// prompt-builder function. The builder receives the args string (e.g.
// "codebase") and returns the full prompt text to send to the agent.
// Decoupling prompt construction from the handler keeps this package free
// of db / agent imports.
//
// onComplete, if non-nil, is called after Submit returns. The CLI uses this
// to set init_complete=true deterministically (its Submit is synchronous);
// the TUI passes nil and uses its initPending / handleTurnComplete path instead.
func NewInitCommand(buildPrompt func(args string) string, onComplete func()) *Command {
	return &Command{
		Name:        "init",
		Description: "Start the guided setup. The AI introduces itself and learns the project.",
		Handler: func(args string, env CommandEnv) error {
			text := buildPrompt(args)
			env.Submit(text)
			if onComplete != nil {
				onComplete()
			}
			return nil
		},
	}
}
