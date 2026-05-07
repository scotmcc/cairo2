# internal/services/automation

**Layer:** AI Application Surfaces  
**Status:** 🔲 SLOT

Automation Hub. The agent can trigger workflows, run runbooks, and execute infrastructure operations.

## Sub-packages

- `n8n/` — trigger N8n workflow instances via N8n API; register N8n workflows as agent tools
- `ansible/` — run Ansible playbooks as agent tools; parse playbook output for the agent

## Planned responsibilities

- Register automation backends as tools in the tool registry at startup
- Allow the agent to say "run workflow X with params Y" and get results back
- Support approval gates (some workflows require human confirmation before execution)

Build when: IT automation use case is active.
