# Adding a Tool

A tool is a Go function the agent can call during its reasoning loop. Every built-in tool lives in `internal/tools/`. This guide walks through the full lifecycle: writing the struct, registering it, granting it to roles, documenting it, and testing it.

## Table of contents

- [Anatomy of a Tool](#anatomy-of-a-tool)
- [Tool Description and Schema](#tool-description-and-schema)
- [Registering the Tool](#registering-the-tool)
- [Role Allowlists](#role-allowlists)
- [Documenting the Tool](#documenting-the-tool)
- [Testing the Tool](#testing-the-tool)
- [Common Mistakes](#common-mistakes)
- [Walkthrough: `read`](#walkthrough-read)

---

## 1. Anatomy of a Tool

The `Tool` interface is defined in `internal/agent/types.go`:

```go
// internal/agent/types.go:12
type Tool interface {
    Name() string
    Description() string
    Parameters() map[string]any // JSON Schema object
    Execute(args map[string]any, ctx *ToolContext) ToolResult
}
```

Every tool is a struct that satisfies this interface. `ToolResult` carries what the model sees plus optional TUI-only structured data:

```go
// internal/agent/types.go:18
type ToolResult struct {
    Content string // returned to the model as the tool result
    Details any    // structured data for the TUI renderer (never sent to model)
    IsError bool
}
```

`ToolContext` gives a tool everything it needs at call time:

```go
// internal/agent/types.go:43
type ToolContext struct {
    Ctx        context.Context
    WorkDir    string
    DB         *db.DB
    Config     ConfigStore
    Bus        EventSink
    Session    *db.Session
    Tools      []Tool
    Registry   *providers.Registry
    Background bool // true in headless/-background mode
}
```

### Minimal stateless tool — `read`

`internal/tools/read.go` is the cleanest example in the codebase:

```go
// internal/tools/read.go:17
type readTool struct{}

func Read() agent.Tool { return readTool{} }

func (readTool) Name() string        { return "read" }
func (readTool) Description() string { return "Read a file from the filesystem. Large files are truncated." }
func (readTool) Parameters() map[string]any {
    return map[string]any{
        "type": "object",
        "properties": map[string]any{
            "path":   prop("string", "Absolute or relative path to the file"),
            "offset": prop("integer", "Line number to start reading from (1-based, optional)"),
            "limit":  prop("integer", "Maximum number of lines to return (default 500)"),
        },
        "required": []string{"path"},
    }
}

func (readTool) Execute(args map[string]any, ctx *agent.ToolContext) agent.ToolResult {
    path := resolvePath(strArg(args, "path"), ctx.WorkDir)
    // ...
    return agent.ToolResult{Content: b.String()}
}
```

The constructor function (`Read()`) returns `agent.Tool`, not `readTool` directly — this keeps the concrete type unexported.

### Stateful tool — `say`

Tools that need DB access hold a reference in the struct:

```go
// internal/tools/say.go:18
type sayTool struct{ db *db.DB }

func Say(database *db.DB) agent.Tool { return sayTool{db: database} }
```

The constructor signature is the contract: whatever the tool needs at construction time goes here. The `Default()` function in `registry.go` calls all constructors and passes the right dependencies.

---

## 2. Tool Description and Schema

### Description

The `Description()` string is injected verbatim into the model's tool manifest. Local models respond much better to descriptions that:

- Say what the tool does in one sentence
- List actions explicitly (if multi-action)
- Name required arguments and explain what "optional" means
- Avoid vague verbs ("manage", "handle") — prefer "add", "search", "delete"

`say.go` is a good example of a rich description that includes usage guidance and configuration cues. `read.go` is a good example of a minimal one.

### Schema helpers

`internal/tools/tool.go` provides helpers used throughout the tools package:

```go
// internal/tools/tool.go:119
func prop(typ, desc string) map[string]any {
    return map[string]any{"type": typ, "description": desc}
}

func propEnum(desc string, values []string) map[string]any {
    return map[string]any{"type": "string", "description": desc, "enum": values}
}

func propOptional(typ, desc, defaultVal string) map[string]any {
    return map[string]any{"type": typ, "description": fmt.Sprintf("%s (optional, default: %s)", desc, defaultVal)}
}
```

Use `propEnum` for `action` parameters — it gives the model an explicit list of valid verbs and produces the best error messages when a model sends an unknown action.

### The action pattern

Multi-verb tools use a single `action` parameter. Routing is handled by `DispatchAction` in `tool.go`:

```go
// internal/tools/tool.go:35
func DispatchAction(args map[string]any, toolName string, handlers map[string]func() agent.ToolResult) agent.ToolResult {
    action := strArg(args, "action")
    if action == "" {
        return agent.ToolResult{Content: fmt.Sprintf("error: action is required for %s", toolName), IsError: true}
    }
    fn, ok := handlers[action]
    if !ok {
        // returns sorted list of valid actions in the error message
        ...
    }
    return fn()
}
```

Usage from `learn.go`:

```go
// internal/tools/learn.go:60
func (t learnTool) Execute(args map[string]any, _ *agent.ToolContext) agent.ToolResult {
    return DispatchAction(args, "learn", map[string]func() agent.ToolResult{
        "add":      func() agent.ToolResult { return t.doAdd(args) },
        "search":   func() agent.ToolResult { return t.doSearch(args) },
        "list":     func() agent.ToolResult { return t.doList() },
        "describe": func() agent.ToolResult { return t.doDescribe(args) },
        "forget":   func() agent.ToolResult { return t.doForget(args) },
        "status":   func() agent.ToolResult { return t.doStatus(args) },
    })
}
```

The `args` map is captured in the closure so each handler can call the arg helpers (`strArg`, `intArg`, `boolArg`, `floatArg`) without re-passing the map. This is the standard pattern — copy it.

### Naming conventions

- Tool names use underscores: `memory_tool`, `tool_list_builtin`
- Action names use underscores: `model_set`, not `modelSet`
- Constructors are TitleCase, matching the concept: `MemoryTool()`, `Learn()`, `PromptShow()`

---

## 3. Registering the Tool

All tools are assembled in `internal/tools/registry.go`, in the `Default()` function:

```go
// internal/tools/registry.go:15
func Default(database *db.DB, embedder Embedder, embedModel string, choiceRequests chan<- ChoiceRequest) []agent.Tool {
    embed := &EmbedClient{Embedder: embedder, Model: embedModel}
    tools := []agent.Tool{
        // filesystem
        Read(),
        Write(),
        // ...
        // Add your tool here
        MyTool(database),
    }
    // ...
    return tools
}
```

`Default()` returns the full tool slice used by every agent. The order in this slice becomes the order in `tool_list_builtin`'s output, which the model reads when asked what tools exist. Group related tools together in the same comment block.

If your tool needs the embed client (for semantic search), add it to the constructor signature and pass `embed`:

```go
MyTool(database, embed),
```

---

## 4. Role Allowlists

Tools are gated per role. Each role stores a JSON array of allowed tool names in the `roles.tools` column. The seed in `internal/store/seed.go` defines the initial allowlists:

```go
// internal/store/seed.go (thinking_partner role — illustrative subset)
{
    RoleThinkingPartner,
    "Interactive collaborator ...",
    "",
    "role:" + RoleThinkingPartner,
    `["read","write","edit","bash","memory_tool","search","fetch","learn","say",...]`,
    "",
},
```

You must update the seed **and** write a migration to backfill existing DBs.

### Step 1: update `seedRoles()` in `seed.go`

Add your tool name to the JSON array of every role that should have access. Think carefully about which roles need it — a coder probably doesn't need `say`; a researcher doesn't need `write`.

### Step 2: write a migration in `schema.go`

Use the same pattern as every other tool grant — a conditional `json_insert` that only adds the name if it isn't already there:

```go
// internal/store/schema.go (add to the migrations slice at the end)
// [v045] Grant my_tool to thinking_partner and planner
`UPDATE roles SET tools = json_insert(tools, '$[#]', 'my_tool')
 WHERE name IN ('thinking_partner', 'planner')
   AND NOT EXISTS (SELECT 1 FROM json_each(roles.tools) WHERE value = 'my_tool')`,
```

See migrations v009, v011, v031, v038, v044a for examples of this exact pattern.

The migration is idempotent: it checks `NOT EXISTS` before inserting, so running it twice is safe.

---

## 5. Documenting the Tool

### `docs/reference/tools.md`

Add a section under the appropriate group. Single-verb tools get a brief block:

```markdown
### `my_tool`
One sentence description of what it does.
- `arg1` (type, required/optional) — description
- `arg2` (type, optional, default N) — description
```

Multi-verb tools follow the `memory` / `learn` pattern: list each action with its arguments.

Update the per-role availability table at the bottom of `tools.md` with a row for your tool.

### `docs/reference/config-keys.md`

If your tool reads a config key, add a row to the appropriate section in `config-keys.md`. See [adding-a-config-key.md](adding-a-config-key.md).

---

## 6. Testing the Tool

### Unit tests in `internal/tools/`

Create `your_tool_test.go` alongside your implementation. The test helpers are shared across the package:

```go
// internal/tools/registry_test.go:11
func openTestDB(t *testing.T) *db.DB {
    t.Helper()
    path := filepath.Join(t.TempDir(), "test.db")
    d, err := db.OpenAt(path)
    if err != nil {
        t.Fatalf("OpenAt: %v", err)
    }
    t.Cleanup(func() { d.Close() })
    return d
}
```

For tools that need embedding, use `stubEmbedder` from `memory_test.go`:

```go
// internal/tools/memory_test.go:16
type stubEmbedder struct{}

func (stubEmbedder) Embed(model, text string) ([]float32, error) {
    return []float32{float32(len(text)), 1.0}, nil
}
```

A typical tool test:

```go
func TestMyTool_BasicRoundTrip(t *testing.T) {
    d := openTestDB(t)
    tool := MyTool(d)
    ctx := &agent.ToolContext{DB: d}

    res := tool.Execute(map[string]any{"action": "add", "content": "hello"}, ctx)
    if res.IsError {
        t.Fatalf("add: %s", res.Content)
    }

    res = tool.Execute(map[string]any{"action": "list"}, ctx)
    if res.IsError {
        t.Fatalf("list: %s", res.Content)
    }
    if !strings.Contains(res.Content, "hello") {
        t.Errorf("expected 'hello' in list output, got: %s", res.Content)
    }
}
```

### The allowlist regression test

`registry_test.go` contains `TestDefault_RespectsSeededRoleAllowlists`, which opens a fresh DB and verifies that every tool name in every role's allowlist exists in `Default()`. This test will catch any drift between your seed entry and your tool's actual `Name()` return value. Run it:

```
go test ./internal/tools/... -run TestDefault
```

---

## 7. Common Mistakes

### Output cap

Every tool result is truncated at `tool_output_limit` bytes (default 64 KB) before it reaches the model. This cap is applied in the agent loop, not inside tools. Tools can still return more data — the TUI sees the full `Content` via the event bus — but the model gets the truncated version with a notice appended. For tools that return large results (file contents, search dumps), document that callers should use `limit` or `offset` arguments to control volume.

The key is `db.KeyToolOutputLimit` (`"tool_output_limit"`) and the truncation happens in `internal/agent/loop.go`.

### JSON serialization of `Details`

`ToolResult.Details` is passed to the TUI's event system as `any`. It is never serialized to JSON and never sent to the model. Type-assert it directly in TUI panel/toast code. If you need structured data in the model result, format it in `Content` — typically as a plain-text list or key-value block.

### Long-running tools

If your tool's work takes more than a few seconds (indexing files, fetching slow URLs), do not block `Execute`. The pattern from `learn.go` is to create a `tasks` row in the DB for progress tracking, spawn a `cairo` subprocess via the `learn.SpawnBackground` helper, and return immediately with a message pointing the user to the threads panel:

```go
// internal/tools/learn.go:96
res, err := learn.SpawnBackground(learn.SpawnRequest{...}, detached)
// ...
return agent.ToolResult{
    Content: fmt.Sprintf("learn add started (task %d, pid %d) ...", res.TaskID, res.PID, ...),
}
```

Do not spin a goroutine inside `Execute` and block on it — that blocks the agent loop. The `say` tool's audio playback goroutine (`go func() { ... }()`) is the only exception and only because it truly fire-and-forgets with no meaningful result.

### Write permission

Tools that write files must call `checkWritePermission` before touching the filesystem:

```go
// internal/tools/tool.go:164
func checkWritePermission(ctx *agent.ToolContext, path string) error {
    unsafeMode, _ := ctx.DB.Config.Get("unsafe_mode")
    if unsafeMode != "true" {
        return requireUnderCWD(path, ctx.WorkDir)
    }
    return nil
}
```

`edit.go` and `write.go` both call this at the top of `Execute`. Skipping it breaks the sandbox.

---

## 8. Walkthrough: `read`

This traces the full lifecycle of the simplest real tool.

### File: `internal/tools/read.go`

The struct is unexported (`readTool`), the constructor is exported (`Read()`), and the four interface methods are defined on value receivers. `Execute` resolves the path relative to `ctx.WorkDir`, reads up to 1 MB, windows the requested line range, and returns numbered output. Errors become `IsError: true` results — the model sees the error message and can decide what to do.

### Registration: `internal/tools/registry.go:19`

```go
tools := []agent.Tool{
    // filesystem
    Read(),
    ...
}
```

No dependencies — `readTool` is stateless, so the constructor takes no arguments.

### Role allowlists: `internal/store/seed.go`

`read` appears in `thinking_partner`, `orchestrator`, `coder`, `planner`, `reviewer`, and `researcher` — every role that does substantive work with files. It does not appear in `dream` because the dream role only consolidates memory, not files.

### Tests: `internal/tools/read_test.go`

Tests cover: basic read, offset+limit windowing, truncation message, non-existent file, empty file, offset past end, subdirectory, binary file. Each test creates a tempdir, writes a file, calls `Execute`, and asserts on `Content`.

### Reference: `docs/reference/tools.md`

Listed under "Filesystem" with its three parameters and behavior notes (1 MB cap, truncation notice).

At no point does `read` appear in `panel_config.go` or `schema.go` — it needs no config keys and no migrations because it requires no persistent state.
