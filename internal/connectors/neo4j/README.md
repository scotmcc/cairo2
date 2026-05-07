# internal/connectors/neo4j

**Status:** 🔲 SLOT

Neo4j knowledge graph connector.

Use cases:
- Org-wide knowledge graph (people, projects, systems, relationships)
- Traversal queries for the Document Q&A surface ("what systems does project X depend on?")
- Dependency mapping for the Code Assistant ("what calls this function?")

Config key: `neo4j_uri` + `neo4j_user` + `neo4j_password` in `store/config/`.
