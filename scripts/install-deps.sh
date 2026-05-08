#!/usr/bin/env bash
# ============================================================================
# install-deps.sh — One-shot dev-system bootstrap for Cairo2.
#
# Installs everything needed on a fresh machine to:
#   1. Run Cairo2 (Go toolchain, sqlite3 CLI for DB inspection).
#   2. Build the VS Code extension (.vsix) — Node.js + npm.
#   3. Build the .deb / .rpm packages (dpkg-deb + fakeroot, rpmbuild + rpmdevtools).
#   4. Build and install cairo, cairo-registry, cairo-ctl to /usr/local/bin/ via scripts/install.sh.
#
# Supported package managers: apt (Debian/Ubuntu), dnf/yum (RHEL/Fedora).
# Re-running is safe — package managers skip already-installed deps and the
# cairo install step rebuilds in place.
#
# Usage:
#   bash scripts/install-deps.sh                # install system deps + cairo
#   bash scripts/install-deps.sh --skip-cairo   # only system deps
#   bash scripts/install-deps.sh --skip-system  # only build/install cairo
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SKIP_CAIRO=false
SKIP_SYSTEM=false

info()    { echo "  [info]  $*"; }
success() { echo "  [ ok ]  $*"; }
warn()    { echo "  [warn]  $*" >&2; }
die()     { echo "  [err ]  $*" >&2; exit 1; }

usage() {
    awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
    exit 0
}

parse_args() {
    while [[ $# -gt 0 ]]; do
        case "$1" in
            --skip-cairo)  SKIP_CAIRO=true ;;
            --skip-system) SKIP_SYSTEM=true ;;
            -h|--help)     usage ;;
            *) die "Unknown argument: $1 (use --help)" ;;
        esac
        shift
    done
}

detect_pkg_manager() {
    if command -v apt-get >/dev/null 2>&1; then echo "apt"
    elif command -v dnf >/dev/null 2>&1;     then echo "dnf"
    elif command -v yum >/dev/null 2>&1;     then echo "yum"
    else echo "unknown"
    fi
}

SUDO=""
if [[ $EUID -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
        SUDO="sudo"
    else
        die "This script needs root privileges or sudo to install system packages"
    fi
fi

# Cairo2's go.mod targets Go 1.25, so a system Go 1.21+ is enough (GOTOOLCHAIN=auto
# will auto-fetch the required toolchain at build time). Ubuntu 24.04 ships Go 1.22.
install_apt_packages() {
    local packages=(
        # Core build / fetch
        build-essential git curl ca-certificates pkg-config
        # Cairo2 build + runtime niceties
        golang-go python3 sqlite3
        # VS Code extension build (vsce runs under Node)
        nodejs npm
        # .deb packaging
        dpkg-dev fakeroot
        # .rpm packaging (rpm package provides rpmbuild on Debian/Ubuntu)
        rpm
    )
    info "apt-get update"
    $SUDO apt-get update -y
    info "Installing: ${packages[*]}"
    $SUDO env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${packages[@]}"
}

install_dnf_packages() {
    local pm="$1"
    local packages=(
        # Core build / fetch
        gcc gcc-c++ make git curl ca-certificates pkgconf-pkg-config
        # Cairo2 build + runtime niceties
        golang python3 sqlite
        # VS Code extension build
        nodejs npm
        # .rpm packaging
        rpm-build rpmdevtools
        # .deb packaging (best-effort on RHEL/Fedora)
        dpkg fakeroot
    )
    info "Installing: ${packages[*]}"
    $SUDO "$pm" install -y "${packages[@]}" || warn "Some packages may not be available on this distro — review output above"
}

install_system_deps() {
    if [[ "$SKIP_SYSTEM" == true ]]; then
        info "Skipping system package install (--skip-system)"
        return
    fi
    local pm
    pm=$(detect_pkg_manager)
    case "$pm" in
        apt)     install_apt_packages ;;
        dnf|yum) install_dnf_packages "$pm" ;;
        *) die "Unsupported package manager — install build-essential, go, sqlite, rpm, dpkg-dev, fakeroot manually" ;;
    esac
    success "System packages installed"
}

install_cairo() {
    if [[ "$SKIP_CAIRO" == true ]]; then
        info "Skipping cairo build/install (--skip-cairo)"
        return
    fi
    if [[ ! -x "$REPO_ROOT/scripts/install.sh" ]]; then
        die "Expected $REPO_ROOT/scripts/install.sh — repo layout changed?"
    fi
    info "Building and installing cairo, cairo-registry, cairo-ctl from $REPO_ROOT"
    "$REPO_ROOT/scripts/install.sh"
    if command -v cairo >/dev/null 2>&1; then
        success "cairo installed: $(command -v cairo)"
    else
        warn "cairo install ran but binary is not on PATH — check /usr/local/bin/cairo"
    fi
}

verify() {
    echo ""
    echo "Verifying toolchain..."
    echo "──────────────────────"
    local ok=true
    local tools=(go python3 sqlite3 dpkg-deb fakeroot rpmbuild git node npm)
    for tool in "${tools[@]}"; do
        if command -v "$tool" >/dev/null 2>&1; then
            info "$(printf '%-10s' "$tool") $(command -v "$tool")"
        else
            warn "$(printf '%-10s' "$tool") MISSING"
            ok=false
        fi
    done
    if [[ "$SKIP_CAIRO" != true ]]; then
        command -v cairo          || echo "  [warn]  cairo            MISSING" >&2
        command -v cairo-registry || echo "  [warn]  cairo-registry   MISSING" >&2
        command -v cairo-ctl      || echo "  [warn]  cairo-ctl        MISSING" >&2
        if command -v cairo >/dev/null 2>&1 && command -v cairo-registry >/dev/null 2>&1 && command -v cairo-ctl >/dev/null 2>&1; then
            info "$(printf '%-10s' cairo)    $(command -v cairo)"
            info "$(printf '%-10s' cairo-registry) $(command -v cairo-registry)"
            info "$(printf '%-10s' cairo-ctl)      $(command -v cairo-ctl)"
        else
            ok=false
        fi
    fi
    echo ""
    if [[ "$ok" == true ]]; then
        success "All required tools are present."
    else
        warn "Some tools are missing — see warnings above."
        exit 1
    fi
}

main() {
    parse_args "$@"
    echo "Cairo2 dev-system bootstrap"
    echo "==========================="
    install_system_deps
    install_cairo
    verify
    echo ""
    echo "Next steps:"
    echo "  Build only (local):   bash scripts/build.sh"
    echo "  Build .vsix only:     bash scripts/build-extension.sh"
    echo "  Build .deb + .rpm:    bash scripts/packaging/build-packages.sh"
    echo "  Run cairo TUI:        cairo --tui"
    echo ""
}

main "$@"
