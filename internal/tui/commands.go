package tui

// commands.go — one registry for every command-like action in the TUI.
// Each entry carries its slash name, any aliases, an optional hotkey, a
// short description, and the handler. The slash drawer, the hotkey
// dispatcher, and the help overlay all read from this same table — adding a
// new command is one struct literal that surfaces everywhere.
//
// Design rule held as law elsewhere in cairo: the user's TUI actions do not
// mutate the DB. Commands that affect Selene's mind (memories, soul,
// prompts, session history) must go through conversation. Commands here
// only touch UI state (view, layout, drawers) or initiate conversation.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/commands"
	"github.com/scotmcc/cairo2/internal/hostedit"
	"github.com/scotmcc/cairo2/internal/learn"
	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
)

// Command is a single invokable action. Handler returns a tea.Cmd (which
// may be nil) and may set any model state it needs through the pointer.
//
// HandlerWithArgs, when non-nil, takes precedence over Handler and receives
// any text typed after the command name (e.g. "/learn ~/foo" → "~/foo").
// Commands that don't care about args use Handler; commands that do (like
// /learn) use HandlerWithArgs. Both forms can coexist on the same struct,
// but HandlerWithArgs wins when both are set.
type Command struct {
	Name            string
	Aliases         []string
	Hotkey          string
	Description     string
	Handler         func(*model) tea.Cmd
	HandlerWithArgs func(*model, string) tea.Cmd
}

// defaultCommands returns the built-in command registry.
func defaultCommands() []Command {
	return []Command{
		{
			Name:        "quit",
			Aliases:     []string{"q", "exit"},
			Hotkey:      "ctrl+q",
			Description: "Close cairo. Drains the background summarizer before exit.",
			Handler: func(_ *model) tea.Cmd {
				return tea.Quit
			},
		},
		{
			Name: "clear",
			// Ctrl-C is handled with state-dependent semantics directly in
			// Update (cancel → clear input → clear view), not via the
			// registry. /clear here is the unconditional "always clear
			// the view" form, intentionally hotkey-less.
			Description: "Clear the visible transcript. Selene's memory is untouched — this is view-only.",
			Handler: func(m *model) tea.Cmd {
				m.transcript.Reset()
				m.pushViewport()
				return nil
			},
		},
		{
			Name:        "help",
			Aliases:     []string{"?"},
			Description: "Show commands and hotkeys. Esc to dismiss.",
			Handler: func(m *model) tea.Cmd {
				return m.openPanel(panelHelpID)
			},
		},
		sharedInitCmd(initPromptFor),
		{
			Name:        "config",
			Aliases:     []string{"settings"},
			Hotkey:      "ctrl+g",
			Description: "Open the configuration panel — browse and edit settings (model, voice, etc).",
			Handler: func(m *model) tea.Cmd {
				return m.openPanel(panelConfigID)
			},
		},
		{
			Name:        "reload",
			Aliases:     []string{"restart"},
			Description: "Restart cairo to apply config changes (model, ollama_url, etc.) — same terminal, fresh process.",
			Handler: func(m *model) tea.Cmd {
				m.reload = true
				return tea.Quit
			},
		},
		{
			Name:        "new",
			Aliases:     []string{"fresh"},
			Description: "Start a fresh session. Drains the current session's unsummarized backlog on the way out, then exec re-launches with -new.",
			Handler: func(m *model) tea.Cmd {
				m.newSession = true
				return tea.Quit
			},
		},
		{
			Name:        "export",
			Description: "Export the current transcript to a markdown file. Usage: /export [path] — defaults to ~/.cairo/exports/<session-id>-<timestamp>.md.",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				path, err := resolveExportPath(args, m.session.ID)
				if err != nil {
					m.addToast("/export: "+err.Error(), toastError)
					return nil
				}
				content := m.transcript.String()
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					m.addToast("/export: mkdir: "+err.Error(), toastError)
					return nil
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					m.addToast("/export: "+err.Error(), toastError)
					return nil
				}
				m.addToast("exported to "+path, toastSuccess)
				return nil
			},
		},
		{
			Name:        "deepen",
			Description: "Second-pass context briefing. Selene searches memories, recent summaries, indexed projects, and facts, then reports what she currently knows about your active work.",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				prompt := deepenPromptFor(m, args)
				m.addToast("deepening context…", toastInfo)
				m.appendUser("(deepening context)")
				m.startAssistant()
				return m.submit(prompt)
			},
		},
		{
			Name:        "stackdump",
			Aliases:     []string{"stack"},
			Hotkey:      `ctrl+\`,
			Description: "Write a goroutine stack dump to ~/.cairo/stack_dump_<timestamp>.txt. Useful for debugging hangs.",
			Handler: func(m *model) tea.Cmd {
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, true)
				ts := time.Now().Format("20060102-150405")
				path := filepath.Join(sqliteopen.DefaultDataDir(), "stack_dump_"+ts+".txt")
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					m.addToast("stackdump: mkdir: "+err.Error(), toastError)
					return nil
				}
				if err := os.WriteFile(path, buf[:n], 0o644); err != nil {
					m.addToast("stackdump: "+err.Error(), toastError)
					return nil
				}
				m.addToast("stack dump written to "+path, toastSuccess)
				return nil
			},
		},
		{
			Name:        "learn",
			Description: "Index a directory: walk files, summarize, embed. Usage: /learn [path] — defaults to the session cwd.",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				root, err := resolveLearnPath(args, m.session.CWD)
				if err != nil {
					m.addToast("/learn: "+err.Error(), toastError)
					return nil
				}
				project := filepath.Base(root)
				res, err := learn.SpawnBackground(learn.SpawnRequest{
					DB:      m.db,
					Project: project,
					Root:    root,
				}, learn.Detached)
				if err != nil {
					m.addToast("/learn: "+err.Error(), toastError)
					return nil
				}
				m.addToast(
					"learning "+project+" — task "+itoa(res.TaskID)+" (Ctrl+T to watch)",
					toastSuccess)
				return nil
			},
		},
		{
			Name:        "pin",
			Description: "Pin a memory so it survives nightly auto-dump. Usage: /pin <memory_id>",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
				if err != nil || id <= 0 {
					m.addToast("/pin: invalid id — usage: /pin <memory_id>", toastError)
					return nil
				}
				if err := m.db.Memories.Pin(id); err != nil {
					m.addToast("/pin: "+err.Error(), toastError)
					return nil
				}
				m.addToast(fmt.Sprintf("memory %d pinned", id), toastSuccess)
				return nil
			},
		},
		{
			Name:        "unpin",
			Description: "Remove the pin from a memory. Usage: /unpin <memory_id>",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				id, err := strconv.ParseInt(strings.TrimSpace(args), 10, 64)
				if err != nil || id <= 0 {
					m.addToast("/unpin: invalid id — usage: /unpin <memory_id>", toastError)
					return nil
				}
				if err := m.db.Memories.Unpin(id); err != nil {
					m.addToast("/unpin: "+err.Error(), toastError)
					return nil
				}
				m.addToast(fmt.Sprintf("memory %d unpinned", id), toastSuccess)
				return nil
			},
		},
		{
			Name:        "pinned",
			Description: "List all pinned memories.",
			Handler: func(m *model) tea.Cmd {
				mems, err := m.db.Memories.ListPinned()
				if err != nil {
					m.addToast("/pinned: "+err.Error(), toastError)
					return nil
				}
				if len(mems) == 0 {
					m.appendSystem("no pinned memories")
					return nil
				}
				var sb strings.Builder
				for _, mem := range mems {
					fmt.Fprintf(&sb, "[P] [%d] %s\n", mem.ID, mem.Content)
				}
				m.appendSystem(strings.TrimRight(sb.String(), "\n"))
				return nil
			},
		},
		{
			Name:        "dream",
			Description: "Manually trigger a dream-pass (maintenance cycle). Runs in the background; toast on completion.",
			Handler: func(m *model) tea.Cmd {
				m.addToast("running dream-pass in background…", toastInfo)
				exe, err := os.Executable()
				if err != nil {
					exe = "cairo"
				}
				cmd := exec.Command(exe, "dream")
				cmd.Stdout = nil
				cmd.Stderr = nil
				if err := cmd.Start(); err != nil {
					m.addToast("/dream: spawn failed: "+err.Error(), toastError)
					return nil
				}
				proc := cmd.Process
				return func() tea.Msg {
					state, err := proc.Wait()
					if err != nil || !state.Success() {
						return dreamDoneMsg{err: err}
					}
					return dreamDoneMsg{}
				}
			},
		},
		{
			Name:        "dreams",
			Description: "List recent dreams or open one. Usage: /dreams [id|YYYY-MM-DD]",
			HandlerWithArgs: func(m *model, args string) tea.Cmd {
				args = strings.TrimSpace(args)
				if args == "" {
					dreams, err := m.db.Dreams.List(10)
					if err != nil {
						m.addToast("/dreams: "+err.Error(), toastError)
						return nil
					}
					if len(dreams) == 0 {
						m.appendSystem("no dreams on record")
						return nil
					}
					var sb strings.Builder
					fmt.Fprintf(&sb, "%-4s  %-10s  %-8s  %-24s  %s\n", "ID", "Date", "Mood", "Themes", "Path")
					for _, d := range dreams {
						themes := d.Themes
						if len(themes) > 24 {
							themes = themes[:21] + "..."
						}
						fmt.Fprintf(&sb, "%-4d  %-10s  %-8s  %-24s  %s\n",
							d.ID, d.Date, d.Mood, themes, d.NarrativePath)
					}
					m.appendSystem(strings.TrimRight(sb.String(), "\n"))
					return nil
				}

				var dream *memory.Dream
				if id, err := strconv.ParseInt(args, 10, 64); err == nil {
					dreams, err := m.db.Dreams.List(1000)
					if err != nil {
						m.addToast("/dreams: "+err.Error(), toastError)
						return nil
					}
					for _, d := range dreams {
						if d.ID == id {
							dream = d
							break
						}
					}
				} else {
					dream, _ = m.db.Dreams.GetByDate(args)
				}

				if dream == nil {
					m.addToast("/dreams: not found: "+args, toastError)
					return nil
				}

				if dream.NarrativePath == "" || dream.NarrativePath == "<pending>" {
					m.appendSystem("/dreams: narrative not yet written for dream " + itoa(dream.ID))
					return nil
				}

				if hostedit.WantsTUISuspend() {
					data, err := os.ReadFile(dream.NarrativePath)
					if err != nil {
						m.addToast("/dreams: read file: "+err.Error(), toastError)
						return nil
					}
					m.appendSystem(string(data))
					return nil
				}
				if err := hostedit.Open(dream.NarrativePath, 0); err != nil {
					data, readErr := os.ReadFile(dream.NarrativePath)
					if readErr != nil {
						m.addToast("/dreams: "+err.Error(), toastError)
						return nil
					}
					m.appendSystem(string(data))
				}
				return nil
			},
		},
	}
}

// dreamDoneMsg is returned by the /dream background goroutine when the
// dream-pass subprocess exits.
type dreamDoneMsg struct{ err error }

// resolveExportPath resolves the output path for /export. Empty arg defaults
// to ~/.cairo/exports/<sessionID>-<timestamp>.md. Expands "~" prefix.
func resolveExportPath(arg string, sessionID int64) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		ts := time.Now().Format("20060102-150405")
		arg = filepath.Join(home, ".cairo", "exports",
			fmt.Sprintf("session%d-%s.md", sessionID, ts))
		return arg, nil
	}
	if arg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(arg, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		arg = filepath.Join(home, arg[2:])
	}
	return arg, nil
}

// resolveLearnPath turns the user-typed argument from /learn into an
// absolute, validated directory path. Empty input falls back to cwd; "~"
// and "~/..." expand to the home dir; relative paths resolve against cwd.
// Returns a clear error if the path doesn't exist or isn't a directory.
func resolveLearnPath(arg, cwd string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		if cwd == "" {
			return "", fmt.Errorf("session has no cwd and no path given")
		}
		return cwd, nil
	}
	if arg == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		arg = home
	} else if strings.HasPrefix(arg, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		arg = filepath.Join(home, arg[2:])
	}
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(cwd, arg)
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	return abs, nil
}

// itoa is a tiny strconv-free int64→string used by the /learn handler.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// sharedInitCmd constructs the TUI-local /init Command backed by the shared
// commands.NewInitCommand handler. The promptFn adapts the shared builder
// signature (func(args string) string) to the TUI-specific initPromptFor.
// Sets m.initPending so handleTurnComplete can set init_complete=true
// deterministically once the turn finishes — small models don't reliably call
// config(set, init_complete, true) themselves.
func sharedInitCmd(promptFn func(*model) string) Command {
	return Command{
		Name:        "init",
		Description: "Start the guided setup. Selene introduces herself, captures your name, learns the project.",
		Handler: func(m *model) tea.Cmd {
			m.initPending = true
			sc := commands.NewInitCommand(func(_ string) string {
				return promptFn(m)
			}, nil)
			return adaptSharedCmd(sc, "")(m)
		},
	}
}

// initPromptFor loads the init skill from the DB and returns the text to
// send as the user's opening message. Mirrors cli.buildInitPrompt but scoped
// to the TUI's assumptions.
func initPromptFor(m *model) string {
	skill, err := m.db.Skills.Get("init")
	if err != nil || skill == nil {
		return "Please run an initialization process: introduce yourself, ask what I should be called, then capture project context using memory, prompt_part, and config tools."
	}
	// NO FollowUp here. /init is an interactive multi-turn conversation —
	// the skill explicitly tells the model to ask questions one at a time
	// and store after each answer. A queued follow-up fires immediately
	// after the model's first response (before the human replies), which
	// preempts the conversation and forces the model to fabricate stored
	// values. See the skill itself for the storage instructions.

	// Apply {{key}} template substitution so the AI sees its actual name and
	// the user's name in the skill text, not the literal placeholder strings.
	// Without this, small models may echo "Hi — I'm {{ai_name}}" verbatim
	// instead of using the value from config.
	// Write session_skill_tools unconditionally — empty list clears any prior
	// skill's restriction (last-skill-wins). A skill without allowed_tools
	// frontmatter therefore lifts scoping rather than inheriting it.
	skillTools := tools.ParseSkillAllowedTools(skill.Content)
	_ = m.db.Config.Set("session_skill_tools", strings.Join(skillTools, ","))

	vars, _ := m.db.Config.All()
	content := agent.ApplyTemplates(tools.SkillBody(skill.Content), vars)
	return "Please run the initialization process now. Follow the guidance carefully.\n\n" + content
}

// deepenPromptFor builds the /deepen synthesis prompt. If a "deepen" skill
// exists in the DB, its content is used (allowing the prompt to be edited
// without recompiling). Otherwise a built-in fallback is returned.
// The optional topic arg (e.g. "/deepen auth refactor") is appended so
// Selene can scope her search to a specific area when supplied.
func deepenPromptFor(m *model, topic string) string {
	topic = strings.TrimSpace(topic)

	// Try to load from the skills table first, matching /init's approach.
	skill, err := m.db.Skills.Get("deepen")
	if err == nil && skill != nil && strings.TrimSpace(skill.Content) != "" {
		if topic != "" {
			return tools.SkillBody(skill.Content) + "\n\nTopic focus for this deepen pass: " + topic
		}
		return tools.SkillBody(skill.Content)
	}

	// Fallback built-in prompt. This is the synthesis prompt Selene runs to
	// build a context briefing at the start of a non-trivial session.
	topicClause := ""
	if topic != "" {
		topicClause = fmt.Sprintf(" with a focus on: %s", topic)
	}
	cwdBase := filepath.Base(m.session.CWD)
	if cwdBase == "" || cwdBase == "." {
		cwdBase = "the current project"
	}
	return fmt.Sprintf(`/deepen — run a context briefing%s.

You are about to work with the user on %s. Before responding to any new request, load the relevant context:

1. Call summary_search with a query related to "%s" to surface recent session summaries.
2. Call memory(action="list") to load recent identity-level context and any feedback from prior sessions.
3. Call learn(action="list") to see what projects are indexed; if any match the current directory, call learn(action="search", query="%s") to load relevant indexed knowledge.
4. Call fact_search or knowledge_search for any facts related to the topic focus.

After loading, produce a concise briefing (≈200 words) of what you now know about the user's active work and current state. Close with one focusing question: "What's the primary outcome you want from this session?"

Do not wait for permission — run the tool calls now and deliver the briefing as your response.`,
		topicClause, cwdBase, cwdBase, cwdBase)
}

// lookupByName returns a pointer to the command whose Name or Aliases matches
// the given bare token (no leading slash). Returns nil if no match.
func lookupByName(cmds []Command, token string) *Command {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return nil
	}
	for i := range cmds {
		if strings.EqualFold(cmds[i].Name, token) {
			return &cmds[i]
		}
		for _, a := range cmds[i].Aliases {
			if strings.EqualFold(a, token) {
				return &cmds[i]
			}
		}
	}
	return nil
}

// lookupByHotkey returns the command bound to the given tea key string
// (e.g. "ctrl+q"). Returns nil if no command has that binding.
func lookupByHotkey(cmds []Command, hotkey string) *Command {
	if hotkey == "" {
		return nil
	}
	for i := range cmds {
		if cmds[i].Hotkey == hotkey {
			return &cmds[i]
		}
	}
	return nil
}

// filterCommands returns commands matching query. Substring-match over Name
// and Aliases (case-insensitive); prefix matches are ranked before
// mid-string matches; within rank, alphabetical. Empty query returns all.
func filterCommands(cmds []Command, query string) []Command {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		out := make([]Command, len(cmds))
		copy(out, cmds)
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}

	type scored struct {
		cmd  Command
		rank int // 0 = prefix, 1 = substring; lower is better
	}
	var matches []scored
	for _, c := range cmds {
		rank := -1
		// Check name first
		n := strings.ToLower(c.Name)
		if strings.HasPrefix(n, query) {
			rank = 0
		} else if strings.Contains(n, query) {
			rank = 1
		}
		// Check aliases — a prefix match on an alias still counts.
		for _, a := range c.Aliases {
			al := strings.ToLower(a)
			if strings.HasPrefix(al, query) && (rank == -1 || rank > 0) {
				rank = 0
			} else if strings.Contains(al, query) && rank == -1 {
				rank = 1
			}
		}
		if rank >= 0 {
			matches = append(matches, scored{c, rank})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].cmd.Name < matches[j].cmd.Name
	})
	out := make([]Command, len(matches))
	for i, m := range matches {
		out[i] = m.cmd
	}
	return out
}
