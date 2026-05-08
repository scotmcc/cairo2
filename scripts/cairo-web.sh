#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
if [[ -n "${CAIRO_WEB_AGENT_DIR:-}" ]]; then
  WEB_AGENT_DIR="$CAIRO_WEB_AGENT_DIR"
elif [[ -f "$REPO_ROOT/web-agent/package.json" ]]; then
  WEB_AGENT_DIR="$REPO_ROOT/web-agent"
elif [[ -f /usr/share/cairo/web-agent/package.json ]]; then
  WEB_AGENT_DIR=/usr/share/cairo/web-agent
else
  printf 'cairo-web: web-agent runtime not found. Set CAIRO_WEB_AGENT_DIR or install cairo-agent.\n' >&2
  exit 1
fi

cd "$WEB_AGENT_DIR"

if [[ ! -d node_modules ]]; then
  printf 'cairo-web: node_modules missing in %s; run scripts/build-web-agent.sh or reinstall the package\n' "$WEB_AGENT_DIR" >&2
  exit 1
fi

if [[ ! -f dist/server/server/src/index.js || ! -f dist/web/index.html ]]; then
  printf 'cairo-web: built runtime missing in %s; run scripts/build-web-agent.sh or reinstall the package\n' "$WEB_AGENT_DIR" >&2
  exit 1
fi

exec node dist/server/server/src/index.js "$@"
