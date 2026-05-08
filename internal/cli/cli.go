package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// Run starts the interactive CLI chat loop.
func Run(a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) error {
	stop := Renderer(a.Bus())
	defer stop()
	defer a.Close() // drain summarizer before exit

	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	a.StartWatcher(watchCtx)

	printBanner(database, session)
	maybeInitHint(database)

	reg := newCLIRegistry(a, database, session)
	env := &cliEnv{agent: a, session: session, db: database}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)

	for {
		fmt.Print("\033[36m> \033[0m")

		if !scanner.Scan() {
			fmt.Println()
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var forceConsider bool
		line, forceConsider = parseConsiderPrefix(line)

		if !forceConsider && strings.HasPrefix(line, "/") {
			// Try the shared registry first.
			parts := strings.Fields(line)
			token := strings.TrimPrefix(parts[0], "/")
			args := ""
			if len(parts) > 1 {
				args = strings.Join(parts[1:], " ")
			}
			if cmd := reg.Find(token); cmd != nil {
				if err := cmd.Handler(args, env); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
				}
				continue
			}

			// Fall back to the local handler for CLI-specific commands.
			exit := handleCommand(line, a, database, session)
			if exit {
				break
			}
			continue
		}

		if err := a.PromptWithOpts(context.Background(), line, agent.PromptOpts{ForceConsider: forceConsider}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}

	return scanner.Err()
}

// RunOnce sends a single message and exits — for scripted use.
// Waits for background work (summarizer) to complete before returning.
func RunOnce(a *agent.Agent, text string) error {
	stop := Renderer(a.Bus())
	defer stop()
	var forceConsider bool
	text, forceConsider = parseConsiderPrefix(text)
	err := a.PromptWithOpts(context.Background(), text, agent.PromptOpts{
		ForceConsider: forceConsider,
		TriggerSource: "cli",
	})
	a.Close() // drain summarizer goroutine before process exits
	return err
}

// parseConsiderPrefix detects the leading "/c " marker that opts a single
// message into the consider step. The trailing space is required so a literal
// "/cabc" command name or path that happens to start with "/c" doesn't trigger
// consider. Returns the input unchanged with force=false when no prefix.
func parseConsiderPrefix(s string) (text string, force bool) {
	if strings.HasPrefix(s, "/c ") {
		return strings.TrimPrefix(s, "/c "), true
	}
	return s, false
}

// handleCommand processes a slash command that was not claimed by the shared registry.
// Returns exit=true if the program should terminate.
func handleCommand(line string, a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) (exit bool) {
	parts := strings.Fields(line)
	cmd := parts[0]

	switch cmd {
	case "/exit", "/quit", "/q":
		fmt.Println("bye.")
		return true

	case "/session":
		printSession(session)

	default:
		text, err := listCommandOutput(cmd, database, session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return false
		}
		if text != "" {
			fmt.Print(text)
		} else {
			fmt.Printf("unknown command: %s (try /help)\n", cmd)
		}
	}

	return false
}

// listCommandOutput formats output for list-style slash commands shared between
// the CLI and VSCode handlers. Returns ("", nil) if cmd is not a list command.
func listCommandOutput(cmd string, database *sqliteopen.DB, session *sessions.Session) (string, error) {
	var b strings.Builder

	switch cmd {
	case "/sessions":
		sessions, err := database.Sessions.List()
		if err != nil {
			return "", err
		}
		if len(sessions) == 0 {
			return "no sessions\n", nil
		}
		for _, s := range sessions {
			marker := "  "
			if s.ID == session.ID {
				marker = "* "
			}
			name := s.Name
			if name == "" {
				name = "(unnamed)"
			}
			fmt.Fprintf(&b, "%s[%d] %s — %s — %s\n",
				marker, s.ID, name, s.Role, s.LastActive.Format("2006-01-02 15:04"))
		}

	case "/jobs":
		jobs, err := database.Jobs.List()
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "no jobs\n", nil
		}
		for _, j := range jobs {
			fmt.Fprintf(&b, "[%d] [%s] %s\n", j.ID, j.Status, j.Title)
		}

	case "/memories":
		memories, err := database.Memories.AllContent()
		if err != nil {
			return "", err
		}
		if len(memories) == 0 {
			return "no memories\n", nil
		}
		for _, m := range memories {
			if m.PinnedAt != nil {
				fmt.Fprintf(&b, "[P] [%d] %s\n", m.ID, m.Content)
			} else {
				fmt.Fprintf(&b, "[%d] %s\n", m.ID, m.Content)
			}
		}

	case "/tools":
		customTools, err := database.Tools.List()
		if err != nil {
			return "", err
		}
		if len(customTools) == 0 {
			return "no custom tools\n", nil
		}
		for _, t := range customTools {
			status := "on"
			if !t.IsEnabled {
				status = "off"
			}
			fmt.Fprintf(&b, "[%s] %s — %s\n", status, t.Name, t.Description)
		}

	case "/skills":
		skills, err := database.Skills.List()
		if err != nil {
			return "", err
		}
		if len(skills) == 0 {
			return "no skills\n", nil
		}
		for _, s := range skills {
			fmt.Fprintf(&b, "%s — %s\n", s.Name, s.Description)
		}

	default:
		return "", nil
	}

	return b.String(), nil
}

// buildInitPrompt loads the appropriate init skill and builds the prompt to send.
// It also injects a follow-up into the agent's queue so that after the exploration
// completes, the agent is forced to store its findings — even if it forgot to do so
// during the main turn.
func buildInitPrompt(arg string, a *agent.Agent, database *sqliteopen.DB, session *sessions.Session) string {
	skillName := "init"
	if strings.EqualFold(arg, "codebase") {
		skillName = "init_codebase"
	}

	skill, err := database.Skills.Get(skillName)
	if err != nil || skill == nil {
		return "Please run an initialization process: ask me about this project and my preferences, explore the codebase if there is one, and store what you learn using memory(action=\"add\") and prompt_part(action=\"add\")."
	}

	// FollowUp only for init_codebase — that flow is a one-shot exploration
	// where storage at the end is the right ending. The interactive /init
	// flow is multi-turn (ask questions one at a time, store inline after
	// each answer); injecting a follow-up there preempts the human's reply
	// and forces the model to fabricate stored values. See skill_init.txt.
	if skillName == "init_codebase" {
		a.FollowUp("Storage step: call memory with action=\"add\" now for every distinct fact you just learned — project purpose, tech stack, architecture, key files, conventions, commands. One call per fact. Then call memory with action=\"list\" to confirm what was stored. Finally, call config with action=\"set\", key=\"init_complete\", value=\"true\" to mark initialization done.")
	}

	var header string
	if skillName == "init_codebase" {
		header = fmt.Sprintf("Working directory: %s\n\nPlease run the codebase exploration process now:\n\n", session.CWD)
	} else {
		header = "Please run the initialization process now. Follow the guidance carefully.\n\n"
	}

	// Write session_skill_tools unconditionally — empty list clears any prior
	// skill's restriction (last-skill-wins). A skill without allowed_tools
	// frontmatter therefore lifts scoping rather than inheriting it.
	skillTools := tools.ParseSkillAllowedTools(skill.Content)
	_ = database.Config.Set("session_skill_tools", strings.Join(skillTools, ","))

	// Apply {{key}} template substitution so the AI sees its actual name and
	// the user's name in the skill text, not the literal placeholder strings.
	vars, _ := database.Config.All()
	return header + agent.ApplyTemplates(tools.SkillBody(skill.Content), vars)
}

func printSession(session *sessions.Session) {
	name := session.Name
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Printf("session %d: %s\nrole: %s\ncwd:  %s\n", session.ID, name, session.Role, session.CWD)
}

// maybeInitHint prints a one-line nudge after the banner if init_complete is
// still "false" — distinguishing a fresh DB from one that ran init. Stays
// quiet once init_complete=true so it isn't noise on every startup.
func maybeInitHint(database *sqliteopen.DB) {
	done, _ := database.Config.Get("init_complete")
	if done == "true" {
		return
	}
	aiName, _ := database.Config.Get("ai_name")
	if aiName == "" {
		aiName = "your agent"
	}
	fmt.Printf("\033[2m(%s is here but hasn't met you yet — type /init to introduce yourself, or /config for direct setup)\033[0m\n", aiName)
	fmt.Println()
}

func printBanner(database *sqliteopen.DB, session *sessions.Session) {
	name := session.Name
	if name == "" {
		name = fmt.Sprintf("session %d", session.ID)
	}
	aiName, _ := database.Config.Get("ai_name")
	if aiName == "" {
		aiName = "cairo"
	}
	fmt.Printf("\033[1mcairo\033[0m · \033[1m%s\033[0m · %s · role:%s\n", aiName, name, session.Role)
	fmt.Println("type /help for commands, /exit to quit")
	fmt.Println()
}
