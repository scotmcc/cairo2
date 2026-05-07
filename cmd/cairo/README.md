# cmd/cairo

**Layer:** Composition Root  
**Status:** ✅ working → being decomposed (Phase 2)

The agent binary. Standalone coding agent with TUI, HTTP API, and fleet enrollment.

Deploys anywhere: a developer's laptop, a CI box, a DoD enclave. Runs fully offline with a local LLM. When `cairo serve --tsnet` is used, the instance enrolls in the fleet and becomes addressable by the enterprise control plane.

## Files (target state after Phase 2)

- `main.go` — ~80 lines: signal handling + subcommand dispatch
- `app.go` — `App` struct + `newApp()` — composition root (the Go equivalent of `Program.cs`)
- `surfaces.go` — `runTUI`, `runCLI`, `runVSCode`, `runOneShot`
- `cmd_serve.go` — `cairo serve --tsnet / --auth`
- `cmd_learn.go`, `cmd_dream.go`, `cmd_config.go`, `cmd_export.go`, `cmd_import.go`, `cmd_diff.go`, `cmd_task.go`
- `wizard.go` — first-run setup

## Source

Migrates from `~/cairo/cmd/cairo/`.
