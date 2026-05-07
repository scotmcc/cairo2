# cmd/cairo audit

## Scale

- `cmd/cairo/main.go`: **429 lines.** (Other files in `cmd/cairo/`: not separately enumerated here; `runExport`, `runImport`, `runDiff`, `runDream`, `runLearn`, `runServe`, `runToken`, `runConfig`, `runTask`, `resolveSession`, `connectOllama`, `chooseOllamaURL`, `runFirstRunWizard`, `resolveOllamaURL`, `configValue`, `resolveLLMAPIKey` are referenced from main.go — they live in sibling files of the same `package main`.)

## What `main()` actually does (initialization sequence)

Reading line by line:

1. **L36–50: Early signal handler.** Installs a SIGINT handler that does `exit(130)` immediately. Has an `immediateExit` channel to deactivate it once setup completes. (~15 lines)
2. **L55–98: Subcommand dispatch.** Hand-rolled switch on `os.Args[1]` BEFORE `flag.Parse()` because each subcommand owns its own flag set. Subcommands: `export`, `import`, `diff`, `dream`, `learn`, `serve`, `token`, `config`. ~45 lines.
3. **L100–135: Top-level flag declaration + Usage.** ~35 lines.
4. **L139–146: Resolve data directory** (flag > env > default). + one-time `.cairo2 → .cairo` migration warning. ~10 lines.
5. **L149–158: Open SQLite DB.** Mkdir, OpenAt, defer Close, defer bgWg.Wait. ~10 lines.
6. **L162–165: `warnEmbedModelMismatch`.** Cross-model embedding warning.
7. **L168–175: Background-task-worker mode.** If `--task` flag is set, dispatch to `runTask` and return.
8. **L177–182: SweepOrphans.** Mark crashed task subprocesses as failed.
9. **L184–204: LLM/Ollama setup.** Resolve URL (config > env > prompt the user on first-run), record last embed model, connect.
10. **L206–210: First-run wizard** (skipped in `--vscode` mode).
11. **L212–219: Session resolution.** `--new` / `--session` / `--name` / `--role` → `db.Session`.
12. **L221–242: Discipline-mode override.** CLI flag overrides stored mode; persists override.
13. **L244–248: Model resolution.** Role → config → fallback.
14. **L250–260: Model context window probe.** `llm.FetchModelInfo`, persist `model_ctx`.
15. **L263–267: Choice channel allocation** (only in TUI mode).
16. **L269–287: Tool wiring.** `tools.Default(...)` + `tools.Worktree(wm)` + `tools.MergeJob(wm, db)` + role allowlist filtering + `LoadCustom` + filter custom by allowlist.
17. **L289–298: Agent construction.** `agent.New(agent.Config{...})`.
18. **L300–305: Signal-handler transition.** Close `immediateExit`, reset SIGINT, install graceful `signal.NotifyContext`.
19. **L307–327: Registry registration.** If `registry_url` is set in config: Register, persist agent_id, start HeartbeatLoop and LivenessStream goroutines on `bgWg`.
20. **L329–334: One-shot mode.** If positional message present, `cli.RunOnce` and return.
21. **L336–362: TUI mode.** `tui.Run`. Handles re-exec for reload/new-session.
22. **L364–369: VS Code mode.** `cli.RunVSCode`.
23. **L371–373: Default CLI mode.** `cli.Run`.

That is **23 distinct concerns** in one function.

## Helper functions in main.go

- `fatalf` — printf+exit (3 lines).
- `warnEmbedModelMismatch` — runs 4 SELECTs across `memories`, `skills`, `summaries`, `facts`. **Knows table names by string.** This is the smell flagged in the briefing.
- `warnCairo2Migration` — one-time path-rename notice.

## Dependency graph of initialization

```
DataDir
  └─> mkdir
        └─> db.OpenAt
              ├─> warnEmbedModelMismatch (read-only)
              ├─> SweepOrphans (read-write)
              └─> resolveOllamaURL ──┐
                  chooseOllamaURL ◄──┤  (first-run prompt loop)
                                     ▼
                                connectOllama ──> llm.Client
                                     │
                                     ▼
                                runFirstRunWizard
                                     │
                                     ▼
                                resolveSession ──> db.Session
                                     │
                                     ▼
                                resolve discipline + model
                                     │
                                     ▼
                                FetchModelInfo (persist model_ctx)
                                     │
                                     ▼
                  ┌──────────────────┼──────────────────┐
                  ▼                  ▼                  ▼
              tools.Default     worktree.NewManager    role allowlist
                  └─────────────► allTools = ...
                                     │
                                     ▼
                                agent.New(Config{db, llm, model, session, tools})
                                     │
                                     ▼
                                signal-handler swap
                                     │
                                     ▼
                                registry register + bgWg.Add(2 goroutines)
                                     │
                                     ▼
                                surface dispatch (one-shot | TUI | vscode | CLI)
```

The graph is **strictly linear** until the surface dispatch — it's a pipeline. Every step depends on its predecessor; nothing parallelizes. That makes extraction straightforward: each step becomes a named function returning the next stage's input.

## Surface selection: clean or not?

**Currently:** `--tui`, `--vscode`, no flag = line CLI, positional message = one-shot. Plus pre-flag-parse subcommand dispatch (`serve`, `learn`, etc.) for headless modes. Plus `--task` for "I am a worker subprocess".

**Issues:**

1. The `--task` mode is a covert subcommand (it doesn't go through `os.Args[1]` dispatch because it's a flag). Inconsistent.
2. The TUI/CLI/VSCode trio is selected by mutually-exclusive flags but not validated as such — passing `--tui --vscode` would silently take the TUI branch.
3. `serve` has its own subcommand but reuses the same DB/LLM/agent setup — except the setup happens in `runServe`, which means setup is duplicated between `runServe` and `main`.
4. The `warnCairo2Migration` and `warnEmbedModelMismatch` helpers live in `main.go` but logically belong to `internal/db/`.

**What would clean it up:**

- A `cmd/cairo/surfaces.go` with one function per surface (`runTUI`, `runCLI`, `runVSCode`, `runOneShot`) — each accepting the same `*App` struct.
- A `cmd/cairo/app.go` exposing `type App struct{ DB *db.DB; LLM llm.Client; Agent *agent.Agent; Session *db.Session; Tools []tools.Tool }` and `func newApp(ctx, opts) (*App, error)` that owns the linear pipeline above.
- Subcommands in `cmd/cairo/cmd_*.go` files (one per subcommand): `cmd_export.go`, `cmd_import.go`, `cmd_serve.go`, etc.
- `--task` becomes a real subcommand `cairo task <id>` (or moves to a separate binary `cairo-worker`).

## What a well-organized `cmd/cairo/` would look like

```
cmd/cairo/
  main.go            ~80 lines: signal handler, subcommand dispatch, surface dispatch
  app.go             ~150 lines: type App struct + newApp(ctx, opts) — the linear pipeline
  surfaces.go        ~120 lines: runTUI, runCLI, runVSCode, runOneShot — accept *App
  cmd_export.go      one subcommand
  cmd_import.go
  cmd_diff.go
  cmd_dream.go
  cmd_learn.go
  cmd_serve.go
  cmd_token.go
  cmd_config.go
  cmd_task.go        --task → real subcommand
  wizard.go          first-run wizard (currently in main.go via runFirstRunWizard)
```

Each `cmd_*.go` registers itself or is dispatched via a tiny table in `main.go`. The 429-line `main.go` becomes ~80 lines. Every step is independently testable because each takes inputs and returns outputs (currently many step return values mutate state via the `database` pointer).

## Wiring section is *already* painful

The `tools.Default(...)` block + worktree manager + `MergeJob` + allowlist filter + custom tool loader (L269–287) is 18 lines of `append` plumbing. `agent.New(Config{...})` is currently the only "DI seam" and it takes 5 fields. None of this is "Spring DI level pain" — it's medium pain. (See `go-di-options.md` for the recommendation: stay manual, just refactor into named constructors.)
