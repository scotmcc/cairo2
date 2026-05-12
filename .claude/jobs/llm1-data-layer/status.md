# Status — llm1-data-layer

**Updated:** 2026-05-12
**Owner:** Selene (llm1)
**Phase:** SCAFFOLD COMPLETE — awaiting TS authkeys to bring stack up

## Where we are

Scaffolding is committed on branch `llm1-data-layer`. The compose, ts-config, smoke, backup, and README are ready. Nothing is running on llm1 yet.

## What's done

- [x] Repo cloned to `~/cairo2-on-llm1/`, branch `llm1-data-layer`
- [x] Re-probed llm1 state — matches briefing
- [x] Inspected existing `vllm-qwen-*` TS sidecar pattern (uses `network_mode: host`); diverged to `network_mode: service:<svc>` per briefing — rationale in `research.md`
- [x] Authored `docker-compose.yml`, 5 ts-config JSONs, `.env.example`
- [x] Authored `smoke.sh`, `backup.sh` (stub), `README.md` with endpoint table

## Blocked on Scot

1. Generate **5 Tailscale auth keys** in the admin console:
   - Reusable, ephemeral, tagged `tag:cairo-data`
   - One key each for: qdrant, neo4j, postgres, redis, minio
2. Confirm the **tailnet ACL** permits `tag:cairo-data` (tagOwners + an accept rule from whichever client tag/identity reaches the data layer). See README §Tailnet ACL.
3. Drop the keys + 5 strong passwords into `/opt/cairo-data/.env` (template from `.env.example`).

## Next actions (once unblocked)

1. `mkdir -p /opt/cairo-data/...` (data tree)
2. `docker compose --env-file /opt/cairo-data/.env up -d` from `infrastructure/llm1/`
3. Verify gates 1–6 from briefing
4. Run `smoke.sh` from llm1, then ask Scot to run from the Linux tower
5. Update this file to `DONE — pending cairo2-Selene wiring` and write the final endpoint table here for the tower-Selene to consume

## Endpoint table (pre-deployment — final values land here after gates pass)

| Service | Tailnet hostname:port | Credential env var |
|---|---|---|
| Qdrant HTTP | cairo-qdrant.tail1bb4f.ts.net:443 | QDRANT_API_KEY |
| Qdrant gRPC | cairo-qdrant.tail1bb4f.ts.net:6334 | QDRANT_API_KEY |
| Neo4j Bolt | cairo-neo4j.tail1bb4f.ts.net:7687 | neo4j / NEO4J_PASSWORD |
| Neo4j UI | cairo-neo4j.tail1bb4f.ts.net:443 | neo4j / NEO4J_PASSWORD |
| Postgres | cairo-postgres.tail1bb4f.ts.net:5432 | POSTGRES_USER/PASSWORD/DB |
| Redis | cairo-redis.tail1bb4f.ts.net:6379 | REDIS_PASSWORD |
| MinIO S3 | cairo-minio.tail1bb4f.ts.net:9000 | MINIO_ROOT_USER/PASSWORD |
| MinIO console | cairo-minio.tail1bb4f.ts.net:443 | same |
