package main

// Blank import MUST come before anything that transitively imports
// bubbletea — this package's init pins lipgloss's background so
// bubbletea's package init() skips its OSC 11 probe. Kept in its own
// declaration to defeat gofmt's alphabetization within grouped imports.
import _ "github.com/scotmcc/cairo2/internal/tuisetup"

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/registry"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
	"github.com/scotmcc/cairo2/internal/worktree"
)

var version = "dev"

func main() {
	// Install signal handler for early (wizard) exit: SIGINT → exit(130).
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	immediateExit := make(chan struct{})
	go func() {
		select {
		case <-sigChan:
			fmt.Fprintln(os.Stderr, "\ninterrupted")
			os.Exit(130)
		case <-immediateExit:
			return
		}
	}()

	// Subcommand dispatch — must happen before flag.Parse() so the subcommand
	// name isn't consumed as a positional arg.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "export":
			if err := runExport(os.Args[2:]); err != nil {
				fatalf("export: %v", err)
			}
			return
		case "import":
			if err := runImport(os.Args[2:]); err != nil {
				fatalf("import: %v", err)
			}
			return
		case "diff":
			if err := runDiff(os.Args[2:]); err != nil {
				fatalf("diff: %v", err)
			}
			return
		case "dream":
			if err := runDream(os.Args[2:]); err != nil {
				fatalf("dream: %v", err)
			}
			return
		case "learn":
			if err := runLearn(os.Args[2:]); err != nil {
				fatalf("learn: %v", err)
			}
			return
		case "serve":
			if err := runServe(os.Args[2:]); err != nil {
				fatalf("serve: %v", err)
			}
			return
		case "token":
			if err := runToken(os.Args[2:]); err != nil {
				fatalf("token: %v", err)
			}
			return
		case "config":
			if err := runConfig(os.Args[2:]); err != nil {
				fatalf("config: %v", err)
			}
			return
		}
	}

	var (
		newSession     = flag.Bool("new", false, "start a new session")
		sessionID      = flag.Int64("session", 0, "resume a specific session by ID")
		sessionName    = flag.String("name", "", "name for a new session")
		roleFlag       = flag.String("role", "", "role for a new session (default: thinking_partner)")
		taskFlag       = flag.Int64("task", 0, "run as a background task worker for this task ID")
		modelFlag      = flag.String("model", "", "LLM model to use (overrides role config)")
		background     = flag.Bool("background", false, "background mode: plain log output, no banner")
		tuiFlag        = flag.Bool("tui", false, "use the Bubble Tea TUI instead of the line CLI")
		vscodeFlag     = flag.Bool("vscode", false, "use VS Code integration mode: JSONL events over stdout")
		dataDirFlag    = flag.String("data-dir", "", "override data directory (default: $CAIRO_DATA_DIR or ~/.cairo)")
		disciplineFlag = flag.String("discipline", "", "discipline mode: readonly|scoped|full (default: full)")
		versionFlag    = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "cairo — local-first AI coding harness")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "usage:")
		fmt.Fprintln(out, "  cairo [flags] [message]            interactive, or one-shot when a message is given")
		fmt.Fprintln(out, "  cairo export [--full] <out.cairo>  export identity to a portable bundle")
		fmt.Fprintln(out, "  cairo import [--force] <bundle>    replace local identity from a bundle")
		fmt.Fprintln(out, "  cairo diff <bundle>                compare a bundle against local identity")
		fmt.Fprintln(out, "  cairo dream                        headless maintenance: consolidate memories, facts, summaries")
		fmt.Fprintln(out, "  cairo learn [path]                 index a directory: summarize, embed (preferred)")
		fmt.Fprintln(out, "  cairo serve [--port N] [--auth]    start the HTTP server (OpenAI-compat, JSONRPC, chat)")
		fmt.Fprintln(out, "  cairo token                        generate a bearer token for cairo serve --auth")
		fmt.Fprintln(out, "  cairo config get <key>             read a config key from the DB")
		fmt.Fprintln(out, "  cairo config set <key> <value>     write a config key to the DB")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "flags (interactive / one-shot mode):")
		flag.PrintDefaults()
		fmt.Fprintln(out)
		fmt.Fprintln(out, "slash commands (inside the interactive CLI): type /help")
		fmt.Fprintln(out, "subcommand help: cairo <subcommand> -h")
	}
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}

	singleMessage := strings.Join(flag.Args(), " ")

	dataDir := sqliteopen.DefaultDataDir()
	if *dataDirFlag != "" {
		dataDir = *dataDirFlag
	}

	warnCairo2Migration(dataDir)

	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fatalf("create data dir: %v", err)
	}
	database, err := sqliteopen.OpenAt(filepath.Join(dataDir, "cairo.db"))
	if err != nil {
		fatalf("open db: %v", err)
	}
	defer database.Close()
	var bgWg sync.WaitGroup
	defer bgWg.Wait()

	if !*background {
		warnEmbedModelMismatch(database)
	}

	// --- background task mode ---
	if *taskFlag != 0 {
		if err := runTask(database, *taskFlag, *modelFlag, *background); err != nil {
			fatalf("task %d failed: %v", *taskFlag, err)
		}
		return
	}

	// SweepOrphans runs once per parent startup, after the subprocess-worker early return above.
	if n, err := database.Tasks.SweepOrphans(); err != nil {
		fmt.Fprintf(os.Stderr, "cairo: warn: SweepOrphans: %v\n", err)
	} else if n > 0 {
		fmt.Fprintf(os.Stderr, "cairo: learn: swept %d orphan task(s)\n", n)
	}

	// --- interactive / single-message mode ---
	ollamaURL := resolveOllamaURL(database)
	if !*vscodeFlag && configValue(database, "setup_complete") != "true" && os.Getenv("OLLAMA_URL") == "" {
		chosen, err := chooseOllamaURL(database, ollamaURL)
		if err != nil {
			fatalf("setup: %v", err)
		}
		ollamaURL = chosen
	}
	embedModel, _ := database.Config.Get(config.KeyEmbedModel)
	if embedModel != "" {
		_ = database.Config.Set(config.KeyLastEmbedModel, embedModel)
	}

	llmClient, err := connectOllama(database, ollamaURL)
	if err != nil {
		fatalf("llm: %v", err)
	}

	if !*vscodeFlag {
		if err := runFirstRunWizard(database, llmClient); err != nil {
			fatalf("setup: %v", err)
		}
	}

	sessionRole := *roleFlag
	if sessionRole == "" {
		sessionRole = identity.RoleThinkingPartner
	}
	session, err := resolveSession(database, llmClient, &bgWg, *newSession, *sessionID, *sessionName, sessionRole)
	if err != nil {
		fatalf("session: %v", err)
	}

	disciplineMode := session.DisciplineMode
	if *disciplineFlag != "" {
		switch *disciplineFlag {
		case "readonly":
			disciplineMode = agent.DisciplineReadonly
		case "scoped":
			disciplineMode = agent.DisciplineScoped
		case "full":
			disciplineMode = agent.DisciplineFull
		default:
			fatalf("unknown discipline mode %q — use readonly, scoped, or full", *disciplineFlag)
		}
		if err := database.Sessions.SetDisciplineMode(session.ID, disciplineMode); err != nil {
			fatalf("set discipline mode: %v", err)
		}
		session.DisciplineMode = disciplineMode
	}

	model, err := sqliteopen.ResolveModel(database, session.Role, "")
	if err != nil {
		fatalf("no model configured for role %q — set one with: config(set, model=<model-name>)", session.Role)
	}

	llmAPIKey := resolveLLMAPIKey(database)
	ctx0, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	info, err := llm.FetchModelInfo(ctx0, ollamaURL, llmAPIKey, model)
	cancel()
	if err == nil && info.ContextLength > 0 {
		_ = database.Config.Set(config.KeyModelCtx, fmt.Sprintf("%d", info.ContextLength))
	} else if savedCtx, _ := database.Config.Get(config.KeyModelCtx); savedCtx == "" {
		fmt.Fprintf(os.Stderr, "cairo: WARN: model_ctx not set and /model_group/info unavailable; defaulting to 8192\n")
		_ = database.Config.Set(config.KeyModelCtx, "8192")
	}

	var choiceRequests chan tools.ChoiceRequest
	if *tuiFlag {
		choiceRequests = make(chan tools.ChoiceRequest, 1)
	}

	builtins := tools.Default(database, llmClient, embedModel, choiceRequests)
	{
		repoRoot, _ := os.Getwd()
		wm := worktree.NewManager(repoRoot, database)
		builtins = append(builtins, tools.Worktree(wm))
		builtins = append(builtins, tools.MergeJob(wm, database))
	}
	allowed, _ := database.Roles.AllowedTools(session.Role)
	if len(allowed) > 0 {
		builtins = tools.FilterByAllowlist(builtins, allowed)
	}
	custom, err := tools.LoadCustom(database)
	if err != nil {
		fatalf("load custom tools: %v", err)
	}
	custom = tools.FilterByAllowlist(custom, allowed)
	allTools := append(builtins, custom...)

	a, err := agent.New(agent.Config{
		DB:      database,
		LLM:     llmClient,
		Model:   model,
		Session: session,
		Tools:   allTools,
	})
	if err != nil {
		fatalf("create agent: %v", err)
	}

	// Transition from immediate-exit handler to graceful shutdown.
	close(immediateExit)
	signal.Reset(os.Interrupt)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if registryURL, _ := database.Config.Get(config.KeyRegistryURL); registryURL != "" {
		agentID, _ := database.Config.Get(config.KeyRegistryAgentID)
		newID, err := registry.Register(ctx, registryURL, agentID, version)
		if err != nil {
			log.Printf("registry: registration failed: %v", err)
		} else {
			if newID != agentID {
				_ = database.Config.Set(config.KeyRegistryAgentID, newID)
			}
			intervalSec := 60
			if v, _ := database.Config.Get(config.KeyRegistryHeartbeatInterval); v != "" {
				if n, err2 := strconv.Atoi(v); err2 == nil && n > 0 {
					intervalSec = n
				}
			}
			bgWg.Add(1)
			go func() { defer bgWg.Done(); registry.HeartbeatLoop(ctx, registryURL, newID, version, intervalSec) }()
			bgWg.Add(1)
			go func() { defer bgWg.Done(); registry.LivenessStream(ctx, registryURL, newID) }()
		}
	}

	app := &App{
		DB:         database,
		LLM:        llmClient,
		OllamaURL:  ollamaURL,
		EmbedModel: embedModel,
		Model:      model,
		Session:    session,
		Agent:      a,
		Choices:    choiceRequests,
		RegistryWG: &bgWg,
	}

	if singleMessage != "" {
		if err := runOneShot(app, ctx, singleMessage); err != nil {
			fatalf("run: %v", err)
		}
		return
	}

	if *tuiFlag {
		if err := runTUI(app, ctx); err != nil {
			fatalf("tui: %v", err)
		}
		return
	}

	if *vscodeFlag {
		if err := runVSCode(app, ctx); err != nil {
			fatalf("vscode: %v", err)
		}
		return
	}

	if err := runCLI(app, ctx); err != nil {
		fatalf("cli: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "cairo: "+format+"\n", args...)
	os.Exit(1)
}

// warnEmbedModelMismatch checks each embeddable table for rows whose
// embed_model differs from the current config, and prints a warning if any
// are found. Cross-model embeddings are silently skipped during search.
func warnEmbedModelMismatch(database *sqliteopen.DB) {
	currentModel, _ := database.Config.Get(config.KeyEmbedModel)
	if currentModel == "" {
		return
	}

	tables := []string{"memories", "skills", "summaries", "facts"}

	var mismatched []string
	for _, table := range tables {
		models, err := database.DistinctEmbedModels(table)
		if err != nil {
			continue
		}
		for _, m := range models {
			if m != "" && m != currentModel {
				mismatched = append(mismatched, table)
				break
			}
		}
	}

	if len(mismatched) > 0 {
		fmt.Fprintf(os.Stderr, "cairo: Warning: some embeddings were created with a different model and will be excluded from search. (tables: %s)\n",
			strings.Join(mismatched, ", "))
	}
}

// warnCairo2Migration prints a one-time notice when ~/.cairo2 exists but the
// resolved data directory does not. Does not migrate data.
func warnCairo2Migration(currentDataDir string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	oldDir := filepath.Join(home, ".cairo2")
	if _, err := os.Stat(oldDir); os.IsNotExist(err) {
		return
	}
	if _, err := os.Stat(currentDataDir); err == nil {
		return
	}
	fmt.Fprintf(os.Stderr, "cairo: notice: found %s but not %s\n", oldDir, currentDataDir)
	fmt.Fprintf(os.Stderr, "cairo: the data directory was renamed from .cairo2 to .cairo.\n")
	fmt.Fprintf(os.Stderr, "cairo: to migrate: mv %s %s\n", oldDir, currentDataDir)
}
