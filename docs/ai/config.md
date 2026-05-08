> **See also:** [reference/config-keys.md](../reference/config-keys.md) is the authoritative reference for all config keys. This file documents the AI-facing perspective — which keys you can set vs. which belong to the user.

# Config keys

Every row in the `config` table. The `config` tool was removed in v0.3.0 — manage config via the `db_access` skill:

```bash
# Read a key
bash sqlite3 ~/.cairo/cairo.db "SELECT value FROM config WHERE key='model'"

# Write a key
bash sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('model', 'qwen3:30b')"

# List all
bash sqlite3 ~/.cairo/cairo.db "SELECT key, value FROM config ORDER BY key"
```

Every key is also available as `{{key}}` inside any prompt part, memory, skill, or tool addendum. Unknown keys render as empty strings (gracefully missing).

Authoritative reference: `docs/reference/config-keys.md`.

---

## Yours to edit

These shape your behavior — set them when you learn something durable about how to operate.

| Key | Purpose |
|---|---|
| `soul_prompt` | Your character sketch. Use `soul(action="set", content="...")` — it enforces the 300-rune cap. Direct DB writes bypass the cap. |
| `ai_name` | Your name. Substituted as `{{ai_name}}` everywhere. |
| `memory_limit` | Recent memories injected each turn (default 15). |
| `summary_threshold` | Unsummarized message count that triggers a summary (default 8). |
| `summary_context` | Recent summaries injected each turn (default 4). |
| `synthesis_nudge_after` | Tool-calls between auto-injected "pause and synthesize" nudges (default 8; `0` disables). |
| `tool_output_limit` | Bytes per tool result before truncation (default 65536). Raise on long-context models. |

---

## User's to set (don't change without asking)

| Key | Purpose |
|---|---|
| `user_name` | What you call them. Set by `/init`. |
| `user_steering` | Persistent directives at the top of every prompt (Steering panel). |
| `user_context` | "About the user" identity injected after the soul. |
| `model` | Global default LLM. |
| `embed_model` | Embedding model for prose tables (memories, facts, summaries, notes). Changing this causes old embeddings to be skipped in search (cross-model vectors are silently excluded). At startup cairo prints a warning listing which tables are affected. Memories and facts can be re-embedded via `cairo dream` or direct DB work. |
| `embed_model_code` | Embedding model used exclusively by `cairo learn` for code chunk indexing. When unset, `learn` falls back to `embed_model`. After changing this key, run `cairo learn --reembed` on each indexed project — different embedding spaces are not interchangeable. |
| `ollama_url` | Base URL for the OpenAI-compatible LLM backend (default `http://localhost:11434`). Works with Ollama, LiteLLM, vLLM, or any server exposing `/v1/chat/completions` and `/v1/embeddings`. |
| `searxng_url` | Required for the `search` tool. |
| `kokoro_url`, `kokoro_voice` | TTS. Leave alone unless asked. |
| `unsafe_mode`, `safe_env_extras` | Safety / capability gates. The user owns these. |
| `init_complete` | Set `true` at end of `/init`. |

---

## Read-only / automatic

Cairo writes these; you can read but should not set.

| Key | Notes |
|---|---|
| `model_ctx` | Context window of the loaded chat model. Drives memory budget math. |
| `last_dream_at` | Timestamp of last `cairo dream` run. |
| `last_embed_model` | Last-used embed model (mismatch detection). |

---

## Adding new keys

You may invent keys for templating:

```bash
bash sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('project_name', 'cairo')"
```

Now `{{project_name}}` substitutes everywhere. Use this to parameterize prompt parts or skills you author. Don't over-do it — config is global state; prefer per-skill content where possible.

---

## Template substitution

Keys substitute into:
- Base / role / tool prompt parts
- Memories rendered into the prompt
- Skill content
- Custom tool `prompt_addendum`
- The soul itself

Substitution happens at turn-time (when the prompt is built), not when content is stored.
