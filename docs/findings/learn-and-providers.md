# Learn, Providers, HostEdit — Findings

**Reviewed:** internal/learn/, internal/providers/, internal/hostedit/
**Date:** 2026-05-02
**Counts:** major: 1, medium: 2, small: 2

## Summary

Walker excludes `.cairo/` but not `.claude/worktrees/` — both are at the project root and `.claude/worktrees/` is a git-worktree landmine that must not be indexed. Silent error suppression in indexer masks stale-file and progress update failures. Providers interface is clean; hostedit shell injection handling is safe.

## Findings

### [major] Walker does not exclude `.claude/worktrees/` directory

- **Where:** `internal/learn/walk.go:31-37`
- **What:** `builtinExcludes` includes `.cairo` but not `.claude`. The CLAUDE.md file explicitly states `.claude/worktrees/` is a landmine containing git worktrees that must not be read as source — they are "use `git worktree prune`, not `rm -rf`" sandbox directories. The matcher (line 242-271) checks each path component against `dirNames` map; a path like `.claude/worktrees/foo/bar` will not match because only the basename `.claude` is checked, not the presence of `worktrees/` after it.
- **Why it matters:** If a user runs `cairo learn` on a project where `.claude/worktrees/` exists (e.g., after using `/worktree` from the agent), stale or incomplete worktree branches will be indexed as project source, polluting embeddings and retrieval. Worktrees contain commits not on main; indexing them violates the single-project contract.
- **Action:** Add `".claude"` to `builtinExcludes` (line 31) alongside `.cairo`. The `.claude/` directory is per-user (global conventions per CLAUDE.md line 5) and should never be indexed as project source.

### [medium] Silent error suppression in indexer progress and stale-file deletion

- **Where:** `internal/learn/indexer.go:201`, `203`, `208`, `214`
- **What:** Four DB operations use `_ =` to suppress errors: stale-file deletion (line 201-203), project recount (line 208), and description update (line 214). Lines 201 and 203 even create fmt errors/strings that are immediately discarded — dead code. These are non-fatal operations, but silent suppression prevents visibility into why a project's metadata is stale.
- **Why it matters:** A user sees "indexed X files" but doesn't learn that stale-file cleanup failed due to a DB lock, or the file count was never recomputed. Debugging "why is my old file still showing up" becomes harder.
- **Action:** Replace `_ = fmt.Errorf(...)` with an actual log call (if one exists in the codebase) or store errors in a notes slice appended to stats. At minimum, surface stale-deletion errors as warnings in progress callbacks.

### [medium] Chunk embedding loop has no early-stop after first error

- **Where:** `internal/learn/indexer.go:309-325` (indexChunks loop)
- **What:** When embedding a chunk fails (line 312), the entire indexChunks call returns an error, but that error is silently discarded by indexOne (line 287: `_ = err`). If the 50th chunk of a 100-chunk file fails to embed, all 49 prior chunks are stored but the file is marked as fully indexed — the partial failure is invisible.
- **Why it matters:** A power loss, network glitch, or rate-limit during chunk embedding leaves dangling rows and incomplete metadata. The next run will see the same SHA256 and skip the file entirely (line 244: `if prev != "" && prev == sha`), orphaning the partial chunks forever.
- **Action:** Track chunk embed success/failure separately from file indexing. Either: (a) wrap indexChunks errors so they don't fail the file upsert but do mark the file as needing re-chunk on the next run (add a `chunks_last_error` timestamp), or (b) move chunk embedding into a transaction with file upsert so failure rolls back both.

### [small] Unused format strings in error suppression

- **Where:** `internal/learn/indexer.go:201`, `203`
- **What:** Two lines create error or string values that are discarded: `_ = fmt.Errorf(...)` and `_ = fmt.Sprintf(...)`. These look like debugging leftovers where logging was intended.
- **Why it matters:** Code noise; suggests incomplete error-handling strategy.
- **Action:** Remove lines 201-203 or replace with a proper logging call if one exists in the project.

### [small] Shell-space handling in hostedit fallback path

- **Where:** `internal/hostedit/hostedit.go:143-153`
- **What:** The fallback path uses `strings.Fields(editor)` to split `$EDITOR` on whitespace. If a user's `$EDITOR` is `"nano --restricted"`, the split works correctly. But if `$EDITOR` contains a quoted path like `"/path with spaces/code" --wait`, it will break into 3 tokens instead of 2.
- **Why it matters:** Edge case, but a user with spaces in their editor path will see a confusing error.
- **Action:** Optional — this is rare. If addressed, use `shlex.Split()` (or similar quoted-string parser) instead of `strings.Fields()`. Or document that $EDITOR must not contain spaces in the first token.

