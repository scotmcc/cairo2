#!/usr/bin/env bash
# ============================================================================
# install-extension.sh — Install the Cairo VS Code extension into the local
# `code` editor for the current user. Builds first if no .vsix is found.
#
# Usage:
#   bash scripts/install-extension.sh [path/to/cairo-vscode.vsix]
# ============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
EXT_DIR="$REPO_ROOT/vscode-extension"
VSIX_DIR="$EXT_DIR/.vscode-extension"

VSIX="${1:-}"
if [[ -z "$VSIX" ]]; then
    VSIX=$(find "$VSIX_DIR" -maxdepth 1 -type f -name '*.vsix' -print 2>/dev/null | sort -V | tail -1 || true)
fi

if [[ -z "$VSIX" ]]; then
    echo "No .vsix found in $VSIX_DIR — building one first."
    "$SCRIPT_DIR/build-extension.sh"
    VSIX=$(find "$VSIX_DIR" -maxdepth 1 -type f -name '*.vsix' -print | sort -V | tail -1)
fi

[[ -f "$VSIX" ]] || { echo "VSIX not found: $VSIX" >&2; exit 1; }

command -v code >/dev/null 2>&1 || { echo "VS Code CLI ('code') not found on PATH" >&2; exit 1; }

echo "=== Installing $VSIX ==="
code --install-extension "$VSIX" --force
echo "Installed. Reload VS Code to activate."
