# Cairo Code Review — Findings Index

**Date:** 2026-05-02
**Method:** Seven parallel Explore subagents, one per area, read-only. No code changes.
**Severity rubric:**
- **major** — breaking: bug, race, data loss, security issue, broken abstraction, doc that actively misleads
- **medium** — doesn't work as designed: SRP violation, overlong file/function, duplicated logic across ≥2 sites, partial migration, leaky abstraction
- **small** — nit; not an issue today, will become one if built on

## Totals

| Area | Major | Medium | Small | File |
|---|---:|---:|---:|---|
| Agent + LLM | 1 | 4 | 1 | [agent-and-llm.md](agent-and-llm.md) |
| DB & Schema | 0 | 1 | 1 | [db-and-schema.md](db-and-schema.md) |
| TUI | 2 | 2 | 1 | [tui.md](tui.md) |
| Tools Registry | 2 | 7 | 0 | [tools-registry.md](tools-registry.md) |
| Commands & CLI | 0 | 1 | 2 | [commands-and-cli.md](commands-and-cli.md) |
| Learn / Providers / HostEdit | 1 | 2 | 2 | [learn-and-providers.md](learn-and-providers.md) |
| Docs | 3 | 2 | 1 | [docs-review.md](docs-review.md) |
| **Total** | **9** | **19** | **8** | |

## Area TL;DRs

### Agent + LLM — [agent-and-llm.md](agent-and-llm.md)
Agent loop has a real bug: message history (`msgs`) and the per-call LLM context (`sendMsgs`) drift apart during tool execution, which can corrupt subsequent turns. Other issues are SRP/error-handling: inconsistent error handling in `ApplyTurnSignals`, the `AgentDB` interface migration is partially adopted (CLAUDE.md says intentional — confirmed), summarizer silently swallows embedding failures, and `ConsiderFn` is dead. Prompt-assembly order is intact.

### DB & Schema — [db-and-schema.md](db-and-schema.md)
Cleanest area. Memory-scoring doctrine is intact at `internal/db/memories.go:224` (still `cosine * decayImportance(importance)`, not multiplicative). Migrations and seeds are in parity, transaction boundaries are present, retired tables (`code_index`) are properly dropped. Only real finding: `memories(embed_model)` is unindexed and used on a hot path. `schema.go` is 1568 lines, but that's a single growing migration log — flagged small, not urgent.

### TUI — [tui.md](tui.md)
Two structural majors. (1) `renderTranscriptWithSides()` at `tui_view.go:262-263` mutates viewport state inside View() — violates Bubble Tea's purity contract, can cause layout flicker on resize. (2) Many panels register **bare single-letter keys** (a, r, d, x, g, G) which directly violates the project's documented ctrl+ hotkey policy and re-introduces a previously-fixed input-stealing bug. The toggle-key validator only guards toggle keys, not internal panel handlers.

### Tools Registry — [tools-registry.md](tools-registry.md)
Registry pattern itself is clean and consistent. Issues are at the implementation level. Two majors: `merge_job.go` `doApprove` is 163 lines (testability + SRP), and `fetch.go:120-132` spawns a background goroutine that ignores context cancellation (data-corruption risk at shutdown). Mediums are mostly oversized `Execute` functions and a duplicated `resolvePath()` across read/write/edit. `sanitizeExternalContent()` swallows errors silently — worth tightening.

### Commands & CLI — [commands-and-cli.md](commands-and-cli.md)
No major bugs. The shared command registry in `commands.go` is the right design; the issue is the CLI bypasses it for `/init` (`cli_command_env.go:56-65`) instead of using `NewInitCommand`. `handleCommand` (`cli.go:85-185`, 100 lines) and `handleVSCodeCommand` (`vscode.go:156-280`, 125 lines) are trending toward the same fetch-format-display duplication. Note: agent did not surface findings on the `/init`-on-resume regression or the shutdown-hang regression flagged in memory — those may live elsewhere or need a focused follow-up.

### Learn / Providers / HostEdit — [learn-and-providers.md](learn-and-providers.md)
One real bug: walker excludes `.cairo/` but **not `.claude/worktrees/`** — directly contradicts the CLAUDE.md landmine warning. Worktrees will get indexed as source. Mediums: silent error suppression (`_ =`) in indexer hides stale-file deletion + progress failures; `indexOne` discards chunk-embedding errors mid-file, leaving orphaned chunks. Provider interface is consistent; hostedit `exec.Command` usage is safe; subprocess lifecycle is correct.

### Docs — [docs-review.md](docs-review.md)
Three majors are link/version drift, not content issues. (1) Eight files reference `../guides/` which doesn't exist — files are in `../development/`. (2) CLAUDE.md says current release is v0.2.0 but docs document v0.3.0 features extensively — pick one source of truth. (3) ROADMAP.md uses `docs/`-prefixed links that break when read from inside `docs/`. Mediums: redundant identity/memory/sessions docs across `concepts/` and `ai/`; large files lack TOCs.

## Cross-cutting themes

- **Silent error suppression** appears in three areas (agent summarizer, learn indexer, tools `sanitizeExternalContent`). Worth a uniform stance: errors at boundaries either surface or get logged with structure — not `_ =`.
- **Function/file size** is the most common medium. Repeated pattern: a top-level `Execute`/handler that mixes parsing + I/O + formatting (~80–160 lines). Consistent split into parse/do/format helpers would address several findings at once.
- **Partial migrations**: `AgentDB` interface, CLI `/init` not using shared factory, embed_model column unindexed. None broken; all friction.
- **Hotkey discipline** is policy in CLAUDE.md but not enforced uniformly — the toggle-key validator only covers toggles.

## Recommended triage order (suggestion, not a plan)

1. Agent loop msg/sendMsgs divergence (correctness, hard to debug if it bites)
2. Walker missing `.claude/worktrees/` exclusion (active landmine, easy fix)
3. TUI View() mutation + bare-key hotkeys (correctness + UX regression)
4. `fetch.go` goroutine ignoring context (shutdown data-corruption risk)
5. Docs: broken `../guides/` links + version drift (cheap, high signal for new contributors/agents)
6. Then mediums by package as you touch them.
