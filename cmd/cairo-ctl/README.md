# cmd/cairo-ctl

**Layer:** Operator / Admin CLI  
**Status:** 🔄 merging from `~/cairo-registry`

Command-line tool for fleet operators and enterprise admins. Talks to `cairo-registry` over its admin API.

## Subcommands

- `list` — show all enrolled agents (id, owner, type, dept, last seen)
- `get <id>` — detail view for a specific agent
- `health` — fleet summary (connected, active, ws_connected count)
- `revoke <id>` — revoke an agent's enrollment
- `broadcast <command>` — queue a command for all (or filtered) agents
- `departments` — manage departments and role assignments *(slot)*
- `audit` — export audit log entries *(slot)*

## Source

Migrates from `~/cairo-registry/cmd/cairo-ctl/`.
