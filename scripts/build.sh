#!/usr/bin/env bash
# build.sh - Build the Cairo binaries into ./bin/.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$REPO_ROOT/bin"

mkdir -p "$BIN_DIR"
go build -o "$BIN_DIR/cairo"          "$REPO_ROOT/cmd/cairo"
go build -o "$BIN_DIR/cairo-registry" "$REPO_ROOT/cmd/cairo-registry"
go build -o "$BIN_DIR/cairo-ctl"      "$REPO_ROOT/cmd/cairo-ctl"

echo "Built: $BIN_DIR/cairo, $BIN_DIR/cairo-registry, $BIN_DIR/cairo-ctl"
