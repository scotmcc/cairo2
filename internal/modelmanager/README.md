# internal/modelmanager

**Layer:** Foundation  
**Status:** 🔲 SLOT

Model lifecycle management for enterprise deployments.

## Planned responsibilities

- Registry of approved models (name, version, endpoint, access tier)
- Model selection policy (which roles/departments can use which models)
- A/B serving config (route X% of requests to model A, Y% to model B)
- Evaluation scaffolding (run a test set against a candidate model before promoting)

## Notes

Standalone cairo uses `internal/llm/` directly with a configured endpoint. Model manager sits in front of `internal/llm/` for enterprise deployments where admins need to control which models are available to which users.

Build when: multi-model enterprise deployment is needed.
