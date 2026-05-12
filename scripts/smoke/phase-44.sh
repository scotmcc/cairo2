#!/usr/bin/env bash
# scripts/smoke/phase-44.sh — phase 4.4 regression smoke (--no-tsnet identity path)
#
# Exercises the X-Operator-Identity header path on the admin listener.
# Tsnet identity is covered by unit tests in internal/authn/ (no real tailnet needed).
# Exit 0 = PASS.

set -euo pipefail

REPO="$(cd "$(dirname "$0")/../.." && pwd)"

REG_PORT=18082
ADMIN_PORT=18083
STATE_DIR="/tmp/cairo-44.state"
REG_STATE="${STATE_DIR}/registry"

REGPID=""

cleanup() {
    [[ -n "${REGPID:-}" ]] && kill "${REGPID}" 2>/dev/null || true
    [[ -n "${REGPID:-}" ]] && wait "${REGPID}" 2>/dev/null || true
    rm -rf "${STATE_DIR}"
}
trap cleanup EXIT

echo "==> build"
bash "${REPO}/scripts/build.sh"

rm -rf "${STATE_DIR}"
mkdir -p "${REG_STATE}"

echo "==> start cairo-registry (no-tsnet, port=${REG_PORT}, admin=${ADMIN_PORT})"
"${REPO}/bin/cairo-registry" \
    --no-tsnet \
    --addr ":${REG_PORT}" \
    --admin-addr "127.0.0.1:${ADMIN_PORT}" \
    --state-dir "${REG_STATE}" \
    --bootstrap-super-admin admin \
    >"${STATE_DIR}/registry.log" 2>&1 &
REGPID=$!

for i in {1..50}; do
    if curl -sSf "http://localhost:${REG_PORT}/healthz" >/dev/null 2>&1; then break; fi
    sleep 0.1
    kill -0 "${REGPID}" 2>/dev/null || { cat "${STATE_DIR}/registry.log" >&2; echo "FAIL: registry exited before /healthz" >&2; exit 1; }
done
curl -sSf "http://localhost:${REG_PORT}/healthz" >/dev/null || { echo "FAIL: /healthz never came up" >&2; exit 1; }

# smoke 1: admin listener returns parseable JSON with X-Operator-Identity header
echo "==> smoke 1: admin /agents with X-Operator-Identity: admin"
COUNT=$(curl -sf -H "X-Operator-Identity: admin" "http://127.0.0.1:${ADMIN_PORT}/agents" | jq 'length')
echo "✓ smoke 1: GET /agents returned JSON (length=${COUNT})"

# smoke 2: admin action writes audit entry with actor=admin, not "local"
echo "==> smoke 2: audit actor records header identity"
curl -sf \
    -H "X-Operator-Identity: admin" \
    -H "Content-Type: application/json" \
    -d '{"name":"smoke-44"}' \
    "http://127.0.0.1:${ADMIN_PORT}/departments" >/dev/null
ACTOR=$(curl -sf -H "X-Operator-Identity: admin" "http://127.0.0.1:${ADMIN_PORT}/audit" | jq -r '.[0].actor')
[[ "${ACTOR}" == "admin" ]] || { echo "FAIL: audit actor expected 'admin', got '${ACTOR}'" >&2; exit 1; }
echo "✓ smoke 2: audit actor='${ACTOR}' (not 'local')"

# smoke 3: skipped — registration path owner extraction via X-Operator-Identity requires a
# running cairo serve, which needs a live Ollama endpoint unavailable in local smoke.
# Full tsnet-mode identity coverage is in internal/authn unit tests (no real tailnet needed).

echo
echo "phase 4.4 smoke: PASS"
