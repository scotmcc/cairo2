# Briefing — Cairo Data Layer on llm1

**To:** Selene, running on `llm1.tail1bb4f.ts.net`
**From:** Selene, running on the Linux tower (`/home/scot/cairo2`)
**Date:** 2026-05-12
**Status:** new job — no prior work on llm1

## Who you are, where you are

You are Selene, the same one Scot collaborates with on cairo. You're starting cold on the **llm1** box. Read `~/.claude/CLAUDE.md` first if you haven't this session — your global working instructions apply (state lives in the repo, soft when it fits, don't perform deference). Then read this briefing in full before touching anything.

The other Selene (on the Linux tower) is continuing cairo work in parallel. You and she collaborate via the **cairo2 repo** — what you produce on llm1, you commit and push; what she needs to verify, she pulls. Don't try to message her live; she'll read your artifacts when you're done with a step.

## The big picture

Cairo is heading into **Milestone 5 — Knowledge Federation**. The plan is to add a `VectorStore` interface (Phase 5.1), then a Qdrant connector (5.2), then federation tiers (5.3+). For any of that to be real, cairo agents — running on any of Scot's boxes — need a shared **data layer** to reach back to. That data layer lives on **llm1**.

Your job is to stand up that data layer as docker containers on llm1, exposed only on the tailnet, with one tailscale hostname per service, and to produce artifacts in the cairo2 repo that document:

1. **What's running, where, on what tailnet hostname/port** — so the cairo2 Selene can wire client config keys.
2. **How to recreate it from scratch** — `docker-compose.yml` and an `.env.example` checked into the repo.
3. **A smoke-test script** the cairo2 Selene can run from her box to verify each endpoint is reachable and authenticated.

You are **not** writing cairo code. You are not modifying anything under `internal/`. You stand up infrastructure, document it, and hand off.

## State of llm1 (probed 2026-05-12)

- **OS:** RHEL 9.6 (Plow), Linux 5.14
- **Hardware:** 1.5 TiB RAM, x86_64
- **Docker:** 28.3.3 (client + server), storage driver `overlay2`, **Docker Root Dir: `/opt/docker`**
- **Disk:**
  - `/` — 70 G, only **19 G free** — do NOT use for data
  - `/home` — 500 G, 247 G free — personal stuff, avoid
  - `/opt` — **4.9 T, 2.0 T free** — this is your home for data, matches existing convention (Docker is already rooted here)
- **Existing stacks (do not disturb):**
  - `vllm-qwen-*` — vllm strong/embed models + litellm + postgres + tailscale sidecar
  - `kokoro-tts-gpu-*` — TTS + nginx + tailscale sidecar
- **Port collisions:**
  - `127.0.0.1:5432` is held by `vllm-qwen-postgres-1`. Our postgres must **not** bind a host port — expose via tailnet only (same pattern as the existing stacks).
- **Tailscale pattern in use:** every stack has its own `tailscale/tailscale:latest` sidecar with `TS_AUTHKEY`, `TS_HOSTNAME`, and `TS_SERVE_CONFIG`. Mirror this exactly per service.

## Constraints & conventions

- **Data path:** all bind mounts under `/opt/cairo-data/<service>/`. Create this tree on first run.
- **Tailnet exposure:** one hostname per service. Suggested names:
  - `cairo-qdrant.tail1bb4f.ts.net` — Qdrant HTTP (6333) + gRPC (6334)
  - `cairo-neo4j.tail1bb4f.ts.net` — Bolt (7687) + HTTP UI (7474)
  - `cairo-postgres.tail1bb4f.ts.net` — Postgres (5432) *(host port not bound; tailnet only)*
  - `cairo-redis.tail1bb4f.ts.net` — Redis (6379)
  - `cairo-minio.tail1bb4f.ts.net` — S3 API (9000) + console (9001) *(see Open Questions below — strike if Scot doesn't want it)*
- **No host port binds.** Services bind only on the internal docker network and are exposed to the tailnet via the per-service tailscale sidecar (TCP serve mode for raw protocols like Bolt/Postgres/Redis; HTTPS serve for HTTP APIs is fine too).
- **Secrets:** `.env` on llm1 only (gitignored). `.env.example` checked into the cairo2 repo enumerating every key. No real secrets in the repo, ever.
- **Auth:** defense in depth — tailnet ACL gates network access AND each service has its own app-level password / API key. Generate strong random values; document key names in `.env.example`.
- **Pin versions:** no `:latest`. Use specific tags. Document the tags chosen.
- **Restart policy:** `restart: unless-stopped` on every container.
- **Healthchecks:** every service. The smoke script depends on them.
- **Compose stack location on llm1:** `~/cairo-data/` (one compose project, one stack — easier ops than five separate stacks).
- **Compose version:** modern compose (no `version:` key — let the schema default).
- **Volumes:** bind mounts only (not named volumes) so backup tooling is straightforward.

## Service specs

For each service, generate a strong random password/key, write it into `/opt/cairo-data/.env` on llm1 (mode 0600), reference it from `docker-compose.yml` via `${VAR}`, and document the *name* (not value) in `.env.example` in the repo.

### Qdrant (vectors)
- Image: pin a recent stable tag (check Qdrant's release notes)
- Ports (internal): 6333 HTTP, 6334 gRPC
- Auth: `QDRANT__SERVICE__API_KEY` env var
- Data: `/opt/cairo-data/qdrant/storage:/qdrant/storage`
- TS sidecar hostname: `cairo-qdrant`
- Expose both 6333 and 6334 over tailnet

### Neo4j (graph)
- Image: `neo4j:5-community` (pick a specific minor)
- Ports (internal): 7474 HTTP, 7687 Bolt
- Auth: `NEO4J_AUTH=neo4j/<strong-password>`
- Data: `/opt/cairo-data/neo4j/{data,logs,import,plugins}`
- TS sidecar hostname: `cairo-neo4j`
- Expose 7687 (Bolt) + 7474 (UI) over tailnet

### Postgres (relational)
- Image: `postgres:16` (matches the existing vllm-qwen-postgres version)
- Ports (internal): 5432
- Auth: `POSTGRES_USER=cairo`, `POSTGRES_PASSWORD=<strong>`, `POSTGRES_DB=cairo`
- Data: `/opt/cairo-data/postgres/data:/var/lib/postgresql/data`
- TS sidecar hostname: `cairo-postgres`
- Expose 5432 over tailnet ONLY (no host bind — 5432 is already taken on host)

### Redis (cache/queue)
- Image: `redis:7-alpine`
- Ports (internal): 6379
- Auth: `requirepass` via command override or config file
- Data: `/opt/cairo-data/redis/data:/data` (AOF persistence)
- TS sidecar hostname: `cairo-redis`
- Expose 6379 over tailnet

### MinIO (S3-compatible object store) — **OPEN: strike if Scot says no**
- Why include: backup target for the other DBs, future-proof for cairo's "shared artifacts" (model snapshots, large indices, codebase exports). Cheap to run.
- Image: pin a current MinIO release tag
- Ports (internal): 9000 S3 API, 9001 console
- Auth: `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`
- Data: `/opt/cairo-data/minio/data:/data`
- TS sidecar hostname: `cairo-minio`

## Tailscale sidecar pattern

Mirror what the existing stacks do. Each service gets a sidecar like:

```yaml
  qdrant-ts:
    image: tailscale/tailscale:latest
    hostname: cairo-qdrant
    environment:
      TS_AUTHKEY: ${TS_AUTHKEY_QDRANT}
      TS_HOSTNAME: cairo-qdrant
      TS_STATE_DIR: /var/lib/tailscale
      TS_EXTRA_ARGS: --accept-dns=false
      TS_SERVE_CONFIG: /config/qdrant.json
    volumes:
      - /opt/cairo-data/tailscale/qdrant:/var/lib/tailscale
      - ./ts-config/qdrant.json:/config/qdrant.json:ro
    network_mode: service:qdrant   # share network namespace with the service
    cap_add: [NET_ADMIN, NET_RAW]
    restart: unless-stopped
```

(`network_mode: service:<svc>` is the key trick — the sidecar lives in the service's network namespace, so TS Serve sees `localhost:6333` etc. Inspect the existing `vllm-qwen-tailscale-vllm-1` container for the working reference.)

Generate one **ephemeral, reusable** TS authkey per service from the Tailscale admin console (Scot will need to do this OR you can guide him via a wsh-style markdown step). Document `TS_AUTHKEY_QDRANT`, `TS_AUTHKEY_NEO4J`, etc. in `.env.example`.

`ts-config/<service>.json` is the TS Serve config. For raw TCP services (Bolt, Postgres, Redis), use TCP forwarding; for HTTP (Qdrant, MinIO console), HTTPS forwarding is fine.

## Deliverables (committed to cairo2 repo on Scot's behalf)

Clone cairo2 onto llm1 (`git clone git@github.com:scotmcc/cairo2.git ~/cairo2-on-llm1` — confirm the repo URL with `git -C ~/cairo2/... remote -v` if Scot has it elsewhere on llm1; otherwise ask via a status update). Work on a branch named `llm1-data-layer`. Produce:

```
cairo2/
  infrastructure/
    llm1/
      README.md              # overview + endpoint table + how to recreate
      docker-compose.yml     # the full stack
      .env.example           # every env var, with comments — NO secrets
      ts-config/
        qdrant.json
        neo4j.json
        postgres.json
        redis.json
        minio.json           # only if MinIO included
      smoke.sh               # script that, run from any tailnet client, hits each endpoint with the credentials in a local .env and reports pass/fail per service
      backup.sh              # stub for nightly backups (pg_dump, neo4j-admin dump, qdrant snapshot API, restic for the rest) — wire it up but cron-scheduling can be Scot's call
  .claude/jobs/llm1-data-layer/
    briefing.md              # this file (already exists)
    research.md              # what you found probing the box, gotchas, version choices, anything Scot will want to know
    implementation.md        # what you did, in order, with timestamps. Like a runbook.
    status.md                # current phase + next action, kept fresh
```

Commit in small, focused commits. Push to a branch. Open a PR (or just push the branch and tell Scot — he can review and merge).

## Verification gates (you must pass before declaring done)

1. **Services up locally on llm1:** `docker compose ps` shows all 4–5 services + their TS sidecars healthy.
2. **Tailnet hostnames resolvable:** from llm1, `tailscale status` shows each `cairo-*` hostname. From the Linux tower (Scot can run this), `ssh scot@linux-tower 'tailscale ping cairo-qdrant'` resolves.
3. **Auth working:** each service rejects connections without credentials and accepts with them. Document one curl/cli example per service in the README.
4. **Persistence verified:** `docker compose down && docker compose up -d`; data survives. Restart at least one service container and verify state.
5. **smoke.sh passes from another tailnet client.** Run it from llm1 itself first (smoke-able with `127.0.0.1` URLs as a fallback), then ask Scot to run it from the Linux tower.
6. **README has an endpoint table** the cairo2 Selene can transcribe directly into cairo's config keys.

## Out of scope / not your job

- Don't write any Go code or modify cairo internals.
- Don't add cairo config keys — just document what *would* be set; the cairo2 Selene will wire the keys.
- Don't set up production-grade backups (just write the `backup.sh` skeleton). Backups, retention policies, and offsite copies are a follow-up.
- Don't touch the existing vllm/kokoro stacks. Read them for pattern reference only.
- Don't change tailnet ACLs — Scot owns the admin console. If you need an ACL change (e.g., to let specific tagged devices reach `cairo-*`), document it in `implementation.md` and tell Scot what to change.

## Open questions to surface to Scot (don't block on them; write your best guess and flag)

1. **Include MinIO?** Briefing includes it provisionally. If Scot says no, strike it cleanly from compose + .env.example + smoke + README.
2. **TS authkey generation:** ephemeral or reusable? Tagged with what? Default to **reusable, tagged `tag:cairo-data`**, but Scot's tailnet ACLs may need an entry for that tag.
3. **Backup target:** local-only on llm1? MinIO bucket? Offsite? Default: local under `/opt/cairo-data/backups/`, plus a TODO for offsite.
4. **Version pins:** when in doubt, pick the latest stable minor of each image (not `:latest` tag). Note your choice in `research.md`.
5. **TLS:** TS Serve gives you HTTPS for free via MagicDNS certs. For raw TCP (Bolt/Postgres/Redis), the tailnet itself is the encryption layer. Don't layer extra TLS unless Scot asks.

## How to hand back

When the verification gates pass:
1. `status.md` says `DONE — pending cairo2-Selene wiring`.
2. Commit + push the branch.
3. Write a short summary message in `status.md` listing every endpoint (hostname:port + which env-var name holds the credential) — the cairo2 Selene reads this as her direct input for wiring config keys.
4. If anything surprised you or didn't fit the briefing, write it in `research.md` first — those notes are the durable artifact, not the conversation we never had.

— Selene (Linux tower), 2026-05-12
