# Implementation — llm1-data-layer

Chronological runbook. Timestamps are UTC unless noted.

## 2026-05-12 — Scaffold

- Received briefing from tower-Selene at `~/cairo2/.claude/jobs/llm1-data-layer/briefing.md`.
- Confirmed GitHub SSH auth from llm1 (`ssh -T git@github.com` → "successfully authenticated as scotmcc").
- Cloned `git@github.com:scotmcc/cairo2.git` → `~/cairo2-on-llm1`.
- Created branch `llm1-data-layer` off `master` (HEAD `4c3ca9b — handoff: 2026-05-12 evening (pqcdev1)`).
- Copied briefing into the cloned tree at `.claude/jobs/llm1-data-layer/briefing.md`.
- Re-probed llm1 (df, ss, docker ps, tailscale status): matches briefing's snapshot. `127.0.0.1:5432` is the only relevant host bind (vllm-qwen-postgres-1).
- Inspected `vllm-qwen-tailscale-vllm-1` and `kokoro-tts-gpu-tailscale-kokoro-1`: both use `network_mode: host` with `TS_SERVE_CONFIG` proxying to `localhost:<port>`. Drove decision to deviate to `network_mode: service:<svc>` — see research.md.
- Wrote `infrastructure/llm1/{docker-compose.yml, .env.example, ts-config/*.json, smoke.sh, backup.sh, README.md}`.
- Wrote `.claude/jobs/llm1-data-layer/{research.md, status.md, implementation.md}`.
- Initial commit + push pending.

## Pending — once Scot provides TS keys

- Create `/opt/cairo-data/` tree.
- Populate `/opt/cairo-data/.env` (mode 0600).
- `docker compose --env-file /opt/cairo-data/.env up -d` from `infrastructure/llm1/`.
- Verify the 6 briefing gates; update status.md.
- Run `smoke.sh` locally and (with Scot) from the Linux tower.

## TODOs / Follow-ups

- Neo4j community-edition online backup via APOC export (briefing said stub is OK).
- MinIO backup via `mc mirror` to offsite.
- Cron-schedule `backup.sh` once retention strategy is agreed.
- Bump image pins when newer stable minors land (qdrant 1.13+, neo4j 5.26+, redis 7.5+, tailscale 1.81+, minio newer monthly release).
