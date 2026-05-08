#!/usr/bin/env bash
# scripts/smoke/registry-client.sh — end-to-end smoke for registry-client (Phase 1C)
#
# Validates: cairo registers with cairo-registry, WS held (ws_connected=1),
# drops to stale after kill + 100s sweep window, resumes same agent_id on restart.
# Uses isolated ports and state dirs; safe to run alongside anything.
#
# Requires: go, jq, python3 (for stub LLM server).
# Exit code 0 = PASS. Non-zero = FAIL.
#
# NOTE: This smoke test requires a working cairo-registry server implementation.
# cmd/cairo-registry in cairo2 is currently a stub. This smoke will fail at the
# /healthz check until Phase 2.1 (registry server merge) is complete.
# The build command below (CAIRO_REPO instead of REGISTRY_REPO) is the only structural
# change from the cairo version. Everything else is the correct target behavior.

set -euo pipefail

REGISTRY_PORT=18080
CAIRO_PORT=18337
LLM_PORT=18338
REGISTRY_URL="http://localhost:${REGISTRY_PORT}"

STATE_DIR="$(mktemp -d -t cairo-registry-client-smoke.XXXXXX)"
REGISTRY_LOG="${STATE_DIR}/registry.log"
CAIRO_LOG="${STATE_DIR}/cairo.log"
CAIRO_HOME="${STATE_DIR}/cairo-home"
mkdir -p "${CAIRO_HOME}"

CAIRO_REPO="$(cd "$(dirname "$0")/../.." && pwd)"

REGISTRY_BIN="${STATE_DIR}/cairo-registry"
CAIRO_BIN="${STATE_DIR}/cairo"

REGPID=""
CAIROPID=""
LLMPID=""

cleanup() {
    [[ -n "${CAIROPID:-}" ]] && kill "${CAIROPID}" 2>/dev/null || true
    [[ -n "${CAIROPID:-}" ]] && wait "${CAIROPID}" 2>/dev/null || true
    [[ -n "${REGPID:-}" ]]  && kill "${REGPID}"  2>/dev/null || true
    [[ -n "${REGPID:-}" ]]  && wait "${REGPID}"  2>/dev/null || true
    [[ -n "${LLMPID:-}" ]]  && kill "${LLMPID}"  2>/dev/null || true
    [[ -n "${LLMPID:-}" ]]  && wait "${LLMPID}"  2>/dev/null || true
    if [[ "${KEEP_STATE:-0}" != "1" ]]; then
        rm -rf "${STATE_DIR}"
    else
        echo "state dir kept: ${STATE_DIR}"
    fi
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }
ok()   { echo "  ok: $*"; }

echo "==> build cairo-registry"
(cd "${CAIRO_REPO}" && go build -o "${REGISTRY_BIN}" ./cmd/cairo-registry)
ok "cairo-registry built"

echo "==> build cairo"
(cd "${CAIRO_REPO}" && go build -o "${CAIRO_BIN}" ./cmd/cairo)
ok "cairo built"

# Write and start a minimal stub LLM server so cairo can pass its Ping() check.
# cairo serve calls GET /health; the stub replies 200. No actual LLM calls happen
# during the registration smoke — only the ping at startup requires a live response.
echo "==> start stub LLM server (port=${LLM_PORT})"
LLM_STUB="${STATE_DIR}/llm_stub.py"
printf '%s\n' \
    'import sys, http.server, socketserver' \
    '' \
    'class H(http.server.BaseHTTPRequestHandler):' \
    '    def do_GET(self):' \
    '        self.send_response(200); self.end_headers(); self.wfile.write(b"ok")' \
    '    def do_POST(self):' \
    '        n = int(self.headers.get("content-length", 0)); self.rfile.read(n)' \
    '        self.send_response(200)' \
    '        self.send_header("Content-Type", "application/json"); self.end_headers()' \
    '        self.wfile.write(b"{}")' \
    '    def log_message(self, *a): pass' \
    '' \
    'socketserver.TCPServer.allow_reuse_address = True' \
    'port = int(sys.argv[1])' \
    'with socketserver.TCPServer(("", port), H) as httpd:' \
    '    httpd.serve_forever()' \
    >"${LLM_STUB}"
python3 "${LLM_STUB}" "${LLM_PORT}" >"${STATE_DIR}/llm.log" 2>&1 &
LLMPID=$!

for i in {1..50}; do
    if curl -sSf "http://localhost:${LLM_PORT}/health" >/dev/null 2>&1; then break; fi
    sleep 0.1
    kill -0 "${LLMPID}" 2>/dev/null || { cat "${STATE_DIR}/llm.log" >&2; fail "stub LLM exited before responding"; }
done
curl -sSf "http://localhost:${LLM_PORT}/health" >/dev/null || fail "stub LLM never came up"
ok "stub LLM /health responding"

echo "==> start cairo-registry (state=${STATE_DIR}, port=${REGISTRY_PORT})"
"${REGISTRY_BIN}" --no-tsnet --addr ":${REGISTRY_PORT}" --state-dir "${STATE_DIR}/registry" >"${REGISTRY_LOG}" 2>&1 &
REGPID=$!

for i in {1..50}; do
    if curl -sSf "${REGISTRY_URL}/healthz" >/dev/null 2>&1; then break; fi
    sleep 0.1
    kill -0 "${REGPID}" 2>/dev/null || { cat "${REGISTRY_LOG}" >&2; fail "registry exited before /healthz"; }
done
curl -sSf "${REGISTRY_URL}/healthz" >/dev/null || fail "/healthz never came up"
ok "registry /healthz responding"

echo "==> start cairo serve --register"
OLLAMA_URL="http://localhost:${LLM_PORT}" HOME="${CAIRO_HOME}" \
    "${CAIRO_BIN}" serve --register "${REGISTRY_URL}" --port "${CAIRO_PORT}" >"${CAIRO_LOG}" 2>&1 &
CAIROPID=$!

# serve.go prints only first 8 chars of agent_id; resolve full UUID from /agents.
AGENT_PREFIX=""
for i in {1..50}; do
    if grep -q "registry:" "${CAIRO_LOG}" 2>/dev/null; then
        AGENT_PREFIX=$(grep "registry:" "${CAIRO_LOG}" | grep -o 'agent_id=[^ )]*' | cut -d= -f2 | head -1)
        break
    fi
    sleep 0.2
    kill -0 "${CAIROPID}" 2>/dev/null || { cat "${CAIRO_LOG}" >&2; fail "cairo exited before registering"; }
done
[[ -n "${AGENT_PREFIX}" ]] || fail "no agent_id captured from cairo log"

LIST=$(curl -sSf "${REGISTRY_URL}/agents")
AGENT_ID=$(echo "${LIST}" | jq -r --arg pfx "${AGENT_PREFIX}" '.[] | select(.agent_id | startswith($pfx)) | .agent_id')
[[ -n "${AGENT_ID}" ]] || fail "could not resolve full agent_id for prefix ${AGENT_PREFIX}: ${LIST}"
ok "cairo registered: agent_id=${AGENT_ID}"

echo "==> verify ws_connected=1, status=active"
LIST=$(curl -sSf "${REGISTRY_URL}/agents")
WS=$(echo "${LIST}" | jq --arg id "${AGENT_ID}" '.[] | select(.agent_id == $id) | .ws_connected')
STATUS=$(echo "${LIST}" | jq -r --arg id "${AGENT_ID}" '.[] | select(.agent_id == $id) | .status')
[[ "${WS}" == "1" || "${WS}" == "true" ]] || fail "expected ws_connected=1, got '${WS}'"
[[ "${STATUS}" == "active" ]] || fail "expected status=active, got ${STATUS}"
ok "ws_connected=1, status=active"

echo "==> kill cairo, wait 130s for sweeper (threshold=90s, ticker=30s)"
kill "${CAIROPID}" 2>/dev/null || true
wait "${CAIROPID}" 2>/dev/null || true
CAIROPID=""
sleep 130

STATUS=$(curl -sSf "${REGISTRY_URL}/agents" | jq -r --arg id "${AGENT_ID}" '.[] | select(.agent_id == $id) | .status')
[[ "${STATUS}" == "stale" ]] || fail "expected status=stale after sweep, got ${STATUS}"
ok "status=stale after kill + 100s"

echo "==> restart cairo, verify same agent_id + active"
OLLAMA_URL="http://localhost:${LLM_PORT}" HOME="${CAIRO_HOME}" \
    "${CAIRO_BIN}" serve --register "${REGISTRY_URL}" --port "${CAIRO_PORT}" >"${CAIRO_LOG}.2" 2>&1 &
CAIROPID=$!

AGENT_PREFIX2=""
for i in {1..50}; do
    if grep -q "registry:" "${CAIRO_LOG}.2" 2>/dev/null; then
        AGENT_PREFIX2=$(grep "registry:" "${CAIRO_LOG}.2" | grep -o 'agent_id=[^ )]*' | cut -d= -f2 | head -1)
        break
    fi
    sleep 0.2
    kill -0 "${CAIROPID}" 2>/dev/null || { cat "${CAIRO_LOG}.2" >&2; fail "cairo exited on restart"; }
done
[[ -n "${AGENT_PREFIX2}" ]] || fail "no agent_id on restart"
[[ "${AGENT_PREFIX2}" == "${AGENT_PREFIX}" ]] || \
    fail "agent_id changed on restart: ${AGENT_PREFIX} -> ${AGENT_PREFIX2}"
ok "same agent_id on restart (prefix: ${AGENT_PREFIX2})"

LIST=$(curl -sSf "${REGISTRY_URL}/agents")
WS=$(echo "${LIST}" | jq --arg id "${AGENT_ID}" '.[] | select(.agent_id == $id) | .ws_connected')
STATUS=$(echo "${LIST}" | jq -r --arg id "${AGENT_ID}" '.[] | select(.agent_id == $id) | .status')
[[ "${WS}" == "1" || "${WS}" == "true" ]] || fail "expected ws_connected=1 after restart, got '${WS}'"
[[ "${STATUS}" == "active" ]] || fail "expected status=active after restart, got ${STATUS}"
ok "ws_connected=1, status=active after restart"

# Phase 2B: verify client sends stored agent_id on restart and registry honors it.
# Note: the hostname-change scenario (where stored id prevents orphan rows) is proven
# by the registry-side TestRegister_StableAgentID subtests and scripts/smoke/2b.sh.
# This phase covers the client-sending-the-id path: same host, same stored id, no new row.
echo "==> Phase 2B: verify agent_id is reused on restart (client sends stored id)"
CAIRO_DB="${CAIRO_HOME}/.cairo/cairo.db"

ID_BEFORE=$(sqlite3 "${CAIRO_DB}" "SELECT agent_id FROM registrations WHERE registry_url='${REGISTRY_URL}'" 2>/dev/null || true)
[[ -n "${ID_BEFORE}" ]] || fail "no stored agent_id in sqlite before Phase 2B restart"
ok "stored agent_id before restart: ${ID_BEFORE}"

kill "${CAIROPID}" 2>/dev/null || true
wait "${CAIROPID}" 2>/dev/null || true
CAIROPID=""

COUNT_BEFORE=$(curl -sSf "${REGISTRY_URL}/agents" | jq 'length')
[[ "${COUNT_BEFORE}" == "1" ]] || fail "expected 1 agent row before Phase 2B restart, got ${COUNT_BEFORE}"

OLLAMA_URL="http://localhost:${LLM_PORT}" HOME="${CAIRO_HOME}" \
    "${CAIRO_BIN}" serve --register "${REGISTRY_URL}" --port "${CAIRO_PORT}" >"${CAIRO_LOG}.3" 2>&1 &
CAIROPID=$!

for i in {1..50}; do
    if grep -q "registry:" "${CAIRO_LOG}.3" 2>/dev/null; then break; fi
    sleep 0.2
    kill -0 "${CAIROPID}" 2>/dev/null || { cat "${CAIRO_LOG}.3" >&2; fail "cairo exited on Phase 2B restart"; }
done
grep -q "registry:" "${CAIRO_LOG}.3" || fail "cairo did not register in Phase 2B restart"

ID_AFTER=$(sqlite3 "${CAIRO_DB}" "SELECT agent_id FROM registrations WHERE registry_url='${REGISTRY_URL}'" 2>/dev/null || true)
[[ "${ID_AFTER}" == "${ID_BEFORE}" ]] || fail "agent_id changed across restart: ${ID_BEFORE} -> ${ID_AFTER}"
ok "agent_id stable across restart: ${ID_AFTER}"

COUNT_AFTER=$(curl -sSf "${REGISTRY_URL}/agents" | jq 'length')
[[ "${COUNT_AFTER}" == "1" ]] || fail "expected 1 agent row after Phase 2B restart, got ${COUNT_AFTER}"
ok "registry still has 1 row (no orphan created)"

echo
echo "PASS — Phase 1C + Phase 2B registry-client smoke"
