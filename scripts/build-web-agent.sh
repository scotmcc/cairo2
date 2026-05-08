#!/usr/bin/env bash
# build-web-agent.sh - Build and stage the Cairo web agent runtime.
#
# Produces a production-ready runtime at:
#   build/web-agent/
#
# Usage:
#   bash scripts/build-web-agent.sh
#   bash scripts/build-web-agent.sh --clean

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
WEB_AGENT_DIR="$REPO_ROOT/web-agent"
STAGE_DIR="$REPO_ROOT/build/web-agent"
CLEAN=false

usage() {
    awk 'NR > 1 { if ($0 !~ /^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --clean)
            CLEAN=true
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

[[ -d "$WEB_AGENT_DIR" ]] || { echo "web-agent directory not found at $WEB_AGENT_DIR" >&2; exit 1; }
command -v node >/dev/null 2>&1 || { echo "node is required to build the web agent" >&2; exit 1; }
command -v npm >/dev/null 2>&1 || { echo "npm is required to build the web agent" >&2; exit 1; }

cd "$WEB_AGENT_DIR"

if [[ "$CLEAN" == true ]]; then
    rm -rf node_modules dist "$STAGE_DIR"
fi

if [[ ! -d node_modules ]]; then
    if [[ -f package-lock.json ]]; then
        npm ci --ignore-scripts
    else
        npm install --ignore-scripts
    fi
fi

npm run build

rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"

install -m 0644 package.json "$STAGE_DIR/package.json"
[[ -f package-lock.json ]] && install -m 0644 package-lock.json "$STAGE_DIR/package-lock.json"
[[ -f README.md ]] && install -m 0644 README.md "$STAGE_DIR/README.md"
cp -a dist "$STAGE_DIR/dist"

(
    cd "$STAGE_DIR"
    if [[ -f package-lock.json ]]; then
        npm ci --omit=dev --ignore-scripts
    else
        npm install --omit=dev --ignore-scripts
    fi
)

echo "built web agent runtime: $STAGE_DIR"
