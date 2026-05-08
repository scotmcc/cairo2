#!/usr/bin/env bash
# pre-package.sh - Run lint and tests before packaging.
#
# Invoked by build-packages.sh unless --skip-tests is passed. Runs go vet,
# go test, and the web-agent vitest suite. Exits non-zero on any failure so
# build-packages.sh aborts before producing a broken artifact.
#
# Usage:
#   bash scripts/packaging/pre-package.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

info()    { echo "  [info]  $*" >&2; }
success() { echo "  [ ok ]  $*" >&2; }
die()     { echo "  [error] $*" >&2; exit 1; }

cd "$REPO_ROOT"

echo "" >&2
echo "Running pre-package checks..." >&2
echo "-----------------------------" >&2

info "go vet ./..."
go vet ./... || die "go vet failed"

info "go test ./..."
go test ./... || die "go test failed"

if [[ -d "$REPO_ROOT/web-agent" ]]; then
    info "web-agent: npm test"
    (cd "$REPO_ROOT/web-agent" && npm test --silent) || die "web-agent tests failed"
fi

success "Pre-package checks passed"
