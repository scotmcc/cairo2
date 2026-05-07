# internal/store/config

Typed key-value config store backed by SQLite. All `KeyXxx` constants and `ConfigQ` query helpers live here.

Also owns the enterprise connector config keys (qdrant_url, postgres_dsn, etc.) — they're just config entries like any other.

Source: `~/cairo/internal/db/` (config-related files).
