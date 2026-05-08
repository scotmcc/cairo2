# cairo2 Packaging Scripts

Build cairo2 as system packages for Debian/Ubuntu (`.deb`) and RHEL/Fedora
(`.rpm`). Three separate packages are produced:

| Package | Contents |
|---|---|
| `cairo` | cairo binary + web agent + VS Code extension + systemd user units |
| `cairo-registry` | registry daemon binary + systemd system unit |
| `cairo-ctl` | control CLI binary only |

All artifacts land in `dist/`.

## Quick Start

```bash
# Build all packages supported on this machine
bash scripts/packaging/build-packages.sh

# Build .deb only
bash scripts/packaging/build-packages.sh --deb

# Build .rpm only
bash scripts/packaging/build-packages.sh --rpm

# Skip pre-package tests (CI speed)
bash scripts/packaging/build-packages.sh --skip-tests

# Skip web agent or extension build (CI speed)
bash scripts/packaging/build-packages.sh --skip-web-agent --skip-extension --skip-tests

# Override version
bash scripts/packaging/build-packages.sh --version 1.0.0
```

Or via make:

```bash
make package
```

## Output

```
dist/
  cairo_<ver>_<arch>.deb
  cairo_<ver>.<dist>.rpm
  cairo-registry_<ver>_<arch>.deb
  cairo-registry_<ver>.<dist>.rpm
  cairo-ctl_<ver>_<arch>.deb
  cairo-ctl_<ver>.<dist>.rpm
  cairo-vscode-<ver>.vsix          # standalone vsix (also embedded in cairo package)
  SHA256SUMS
```

## Installation

```bash
# Debian/Ubuntu
sudo dpkg -i dist/cairo_*.deb
sudo dpkg -i dist/cairo-registry_*.deb
sudo dpkg -i dist/cairo-ctl_*.deb

# RHEL/Fedora
sudo rpm -i dist/*/cairo-*.rpm
sudo rpm -i dist/*/cairo-registry-*.rpm
sudo rpm -i dist/*/cairo-ctl-*.rpm
```

The cairo package post-install hook:
- Enables `cairo.service` and `cairo-web.service` for the invoking user (via loginctl linger)
- Attempts to register the bundled VSIX with the invoking user's VS Code install

If VS Code auto-registration fails:
```bash
code --install-extension /usr/share/cairo/cairo-vscode-*.vsix --force
```

## Requirements

- Go 1.21+
- Node.js + npm (for web agent and extension builds)
- `dpkg-deb` + `fakeroot` for `.deb` — `sudo apt-get install -y dpkg fakeroot`
- `rpmbuild` for `.rpm` — `sudo dnf install -y rpm-build rpmdevtools`

Run `bash scripts/install-deps.sh --skip-cairo` to bootstrap all packaging
dependencies on supported distros.

## Package Contents

### cairo

- `/usr/local/bin/cairo`
- `/usr/local/bin/cairo-web` (web agent launcher)
- `/usr/share/cairo/web-agent/` (staged Node runtime)
- `/usr/share/cairo/cairo-vscode-<ver>.vsix`
- `/usr/lib/systemd/user/cairo.service` (serve mode daemon)
- `/usr/lib/systemd/user/cairo-web.service`

Conflicts with and replaces the legacy `cairo-agent` package name.

Runtime deps: `bash`, `ca-certificates`, `git`, `nodejs`, `sqlite3`/`sqlite`.

### cairo-registry

- `/usr/local/bin/cairo-registry`
- `/usr/lib/systemd/system/cairo-registry.service`

Runs as a dedicated `cairo` system user created by postinst.

Runtime deps: `bash`, `ca-certificates`.

### cairo-ctl

- `/usr/local/bin/cairo-ctl`

No services, no system user.

Runtime deps: `bash`.

## Upgrade from cairo-agent

The `cairo` package declares `Conflicts: cairo-agent` and `Replaces: cairo-agent`
(deb) / `Conflicts: cairo-agent` and `Obsoletes: cairo-agent` (rpm). On upgrade:

```bash
sudo dpkg -i dist/cairo_*.deb        # removes cairo-agent, installs cairo
sudo rpm -U dist/*/cairo-*.rpm       # upgrades cairo-agent → cairo
```

## macOS .pkg

Not yet implemented. Requires a macOS runner with `pkgbuild`/`productbuild`.
Reopen when: (a) cairo2 has a macOS CI runner, or (b) a .pkg is needed for
distribution. See ROADMAP Phase 1.6 for context.

## systemd Units

Unit file content is embedded inline in `build-packages.sh`. Batch 3 of Phase
1.6 extracts them to `scripts/packaging/systemd/` for standalone linting.
