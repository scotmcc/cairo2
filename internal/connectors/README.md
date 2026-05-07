# internal/connectors

**Layer:** Enterprise Data Plane  
**Status:** 🔲 SLOT (all sub-packages)

Adapters to external enterprise data sources. Each connector implements an interface defined in the domain package it serves — the agent and store packages never import connectors directly; `cmd/cairo/app.go` wires the right backend at startup based on config.

## Sub-packages

| Package | Implements | Used by |
|---|---|---|
| `qdrant/` | `store/index.VectorStore` | `learn/` pipeline (replaces SQLite FTS5) |
| `postgres/` | shared session/memory store | dept agents with shared context |
| `neo4j/` | knowledge graph queries | `services/docqa/` |
| `s3/` | document source for indexing | `learn/` pipeline input |

## The wiring pattern

```go
// In cmd/cairo/app.go:
if cfg.QdrantURL != "" {
    app.VectorStore = qdrant.New(cfg.QdrantURL)
} else {
    app.VectorStore = app.DB.Index() // SQLite default
}
```

The agent loop, the learn pipeline, and the docqa service never change. Only the wiring in `app.go` changes between standalone and enterprise deployments.

Build when: the first enterprise deployment needs a shared knowledge base.
