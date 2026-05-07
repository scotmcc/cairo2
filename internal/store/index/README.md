# internal/store/index

Indexed files, chunks, project metadata, and embedding search.

Defines the `VectorStore` interface — the abstraction that lets the `learn/` pipeline write to either SQLite (standalone) or Qdrant (enterprise) without knowing which backend is wired in.

Source: `~/cairo/internal/db/` (index/learn-related files).
