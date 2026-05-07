# internal/connectors/postgres

**Status:** 🔲 SLOT

PostgreSQL connector for shared relational storage.

Use cases:
- Shared session store for departmental agents (all dept members see the same history)
- Cross-agent memory/fact visibility for the enterprise main agent
- Admin-visible session log (for compliance, not for the agent's own use)

Config key: `postgres_dsn` in `store/config/`.
