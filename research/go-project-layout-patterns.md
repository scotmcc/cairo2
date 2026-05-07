# Go project layout patterns

## 1. Standard layout for multi-binary repos at 50K–100K LOC

The widely-cited [`golang-standards/project-layout`](https://github.com/golang-standards/project-layout) reference recommends:

```
cmd/<binary>/main.go     — one directory per binary; keeps main.go tiny
internal/                — code importable only by this module (compiler-enforced)
pkg/                     — code importable by external modules
api/                     — protobuf, OpenAPI, JSON schemas
configs/                 — config templates
scripts/                 — build/release/install scripts
test/                    — integration test data (unit tests live next to code)
```

**Where real projects diverge:** the `golang-standards` repo is a *community* convention, not blessed by the Go team — Russ Cox has explicitly said it is not a Go-team standard. Real projects pick what they need:

- `cmd/` and `internal/` are **near-universal** in serious projects.
- `pkg/` is **contested**. The Go team and many large projects (Tailscale, Caddy, many Kubernetes subprojects) avoid it. The argument: if a package is exported, just put it at the top level — `pkg/` adds a meaningless prefix to import paths.
- `api/` shows up only when there is a generated-code/protobuf surface.

## 2. How real projects organize multi-binary repos

### Tailscale (`github.com/tailscale/tailscale`)

- ~500K LOC, ~30 binaries.
- **Single `go.mod`.** No workspace.
- **No `internal/`, no `pkg/`.** Top-level dirs are by domain: `tsnet/`, `wgengine/`, `derp/`, `ipn/`, `net/`, `control/`, `tailcfg/`, etc.
- Binaries in `cmd/`: `cmd/tailscale/`, `cmd/tailscaled/`, `cmd/derper/`, ~25 more.
- Shared libraries are flat top-level packages, accepting that they're public API of the module.
- Pattern: every directory is a Go package whose name matches the directory. Cohesion is the test — `wgengine` is "everything WireGuard-engine", not "everything that needs WireGuard-related types".

### CockroachDB (`github.com/cockroachdb/cockroach`)

- ~3M LOC, the canonical large Go project.
- **Single `go.mod`.**
- Source tree is `pkg/` (their flavor — they treat it as the equivalent of `internal/` for their own use; very few external consumers).
- `pkg/cmd/cockroach/main.go` and ~40 sibling `pkg/cmd/<tool>/`.
- Heavy use of subdirectories for grouping: `pkg/sql/`, `pkg/kv/`, `pkg/storage/`, `pkg/server/`. Each is a domain.
- Pattern: domain-driven flat packages, one big module.

### Kubernetes (`github.com/kubernetes/kubernetes`)

- ~5M LOC, 30+ binaries, the most extreme Go monorepo.
- **Single `go.mod`** at the top. (Used to be Bazel + multi-module — collapsed back to one go.mod years ago.)
- Layout: `cmd/<binary>/`, `pkg/<domain>/`, `staging/src/k8s.io/<published-module>/` for the bits they republish to other repos.
- The `staging/` trick is unique to Kubernetes: it lets internal code be developed in-tree but published as separate modules (`client-go`, `apimachinery`). For Cairo's scale this is overkill.
- Pattern: one go.mod, many binaries, `pkg/` over `internal/` (for historical reasons; new code often in `staging/` if it needs publishing).

### Common patterns across all three

1. **One go.mod.** None of them use `go.work` for the main repo.
2. **`cmd/<binary>/main.go` is the sole pattern for binaries.**
3. **Top-level domain packages, not deep folder hierarchies.** Tailscale's `wgengine/` is a flat 50-file package, not `wgengine/router/handler/...`.
4. **Resist `pkg/` as a namespace prefix.** Either you have `internal/` (protected by Go's compiler) or you put domain dirs at the root.

## 3. `go.mod` vs `go.work`

### `go.mod` (single module)

- One `go.mod` at the repo root. All packages import each other via `<module>/<path>`.
- All binaries share one dependency graph. One `go.sum`.
- Adding a new binary or package is free — just create a directory.
- **Cost:** every consumer of the module sees the full dep tree (no per-binary slimming). Not a real cost when only the binaries you build link the deps they use; Go's linker drops unused packages.

### `go.work` (workspace mode)

- Multiple `go.mod` files under one tree, plus a `go.work` linking them.
- Each module has its own dep graph and version policy.
- **Designed for:** developing two modules in lockstep without having to publish a version of one to test changes in the other (e.g. `cockroach-go` workspace alongside `cockroach`).
- **Cost:** every module needs its own version bumps; cross-module refactoring is harder (you change one module, then bump it in the consumer's `go.mod`); CI needs to be aware of multiple modules.

### Tradeoffs (real, not theoretical)

| Concern | One `go.mod` | `go.work` |
|---|---|---|
| Refactor across binaries | Trivial (no version bumps) | Painful (bump pseudoversion, update consumer go.mod) |
| Per-binary dep slimming | Not needed (linker drops unused) | True per-module deps |
| External consumers | Module is one indivisible chunk | Each module published separately |
| CI complexity | One `go test ./...` | One `go work sync` then per-module |
| Onboarding | Standard | Non-obvious; new dev confused by `go.work` |

**Rule of thumb:** use `go.work` only when you have **separately-published** modules that need to evolve in lockstep during development. If everything in the tree ships as one product (Cairo's case), use one `go.mod`.

## 4. `internal/` semantics in Go

`internal/` is not just convention — it's compiler-enforced.

- A package at `<module>/internal/foo` can only be imported by packages whose import path begins with `<module>/`. Outside callers get a build error.
- A package at `<module>/x/internal/y` can be imported by any package under `<module>/x/...`, scoping the protection.
- This is the **only** access-control feature Go has. There is no `private`, no `internal class`. `internal/` is the language's answer.
- `pkg/` has **no special meaning to the compiler.** It is purely a folder-naming convention.

How real projects use it:
- **Tailscale**: rare use; they leave packages public-by-name and rely on `// Package x is a public API of...` doc convention.
- **CockroachDB**: doesn't use `internal/` at all — they use `pkg/` as their de-facto internal namespace and don't expose their module's packages to outside consumers in practice.
- **Kubernetes**: uses `internal/` heavily inside `staging/src/k8s.io/<mod>/internal/` to lock down per-published-module privates.
- **Caddy / Cobra / Charm projects** (Cairo's nearer neighbors): `internal/` for everything not part of the documented plugin API. This is the right model for an application like Cairo.

## 5. Recommended layout for Cairo

Cairo's profile:
- ~66K Go LOC + ~3.5K TS.
- Multiple binaries: `cairo` (TUI/CLI/serve/etc), `cairo-registry`, `cairo-ctl` (after merge).
- All binaries owned by one team, shipped together as one product (rpm/deb).
- ~70% package overlap between binaries (the registry server uses very few cairo packages, but the registry CLIENT is shared).
- No external consumers of any package. Cairo is an application, not a library.

**Recommendation: single `go.mod`, `cmd/<binary>/` for binaries, `internal/` for everything else, no `pkg/`.**

Justification, point by point:

1. **One `go.mod`.** Cairo is a product, not a federation of libraries. The `cairo-registry` merge confirms this — the protocol drift problem was caused by *splitting* a module that should have been one. Everything in this repo ships as one release.
2. **`cmd/<binary>/`.** Already used. Keep. Add `cmd/cairo-registry/` and `cmd/cairo-ctl/` after merge.
3. **`internal/`.** Compiler-enforced privacy is the right default for an app. Cairo has no public Go API — even the embedded TUI library is consumed only by Cairo itself.
4. **No `pkg/`.** Adds nothing for Cairo. Saves the `pkg/` prefix in every import path. Tailscale and Caddy demonstrate this scales.
5. **Top-level domain packages under `internal/`, not deep nesting.** Cairo today has `internal/agent/`, `internal/db/`, `internal/server/` — flat. Continue. After the proposed split, `internal/store/<sub>/` is one level deeper, which is fine.
6. **Skip `api/`** unless and until Cairo grows a generated/typed wire format (e.g. Protobuf). Today the wire types are Go structs in `internal/protocol/` — keep that.
7. **Skip `pkg/`-as-publish-staging.** Cairo doesn't republish.

What this enables:
- `go test ./...` runs everything.
- `go build ./cmd/...` builds every binary.
- Cross-binary refactoring is one edit and one commit.
- New contributor sees a familiar layout.

What this costs:
- The whole tree's deps live in one `go.mod`. (Linker drops what each binary doesn't use, so the runtime cost is zero.)

This is the same answer Tailscale, Caddy, and most pragmatic Go projects landed on. It's boring, and that's the point.
