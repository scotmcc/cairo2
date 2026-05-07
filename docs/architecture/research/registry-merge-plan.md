# Registry merge plan

## 1. Module paths today

| Repo | `go.mod` module path |
|---|---|
| `~/cairo` | `github.com/scotmcc/cairo` |
| `~/cairo-registry` | `github.com/scotmcc/cairo-registry` |

`go 1.25.0` in both. Dep sets are nearly identical (`tailscale.com v1.86.0`, `coder/websocket`, `modernc.org/sqlite v1.49.1`, `google/uuid`, AWS SDK v2 family pulled in by tsnet). The registry's go.mod is a strict subset of cairo's.

## 2. Type duplication

`internal/protocol/registry.go` is **byte-identical** between the two repos:

- `~/cairo/internal/protocol/registry.go` (28 lines)
- `~/cairo-registry/internal/protocol/registry.go` (28 lines)

Both define `RegisterRequest`, `RegisterResponse`, `Frame`. Both contain a comment: `// Copy-pasted in cairo and cairo-registry — see README sync ritual.` This is the documented drift hazard; verified to be in sync today, but the comment exists because it has drifted in the past.

No other types duplicate — the registry's `Ledger`, `Server`, `wsHandler`, `Sweeper` are server-only; the cairo-side's `Register`/`Stream`/`HeartbeatLoop`/`LivenessStream` are client-only.

## 3. Why two registry-client packages exist

| Package | File | Public API | Notes |
|---|---|---|---|
| `internal/registry/` | `client.go` (141 lines) | `Register`, `HeartbeatLoop`, `LivenessStream` | **Defines its own private `registerRequest`/`registerResponse`/`frame` structs** — does not import `internal/protocol`. Used by `cmd/cairo/main.go` (`registry.Register`, `registry.HeartbeatLoop`, `registry.LivenessStream`). Driven by config keys (`registry_url`, `registry_agent_id`). |
| `internal/registry-client/` | `client.go` (136 lines) | `Register`, `Stream` | **Imports `github.com/scotmcc/cairo/internal/protocol`** for the wire types. Has jittered exponential backoff. Used by `cairo serve --register URL`. |

Both implement the same wire protocol; the duplication is incidental. Reading `client.go` for both, `registry-client/` is the better-engineered version (uses `protocol` package, has backoff with jitter, has tests). `registry/` is older code that was never updated to use the shared types.

**This is an accident, not intent.** It is the merge-conflict artifact the briefing flags. The simplest unification: delete `internal/registry/`, port its two callsites in `main.go` to use `internal/registry-client/` (which already accepts the same args), then rename to a single canonical name.

## 4. Concrete merge steps

These steps are ordered so each step compiles and tests cleanly. Each step is a separate commit.

### Step A — Land cairo-registry source into cairo (no behavior change)

1. From `~/cairo`, copy `~/cairo-registry/cmd/cairo-registry/main.go` to `~/cairo/cmd/cairo-registry/main.go`.
2. Copy `~/cairo-registry/cmd/cairo-ctl/main.go` to `~/cairo/cmd/cairo-ctl/main.go`.
3. Copy `~/cairo-registry/internal/registry/*.go` to a NEW dir `~/cairo/internal/registryserver/` (renamed to disambiguate from existing `internal/registry/` client; see Step C for the eventual rename).
4. **Do NOT copy** `~/cairo-registry/internal/protocol/registry.go` — the cairo repo already has it at `internal/protocol/registry.go` (identical content).
5. Update import paths in copied files:
   - `github.com/scotmcc/cairo-registry/internal/registry` → `github.com/scotmcc/cairo/internal/registryserver`
   - `github.com/scotmcc/cairo-registry/internal/protocol` → `github.com/scotmcc/cairo/internal/protocol`
6. `go.mod`: no edits needed for cairo (the new code's deps are a subset of cairo's existing deps).
7. Build all binaries: `go build ./cmd/...` — verify `cmd/cairo`, `cmd/cairo-registry`, `cmd/cairo-ctl` all compile.
8. Run cairo's existing tests: `go test ./...` — verify nothing regresses.

**Success test:** `go build ./cmd/cairo-registry && ./cmd/cairo-registry --no-tsnet --addr :18000` brings up the registry on port 18000 with a fresh `~/.cairo-registry/registry.db`. `curl localhost:18000/healthz` returns 200.

### Step B — Wire up scripts/packaging for the new binaries

1. Update `scripts/build.sh` to also build `cmd/cairo-registry` and `cmd/cairo-ctl`.
2. Update packaging (`scripts/packaging/build-packages.sh`) to include these binaries in the rpm/deb.
3. Update `scripts/smoke/registry-client.sh` — currently references `~/cairo-registry/`; change to in-tree paths.

**Success test:** `bash scripts/build.sh` produces `bin/cairo`, `bin/cairo-registry`, `bin/cairo-ctl`. `bash scripts/smoke/registry-client.sh` passes against the new build.

### Step C — Consolidate the two client packages

1. In `internal/registry-client/`, ensure all callsites in cairo can be served. Currently:
   - `cmd/cairo/main.go` imports `internal/registry` and calls `registry.Register`, `registry.HeartbeatLoop`, `registry.LivenessStream`.
   - `internal/server/` (or `cmd/cairo serve` path) imports `internal/registry-client` for `Register`, `Stream`.
2. Add a `HeartbeatLoop` function to `internal/registry-client/` (port from `internal/registry/client.go`). Use the `protocol` package types.
3. Rename: move `internal/registry-client/` → `internal/registry/` after deleting the old `internal/registry/`. There are two ways:
   - **(a) Atomic:** delete `internal/registry/`, rename `internal/registry-client/` to `internal/registry/`, update all imports in one commit.
   - **(b) Two-step:** in commit 1 — delete `internal/registry/client.go` (the old one), update `cmd/cairo/main.go` to import `internal/registry-client` instead. In commit 2 — rename the surviving package to `internal/registry/`.
   - Recommend (b); it's safer and each commit compiles.
4. Update `cmd/cairo/main.go` lines 27, 309, 323, 325 to use the new package and the new function signatures (Register/HeartbeatLoop/LivenessStream → Register/HeartbeatLoop/Stream from the consolidated client).

**Success test:**
- `go test ./internal/registry/...` passes.
- `bash scripts/smoke/registry-client.sh` still passes end-to-end.
- The `registry_url` config key still drives heartbeat + WS liveness identically.

### Step D — Delete the old cairo-registry repo (or archive it)

After A–C all land and have shipped at least one release:
1. Tag `~/cairo-registry` `v0.x.y-final` and push.
2. Add a README note: "Merged into github.com/scotmcc/cairo at <commit>. Use that repo going forward."
3. Optionally archive on GitHub.

**Do not delete the old repo until after at least one cairo release is in production using the merged code.** This is the rollback path.

## 5. Single source of truth after the merge

```
~/cairo/
  cmd/
    cairo/             — main binary (TUI, CLI, serve, etc.)
    cairo-registry/    — registry server binary  (NEW, from merge)
    cairo-ctl/         — operator CLI            (NEW, from merge)
  internal/
    protocol/
      registry.go      — Register/Response/Frame wire types  (SINGLE SOURCE OF TRUTH)
    registry/          — registry CLIENT (consolidated; the merged registry-client/)
                         exports Register, HeartbeatLoop, Stream
                         imports internal/protocol
    registryserver/    — registry SERVER (Ledger, Server, wsHandler, Sweeper, Admin, Tsnet)
                         imports internal/protocol
                         consumed only by cmd/cairo-registry/main.go
```

Why `registry/` and `registryserver/` are separate packages (not one `registry/`):
- `cmd/cairo/main.go` imports the client. It must NOT pull in `Ledger`, `tsnet.Server` server-side bits, or the SQLite ledger schema. Keeping the server in a sibling package keeps cairo's binary slim and the dep graph clean.
- The server package is only ever imported by `cmd/cairo-registry/main.go`.
- A future shared piece (e.g. server-side validation that the client also wants) could live in `internal/protocol/`.

## 6. Risk surface

| Risk | Mitigation |
|---|---|
| Import path churn breaks downstream forks/builds | None known to exist. cairo-registry has 1 consumer (cairo). Low risk. |
| `go.sum` rewrite causes vendoring drift | Single-step `go mod tidy` after Step A. Verify `go.sum` diff is reasonable. |
| Smoke test (`scripts/smoke/registry-client.sh`) hard-codes paths to `~/cairo-registry/` | Updated in Step B. Catch in CI. |
| Two registry packages with similar names (`registry/` vs `registryserver/`) confuses contributors | Top-of-file package doc comments make the boundary explicit. README diagram. |
| `internal/registry/` currently has tests; new merged package may not have parity coverage | Carry over `internal/registry-client/client_test.go` (already exists, 88 lines) and any tests from old `internal/registry/`. Not a regression — old `registry/` had no tests in tree. |
| `cairo-ctl` may use registry-server-internal helpers that aren't exported | Audit `cmd/cairo-ctl/main.go` after Step A; export anything it transitively needs. |
| Workspace state files (`~/.cairo-registry/`) are unaffected | The new `cmd/cairo-registry` still writes to `~/.cairo-registry/registry.db`. Operators upgrading don't lose state. |
| Single combined `go.mod` pulls more deps when downstream consumers `go get` cairo as a library | Cairo isn't consumed as a library. No risk. |
| Premerge changes in `cairo-registry` after merge starts | Freeze `cairo-registry` at the merge commit; any new work goes in cairo. Communicate this in the cairo-registry README. |

## 7. Day-1 work estimate

- Step A (copy + import-path rewrite + build): **2 hours.**
- Step B (scripts + packaging + smoke test): **1 hour.**
- Step C (client consolidation): **2 hours** (the touchy step — reads main.go, updates 4 callsites, removes one package, runs smoke).
- Step D (archive old repo): **15 minutes**, scheduled after one release.

Total: **half a day** of focused work, plus one release cycle of soak time before Step D.
