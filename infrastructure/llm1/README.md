# Cairo Data Layer — llm1

Shared data plane for cairo. Runs on **llm1** (`100.97.111.72`), exposed only on the tailnet (`tail1bb4f.ts.net`) via per-service Tailscale sidecars. No host ports are bound.

## Endpoint table

| Service | Tailnet hostname | Port | Protocol | Credential env var |
|---|---|---|---|---|
| Qdrant (HTTP) | `cairo-qdrant.tail1bb4f.ts.net` | 443 | HTTPS → 6333 | `QDRANT_API_KEY` (sent as `api-key:` header) |
| Qdrant (gRPC) | `cairo-qdrant.tail1bb4f.ts.net` | 6334 | gRPC (plain) | `QDRANT_API_KEY` (metadata) |
| Neo4j (HTTP UI) | `cairo-neo4j.tail1bb4f.ts.net` | 443 | HTTPS → 7474 | basic auth: `neo4j` / `NEO4J_PASSWORD` |
| Neo4j (Bolt) | `cairo-neo4j.tail1bb4f.ts.net` | 7687 | Bolt (plain TCP) | `neo4j` / `NEO4J_PASSWORD` |
| Postgres | `cairo-postgres.tail1bb4f.ts.net` | 5432 | Postgres wire | `POSTGRES_USER` / `POSTGRES_PASSWORD` / `POSTGRES_DB` |
| Redis | `cairo-redis.tail1bb4f.ts.net` | 6379 | RESP | `REDIS_PASSWORD` (AUTH) |
| MinIO (S3 API) | `cairo-minio.tail1bb4f.ts.net` | 9000 | HTTPS over plain TCP fwd | `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD` *(create per-app keys in the console)* |
| MinIO (console) | `cairo-minio.tail1bb4f.ts.net` | 443 | HTTPS → 9001 | same |

Tailnet is the encryption layer for plain-TCP services (Bolt, Postgres, Redis, Qdrant gRPC). Don't terminate extra TLS in clients.

## Quick client examples

```bash
# Qdrant
curl -H "api-key: $QDRANT_API_KEY" https://cairo-qdrant.tail1bb4f.ts.net/collections

# Neo4j (Bolt)
cypher-shell -a neo4j://cairo-neo4j.tail1bb4f.ts.net:7687 -u neo4j -p "$NEO4J_PASSWORD"

# Postgres
PGPASSWORD="$POSTGRES_PASSWORD" psql -h cairo-postgres.tail1bb4f.ts.net -U cairo -d cairo

# Redis
redis-cli -h cairo-redis.tail1bb4f.ts.net -p 6379 -a "$REDIS_PASSWORD" PING

# MinIO (mc)
mc alias set cairo https://cairo-minio.tail1bb4f.ts.net:9000 "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD"
mc ls cairo
```

## How to bring this up from scratch

Prereqs on llm1: Docker (28.x), `/opt/cairo-data/` writable by the user running compose, five Tailscale auth keys (reusable + ephemeral, tagged `tag:cairo-data`).

1. `git clone git@github.com:scotmcc/cairo2.git ~/cairo2-on-llm1`
2. `mkdir -p /opt/cairo-data/{qdrant/{storage,snapshots},neo4j/{data,logs,import,plugins},postgres/data,redis/data,minio/data,tailscale/{qdrant,neo4j,postgres,redis,minio},backups}`
3. `cp ~/cairo2-on-llm1/infrastructure/llm1/.env.example /opt/cairo-data/.env`
4. Fill `/opt/cairo-data/.env`: TS authkeys (5), service passwords (5). `chmod 0600 /opt/cairo-data/.env`.
5. From `~/cairo2-on-llm1/infrastructure/llm1/`:
   ```
   docker compose --env-file /opt/cairo-data/.env up -d
   ```
6. Verify: `docker compose ps` shows 10 containers healthy (5 services + 5 TS sidecars).
7. Verify tailnet: `tailscale status | grep cairo-` shows 5 entries.
8. Smoke from a tailnet client: `./smoke.sh --env-file /path/to/.env-with-secrets`.

## Layout

```
infrastructure/llm1/
├── README.md            (this file)
├── docker-compose.yml   the stack
├── .env.example         every key, no secrets
├── ts-config/           per-service Tailscale Serve configs (TCPForward / Web)
│   ├── qdrant.json
│   ├── neo4j.json
│   ├── postgres.json
│   ├── redis.json
│   └── minio.json
├── smoke.sh             run from any tailnet client; per-service pass/fail
└── backup.sh            stub — pg_dump, qdrant snapshot, redis BGSAVE; neo4j+minio TODO
```

## Operational notes

- **Never `docker compose down`** if you care about anything in the same compose project beyond this stack. (No collision here — this is its own compose project `cairo-data` — but the convention is to `docker compose stop` and `docker compose start` so volumes and TS state stay put.)
- **Restarts.** TS state lives under `/opt/cairo-data/tailscale/<svc>/`. Container restarts re-authenticate from cached state. Auth-key consumption only occurs on first boot per machine identity.
- **Backups.** `backup.sh` is a stub. Postgres, Qdrant, Redis are wired; Neo4j (community) and MinIO are TODO. Cron-scheduling: Scot's call.
- **Logging.** All services use json-file driver, 50MB × 3 rotation.

## Tailnet ACL — Scot to verify

For `cairo-*` machines to join the tailnet with `tag:cairo-data`, the ACL needs (paraphrased):

```
"tagOwners": {
  "tag:cairo-data": ["autogroup:admin"]
},
"acls": [
  { "action": "accept",
    "src": ["tag:cairo-client", "autogroup:admin"],
    "dst": ["tag:cairo-data:*"] }
]
```

Substitute whatever client tag/identity is appropriate for cairo agents.
