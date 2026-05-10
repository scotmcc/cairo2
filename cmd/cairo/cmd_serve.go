package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/cli"
	"github.com/scotmcc/cairo2/internal/registry"
	"github.com/scotmcc/cairo2/internal/server"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	"github.com/scotmcc/cairo2/internal/tools"
	"github.com/scotmcc/cairo2/internal/worktree"
)

// runServe starts the cairo HTTP server.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	portFlag := fs.Int("port", 0, "TCP port to listen on (default: 1337 or server_port config)")
	authFlag := fs.Bool("auth", false, "require Bearer token authentication")
	tsnetFlag := fs.Bool("tsnet", false, "serve over Tailscale tsnet on :443 (requires HTTPS enabled in tailnet admin)")
	registerFlag := fs.String("register", "", "URL of a cairo-registry to register with (independent of --tsnet)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo serve [flags]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Start the cairo HTTP server. Exposes /api/chat, OpenAI-compatible")
		fmt.Fprintln(os.Stderr, "/v1/chat/completions, and JSON-RPC 2.0 /rpc endpoints.")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	rc, err := newRunContext()
	if err != nil {
		return err
	}
	database := rc.DB
	llmClient := rc.LLM
	embedModel := rc.EmbedModel

	model, err := sqliteopen.ResolveModel(database, identity.RoleThinkingPartner, "")
	if err != nil {
		database.Close()
		return fmt.Errorf("no model configured — set one with: config(set, model=<model-name>)")
	}

	cwd, _ := os.Getwd()
	session, err := database.Sessions.Latest()
	if err != nil {
		database.Close()
		return fmt.Errorf("resolve session: %w", err)
	}
	if session == nil {
		session, err = database.Sessions.Create("", cwd, identity.RoleThinkingPartner)
		if err != nil {
			database.Close()
			return fmt.Errorf("create session: %w", err)
		}
	}

	builtins := tools.Default(database, llmClient, embedModel, nil)
	{
		repoRoot, _ := os.Getwd()
		wm := worktree.NewManager(repoRoot, database)
		builtins = append(builtins, tools.Worktree(wm))
		builtins = append(builtins, tools.MergeJob(wm, database))
	}
	if allowed, _ := database.Roles.AllowedTools(session.Role); len(allowed) > 0 {
		builtins = tools.FilterByAllowlist(builtins, allowed)
	}
	custom, _ := tools.LoadCustom(database)
	allTools := append(builtins, custom...)

	a, err := agent.New(agent.Config{
		DB:           database,
		LLM:          llmClient,
		Model:        model,
		Session:      session,
		Tools:        allTools,
		IsBackground: false,
	})
	if err != nil {
		database.Close()
		return fmt.Errorf("create agent: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer a.Close()
	defer database.Close()

	var wg sync.WaitGroup
	defer wg.Wait()

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
			wg.Add(1)
			go func() { defer wg.Done(); registry.HeartbeatLoop(ctx, registryURL, newID, version, intervalSec) }()
			wg.Add(1)
			go func() { defer wg.Done(); registry.LivenessStream(ctx, registryURL, newID) }()
		}
	}

	stopRenderer := cli.BackgroundRenderer(a.Bus(), os.Stdout)
	defer stopRenderer()

	var token string
	if *authFlag {
		stored, _ := database.Config.Get(config.KeyServerToken)
		if stored != "" {
			token = stored
		} else {
			tok, err := server.GenerateToken()
			if err != nil {
				return fmt.Errorf("generate token: %w", err)
			}
			if err := database.Config.Set(config.KeyServerToken, tok); err != nil {
				return fmt.Errorf("save token: %w", err)
			}
			token = tok
		}
	}

	port := *portFlag
	if port == 0 {
		if v, _ := database.Config.Get(config.KeyServerPort); v != "" {
			_, _ = fmt.Sscanf(v, "%d", &port)
		}
	}
	if port == 0 {
		port = server.DefaultPort
	}

	bridge := server.NewBridge(a)
	bridge.Start(ctx)
	defer bridge.Stop()

	opts := server.Options{
		Port:   port,
		Auth:   *authFlag,
		Token:  token,
		DBPath: filepath.Join(sqliteopen.DefaultDataDir(), "cairo.db"),
	}
	srv := server.New(a, database, bridge, opts)

	var ln net.Listener
	if *tsnetFlag {
		var cleanup func() error
		var lerr error
		ln, cleanup, lerr = server.NewTsnetListener(ctx)
		if lerr != nil {
			return fmt.Errorf("tsnet: %w", lerr)
		}
		defer cleanup()
	} else {
		addr := fmt.Sprintf(":%d", port)
		var lerr error
		ln, lerr = net.Listen("tcp", addr)
		if lerr != nil {
			var opErr *net.OpError
			if errors.As(lerr, &opErr) {
				return fmt.Errorf("port %d already in use — specify another with --port", port)
			}
			return lerr
		}
	}

	if *tsnetFlag {
		if *authFlag {
			fmt.Printf("  token: %s\n", token)
		} else {
			fmt.Printf("  token: (none — open)\n")
		}
	} else {
		fmt.Printf("cairo server listening\n")
		fmt.Printf("  url:   http://localhost:%d\n", port)
		if *authFlag {
			fmt.Printf("  token: %s\n", token)
		} else {
			fmt.Printf("  token: (none — open)\n")
		}
	}

	if *registerFlag != "" {
		// Phase 2.3 will reintroduce the fleet client here (hostname/tailnetNode args).
		storedID, err := database.Registrations.Get(*registerFlag)
		if err != nil {
			return fmt.Errorf("read stored registration: %w", err)
		}
		newID, err := registry.Register(ctx, *registerFlag, storedID, version)
		if err != nil {
			return fmt.Errorf("register with %s: %w", *registerFlag, err)
		}
		if storedID != "" && storedID != newID {
			log.Printf("registry: stored agent_id %s was not honored; replaced with %s", storedID[:8], newID[:8])
		}
		if err := database.Registrations.Upsert(*registerFlag, newID); err != nil {
			return fmt.Errorf("persist registration: %w", err)
		}
		go registry.LivenessStream(ctx, *registerFlag, newID)
		fmt.Printf("  registry: %s (agent_id=%s)\n", *registerFlag, newID[:8])
	}

	return srv.Serve(ctx, ln)
}
