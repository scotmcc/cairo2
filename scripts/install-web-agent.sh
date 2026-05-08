#!/usr/bin/env bash
# install-web-agent.sh - Build and install the Cairo web agent separately.
#
# Installs:
#   /usr/local/bin/cairo-web
#   /usr/share/cairo/web-agent/
#
# Usage:
#   bash scripts/install-web-agent.sh
#   bash scripts/install-web-agent.sh --clean
#   bash scripts/install-web-agent.sh --no-build
#   bash scripts/install-web-agent.sh --prefix /tmp/cairo --share-dir /tmp/cairo/share/cairo

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
STAGE_DIR="$REPO_ROOT/build/web-agent"
PREFIX="/usr/local"
SHARE_DIR="/usr/share/cairo"
BUILD=true
BUILD_CLEAN=false

usage() {
    awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --clean)
            BUILD_CLEAN=true
            ;;
        --no-build)
            BUILD=false
            ;;
        --prefix)
            shift
            [[ $# -gt 0 ]] || { echo "--prefix requires a value" >&2; exit 1; }
            PREFIX="${1%/}"
            ;;
        --prefix=*)
            PREFIX="${1#*=}"
            PREFIX="${PREFIX%/}"
            ;;
        --share-dir)
            shift
            [[ $# -gt 0 ]] || { echo "--share-dir requires a value" >&2; exit 1; }
            SHARE_DIR="${1%/}"
            ;;
        --share-dir=*)
            SHARE_DIR="${1#*=}"
            SHARE_DIR="${SHARE_DIR%/}"
            ;;
        --help|-h)
            usage
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage
            ;;
    esac
    shift
done

[[ -n "$PREFIX" ]] || { echo "prefix cannot be empty" >&2; exit 1; }
[[ -n "$SHARE_DIR" ]] || { echo "share-dir cannot be empty" >&2; exit 1; }

if [[ "$BUILD" == true ]]; then
    if [[ "$BUILD_CLEAN" == true ]]; then
        "$SCRIPT_DIR/build-web-agent.sh" --clean
    else
        "$SCRIPT_DIR/build-web-agent.sh"
    fi
fi

[[ -f "$STAGE_DIR/package.json" ]] || { echo "web-agent runtime missing at $STAGE_DIR; run scripts/build-web-agent.sh" >&2; exit 1; }
[[ -f "$STAGE_DIR/dist/server/server/src/index.js" ]] || { echo "web-agent server runtime missing in $STAGE_DIR" >&2; exit 1; }
[[ -f "$STAGE_DIR/dist/web/index.html" ]] || { echo "web-agent browser build missing in $STAGE_DIR" >&2; exit 1; }
[[ -d "$STAGE_DIR/node_modules" ]] || { echo "web-agent production dependencies missing in $STAGE_DIR" >&2; exit 1; }

BIN_DIR="$PREFIX/bin"
LAUNCHER="$BIN_DIR/cairo-web"
RUNTIME_DIR="$SHARE_DIR/web-agent"

run_privileged() {
    if [[ $EUID -eq 0 ]]; then
        "$@"
    else
        sudo "$@"
    fi
}

ensure_dir() {
    local dir="$1"
    mkdir -p "$dir" 2>/dev/null || run_privileged mkdir -p "$dir"
}

install_file() {
    local src="$1"
    local dst="$2"
    local mode="$3"
    ensure_dir "$(dirname "$dst")"
    if [[ -w "$(dirname "$dst")" ]]; then
        install -m "$mode" "$src" "$dst"
    else
        run_privileged install -m "$mode" "$src" "$dst"
    fi
}

install_tree() {
    local src="$1"
    local dst="$2"
    ensure_dir "$(dirname "$dst")"
    if [[ -w "$(dirname "$dst")" ]]; then
        rm -rf "$dst"
        mkdir -p "$dst"
        cp -a "$src"/. "$dst"/
    else
        run_privileged rm -rf "$dst"
        run_privileged mkdir -p "$dst"
        run_privileged cp -a "$src"/. "$dst"/
    fi
}

install_tree "$STAGE_DIR" "$RUNTIME_DIR"
install_file "$REPO_ROOT/scripts/cairo-web.sh" "$LAUNCHER" 0755

echo "installed $LAUNCHER"
echo "installed $RUNTIME_DIR"
echo "run: cairo-web"
