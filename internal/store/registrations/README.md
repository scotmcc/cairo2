# internal/store/registrations

**ZT role:** Device posture store — the Device pillar's persistent state

Agent registration records for the fleet registry. Each record tracks:

- Agent ID, hostname, owner (tsnet identity)
- Agent type: `personal` | `departmental` | `enterprise`
- Department association
- Access policy: which roles/users can address this agent
- Status: active, revoked
- Last seen, ws_connected

This is the agent catalog. The `access/` package evaluates policy against these records to decide if a user can reach a given agent.

Source: `~/cairo-registry/internal/` (registration store).
