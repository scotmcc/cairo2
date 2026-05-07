# internal/services/docqa

**Layer:** AI Application Surfaces  
**Status:** 🔲 SLOT

Document Q&A surface. Users ask natural-language questions about indexed documents; the service retrieves relevant chunks and synthesizes an answer.

## Planned responsibilities

- Accept a query and a scope (which knowledge base / department)
- Retrieve top-K chunks from `store/index/` or `connectors/qdrant/`
- Optionally traverse relationships via `connectors/neo4j/` ("what else relates to this?")
- Assemble a retrieval-augmented prompt and run it through the agent
- Return answer + source citations

Build when: the first non-code document corpus needs to be queryable.
