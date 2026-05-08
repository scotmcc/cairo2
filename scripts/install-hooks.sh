#!/bin/sh
set -e
REPO_ROOT="$(git rev-parse --show-toplevel)"
cp "$REPO_ROOT/.githooks/pre-commit" "$REPO_ROOT/.git/hooks/pre-commit"
chmod +x "$REPO_ROOT/.git/hooks/pre-commit"
echo "installed .githooks/pre-commit -> .git/hooks/pre-commit"
