# Tools Registry — Findings

**Reviewed:** internal/tools/
**Date:** 2026-05-02
**Counts:** major: 2, medium: 7, small: 0

## Summary

The tools registry is well-structured with consistent patterns (DispatchAction, checkDiscipline, DRY helpers). Registry and path validation are sound. However, two functions significantly exceed the 60-line target, and several others approach it. The `doApprove` function in merge_job is a critical breaking violation at 163 lines.

## Findings

### [major] Function size violation: merge_job doApprove
- **Where:** `internal/tools/merge_job.go:80–243`
- **What:** `doApprove` spans 163 lines, far exceeding the ~60-line target. Combines job loading, worktree validation, rebase orchestration, squash-merge, commit, push, and status updates in a single monolithic function.
- **Why it matters:** Violates SRP. Makes the function hard to test, reason about, and modify. Any of the 8+ distinct steps (verify-commits, rebase, squash-merge, commit, push, cleanup, status-set, artifact-record) could be extracted.
- **Action:** Extract steps into helpers: `verifyCommits()`, `rebaseWorktree()`, `squashMerge()`, `commitChanges()`, `pushBranch()`. Reduce `doApprove` to ~50 lines orchestrating these calls.

### [major] Possible race condition: fetch ingestAsync goroutine lifecycle
- **Where:** `internal/tools/fetch.go:120–132`
- **What:** `ingestAsync` fires a background goroutine with `go func()` inside `Execute`. No synchronization with tool context cancellation; if the session ends mid-ingest, the goroutine may access a closed database or deleted temp files.
- **Why it matters:** Background work that doesn't respect context cancellation can cause panics or data corruption at shutdown. CLAUDE.md documents this pattern risk ("`cairo learn` is a background subprocess").
- **Action:** Trap `ctx.Ctx` in the goroutine; return early if `ctx.Ctx.Done()` fires. Or use a bounded queue pattern to decouple the fetch result from ingest scheduling.

### [medium] Function size: memory_tool doSearch
- **Where:** `internal/tools/memory_tool.go:171–258`
- **What:** `doSearch` is 87 lines. Handles scope parsing, embedding, dispatching to 3 search variants, result merging, weight bumping, and formatting in one function.
- **Why it matters:** Violates SRP; mixes orchestration (scope parse, dispatch) with result handling. Hard to test edge cases (missing embed model, scope validation, dedup logic).
- **Action:** Extract scope parsing to `parseScope()`, search dispatch to `searchByScope()`. Reduce `doSearch` to ~40 lines.

### [medium] Function size: bash Execute
- **Where:** `internal/tools/bash.go:35–106`
- **What:** `Execute` is 71 lines. Handles timeout parsing, process group setup, output capture, truncation, and error classification in one function.
- **Why it matters:** Hard to test timeout vs. cancellation vs. exit-code logic independently. Output truncation logic mixes with command execution.
- **Action:** Extract `configureProcAttr()`, `captureAndTruncate()`. Reduce to ~50 lines.

### [medium] Function size: search Execute
- **Where:** `internal/tools/search.go:38–126`
- **What:** `Execute` is 88 lines. Handles URL resolution, HTTP request, JSON parsing, result formatting, truncation, and sanitization.
- **Why it matters:** Mixing HTTP orchestration with output formatting makes unit testing (mock HTTP, test truncation separately) cumbersome.
- **Action:** Extract `buildSearchURL()`, `executeSearch()`, `formatResults()`. Reduce to ~40 lines.

### [medium] Function size: custom_tool_runtime Execute
- **Where:** `internal/tools/custom_tool_runtime.go:51–116`
- **What:** `Execute` is 65 lines. Builds environment (merging PATH, HOME, TMPDIR, CAIRO_ARG_*, CAIRO_ARGS, safe_env_extras), switches on impl type, and runs subprocess.
- **Why it matters:** Complex environment construction + subprocess dispatch makes it hard to test environment variable handling independently. No extraction of environment-building logic.
- **Action:** Extract `buildEnvironment()` to a testable function. Reduce `Execute` to ~30 lines orchestrating env build + spawn.

### [medium] Duplicated path resolution logic
- **Where:** `internal/tools/read.go:74–78`, `internal/tools/write.go:35`, `internal/tools/edit.go:36`
- **What:** Three tools independently call `resolvePath()` (defined in read.go). Edit.go and write.go reuse it, but the pattern is fragile — any change to read.go's definition requires audit elsewhere.
- **Why it matters:** SRP violation across files. If read.go is refactored, callers in write.go and edit.go might silently break.
- **Action:** Move `resolvePath()` to `tool.go` as a shared helper (alongside `strArg`, `intArg`, etc.). Update imports in read.go, write.go, edit.go.

### [medium] Inconsistent error envelope in sanitizeExternalContent
- **Where:** `internal/tools/fetch.go:136–162`, `internal/tools/search.go:123`
- **What:** `sanitizeExternalContent` returns a string; errors are swallowed (no logging). If a sanitization bug occurs, caller has no way to detect it. Result is silently corrupted.
- **Why it matters:** Silent failure. The tool result appears normal but contains injected patterns that sanitization should have stripped. Agent may be confused or exploited.
- **Action:** Return `(string, error)`. Log injection patterns found. Bubble errors to caller so tool result can signal sanitization failure.

### [small] Function trending toward duplication: search result building
- **Where:** `internal/tools/skill.go:290–298`, `internal/tools/search.go:112–115`, `internal/tools/learn.go` (mergeLearnResults)
- **What:** Three tools format search results via `strings.Builder` loop. Similar pattern: `fmt.Fprintf(&b, format, ...)` for each result, trim space, return as Content + Details.
- **Why it matters:** Trending pattern duplication. Once ≥3 tools share the same formatting, extract to shared helper to reduce drift.
- **Action:** Create `formatResultsList(results []string, titles []string) string` helper in tool.go. Adopt across skill, search, learn.

