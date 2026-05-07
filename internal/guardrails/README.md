# internal/guardrails

**Layer:** Foundation  
**Status:** 🔲 SLOT

Content safety layer that wraps the agent loop. Runs before and after LLM calls.

## Planned responsibilities

- **Input scanning** — prompt injection detection, jailbreak patterns, blocked topics
- **Output scanning** — PII detection (names, SSNs, credentials), sensitive data masking
- **Policy enforcement** — per-role or per-department guardrail profiles
- **Audit hooks** — emit events to `internal/audit/` when guardrails fire

## Notes

Not needed for standalone cairo deployment. Required for enterprise deployments handling sensitive data (DoD, healthcare, finance). Plugs in at the agent loop boundary — the loop calls `guardrails.CheckInput(prompt)` and `guardrails.CheckOutput(response)` if a guardrails provider is configured.

Build when: the first enterprise deployment with a compliance requirement lands.
