#!/usr/bin/env bash
# build.sh - Build the Cairo binaries into ./bin/.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$REPO_ROOT/bin"

mkdir -p "$BIN_DIR"

VERSION=$(cd "$REPO_ROOT" && git describe --tags --always --dirty 2>/dev/null)
if [ -z "$VERSION" ]; then
  VERSION=$(grep 'var Version' "$REPO_ROOT/internal/version/version.go" 2>/dev/null \
    | sed 's/.*"\(.*\)".*/\1/' || echo "dev")
fi

go build -ldflags "-X main.version=$VERSION" -o "$BIN_DIR/cairo"          "$REPO_ROOT/cmd/cairo"
go build -ldflags "-X main.version=$VERSION" -o "$BIN_DIR/cairo-registry" "$REPO_ROOT/cmd/cairo-registry"
go build -ldflags "-X main.version=$VERSION" -o "$BIN_DIR/cairo-ctl"      "$REPO_ROOT/cmd/cairo-ctl"

echo "Built: $BIN_DIR/cairo, $BIN_DIR/cairo-registry, $BIN_DIR/cairo-ctl"
