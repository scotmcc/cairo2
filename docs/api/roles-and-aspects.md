# Roles and Consider-Aspects

## PATCH /api/roles/{name}

Update a single field on a role. Auth-gated. Source: `internal/server/api_mutations.go:72`.

`{name}` is the role name string (e.g. `default`, `orchestrator`).

**Request body**

```json
{
  "field": "model",       // string — "model" or "think" (only these two)
  "value": "llama3.2"     // string
}
```

Valid `field` values:

| Field | Accepted `value` strings | Effect |
|-------|--------------------------|--------|
| `model` | any non-empty string | Override model for this role; falls back to global config when empty |
| `think` | `""`, `"true"`, `"false"` | `""` = inherit global think setting; `"true"` / `"false"` override per-role |

Any other `field` value returns 400.

**Response 200**

```json
{"ok": true}
```

**Response 400** — unsupported field

```json
{"error": "field must be 'model' or 'think'"}
```

**Response 404** — role not found

```json
{"error": "role not found"}
```

**curl**

```bash
# Set model for the 'default' role
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field": "model", "value": "llama3.2"}' \
  http://localhost:8080/api/roles/default

# Enable thinking for the 'orchestrator' role
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field": "think", "value": "true"}' \
  http://localhost:8080/api/roles/orchestrator

# Reset think to inherit global setting
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"field": "think", "value": ""}' \
  http://localhost:8080/api/roles/orchestrator
```

**Notes**

- Changes flip `source='user'` in the DB, which prevents `seedRoles` from overwriting them on next `Open`.
- Other role fields (`description`, `base_prompt_key`, `tools`, `consider`) are not patchable via HTTP. Use `cairo config set` or direct DB edits for those.

---

## PUT /api/consider/aspects/{name}

Upsert a consider-aspect by name. Creates if it doesn't exist; replaces all fields if it does. Auth-gated. Source: `internal/server/api_mutations.go:107`.

`{name}` is the aspect name string (e.g. `curiosity`, `caution`).

**Request body**

```json
{
  "traits": "Approach each topic with genuine curiosity...", // string — prompt fragment
  "enabled": true,   // bool
  "position": 1      // int — render/injection order (ascending); 0 is valid
}
```

**Response 200** — the upserted aspect (`identity.ConsiderAspect`, `internal/store/identity/consider_aspects.go:6`):

```json
{
  "name": "curiosity",
  "traits": "Approach each topic with genuine curiosity...",
  "enabled": true,
  "position": 1
}
```

**curl**

```bash
curl -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"traits": "Be methodical. Check preconditions.", "enabled": true, "position": 2}' \
  http://localhost:8080/api/consider/aspects/caution
```

---

## PATCH /api/consider/aspects/{name}

Toggle an aspect's `enabled` flag without touching other fields. Auth-gated. Source: `internal/server/api_mutations.go:131`.

**Request body**

```json
{"enabled": false}
```

**Response 200**

```json
{"ok": true}
```

**Response 404**

```json
{"error": "aspect not found"}
```

**curl**

```bash
# Disable the 'caution' aspect
curl -X PATCH \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}' \
  http://localhost:8080/api/consider/aspects/caution
```

---

## DELETE /api/consider/aspects/{name}

Delete a consider-aspect. Auth-gated. Source: `internal/server/api_mutations.go:155`.

Returns 404 on a missing aspect (R6 mitigation).

**Response 204** — no body.

**Response 404**

```json
{"error": "aspect not found"}
```

**curl**

```bash
curl -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8080/api/consider/aspects/caution
```
