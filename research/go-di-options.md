# Go DI options for Cairo

## Audience note

You know .NET DI: `IServiceCollection`, `services.AddSingleton<IFoo, Foo>()`, constructor injection in every class, scopes for per-request lifetimes. Go has none of this in the standard library. Below, each pattern is mapped to a .NET equivalent.

## 1. Manual constructor injection (the Go default)

The idiomatic Go approach: every type has a `NewX(dep1, dep2, ...) *X` constructor; you wire them up in `main()`. Interfaces are defined where they are *consumed*, not where they are implemented.

### Realistic example (4 dependencies)

```go
// internal/db/db.go
type DB struct{ /* ... */ }
func Open(path string) (*DB, error) { /* ... */ }

// internal/llm/llm.go
type Client interface {
    Chat(ctx context.Context, msgs []Message) (Reply, error)
    Embed(ctx context.Context, text string) ([]float32, error)
}
type OllamaClient struct{ /* ... */ }
func NewOllama(url, apiKey string) *OllamaClient { /* ... */ }

// internal/tools/tools.go
type Tool interface { /* ... */ }
func Default(db *db.DB, llm llm.Client, embed string) []Tool { /* ... */ }

// internal/agent/agent.go
type Config struct {
    DB      *db.DB
    LLM     llm.Client
    Model   string
    Session *db.Session
    Tools   []tools.Tool
}
type Agent struct{ /* ... */ }
func New(cfg Config) (*Agent, error) { /* ... */ }

// cmd/cairo/main.go (the wiring)
func main() {
    database, _ := db.Open(dataPath)
    defer database.Close()

    llmClient := llm.NewOllama(ollamaURL, apiKey)
    builtins := tools.Default(database, llmClient, embedModel)

    a, _ := agent.New(agent.Config{
        DB:      database,
        LLM:     llmClient,
        Model:   model,
        Session: session,
        Tools:   builtins,
    })
    _ = a.Run(ctx)
}
```

**.NET equivalent:**

```csharp
// In Program.cs:
services.AddSingleton<DB>(sp => DB.Open(dataPath));
services.AddSingleton<ILlmClient>(sp => new OllamaClient(ollamaURL, apiKey));
services.AddSingleton<IEnumerable<ITool>>(sp =>
    Tools.Default(sp.GetRequiredService<DB>(), sp.GetRequiredService<ILlmClient>(), embedModel));
services.AddSingleton<Agent>();
```

The Go version is ~6 lines of wiring instead of ~6 service registrations, but the *runtime cost* is zero (no reflection, no service-locator lookups, no scope resolution). Compiler verifies the graph at build time. If you forget a dependency, your code does not compile.

### Where it gets painful

Symptoms that manual DI is becoming a problem:
1. **`main()` is over ~150 lines of pure wiring** (not subcommand dispatch — wiring).
2. **You have circular dependencies** between subsystems and need lazy-init.
3. **You have many distinct surfaces sharing partial graphs** (e.g. 12 binaries each picking 6 of 20 services).
4. **Scope/lifetime management** beyond singletons (per-request, per-tenant). Go web stacks usually solve this with explicit context-passing, not DI scopes.
5. **Tests need to mock 8+ dependencies** and the test setup is longer than the test.

If you have 1–2 of these, refactor your `main` (named init functions, App struct). If you have 4–5, consider Wire.

## 2. Google Wire (`github.com/google/wire`)

Codegen-based DI. You write provider functions and an injector signature; `wire` generates the wiring code at build time.

```go
//go:build wireinject

func InitializeAgent(ctx context.Context, opts Options) (*agent.Agent, func(), error) {
    wire.Build(
        db.Open,
        llm.NewOllama,
        tools.Default,
        agent.New,
    )
    return nil, nil, nil
}
```

You run `wire ./cmd/cairo` and it generates `wire_gen.go` that contains the literal sequence of constructor calls.

**.NET equivalent:** the Microsoft `IHostBuilder` pattern, but with all wiring resolved at compile time instead of via reflection.

**Cost to adopt:**
- Add `wire` as a build-time tool. New devs must run `go generate`.
- Refactors that change a constructor signature require regenerating `wire_gen.go`.
- Errors from Wire are sometimes cryptic ("no provider for type X" — you have to find the missing arrow in the graph yourself).

**When it's appropriate:**
- 50+ services, many binaries, complex graphs.
- Teams that already use codegen heavily.
- Projects where the wiring code itself becomes a maintenance liability.

For Cairo: **overkill.** Cairo has ~10 top-level dependencies (db, llm, tools, agent, registry, server, ...). Wire would generate code that's not meaningfully shorter than the manual version.

## 3. Uber Fx (`go.uber.org/fx`)

Runtime-reflection DI. Closest spiritual cousin to .NET's `IServiceCollection`.

```go
fx.New(
    fx.Provide(db.Open),
    fx.Provide(llm.NewOllama),
    fx.Provide(tools.Default),
    fx.Provide(agent.New),
    fx.Invoke(func(a *agent.Agent) { /* run */ }),
).Run()
```

Lifecycle hooks (`fx.Lifecycle`) for startup/shutdown order, scopes via modules, etc.

**.NET equivalent:** very nearly identical to `services.AddSingleton(...)` + `IHostedService` + `IHostApplicationLifetime`.

**When it's appropriate:**
- Microservice fleets with shared service-registration patterns.
- Projects already using Fx (Uber's own services, parts of YugabyteDB).
- Heavy use of cross-cutting concerns (interceptors, metrics, tracing) that benefit from a registration model.

**Cost:**
- Reflection-based — startup is slower, runtime errors instead of compile errors.
- Stack traces become harder to read (frames inside `fx.Invoke`).
- Brings a non-trivial dep tree.

For Cairo: **wrong.** Cairo is one product binary, not a service fleet. Fx's value is in operational consistency across many services.

## 4. At what scale does manual injection break?

Empirically, looking at real Go codebases:

- **Up to ~150 wiring lines:** manual is unambiguously fine.
- **150–400 lines:** manual is fine if structured (named init functions, an `App` struct, clear pipeline). Above ~300 lines, it starts to feel heavy.
- **Above ~400 lines, or 30+ services in a single graph:** Wire becomes attractive.
- **Multi-binary, multi-team, ops-heavy projects:** Fx makes sense.

Cairo's `cmd/cairo/main.go` is **429 lines today**, but most of those lines are NOT wiring — they are signal handling, subcommand dispatch, flag parsing, surface selection. The actual wiring section is roughly L139–298 (~160 lines), and at least 60 of those are first-run-wizard prompts and DB-config reads, not `New()` calls. The pure constructor-call portion is more like ~30 lines.

**This is not a DI-tool problem. This is a `main()` is doing too many jobs problem.**

## 5. The Cairo-specific recommendation

**Stay with manual injection. Refactor `main()`.** No frameworks.

Concretely:

1. Extract a `cmd/cairo/app.go` with:

   ```go
   type App struct {
       Cfg     Options
       DB      *db.DB
       LLM     llm.Client
       Session *db.Session
       Agent   *agent.Agent
       Tools   []tools.Tool
       BgWg    *sync.WaitGroup
   }

   func newApp(ctx context.Context, opts Options) (*App, func(), error) {
       app := &App{Cfg: opts, BgWg: &sync.WaitGroup{}}
       cleanup := func() { app.BgWg.Wait(); if app.DB != nil { app.DB.Close() } }

       if err := app.openDB(); err != nil      { return nil, cleanup, err }
       if err := app.connectLLM(ctx); err != nil { return nil, cleanup, err }
       if err := app.resolveSession(); err != nil { return nil, cleanup, err }
       if err := app.buildTools(); err != nil   { return nil, cleanup, err }
       if err := app.buildAgent(); err != nil   { return nil, cleanup, err }
       if err := app.startRegistry(ctx); err != nil { return nil, cleanup, err }

       return app, cleanup, nil
   }
   ```

2. Each `app.openDB()`, `app.connectLLM(...)` is a method on App that mutates one field. Each is short (10–30 lines), each is testable in isolation.

3. `main()` shrinks to: parse flags → dispatch subcommand OR build app → dispatch surface.

4. Surfaces accept `*App`:
   ```go
   func runTUI(ctx context.Context, app *App) error { ... }
   func runCLI(ctx context.Context, app *App) error { ... }
   func runOneShot(ctx context.Context, app *App, msg string) error { ... }
   func runVSCode(ctx context.Context, app *App) error { ... }
   ```

5. Subcommands that need a different graph (e.g. `cairo serve` doesn't need a TUI) can call a slimmer `newServeApp()` or just call `newApp()` and ignore the unused fields. There's no runtime cost — Go's compiler tree-shakes nothing because everything was already in the linker; the cost is conceptual cleanliness.

This is **the same DI you do with constructor injection in C#**, minus the `IServiceCollection` ceremony. The compiler enforces the graph; tests can construct an `App` directly with stubs.

## 6. Mocking and testability

In .NET you'd inject `IFoo` and pass `Mock<IFoo>` in tests. Go does the same, but with zero ceremony — interfaces are structurally typed:

```go
// in package agent:
type llmClient interface {
    Chat(ctx context.Context, msgs []Message) (Reply, error)
}

func New(cfg Config) *Agent { /* uses cfg.LLM as llmClient */ }

// in tests:
type fakeLLM struct{}
func (fakeLLM) Chat(ctx, msgs) (Reply, error) { return Reply{Text: "ok"}, nil }

a, _ := agent.New(agent.Config{LLM: fakeLLM{}, ...})
```

No interface registration, no `Mock<>`, no DI container override. The test just constructs the type. This is why Go projects rarely need DI frameworks: the language already gives you the seam.

## Summary table

| Approach | Wiring LOC at Cairo scale | Compile-time safety | Reflection? | Learning cost | Fit for Cairo |
|---|---|---|---|---|---|
| Manual + App struct | ~50–80 | Yes | No | Zero | **Recommended** |
| Manual (current) | ~30 lines pure wiring + 100 lines tangled with logic | Yes | No | Zero | Already in use; needs refactor not replacement |
| Wire | ~10 lines + provider funcs | Yes | No | 1 day | Overkill |
| Fx | ~15 lines + provider funcs | No | Yes | 2-3 days | Wrong shape (this is an app, not a service fleet) |
