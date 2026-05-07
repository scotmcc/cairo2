# internal/telemetry

**Layer:** Cross-cutting — Monitoring  
**Status:** 🔲 SLOT

System health, metrics, and alerting. The operational observability layer.

## Planned responsibilities

- Health check endpoint aggregation (agent alive, DB accessible, LLM reachable, registry connected)
- Prometheus-compatible metrics exposition (`/metrics`)
- Anomaly detection hooks (unusual tool call patterns, error rate spikes)
- Alert emission (to PagerDuty, Slack, or email) for enterprise ops teams

## Notes

Standalone cairo already has `/healthz`. This package formalizes and extends it for enterprise deployments that need Prometheus scraping or alerting.

Build when: the first production enterprise deployment needs monitoring.
