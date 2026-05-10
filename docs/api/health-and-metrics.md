# Health and Metrics

## GET /healthz

Liveness probe. Public — no auth required.

**Response 200**

```json
{"status": "ok"}
```

**curl**

```bash
curl http://localhost:8080/healthz
```

---

## GET /api/health

Richer health check. Public — no auth required. Source: `internal/server/api_read.go:14`.

**Response 200**

```json
{
  "ok": true,              // bool — always true when the handler responds
  "version": "0.4.0",     // string — set by ldflags at build time (internal/version)
  "uptime_seconds": 3721, // int64 — seconds since cairo serve started
  "db_path": "/home/scot/.cairo/cairo.db"  // string — absolute path to SQLite DB
}
```

**curl**

```bash
curl http://localhost:8080/api/health
```

---

## GET /api/metrics

Runtime counts. Auth-gated. Source: `internal/server/api_read.go:137`.

**Response 200**

```json
{
  "sessions": 12,  // int — total rows in sessions table
  "memories": 47,  // int — total rows in memories table
  "jobs": 3        // int — total rows in jobs table
}
```

**curl**

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/metrics
```

**Notes**

- Counts are live SQL `COUNT(*)` queries, not cached.
- Does not include messages, chunks, embeddings, or task counts. Those tables exist but are not yet surfaced here.
