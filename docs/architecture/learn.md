# Learn

The `learn` feature builds a per-project semantic index of source files. Users invoke it explicitly — via the `/learn [path]` slash command, the `learn(action="add")` tool call, or `cairo learn` on the command line — and the indexer runs as a background subprocess, updating progress in the threads panel.

Source: `internal/learn/`, `internal/tools/learn.go`, `cmd/cairo/` (the `learn` subcommand).

Cross-reference: [Memory model — Learn project index](../concepts/memory-model.md), [Database — projects and indexed_files](database.md).

---

## How it differs from the legacy `code_index` / `code_search` approach

`code_search` was removed in v0.2.1; `learn` is now the sole codebase-search path. The table below preserves the comparison for historical context.

| | `code_index` / `code_search` (removed v0.2.1) | `learn` |
|---|---|---|
| Trigger | `cairo index` (batch) | `/learn`, `learn(action="add")` (intentional) |
| Granularity | Raw file content (first ~4 KB) | Model-generated 1–2 sentence summary |
| Scope | Single global pool, keyed by `(root, rel_path)` | Per-project namespace (`projects` + `indexed_files` tables) |
| Metadata | File path, language, byte count | Path, type, bytes, SHA-256, summary, embed_model |
| Change detection | Upsert on `(root, rel_path)` | SHA-256 hash comparison — unchanged files skipped |
| Project description | None | Auto-generated from file summaries at end of run |
| Prompt integration | None | `## Indexed projects` section in every system prompt |

---

## Package: `internal/learn`

Four files:

```
walk.go      Walk() — directory traversal with gitignore + builtin exclusions
indexer.go   Run() — per-file summarize+embed loop; project description generation
spawn.go     SpawnBackground() — job/task row creation + subprocess launch
spawn_unix.go  detached sysattr (process group isolation)
```

### `walk.go` — directory traversal

`Walk(root string, extraExcludes []string) ([]Candidate, error)` returns a sorted list of `Candidate` structs (one per file that passed all filters):

```go
type Candidate struct {
    AbsPath string
    RelPath string
    Bytes   int
    Type    string   // extension without dot, lowercased
}
```

**Exclusion layers (applied in order):**

1. **Built-in directory names** — always skipped wherever they appear in the tree: `.git`, `.svn`, `.hg`, `node_modules`, `vendor`, `target`, `dist`, `build`, `.next`, `.venv`, `venv`, `__pycache__`, `.pytest_cache`, `.tox`, `.idea`, `.vscode`, `.cairo`, `.DS_Store`.
2. **Binary file extensions** — `.jpg`, `.mp4`, `.zip`, `.pdf`, `.db`, `.sqlite`, `.o`, `.so`, `.woff`, etc.
3. **Size filter** — files over 256 KB skipped.
4. **Binary content check** — first 512 bytes scanned for NUL bytes; NUL = binary.
5. **`.gitignore` at root** — parsed; patterns honored at root (anchored) or anywhere in the tree (free-floating). Negation (`!`) is not honored — false positives from skipped negation are acceptable for this use case.
6. **`extraExcludes`** from the caller (e.g. `learn add -exclude ...`).

### `indexer.go` — the Run loop

`Run(ctx context.Context, cfg Config) (*Stats, error)` is the main indexing function. It:

1. Calls `Walk` to get the candidate list.
2. For each candidate, calls `indexOne`:
   - Computes SHA-256 of the file.
   - Looks up the previous SHA via `IndexedFiles.GetSHA(project, relPath)`.
   - If unchanged, increments `stats.Skipped` and moves on.
   - Otherwise, reads the file (content capped at 16 KB before summarization), calls `summarizeFile`.
   - Embeds the augmented summary: `"project=<name> file=<relPath> · <summary>"`.
   - Upserts via `IndexedFiles.Upsert(...)`.
3. After the loop, calls `Projects.RecountFiles(project)`.
4. Calls `generateProjectDescription` — asks the summary model to write a 2–3 sentence blurb from up to 200 file summaries.
5. Reports final stats.

**Embedding model selection:** `learn` uses a separate embedding space from prose tables (memories, facts, summaries). At startup it calls `db.ResolveCodeEmbedModel()` which reads `embed_model_code` first, falling back to `embed_model` when the code key is unset. This lets a code-specialized model (e.g. `manutic/nomic-embed-code:7b-q8_0`) be used for `indexed_files` + `indexed_chunks` without disturbing the prose embedding space. When `embed_model_code` is changed, run `cairo learn --reembed` on each project to migrate all code embeddings to the new space.

**Summary model calls** use Ollama's structured-output mode (`ChatOptions.Format` = `fileSummarySchema`). The schema pins the wire shape to `{"summary": "..."}` so the model can't return an essay. Hard truncation at 600 characters is applied after parsing as a belt-and-suspenders guard.

**Project description** follows the same schema pattern with `projectDescSchema` (`{"description": "..."}`), capped at 800 characters.

**Progress reporting:** `cfg.ProgressFn` (if set) is called after each file. When `cfg.TaskID > 0`, the indexer also calls `DB.Tasks.SetProgress(taskID, current, total, label, detail)` so the TUI progress bar and threads panel update in real time.

**Cancellation:** `ctx.Err()` is checked between files. Cancellation mid-run returns the partial stats and the context error — already-indexed files are kept.

### `spawn.go` — background subprocess

`SpawnBackground(req SpawnRequest, sysProc func() *syscall.SysProcAttr) (*SpawnResult, error)` creates the infrastructure for a background indexing run:

1. Creates a `jobs` row (`learn <project>`) and a `tasks` row (`index <project>`).
2. Sets the task to `status=running`.
3. Creates a log file at `~/.cairo/logs/task_<id>.log`.
4. Launches `cairo learn -task <id> -background -path <root> -project <name>` as a detached subprocess.
5. Releases the process (fire-and-forget) and returns `SpawnResult{TaskID, JobID, PID, LogPath}`.

The subprocess runs `learn.Run()` with the task ID wired in, updating progress via `SetProgress`. When done, it calls `Tasks.SetStatusAndResult` and exits.

The `sysProc` function pointer is supplied by `spawn_unix.go` (sets `Setpgid: true` for process group isolation) or `nil` for callers that don't need it. This keeps the cross-platform build clean.

---

## Tool: `internal/tools/learn.go`

The `learnTool` struct wraps the `learn` package and implements the `agent.Tool` interface. It dispatches on the `action` argument:

| Action | Required args | Optional args | What it does |
|---|---|---|---|
| `add` | `path` | `project`, `summary_model` | Calls `learn.SpawnBackground`; returns task ID and PID |
| `search` | `project`, `query` | `limit` (default 10) | Embeds query, calls `IndexedFiles.SearchSummaries` |
| `list` | — | — | Calls `Projects.List()`, sorted by last_updated desc |
| `describe` | `project` | — | Calls `Projects.Get(project)`, returns description + metadata |
| `forget` | `project` | — | Calls `Projects.Delete(project)` (cascades to indexed_files) |
| `status` | — | `project` (filter) | Calls `Tasks.RunningWithProgress()`, filters by label |

`doSearch` requires `embed_model` to be configured — returns an error if `EmbedClient.Embedder` is nil or `Model` is empty.

---

## Slash command: `/learn [path]`

Registered in `internal/tui/commands.go` as a `Command` with `HandlerWithArgs`. The handler:

1. Calls `resolveLearnPath(arg, m.session.CWD)` — expands `~`, makes relative paths absolute, validates the result is an existing directory.
2. Infers `project` as `filepath.Base(root)`.
3. Calls `learn.SpawnBackground(...)`.
4. Shows a success toast: `"learning <project> — task N (Ctrl+T to watch)"`.

If no path is given, defaults to the session's working directory.

---

## System prompt integration

When any projects exist in the DB, `BuildSystemPrompt` calls `appendIndexedProjects` (in `internal/agent/prompt.go`), which writes:

```
## Indexed projects

These projects have been mapped via `cairo learn`. Search any of them with
`learn(action="search", project="<name>", query="...")` — it's the right tool
for codebase / file-location questions.

- **cairo** (312 files) — A local-first AI coding harness in Go.
- **myproject** (48 files) — (no description yet)
```

The `## Where to look for things` base prompt part (`search_protocol` key) reinforces this: "For codebase questions, search learn FIRST — before memory or notes."
