# Installation

Cairo is distributed three ways: as a pre-built package (RPM or DEB), as a build-and-install from source, or as a local build you run directly from the source directory. Pick whichever fits your situation.

---

## Prerequisites

Before installing Cairo you need a few things on your system:

| Tool | What it's for | Minimum version |
|---|---|---|
| **Go** | Compiling the Cairo binary | 1.21+ |
| **Git** | Cloning the source | any recent |
| **SQLite** | Cairo's local database (runtime) | 3.x |
| **Node.js + npm** | Building the web UI (optional) | Node 18+ |

If you're on a fresh machine, the one-shot bootstrap script installs all of these for you. See [Option A below](#option-a-one-shot-bootstrap-linux-only) or the manual instructions for macOS.

---

## Option A: Pre-built packages (RPM or DEB)

If someone has built packages from this repository and shared them with you, this is the fastest path.

**On Debian / Ubuntu (.deb):**

```bash
sudo dpkg -i cairo_<version>_amd64.deb
```

**On RHEL / Fedora / Rocky (.rpm):**

```bash
sudo rpm -i cairo-<version>.x86_64.rpm
```

After installation, three binaries will be on your PATH:

```
cairo          — the agent (the one you'll use every day)
cairo-registry — the fleet registry server (for enterprise/team setups)
cairo-ctl      — the operator CLI (for fleet administrators)
```

Most people only ever need `cairo`. You can ignore the other two unless you're setting up a team deployment.

Verify the install worked:

```bash
cairo -help
```

You should see a short list of flags and subcommands. If you see `cairo: command not found`, make sure `/usr/local/bin` is on your PATH:

```bash
export PATH=$PATH:/usr/local/bin
```

Add that line to your shell's startup file (`~/.bashrc`, `~/.zshrc`, or equivalent) to make it permanent.

---

## Option B: Build from source and install to /usr/local/bin

This is the recommended path for developers and for anyone who wants to track the latest changes.

### Step 1: Install prerequisites (Linux)

On a fresh Linux machine, the bootstrap script handles everything:

```bash
bash scripts/install-deps.sh
```

This script detects whether you're on a Debian/Ubuntu or RHEL/Fedora system and installs Go, Node, SQLite, Git, and the packaging tools. It's safe to re-run — it skips things already installed. At the end it builds and installs Cairo automatically.

If the bootstrap ran without errors, **you're done** — skip to [Step 3](#step-3-verify).

To install only the system dependencies without building Cairo:

```bash
bash scripts/install-deps.sh --skip-cairo
```

### Step 1 (macOS): Install prerequisites

On macOS, use [Homebrew](https://brew.sh) (install it first if you don't have it):

```bash
brew install go node git sqlite
```

### Step 2: Build and install Cairo

Clone the repository and run the install script:

```bash
git clone https://github.com/scotmcc/cairo2.git
cd cairo2
bash scripts/install.sh
```

`scripts/install.sh` builds all three binaries and copies them to `/usr/local/bin/`. It may ask for your password (via `sudo`) to write to that directory.

### Step 3: Verify

```bash
cairo -help
```

Expected output is a list of flags and subcommands. If it works, installation is complete.

---

## Option C: Build locally and run from ./bin/

Use this if you want to try Cairo without putting anything in `/usr/local/bin`, or if you're doing development work on Cairo itself.

```bash
git clone https://github.com/scotmcc/cairo2.git
cd cairo2
bash scripts/build.sh
```

The binaries land in `./bin/`:

```
./bin/cairo
./bin/cairo-registry
./bin/cairo-ctl
```

Run Cairo directly from there:

```bash
./bin/cairo -tui
```

You can also add `./bin/` to your PATH for the session:

```bash
export PATH=$PATH:$(pwd)/bin
cairo -tui
```

**Why `bash scripts/build.sh` instead of `go build`?** The build script sets version flags and ensures the output matches what the packaging scripts produce. Using bare `go build` will work, but the version string in the binary will be wrong. Use the script.

---

## Building packages yourself (RPM / DEB)

If you need to distribute Cairo on a team or install it on machines without internet access, you can build packages from source.

```bash
bash scripts/packaging/build-packages.sh
```

Built packages land in `dist/`. To build only one format:

```bash
bash scripts/packaging/build-packages.sh --deb    # Debian/Ubuntu only
bash scripts/packaging/build-packages.sh --rpm    # RHEL/Fedora only
```

The packaging script requires `dpkg-dev` and `fakeroot` for DEB, and `rpm-build` and `rpmdevtools` for RPM. The bootstrap script (`install-deps.sh`) installs these.

---

## Data directory

Cairo stores everything in `~/.cairo/cairo.db` — a single SQLite database file created on first run. You don't need to create it; Cairo creates it when it first starts.

If you want to store data somewhere else (useful for testing, or for running multiple Cairo instances):

```bash
export CAIRO_DATA_DIR=/path/to/your/data/dir
```

Set that environment variable before running Cairo and it will use that directory instead.

---

## Troubleshooting

**`cairo: command not found` after installing**
`/usr/local/bin` is not on your PATH. Run `echo $PATH` to check. Add `export PATH=$PATH:/usr/local/bin` to your shell startup file.

**Build fails with Go version error**
Cairo requires Go 1.21 or later. Check your version with `go version`. If it's older, update Go — `install-deps.sh` handles this on Linux, or `brew upgrade go` on macOS.

**`permission denied` when running `install.sh`**
The script needs `sudo` to write to `/usr/local/bin`. Make sure `sudo` is available and you have the necessary permissions on your machine.

**SQLite errors on first run**
Make sure `sqlite3` is installed. On Debian/Ubuntu: `sudo apt-get install sqlite3`. On RHEL/Fedora: `sudo dnf install sqlite`. On macOS: `brew install sqlite`.

---

## Next

Once Cairo is installed: [First Run](03-first-run.md) — start it up and connect it to an LLM.
