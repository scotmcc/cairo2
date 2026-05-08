package cli

// cli_command_env.go — implements commands.CommandEnv for the CLI.
// The CLI has no panels and no streaming state to report from the main loop,
// so SetPanel is a no-op and IsStreaming always returns false.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/commands"
	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// itoa64 is a tiny int64-to-string helper used in the /dreams CLI handler.
func itoa64(n int64) string {
	return strconv.FormatInt(n, 10)
}

// cliEnv implements commands.CommandEnv for the CLI read-loop.
type cliEnv struct {
	agent   *agent.Agent
	session *sessions.Session
	db      *sqliteopen.DB
}

// Output prints text to stdout.
func (e *cliEnv) Output(text string) {
	fmt.Print(text)
}

// Submit sends a message to the agent synchronously, the same way the main
// read-loop does. Errors are printed to stderr.
func (e *cliEnv) Submit(text string) {
	if err := e.agent.Prompt(context.Background(), text); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}

// SetPanel is a no-op in the CLI; there are no panels.
func (e *cliEnv) SetPanel(_ string) {}

// IsStreaming always returns false in the CLI; the CLI's read-loop is
// synchronous so there is never a concurrent agent turn.
func (e *cliEnv) IsStreaming() bool { return false }

// newCLIRegistry builds the shared command registry for CLI use.
// The shared /init command is registered here; the remaining CLI-specific
// commands (quit, session list, etc.) are handled by the switch in
// handleCommand as before.
func newCLIRegistry(a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) *commands.Registry {
	env := &cliEnv{agent: a, session: session, db: database}
	r := commands.NewRegistry()

	// /init — shared factory, CLI-specific prompt builder.
	// Submit is synchronous in the CLI, so onComplete sets init_complete=true
	// deterministically after the turn finishes — small models don't reliably
	// call config(set, init_complete, true) themselves.
	r.Register(commands.NewInitCommand(
		func(args string) string {
			return buildInitPrompt(args, a, database, session)
		},
		func() { _ = database.Config.Set("init_complete", "true") },
	))

	// /help — shared handler: Output the help text.
	r.Register(&commands.Command{
		Name:        "help",
		Aliases:     []string{"?"},
		Description: "Show commands and hotkeys.",
		Handler: func(_ string, _ commands.CommandEnv) error {
			env.Output(helpText)
			return nil
		},
	})

	// /clear — no-op in CLI (terminal has its own scrollback).
	r.Register(&commands.Command{
		Name:        "clear",
		Description: "Clear the visible transcript (no-op in CLI).",
		Handler: func(_ string, _ commands.CommandEnv) error {
			return nil
		},
	})

	// /pin <id> — pin a memory by ID.
	r.Register(&commands.Command{
		Name:        "pin",
		Description: "Pin a memory so it survives nightly auto-dump. Usage: /pin <memory_id>",
		Handler: func(args string, e commands.CommandEnv) error {
			id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
			if err != nil || id <= 0 {
				e.Output("/pin: invalid id — usage: /pin <memory_id>\n")
				return nil
			}
			if err := database.Memories.Pin(id); err != nil {
				return err
			}
			e.Output(fmt.Sprintf("memory %d pinned\n", id))
			return nil
		},
	})

	// /unpin <id> — remove pin from a memory.
	r.Register(&commands.Command{
		Name:        "unpin",
		Description: "Remove the pin from a memory. Usage: /unpin <memory_id>",
		Handler: func(args string, e commands.CommandEnv) error {
			id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
			if err != nil || id <= 0 {
				e.Output("/unpin: invalid id — usage: /unpin <memory_id>\n")
				return nil
			}
			if err := database.Memories.Unpin(id); err != nil {
				return err
			}
			e.Output(fmt.Sprintf("memory %d unpinned\n", id))
			return nil
		},
	})

	// /pinned — list all pinned memories.
	r.Register(&commands.Command{
		Name:        "pinned",
		Description: "List all pinned memories.",
		Handler: func(_ string, e commands.CommandEnv) error {
			mems, err := database.Memories.ListPinned()
			if err != nil {
				return err
			}
			if len(mems) == 0 {
				e.Output("no pinned memories\n")
				return nil
			}
			for _, m := range mems {
				e.Output(fmt.Sprintf("[P] [%d] %s\n", m.ID, m.Content))
			}
			return nil
		},
	})

	// /dream — manually trigger a dream-pass. Streams cairo's dream subcommand
	// to stdout synchronously. The shell `cairo dream` does the same thing;
	// /dream lets users trigger it without leaving the line CLI session.
	r.Register(&commands.Command{
		Name:        "dream",
		Description: "Manually trigger a dream-pass. Streams output to stdout.",
		Handler: func(_ string, e commands.CommandEnv) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("locate cairo binary: %w", err)
			}
			cmd := exec.Command(exe, "dream")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			return cmd.Run()
		},
	})

	// /dreams [id|YYYY-MM-DD] — list recent dreams or print a narrative.
	r.Register(&commands.Command{
		Name:        "dreams",
		Description: "List recent dreams or print a narrative. Usage: /dreams [id|YYYY-MM-DD]",
		Handler: func(args string, e commands.CommandEnv) error {
			args = strings.TrimSpace(args)
			if args == "" {
				dreams, err := database.Dreams.List(10)
				if err != nil {
					return err
				}
				if len(dreams) == 0 {
					e.Output("no dreams on record\n")
					return nil
				}
				e.Output(fmt.Sprintf("%-4s  %-10s  %-8s  %-24s  %s\n", "ID", "Date", "Mood", "Themes", "Path"))
				for _, d := range dreams {
					themes := d.Themes
					if len(themes) > 24 {
						themes = themes[:21] + "..."
					}
					e.Output(fmt.Sprintf("%-4s  %-10s  %-8s  %-24s  %s\n",
						itoa64(d.ID), d.Date, d.Mood, themes, d.NarrativePath))
				}
				return nil
			}

			var dream *memory.Dream
			if id, err := strconv.ParseInt(args, 10, 64); err == nil {
				all, err := database.Dreams.List(1000)
				if err != nil {
					return err
				}
				for _, d := range all {
					if d.ID == id {
						dream = d
						break
					}
				}
			} else {
				dream, _ = database.Dreams.GetByDate(args)
			}

			if dream == nil {
				e.Output("/dreams: not found: " + args + "\n")
				return nil
			}
			if dream.NarrativePath == "" || dream.NarrativePath == "<pending>" {
				e.Output("/dreams: narrative not yet written for dream " + itoa64(dream.ID) + "\n")
				return nil
			}
			data, err := os.ReadFile(dream.NarrativePath)
			if err != nil {
				return err
			}
			e.Output(string(data))
			return nil
		},
	})

	return r
}

// helpText is the CLI help string. Kept here so newCLIRegistry can reference
// it without importing cli.go's package-level var. Matches the /help case
// in the original handleCommand switch.
const helpText = `
slash commands:
  /init              guided setup: learn this project and configure the AI
  /init codebase     explore and learn the current codebase only
  /session           show current session info
  /sessions          list all sessions (restart with -session <id> to switch)
  /jobs              list all jobs
  /memories          list stored memories
  /pinned            list pinned memories
  /pin <id>          pin a memory (protects it from auto-dump)
  /unpin <id>        remove pin from a memory
  /dreams            list recent dream-pass runs
  /dreams <id>       print narrative for dream id (or YYYY-MM-DD)
  /tools             list custom tools
  /skills            list skills
  /clear             clear the visible transcript
  /help              show this help
  /exit, /quit, /q   exit cairo
`
