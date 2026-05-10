# Packaging: RPM and DEB

cairo ships `.deb` and `.rpm` packages built by `scripts/packaging/build-packages.sh`. This script builds all three binaries, runs a pre-package gate (lint + tests), assembles the packages, and writes them to `build/packages/`.

---

## Quick build

```sh
bash scripts/packaging/build-packages.sh
```

Produces both `.deb` and `.rpm` packages with the current version string.

```sh
# Build only .deb
bash scripts/packaging/build-packages.sh --deb

# Build only .rpm
bash scripts/packaging/build-packages.sh --rpm

# Pin a specific version string
bash scripts/packaging/build-packages.sh --version 0.4.1
```

---

## Flags

| Flag | Description |
|------|-------------|
| `--deb` | Build only `.deb` packages |
| `--rpm` | Build only `.rpm` packages |
| `--version VERSION` | Override the version string embedded in binaries and package metadata |
| `--skip-extension` | Skip building the VS Code extension `.vsix` |
| `--skip-web-agent` | Skip building the web agent (Node.js compile step) |
| `--skip-tests` | Skip the pre-package gate (lint + tests) |

Omitting both `--deb` and `--rpm` builds both formats.

---

## Pre-package gate

Unless `--skip-tests` is passed, the script calls `scripts/packaging/pre-package.sh` before assembling packages. The gate runs:

```sh
go vet ./...
go test ./...
```

If either step fails, the build aborts. This prevents packaging a broken binary.

To run the gate independently:

```sh
bash scripts/packaging/pre-package.sh
```

---

## Output

All packages land in `build/packages/`:

```
build/packages/
  cairo_0.4.0_amd64.deb
  cairo-registry_0.4.0_amd64.deb
  cairo-ctl_0.4.0_amd64.deb
  cairo_0.4.0-1.x86_64.rpm
  cairo-registry_0.4.0-1.x86_64.rpm
  cairo-ctl_0.4.0-1.x86_64.rpm
  cairo-vscode-0.4.0.vsix
```

`build/` is gitignored — packages will not appear as untracked files.

---

## What each package installs

### `cairo` package

| Path | Description |
|------|-------------|
| `/usr/local/bin/cairo` | Main binary |
| `/usr/local/bin/cairo-web` | Web agent launcher (if `--skip-web-agent` not set) |
| `/usr/share/cairo/web-agent/` | Web agent runtime (if `--skip-web-agent` not set) |
| `/usr/share/cairo/*.vsix` | VS Code extension (if `--skip-extension` not set) |
| `/usr/lib/systemd/user/cairo.service` | User systemd unit |
| `/usr/lib/systemd/user/cairo-web.service` | Web agent user systemd unit |

Post-install: if `~/.config/cairo/services.enabled` exists, the `cairo.service` and `cairo-web.service` user units are auto-enabled for the installing user.

### `cairo-registry` package

| Path | Description |
|------|-------------|
| `/usr/local/bin/cairo-registry` | Registry server binary |
| `/usr/lib/systemd/system/cairo-registry.service` | System systemd unit |

Post-install: creates the `cairo` system user and group (if absent), enables and starts `cairo-registry.service`.

### `cairo-ctl` package

| Path | Description |
|------|-------------|
| `/usr/local/bin/cairo-ctl` | Operator CLI binary |

No systemd unit — `cairo-ctl` is an interactive command, not a service.

---

## Installing packages

### Debian / Ubuntu

```sh
sudo dpkg -i build/packages/cairo_0.4.0_amd64.deb
sudo dpkg -i build/packages/cairo-registry_0.4.0_amd64.deb
sudo dpkg -i build/packages/cairo-ctl_0.4.0_amd64.deb
```

### RHEL / Fedora / CentOS

```sh
sudo rpm -i build/packages/cairo_0.4.0-1.x86_64.rpm
sudo rpm -i build/packages/cairo-registry_0.4.0-1.x86_64.rpm
sudo rpm -i build/packages/cairo-ctl_0.4.0-1.x86_64.rpm
```

### Upgrading

```sh
# .deb
sudo dpkg -i build/packages/cairo_0.4.1_amd64.deb

# .rpm
sudo rpm -U build/packages/cairo_0.4.1-1.x86_64.rpm
```

---

## Systemd units after install

### cairo (user unit)

```sh
systemctl --user enable --now cairo.service
systemctl --user status cairo.service
journalctl --user -u cairo.service -f
```

The user unit starts `cairo serve` with no flags (plain TCP, no auth, port 1337 unless config overrides). To enable auth, set the config key before enabling the unit:

```sh
cairo config set server_token "$(cairo token)"
# Then edit /usr/lib/systemd/user/cairo.service to add --auth to ExecStart
systemctl --user daemon-reload
systemctl --user restart cairo.service
```

### cairo-registry (system unit)

```sh
sudo systemctl status cairo-registry.service
sudo journalctl -u cairo-registry.service -f
```

The system unit runs as the `cairo` system user with `-state-dir /var/lib/cairo-registry`. Edit `/usr/lib/systemd/system/cairo-registry.service` to change flags (e.g., add `-no-tsnet -addr :8080` for plain TCP).

---

## Build dependencies

The packaging script requires these tools to be present on the build host:

- `go` (1.22+)
- `dpkg-deb` (for `.deb`)
- `rpmbuild` (for `.rpm`)
- `node` + `npm` (unless `--skip-web-agent`)
- `vsce` (VS Code extension CLI, unless `--skip-extension`)

Install all build dependencies on a fresh machine:

```sh
bash scripts/install-deps.sh
```

---

## Dirty version tags

If the working tree has uncommitted changes, the version string is tagged `-dirty` (e.g., `0.4.0-dirty`). Add `/build/` to `.gitignore` (already done in this repo) and commit all changes before packaging to get a clean version string.
