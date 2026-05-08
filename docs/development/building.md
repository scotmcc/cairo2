# Building

Cairo2 produces three pure-Go binaries from a single cgo-free SQLite driver (`modernc.org/sqlite`). No C toolchain is needed. Node.js is only needed when building bundled packages that include the VS Code extension.

---

## Requirements

- **Go 1.25 or later.** Earlier versions may work but aren't tested.
- **Ollama running** at the target URL (default `http://localhost:11434`) for runtime, not for build.

Build is self-contained. Nothing in the build step contacts a network beyond `go mod download`.

---

## The three binaries

| Binary | Entry point | Status |
|---|---|---|
| `cairo` | `cmd/cairo` | Full TUI + agent loop + subcommands |
| `cairo-registry` | `cmd/cairo-registry` | Stub — prints `--version` and exits; real implementation pending |
| `cairo-ctl` | `cmd/cairo-ctl` | Stub — prints `--version` and exits; real implementation pending |

All three are built by every `make build` / `bash scripts/build.sh` invocation and installed together by `make install` / `bash scripts/install.sh`.

---

## Preferred build: scripts/build.sh

```bash
bash scripts/build.sh
```

This is the canonical build path. It stamps the version from `git describe --tags --always --dirty` into all three binaries via `-ldflags "-X main.version=$VERSION"`, then writes the outputs to `./bin/`:

```
./bin/cairo
./bin/cairo-registry
./bin/cairo-ctl
```

`make build` is a thin wrapper that calls `scripts/build.sh`.

---

## Make targets

```bash
make build         # build all three binaries to ./bin/  (calls scripts/build.sh)
make install       # build then install all three to /usr/local/bin/
make run           # make build then ./bin/cairo
make test          # go test ./...
make lint          # go vet ./...
make clean         # rm ./bin
make package       # build .deb and .rpm packages (calls scripts/packaging/build-packages.sh)
```

---

## System install

```bash
bash scripts/install.sh        # preferred — calls build.sh, then installs all three
make install                   # equivalent
```

Both place the binaries at `/usr/local/bin/cairo`, `/usr/local/bin/cairo-registry`, and `/usr/local/bin/cairo-ctl`, matching the paths used by the `.deb` and `.rpm` packages. Make sure `/usr/local/bin` is on PATH:

```bash
export PATH=$PATH:/usr/local/bin
```

---

## Packaging

```bash
make package
# or
bash scripts/packaging/build-packages.sh
```

Produces `.deb` and `.rpm` packages under `build/packages/`. The packages include all three binaries. Flags: `--deb`, `--rpm`, `--version VERSION`, `--skip-extension`, `--skip-tests`.

---

## Equivalent raw `go` commands

If you don't want to use `make` or the scripts (note: raw builds don't stamp the version):

```bash
go build -o ./bin/cairo          ./cmd/cairo
go build -o ./bin/cairo-registry ./cmd/cairo-registry
go build -o ./bin/cairo-ctl      ./cmd/cairo-ctl
```

From outside the repo (once public):

```bash
go install github.com/scotmcc/cairo2/cmd/cairo@latest
go install github.com/scotmcc/cairo2/cmd/cairo-registry@latest
go install github.com/scotmcc/cairo2/cmd/cairo-ctl@latest
```

---

## Build layout

```
cmd/cairo/               entrypoint — main.go (TUI + subcommands)
cmd/cairo-registry/      entrypoint — registry server (stub)
cmd/cairo-ctl/           entrypoint — control CLI (stub)
internal/agent/          agent loop, event bus, prompt composition
internal/cli/            line-oriented chat interface + stdout renderer
internal/store/          SQLite schema, migrations, query helpers (split into sub-packages)
  internal/store/schema/     DDL + migration runner
  internal/store/sqliteopen/ DB open/seed/wiring
  internal/store/config/     config queries
  internal/store/sessions/   session + message queries
  internal/store/memory/     memory, summary, fact queries
  internal/store/identity/   role, prompt, skill, tool queries
  internal/store/jobs/       job, task, worktree queries
  internal/store/index/      learn/project/file indexing queries
internal/llm/            Ollama HTTP client + streaming + embeddings
internal/tools/          every built-in tool
internal/tui/            Bubble Tea TUI
```

Nothing in `internal/` is importable externally — Go's `internal/` package convention enforces that. If you ever need to expose a package for reuse, move it to a top-level directory.

---

## Cross-compilation

Standard Go cross-compilation works because the SQLite driver is pure Go:

```bash
GOOS=linux   GOARCH=amd64 go build -o cairo-linux-amd64   ./cmd/cairo
GOOS=darwin  GOARCH=arm64 go build -o cairo-darwin-arm64  ./cmd/cairo
GOOS=windows GOARCH=amd64 go build -o cairo-windows.exe   ./cmd/cairo
```

Windows is untested — Ollama supports Windows, Cairo *should* build and run there, but no one on the project has tried as of this writing. Reports welcome.

---

## Common build errors

### `go: module lookup disabled by GOPROXY=off`
Your GOPROXY is set restrictively. Unset it (`unset GOPROXY`) or set it to `direct`.

### `undefined: os.<Something>`
Your Go version is too old. `go version` should show 1.25 or later.

### Tests fail with `ollama: unreachable`
Tests shouldn't require Ollama — if a test does hit Ollama it's a bug. File it.

### Linker errors on Linux
The `modernc.org/sqlite` driver should build cleanly on any modern Linux. If you're hitting link errors, report the full error and your distro/arch.

---

## Binary size

The `cairo` binary is ~25-30MB (Linux amd64, unstripped). Most of the size is `modernc.org/sqlite` (SQLite ported to Go) and the Bubble Tea / lipgloss / termenv stack. The stub binaries (`cairo-registry`, `cairo-ctl`) are much smaller — they carry only the version flag handler.

You can shrink with `-ldflags="-s -w"` if size matters:

```bash
go build -ldflags="-s -w" -o cairo ./cmd/cairo
```

Strips debug symbols and DWARF info. Not recommended for dev builds; fine for distribution.

---

## Reproducible builds

Cairo2 doesn't currently pin Go version via `toolchain` directive in `go.mod`, nor does it use `-trimpath` or `GOTOOLCHAIN=auto` in the Makefile. Reproducible builds across machines need:

```bash
go build -trimpath -o cairo ./cmd/cairo
```

Plus the same Go version on both machines. Not a priority for solo dev use, worth doing if we ever publish released artifacts.

---

## See also

- [Testing](testing.md) — the test suite
- [Contributing](contributing.md) — what kinds of changes fit the project
