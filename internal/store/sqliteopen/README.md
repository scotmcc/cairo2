# internal/store/sqliteopen

Opens and configures the SQLite database. Houses `Open`, `OpenAt`, `WithTx`, premigration backup, and the composite `*DB` type that vends all sub-stores.

Source: `~/cairo/internal/db/db.go`.
