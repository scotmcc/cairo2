# Agent + LLM — Findings

**Reviewed:** internal/agent/, internal/llm/
**Date:** 2026-05-02
**Counts:** major: 1, medium: 4, small: 1

## Summary
The agent loop and LLM client are architecturally sound with clear separation of concerns. One major consistency issue exists around message history synchronization that could cause silent bugs during tool call execution. The AgentDB interface exists but is not adopted, remaining a partial migration. Several medium-weight violations of SRP and error-handling discipline exist at boundaries.

## Findings

### [major] Message history out-of-sync with LLM send-list during tool execution
- **Where:** `internal/agent/loop.go:254–324` (inner loop, tool execution)
- **What:** When tool calls are executed, `sendMsgs` (sent to LLM) is updated inline (line 266, 317) but `msgs` (in-memory conversation history) is NOT updated until after the inner loop exits (line 360). Between tool results arriving and the next LLM call, the two lists diverge. This creates a window where if an error occurs mid-tool-execution or if the loop is interrupted, `a.history` used by `buildSystemPrompt()` on the next turn reflects an older state than what the model should see.
- **Why it matters:** The system prompt is rebuilt at the start of every outer iteration and relies on `a.history` being consistent with the message sequence the model saw. If history lags, the prompt will embed stale summaries/memories keyed to a different message index than the model knows, causing context misalignment in multi-turn recovery scenarios.
- **Action:** Unify message list updates so `msgs` and `sendMsgs` stay in lock-step. After each tool result is appended to `sendMsgs` (line 317), immediately append the same to `msgs` to avoid the divergence window.

### [medium] ApplyTurnSignals returns error but call site ignores it with `_ = err`
- **Where:** `internal/agent/loop.go:365–367`
- **What:** `ApplyTurnSignals()` can return a wrapped error, but the loop handles it by discarding it (`_ = err`). The wrapper function `ApplyTurnSignals` always returns nil anyway (calls inner `applyTurnSignals`, wraps error, but then returns nil—see `state_apply.go:119–122`), making the signature misleading.
- **Why it matters:** The return type suggests errors are expected and should be handled, but the implementation guarantees nil. This is a false contract that confuses callers and masks the real issue: `applyState()` logs errors internally but never surfaces them, so `ApplyTurnSignals` returning error is dead.
- **Action:** Either make `ApplyTurnSignals` return `(error)` with actual propagation (log in the loop, don't swallow), or change the signature to `(error)` but always return nil and document why. Current state is inconsistent.

### [medium] AgentDB interface defined but adoption is incomplete and inconsistent
- **Where:** `internal/agent/db_interfaces.go` (definition) vs. call sites throughout `agent/` (still using `*db.DB`)
- **What:** CLAUDE.md notes the partial migration is intentional. However, 33 function signatures in the package still take `*db.DB` directly (grep count). `NewAgentDB()` exists but is not called anywhere. The interface boundaries are not enforced, so tests and new code default to the full `*db.DB` handle.
- **Why it matters:** The migration is a documented landmine but creates inconsistency: `Summarize()`, `ApplyToolResult()`, etc. accept `*db.DB`, while tests would benefit from injecting narrow fakes. Future contributors won't know which pattern to follow.
- **Action:** Adopt AgentDB consistently in new functions (e.g., the summarizer should accept `AgentDB` instead of `*db.DB`). For existing functions, add a comment marker (e.g., `// TODO: migrate to AgentDB`) so intent is visible. This unblocks incremental migration without forcing a rewrite.

### [medium] Error handling at summarizer boundary: silent failures on embed and fact storage
- **Where:** `internal/agent/summarizer.go:244–252`
- **What:** Embedding errors for facts and the summary itself are logged but don't stop fact storage or fail the summarize operation. Line 252 calls `database.Facts.Add()` without checking the error. If embedding fails, facts are stored with nil embeddings silently. Line 220 embeds the summary; if it fails, the summary is stored unembedded without any signal.
- **Why it matters:** Silent partial failures create stale or degraded data (facts with no embeddings won't be retrieved). The summarizer's contract is "summarize and embed or fail cleanly;" splitting that contract here means subsequent memory searches will miss facts that should have been embedded.
- **Action:** Return an error from `summarizeImpl` if summary embedding fails (not just log). For facts, either skip storing a fact if its embedding fails, or store a sentinel that marks it as unembedded and log a warning. Explicit failure mode >> silent degradation.

### [medium] ConsiderFn callback parameter in BuildSystemPrompt is dead code path
- **Where:** `internal/agent/prompt.go:48–98`
- **What:** `BuildSystemPrompt` accepts a `considerFn` parameter (line 76) as an optional callback to inject inner-dialogue. The function signature includes it, but in production agent code (`agent.go:395`), `nil` is always passed. The `appendInnerDialogue()` function is called only when `considerFn != nil` (line 94), which is never true in production. The actual inner-dialogue step now runs in `agent.Prompt()` (line 204) and stores the result on the message via `AddWithInnerVoice()`. The callback in BuildSystemPrompt is a vestigial legacy path.
- **Why it matters:** Dead parameter increases signature complexity and confusion. Tests might exercise the callback path, but production never uses it. New contributors might assume it's how to add inner dialogue, missing the fact that it's done earlier now.
- **Action:** Remove the `considerFn` parameter and `appendInnerDialogue()` function. Inner dialogue already flows through `wrapUserMessage()` (line 89). Simplify the signature.

### [small] ToolCall.Args() in types.go calls unexported normalizeArgs but is in a different package
- **Where:** `internal/llm/types.go:30`
- **What:** `ToolCall.Args()` method calls `normalizeArgs()` (unexported), which is defined in the same package (`args.go`). This works and is fine, but there's a pattern to watch: if `normalizeArgs()` ever grows multiple call sites outside `llm/` (e.g., in tools or agent), it should be exported. Currently it's only used internally.
- **Why it matters:** Not a bug today, but the function handles a subtle dual-format normalization (map vs. JSON string) that other packages might need. If duplicated elsewhere, it becomes a DRY violation.
- **Action:** No action needed now. If a second call site in a different package appears, export `normalizeArgs` and document the why.

