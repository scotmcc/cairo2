# DB & Schema — Findings

**Reviewed:** internal/db/
**Date:** 2026-05-02
**Counts:** major: 0, medium: 1, small: 1

## Summary

Memory retrieval scoring is correct: line 224 stays `cosine * decayImportance(importance)` per spec. Schema/seed parity is intact (consider_aspects added in v087 migration, seedConsiderAspects runs after all migrations). Code_index table successfully removed v064. All roles granted memory_tool in v062. One unindexed hot query and one large file maintenance concern.

## Findings

### [medium] No index on memories(embed_model) — hot query path unindexed
- **Where:** `internal/db/memories.go:200-224` Search method
- **What:** Search() fetches all memories via All() then filters by `m.EmbedModel != queryModel` in client code (lines 216-218). This is O(n) table scan + full BLOB decode, then in-memory filtering. embed_model is low-cardinality, set once per embedding model swap, and accessed on every semantic search (every turn in agent loop). No index exists on memories(embed_model).
- **Why it matters:** As memory count grows (100 → 1000 → 10000+), every search decodes all embeddings in RAM just to skip most rows. Index cuts query time and memory load dramatically.
- **Action:** Add migration creating `CREATE INDEX IF NOT EXISTS idx_memories_embed_model ON memories(embed_model)`. Profile Search before/after to confirm improvement at scale.

### [small] schema.go is 1568 lines — file size exceeds ~300-line maintenance target
- **Where:** `internal/db/schema.go` (entire file)
- **What:** Single file contains 80+ numbered migrations, three helper functions, and two large constants. Navigating to a specific migration or adding a new one requires scrolling through hundreds of lines. Migrations are tightly coupled in one file.
- **Why it matters:** Maintenance friction. Code review of a single migration is tedious. Adding a new migration risks off-by-one errors or misplaced comments. Not a correctness issue but violates the ~300-line-per-file principle.
- **Action:** Refactor out: split into `schema_base.go` (base table definitions + constants) and `migrations.go` (migration array only). File-per-migration is overkill, but 1500+ in one file is hard to maintain.

