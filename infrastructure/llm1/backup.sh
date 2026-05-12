#!/usr/bin/env bash
# Cairo data-layer backup — STUB.
# Run from llm1 (uses docker exec; doesn't need tailnet).
#
# Writes to /opt/cairo-data/backups/<service>/<UTC-timestamp>/.
# Retention/rotation: NOT implemented yet — Scot's call when to add restic + offsite.

set -euo pipefail

ENV_FILE="${ENV_FILE:-/opt/cairo-data/.env}"
[[ -r "$ENV_FILE" ]] && { set -a; . "$ENV_FILE"; set +a; }

TS=$(date -u +%Y%m%dT%H%M%SZ)
ROOT="/opt/cairo-data/backups"
log() { echo "[backup $TS] $*"; }

mkdir -p "$ROOT"/{postgres,neo4j,qdrant,redis,minio}

# ---- postgres ---------------------------------------------------------------
log "postgres: pg_dump"
docker exec -e PGPASSWORD="${POSTGRES_PASSWORD}" cairo-postgres \
  pg_dump -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" -Fc \
  > "$ROOT/postgres/${TS}.dump"

# ---- neo4j ------------------------------------------------------------------
# Online dump requires Neo4j Enterprise; community needs the DB stopped, OR you
# can use APOC's apoc.export.* into /var/lib/neo4j/import. Quick-and-dirty:
# tar the data dir while the DB is quiesced. Skipped here; placeholder.
log "neo4j: SKIPPED (community-edition online dump requires APOC export — wire up later)"

# ---- qdrant -----------------------------------------------------------------
log "qdrant: snapshot via API"
if [[ -n "${QDRANT_API_KEY:-}" ]]; then
  docker exec cairo-qdrant sh -c \
    "curl -fsS -X POST -H 'api-key: ${QDRANT_API_KEY}' http://127.0.0.1:6333/snapshots" \
    > "$ROOT/qdrant/${TS}.snapshot.json" || log "qdrant snapshot failed"
fi

# ---- redis ------------------------------------------------------------------
log "redis: BGSAVE + copy dump.rdb + appendonly"
docker exec cairo-redis sh -c "redis-cli -a \"\$REDIS_PASSWORD\" --no-auth-warning BGSAVE" >/dev/null
sleep 2
cp -a /opt/cairo-data/redis/data/dump.rdb       "$ROOT/redis/${TS}.dump.rdb" 2>/dev/null || true
cp -a /opt/cairo-data/redis/data/appendonlydir  "$ROOT/redis/${TS}.aof"      2>/dev/null || true

# ---- minio ------------------------------------------------------------------
# `mc mirror` is the right tool; not bundled here. Placeholder.
log "minio: SKIPPED (install mc client and mirror to offsite target — wire up later)"

log "DONE — see $ROOT"
