#!/usr/bin/env bash
# ============================================================================
# build-extension.sh - Build/package the Cairo VS Code extension.
#
# Operates on $REPO_ROOT/vscode-extension/. Produces a .vsix at
# $REPO_ROOT/vscode-extension/.vscode-extension/cairo-vscode-<ver>.vsix.
#
# Usage:
#   bash scripts/build-extension.sh [build|package|clean|all]
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
EXT_DIR="$REPO_ROOT/vscode-extension"

[[ -d "$EXT_DIR" ]] || { echo "vscode-extension directory not found at $EXT_DIR" >&2; exit 1; }

cd "$EXT_DIR"

ensure_deps() {
    if [[ -d node_modules ]]; then
        return
    fi
    if [[ -f package-lock.json ]]; then
        npm ci --ignore-scripts
    else
        npm install --ignore-scripts
    fi
}

vsix_path() {
    local version
    version="$(node -p "require('./package.json').version")"
    printf '.vscode-extension/cairo-vscode-%s.vsix\n' "$version"
}

package_vsix() {
    [[ -f dist/extension.js ]] || { echo "dist/extension.js missing - run: bash scripts/build-extension.sh build" >&2; exit 1; }
    mkdir -p .vscode-extension
    local out
    out="$(vsix_path)"
    rm -f "$out"
    npx vsce package --out "$out" --allow-missing-repository --no-dependencies
    echo "Package created: $EXT_DIR/$out"
}

case "${1:-all}" in
    build)
        echo "=== Building Extension ==="
        ensure_deps
        npm run compile
        rm -rf dist
        npm run bundle
        echo "Build complete."
        ;;
    package)
        echo "=== Packaging Extension ==="
        ensure_deps
        package_vsix
        ;;
    clean)
        echo "=== Cleaning ==="
        rm -rf node_modules dist .vscode-extension
        echo "Clean complete."
        ;;
    all|"")
        echo "=== Building Extension ==="
        ensure_deps
        npm run compile
        rm -rf dist
        npm run bundle
        echo "=== Packaging Extension ==="
        package_vsix
        echo "Complete."
        ;;
    help|--help|-h)
        echo "Usage: bash scripts/build-extension.sh [build|package|clean|all]"
        ;;
    *)
        echo "Unknown action: $1" >&2
        exit 1
        ;;
esac
