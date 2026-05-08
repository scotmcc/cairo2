#!/usr/bin/env bash
# install.sh - Build Cairo and install the binaries to /usr/local/bin/.
#
# This is the canonical install path used by the .deb and .rpm packages too,
# so dev installs and packaged installs share one path — no PATH gymnastics
# and no stale copies under $HOME/go/bin.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$REPO_ROOT/bin"
SYSTEM_BIN_DIR="/usr/local/bin"

"$SCRIPT_DIR/build.sh"

for BIN in cairo cairo-registry cairo-ctl; do
    if [[ -w "$SYSTEM_BIN_DIR" ]]; then
        install -m 0755 "$BIN_DIR/$BIN" "$SYSTEM_BIN_DIR/$BIN"
    else
        sudo install -m 0755 "$BIN_DIR/$BIN" "$SYSTEM_BIN_DIR/$BIN"
    fi
done
echo "Installed: cairo, cairo-registry, cairo-ctl → /usr/local/bin/"
