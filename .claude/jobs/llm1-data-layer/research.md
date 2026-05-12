# Research — llm1 Data Layer

**Author:** Selene (llm1) — 2026-05-12

## State of llm1 (re-probed, matches briefing)

- OS: RHEL 9.6 (`Linux 5.14.0-570.32.1.el9_6.x86_64`)
- Docker: `28.3.3`, Root Dir `/opt/docker`, overlay2
- Disk: `/` 19 G free (avoid), `/home` 246 G free, `/opt` 2.0 T free → data goes under `/opt/cairo-data/`
- Host port already bound: `127.0.0.1:5432` by `vllm-qwen-postgres-1`. Our postgres must not bind a host port.
- Existing stacks (untouched): `vllm-qwen-*` (vllm + litellm + postgres + TS), `kokoro-tts-gpu-*` (TTS + nginx + TS). Both use `network_mode: host` for their TS sidecars.
- Tailnet: `tail1bb4f.ts.net`. llm1 is `100.97.111.72 / llm1`.

## Divergence from briefing: TS sidecar network mode

Briefing recommended `network_mode: service:<svc>` (shared netns). Existing stacks use `network_mode: host`. I followed the **briefing** rather than the existing pattern. Why:

1. **No host port collisions.** Five services + their internal-only listeners need to coexist; `service:<svc>` keeps everything off the host net.
2. **5432 is already host-bound** by vllm-qwen-postgres. Host-mode would have forced a non-standard postgres port on our stack, breaking client expectations.
3. **Cleaner isolation per service.** Each TS sidecar sees only its own service's `localhost`, which matches how TS Serve's `TCPForward 127.0.0.1:<port>` and `Proxy http://127.0.0.1:<port>` directives are typically written.

Tradeoff: when troubleshooting, `docker exec <ts-sidecar> tailscale status` works the same; but `tailscale serve status` shows only that one service's forwards.

## Image version pins (chosen 2026-05-12; bump if newer stable minors exist)

| Service | Tag | Rationale |
|---|---|---|
| Qdrant | `qdrant/qdrant:v1.12.4` | Recent stable as of late-2025; check qdrant.tech/releases for newer 1.13.x. |
| Neo4j | `neo4j:5.25-community` | 5.x community line, late-2025 minor. |
| Postgres | `postgres:16` | Matches existing vllm-qwen-postgres for consistency / shared backup tooling. |
| Redis | `redis:7.4-alpine` | 7.4 GA Sep 2024; alpine for small footprint. |
| MinIO | `minio/minio:RELEASE.2025-04-08T15-41-24Z` | MinIO ships frequently; pin a 2025 release. Console is integrated since 2023. |
| Tailscale | `tailscale/tailscale:v1.80.0` | Pinned (existing stacks use `:latest` — we deviate per briefing rule). |

If any of these fail to pull, bump to the nearest newer minor and record the actual tag in `implementation.md`.

## TS Serve config per service

- HTTP-only services proxy via `Web` HTTPS handler on :443.
- Raw TCP services (Bolt, Postgres, Redis, Qdrant gRPC, MinIO S3 API) use `TCP` forwards on the protocol's native port. Tailnet is the encryption layer; we don't terminate TLS for raw TCP.
- Each sidecar shares its service's network namespace, so `127.0.0.1:<port>` resolves to the service.

| Service | Tailnet hostname | Tailnet-exposed ports | Mode |
|---|---|---|---|
| Qdrant | cairo-qdrant | 443 (→6333 HTTP), 6334 (gRPC) | HTTPS Web + TCP fwd |
| Neo4j | cairo-neo4j | 443 (→7474 UI), 7687 (Bolt) | HTTPS Web + TCP fwd |
| Postgres | cairo-postgres | 5432 | TCP fwd only |
| Redis | cairo-redis | 6379 | TCP fwd only |
| MinIO | cairo-minio | 443 (→9001 console), 9000 (S3) | HTTPS Web + TCP fwd |

## Open questions to Scot

1. **MinIO confirmed IN** per Scot 2026-05-12.
2. **TS authkeys** — Scot will generate per-service ephemeral+reusable keys tagged `tag:cairo-data` after scaffolding lands.
3. **ACL** — `tag:cairo-data` may need an entry in tailnet ACL. Documented in `implementation.md` as a TODO for Scot.
4. **Backup target** — defaulting to local `/opt/cairo-data/backups/` with a TODO for offsite to MinIO bucket or external.
