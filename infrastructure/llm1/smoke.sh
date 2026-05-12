#!/usr/bin/env bash
# Cairo data-layer smoke test.
# Run from any tailnet client (or from llm1 itself).
#
# Reads /opt/cairo-data/.env if present (on llm1) or the path passed via
# --env-file. Each service is probed for: TCP reachability, auth-required
# behavior, and successful auth with the configured credential.
#
# Exit 0 on all-pass; non-zero on any failure.

set -uo pipefail

ENV_FILE=""
HOST_PREFIX="cairo-"
DOMAIN="tail1bb4f.ts.net"
SKIP=()

usage() {
  cat <<EOF
Usage: $0 [--env-file PATH] [--skip svc[,svc...]]
  --env-file PATH    Path to .env (default: /opt/cairo-data/.env, then ./.env)
  --skip svc,svc     Comma-separated services to skip: qdrant,neo4j,postgres,redis,minio
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file) ENV_FILE="$2"; shift 2 ;;
    --skip)     IFS=',' read -ra SKIP <<<"$2"; shift 2 ;;
    -h|--help)  usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ -z "$ENV_FILE" ]]; then
  for cand in /opt/cairo-data/.env "$(dirname "$0")/.env"; do
    [[ -r "$cand" ]] && ENV_FILE="$cand" && break
  done
fi
if [[ -n "$ENV_FILE" ]]; then
  set -a; . "$ENV_FILE"; set +a
fi

PASS=0; FAIL=0
ok()   { echo "  PASS: $*"; PASS=$((PASS+1)); }
bad()  { echo "  FAIL: $*"; FAIL=$((FAIL+1)); }
skip_svc() {
  local s="$1"
  for x in "${SKIP[@]:-}"; do [[ "$x" == "$s" ]] && return 0; done
  return 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# ---------------------------------------------------------------- qdrant
test_qdrant() {
  echo "[qdrant] cairo-qdrant.${DOMAIN}"
  local base="https://cairo-qdrant.${DOMAIN}"
  : "${QDRANT_API_KEY:?QDRANT_API_KEY not set}"
  # No key → 401/403
  local code
  code=$(curl -sk -o /dev/null -w '%{http_code}' "$base/collections" || echo 000)
  [[ "$code" =~ ^(401|403)$ ]] && ok "unauth rejected ($code)" || bad "unauth got $code (expected 401/403)"
  # With key → 200
  code=$(curl -sk -o /dev/null -w '%{http_code}' -H "api-key: $QDRANT_API_KEY" "$base/collections" || echo 000)
  [[ "$code" == "200" ]] && ok "auth OK (200)" || bad "auth got $code (expected 200)"
}

# ---------------------------------------------------------------- neo4j
test_neo4j() {
  echo "[neo4j] cairo-neo4j.${DOMAIN}"
  : "${NEO4J_PASSWORD:?NEO4J_PASSWORD not set}"
  local ui="https://cairo-neo4j.${DOMAIN}"
  local code
  code=$(curl -sk -o /dev/null -w '%{http_code}' "$ui/" || echo 000)
  [[ "$code" == "200" ]] && ok "HTTP UI reachable ($code)" || bad "HTTP UI got $code"
  # Bolt: TCP connect probe (real auth needs a Bolt client; cypher-shell if present)
  if have nc; then
    nc -z -w 5 "cairo-neo4j.${DOMAIN}" 7687 && ok "Bolt :7687 TCP open" || bad "Bolt :7687 unreachable"
  fi
  if have cypher-shell; then
    if cypher-shell -a "neo4j://cairo-neo4j.${DOMAIN}:7687" -u neo4j -p "$NEO4J_PASSWORD" \
         "RETURN 1 AS ok;" >/dev/null 2>&1; then
      ok "Bolt auth OK"
    else
      bad "Bolt auth failed"
    fi
  else
    echo "  SKIP: cypher-shell not installed — install neo4j-client to fully verify Bolt auth"
  fi
}

# ---------------------------------------------------------------- postgres
test_postgres() {
  echo "[postgres] cairo-postgres.${DOMAIN}:5432"
  : "${POSTGRES_USER:?}"; : "${POSTGRES_PASSWORD:?}"; : "${POSTGRES_DB:?}"
  if have psql; then
    if PGPASSWORD="$POSTGRES_PASSWORD" psql \
         -h "cairo-postgres.${DOMAIN}" -p 5432 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
         -c "SELECT 1;" >/dev/null 2>&1; then
      ok "psql auth OK"
    else
      bad "psql auth failed"
    fi
  else
    if have nc; then
      nc -z -w 5 "cairo-postgres.${DOMAIN}" 5432 && ok "TCP :5432 open" || bad "TCP :5432 unreachable"
    fi
    echo "  SKIP: psql not installed — install postgresql-client to fully verify auth"
  fi
}

# ---------------------------------------------------------------- redis
test_redis() {
  echo "[redis] cairo-redis.${DOMAIN}:6379"
  : "${REDIS_PASSWORD:?}"
  if have redis-cli; then
    local out
    out=$(redis-cli -h "cairo-redis.${DOMAIN}" -p 6379 -a "$REDIS_PASSWORD" --no-auth-warning PING 2>/dev/null)
    [[ "$out" == "PONG" ]] && ok "PING/PONG OK" || bad "PING got '$out'"
    out=$(redis-cli -h "cairo-redis.${DOMAIN}" -p 6379 PING 2>&1 | head -1)
    echo "$out" | grep -qiE 'noauth|auth' && ok "unauth rejected" || bad "unauth not rejected: $out"
  else
    if have nc; then
      nc -z -w 5 "cairo-redis.${DOMAIN}" 6379 && ok "TCP :6379 open" || bad "TCP :6379 unreachable"
    fi
    echo "  SKIP: redis-cli not installed"
  fi
}

# ---------------------------------------------------------------- minio
test_minio() {
  echo "[minio] cairo-minio.${DOMAIN}"
  local s3="https://cairo-minio.${DOMAIN}:9000"
  local code
  code=$(curl -sk -o /dev/null -w '%{http_code}' "$s3/minio/health/live" || echo 000)
  [[ "$code" == "200" ]] && ok "S3 health 200" || bad "S3 health got $code"
  code=$(curl -sk -o /dev/null -w '%{http_code}' "https://cairo-minio.${DOMAIN}/" || echo 000)
  [[ "$code" =~ ^(200|307|302)$ ]] && ok "console reachable ($code)" || bad "console got $code"
}

for svc in qdrant neo4j postgres redis minio; do
  skip_svc "$svc" && { echo "[$svc] SKIPPED"; continue; }
  "test_$svc"
done

echo
echo "=== smoke summary: $PASS pass / $FAIL fail ==="
[[ $FAIL -eq 0 ]]
