# internal/learn

**Layer:** Foundation  
**Status:** ✅ working, moves as-is

Codebase and document indexing pipeline (RAG). Walks a directory tree, chunks files, generates embeddings, and stores them for semantic search.

In standalone mode, writes to `internal/store/index/` (SQLite). In enterprise mode, can write to `internal/connectors/qdrant/` instead — the pipeline is the same, the backend is swappable via the `VectorStore` interface defined in `internal/store/index/`.

Source: `~/cairo/internal/learn/`.
