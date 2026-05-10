# Config

## GET /api/config/snapshot

Returns the full runtime configuration: all config key/value pairs, all roles, and all consider-aspects. Auth-gated. Source: `internal/server/api_read.go:24`.

**Response 200**

```json
{
  "config": {
    "ollama_url": "http://100.71.195.54:4000",
    "llm_api_key": "sk-llm1",
    "model": "llama3.2"
    // ... all key/value pairs from the config table (map[string]string)
  },
  "roles": [
    {
      "id": 1,                          // int64
      "name": "default",                // string
      "description": "...",             // string
      "model": "llama3.2",              // string — empty means inherit global config model
      "base_prompt_key": "base",        // string
      "tools": "[\"bash\",\"read\"]",   // string — JSON-encoded array of tool names; empty = unrestricted
      "think": "",                      // string — "" inherit | "true" | "false"
      "consider": true,                 // bool — false disables consider for this role
      "created_at": "2026-04-01T...",   // time.Time, RFC3339
      "updated_at": "2026-05-01T..."    // time.Time, RFC3339
    }
    // ... array of all roles
  ],
  "considerAspects": [
    {
      "name": "curiosity",  // string
      "traits": "...",      // string — free-text prompt fragment
      "enabled": true,      // bool
      "position": 1         // int — render order (ascending)
    }
    // ... array of all consider-aspects, ordered by position ASC, name ASC
  ]
}
```

**curl**

```bash
curl -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/config/snapshot
```

---

## PUT /api/config/{key}

Set a single config key. Body is a **raw JSON string value** — not an object wrapper. Auth-gated. Source: `internal/server/api_mutations.go:9`.

**Request body**

The body must be a bare JSON string literal:

```
"llama3.2"
```

Not `{"value": "llama3.2"}`. The decoder calls `json.Decode(&string)` directly, so the outermost quotes are required.

**Response 200**

```json
{"key": "model", "value": "llama3.2"}
```

**curl**

```bash
# Set the model
curl -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '"llama3.2"' \
  http://localhost:8080/api/config/model

# Set the ollama_url
curl -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '"http://100.71.195.54:4000"' \
  http://localhost:8080/api/config/ollama_url
```

**Notes**

- Any key name is accepted — there is no server-side allowlist. Unknown keys are stored and returned by the snapshot but ignored by the agent.
- Changes take effect on the next agent turn; already-running turns are not affected.
- See `docs/reference/config-keys.md` for the full list of recognized keys.
