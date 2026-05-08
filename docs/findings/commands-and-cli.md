# Commands & CLI — Findings

**Reviewed:** internal/commands/, internal/cli/, cmd/cairo/
**Date:** 2026-05-02
**Counts:** major: 0, medium: 1, small: 2

## Summary

The command dispatch architecture is clean at the registry level, but the CLI implementation diverges from the shared design: /init handler is duplicated in cli_command_env.go instead of using NewInitCommand, creating SRP and maintenance risk. Two functions exceed 100 lines (handleCommand, handleVSCodeCommand) due to accumulated case branches — acceptable for dispatch/formatting but trending toward bloat.

## Findings

### [medium] CLI /init handler duplicates shared command instead of using NewInitCommand
- **Where:** `internal/cli/cli_command_env.go:56-65`
- **What:** The /init command is registered inline with a custom handler that hardcodes init_complete logic, instead of using the shared `NewInitCommand` factory from `internal/commands/commands.go:19`. The TUI correctly uses `NewInitCommand` (via `sharedInitCmd` in commands.go:270), but the CLI reinvents the handler locally.
- **Why it matters:** Duplication violates DRY and SRP. The init_complete=true behavior (line 62) is correct but not shared with TUI, increasing maintenance burden. If the init flow needs to change, both implementations must be updated.
- **Action:** Refactor CLI registry to use `NewInitCommand(buildInitPrompt)` like the TUI does, moving CLI-specific init_complete logic into the shared factory or a wrapper to preserve the deterministic-set behavior for small models.

### [small] handleCommand function in CLI is 100 lines — trending toward bloat
- **Where:** `internal/cli/cli.go:85-185`
- **What:** The command dispatcher is a 100-line switch statement handling /session, /sessions, /jobs, /memories, /tools, /skills. Each case follows the same pattern: fetch data, check error, format output. No logic bugs, but repeated structure.
- **Why it matters:** Trending issue, not yet broken. The pattern scales poorly — each new command adds ~15-20 lines. At ~5-6 more commands, this hits 150+ and readability drops.
- **Action:** Consider factoring the fetch-format-display pattern into a helper, or move each case into a subcommand handler function (same pattern used in cmd/cairo/). Watch this on next review.

### [small] handleVSCodeCommand function exceeds 100 lines
- **Where:** `internal/cli/vscode.go:156-280`
- **What:** The VSCode command dispatcher is 125 lines with similar structure to handleCommand — switch cases that fetch and format data.
- **Why it matters:** Same trending issue as handleCommand. The duplicated pattern (both in cli.go and vscode.go) is a signal to extract a reusable helper.
- **Action:** Track alongside handleCommand. If either grows further, refactor both into a shared `outputListCommand(name, formatter func)` helper.
