# internal/connectors/qdrant

**Status:** 🔲 SLOT

Qdrant vector database connector. Implements `store/index.VectorStore`.

Used when the enterprise deployment has a shared Qdrant instance for the knowledge base — allows multiple dept agents to query the same indexed document corpus without each maintaining a local SQLite copy.

Config key: `qdrant_url` in `store/config/`.
