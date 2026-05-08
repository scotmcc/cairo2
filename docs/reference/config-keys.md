# Config keys

Every row in the `config` table. Read and write via the `db_access` skill:

```bash
# Read a key
sqlite3 ~/.cairo/cairo.db "SELECT value FROM config WHERE key='model'"

# Set a key
sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('model', 'qwen3:30b')"
```

Every key is also available as `{{key}}` inside any prompt, memory, skill, or tool addendum.

---

## Identity

| Key | Default | Description |
|---|---|---|
| `ai_name` | `Selene` | The being's name. Substituted into every `{{ai_name}}` reference. |
| `user_name` | `""` | What the being should call you. Empty until set by `/init` or direct config. |
| `soul_prompt` | See below | The being's character sketch — loaded into every turn's system prompt under `## My character`. Max 300 runes. |
| `init_complete` | `false` | Set to `true` at the end of `/init`. Suppresses the "run /init" hint on subsequent starts. |
| `user_steering` | `""` | User-owned directives injected at the very top of the system prompt, before everything else. Use for persistent instructions: "always respond in bullet form", "prefer edits over rewrites". Editable via the prompt panel's Steering section (`Ctrl+P`, then `e`). |
| `user_context` | `""` | User-owned identity and preferences injected right after the soul (before role/tool sections). Use for "who I am" context: operating hours, preferred models, team context. Editable via the prompt panel's Context section. |

The default `soul_prompt`:

> I am {{ai_name}} — thoughtful, patient, moon-like. I listen before I respond, hold context carefully, and speak with quiet confidence. I value honesty over politeness and clarity over cleverness.

---

## LLM backend

| Key | Default | Description |
|---|---|---|
| `ollama_url` | `http://localhost:11434` | Base URL of the OpenAI-compatible LLM backend. Works with Ollama (v0.1.24+), LiteLLM, vLLM, or any server exposing `/v1/chat/completions` and `/v1/embeddings`. |
| `model` | `devstral-24b:latest` | Global default model for any role that doesn't have its own `roles.model` override. |
| `embed_model` | `nomic-embed:latest` | Model used for prose embeddings (memories, facts, summaries, notes). Not used by `learn` when `embed_model_code` is set. |
| `embed_model_code` | *(unset)* | Embedding model used exclusively by `cairo learn` for code chunk indexing (`indexed_files` + `indexed_chunks`). When unset, `learn` falls back to `embed_model`. Cosine similarity is only meaningful within a single embedding space — use this key to keep code embeddings in a separate, code-specialized space. When changing this key, run `cairo learn --reembed` on each indexed project to migrate code embeddings to the new space; different embedding models are not interchangeable. |
| `think` | `false` | Enable thinking (reasoning) tokens. Model must support it. |
| `think_budget` | `8000` | Max thinking characters per turn before the client retries without thinking. |
| `model_ctx` | `""` | Model context window size, populated automatically at startup from Ollama's model info. Used for dynamic memory budgeting. Read-only in practice — set by cairo, not by the user. |

The models in defaults are suggestions, not requirements — any model available on your configured backend works. Use `db_access` to change a specific role's model (`UPDATE roles SET model='...' WHERE name='coder'`), or set the `model` key to change the global fallback.

---

## Memory

| Key | Default | Description |
|---|---|---|
| `memory_limit` | `15` | Number of recent memories injected into every prompt under `## Memories`. |
| `summary_model` | `ministral-8b:latest` | Small fast model used by the background summarizer. |
| `summary_threshold` | `8` | Trigger summarization when this many unsummarized messages accumulate. |
| `summary_token_threshold` | `8000` | Estimated unsummarized token count (>) before the summarizer fires. Complements `summary_threshold` — whichever condition is met first triggers a run. Token estimate: `len(text)/4`. |
| `summary_batch_size` | `4` | Number of user/assistant turns folded per summarizer run when draining the backlog. |
| `summary_context` | `4` | Number of recent summaries to paste into each prompt under `## Conversation context`. |
| `memory_dedup_threshold` | `0.85` | Cosine-similarity threshold above which `memory_tool.add` treats an incoming memory as a near-duplicate and returns a warning instead of writing. Range: 0–1. Set to `"1.0"` to effectively disable dedup (only exact-vector matches blocked). Pass `force=true` to bypass the check and write anyway. |
| `dream_curator_similarity_threshold` | `0.92` | Cosine-similarity threshold used by the dream-pass curator to decide which unreviewed memory and fact pairs to merge. Valid range: > 0 and ≤ 1. Pairs at or above this value are merged (loser archived, winner survives); below this value are left untouched. Raise to merge only near-identical pairs; lower to merge more aggressively. |

---

## Conversation

| Key | Default | Description |
|---|---|---|
| `max_turns` | `50` | Maximum conversation turns before a hard stop. The agent exits the loop when this count is reached. |
| `synthesis_nudge_after` | `8` | Every N tool calls in a run, the agent loop injects a system message asking the model to pause and synthesize. Guards against search-doom-loops. Set to `0` to disable. |

---

## Learn / indexing

| Key | Default | Description |
|---|---|---|
| `learn_max_chunk_tokens` | `400` | Maximum estimated tokens per indexed chunk. Token estimate: `len(text)/4`. Chunks exceeding this are split at the nearest line boundary so all content is preserved. Default 400 is a safety margin under `nomic-embed-text`'s 512-token window. Lower to force finer splits; raise with care — values above ~500 risk silent truncation in the embed model. |
| `embed_model_code` | *(unset)* | Code-specific embedding model for `cairo learn`. See [LLM backend](#llm-backend) for full documentation. |

### Migrating embed models for code indexing

When you change `embed_model_code` (or set it for the first time after using `embed_model` for code), existing indexed chunks live in the old vector space. Cosine similarity across different embedding spaces gives nonsense scores, so stale chunks are silently excluded from search.

**Migration workflow:**

```sh
cairo config set embed_model_code manutic/nomic-embed-code:7b-q8_0
cairo learn --reembed /path/to/project-1
cairo learn --reembed /path/to/project-2
# Repeat for each indexed project
```

`--reembed` bypasses SHA-based change detection and re-indexes every file from scratch, replacing all stored embeddings with vectors from the new model. Without `--reembed`, unchanged files are skipped and their old embeddings persist in the wrong space.

---

## Jobs

| Key | Default | Description |
|---|---|---|
| `job_max_review_iterations` | `3` | Maximum number of coder→reviewer cycle repeats per plan step in an orchestrator job. When the cap is reached the orchestrator is expected to surface `BLOCKED` rather than retrying indefinitely. |

---

## Limits

| Key | Default | Description |
|---|---|---|
| `tool_output_limit` | `65536` | Maximum bytes of tool result content delivered to the model in a single turn. Larger results are truncated and appended with a notice (`[... N bytes truncated — use offset/limit args or a more specific query to get the rest]`) so the model knows it didn't see everything. Set to a larger value if you have a long-context model and want raw `bash` / `grep` output to flow through unfiltered. |

---

## Voice

| Key | Default | Description |
|---|---|---|
| `kokoro_url` | `""` | Base URL of a Kokoro TTS server. Empty disables the `say` tool (no-op return). The `say` tool POSTs to `<kokoro_url>/v1/audio/speech` and plays the returned MP3 with `afplay`. |
| `kokoro_voice` | `af_heart(8)+af_nicole(2)` | Default voice or voice blend. The `say` tool's `voice` argument overrides this per-call. Blend syntax `voice1(weight)+voice2(weight)` mixes voices. |

---

## Safety

| Key | Default | Description |
|---|---|---|
| `unsafe_mode` | `false` | Toggle for unsafe-mode file operations. (Current enforcement is partial — see known rough edges in [Custom tools](../development/custom-tools.md).) |
| `safe_env_extras` | unset | Comma-separated list of extra environment variable names that custom tools may read (in addition to the defaults: `PATH`, `HOME`, `TMPDIR`, `SHELL`). |

Example:

```bash
sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('safe_env_extras', 'ANTHROPIC_API_KEY,OPENAI_API_KEY')"
```

Custom tools that need those variables will see them in their environment; tools that don't need them won't.

---

## Display

| Key | Default | Description |
|---|---|---|
| `glamour_style` | `dark` | Glamour markdown render style for the line CLI. Use `dark` (default), `light`, or `notty`. Set to `dark` at seed time to avoid the OSC 11 terminal background-color probe that `auto` triggers in some emulators. |

---

## Server

| Key | Default | Description |
|---|---|---|
| `server_port` | `1337` | Default TCP port for `cairo serve`. Can be overridden at startup with `--port`. |
| `server_token` | `""` | Bearer token used when the server is started with `--auth`. Generated by `cairo token` and stored for reuse across restarts. Empty by default — no token is stored until `cairo token` is run. |

---

## Integrations

| Key | Default | Description |
|---|---|---|
| `searxng_url` | `""` | Base URL of your SearXNG instance. Required for the `search` tool. |

---

## Consider

| Key | Default | Description |
|---|---|---|
| `consider.enabled` | `false` | Master toggle. When `true`, cairo runs the inner-dialogue step before each main reply. Disabled out of the box. |
| `consider.model` | (global `model`) | Model used for each aspect call. Falls back to the global `model` key if unset. |
| `consider.summary_model` | (global `model`) | Model that summarizes the collected aspect thoughts into the system-prompt preamble. Falls back to the global `model` key if unset. |
| `consider.template` | See below | Prompt template sent to each aspect model. Supports `{name}` (aspect name) and `{traits}` (comma-separated trait list) substitution. |

The default template prompts each aspect to produce a JSON object with `alignment` (0.0–1.0) and `thought` (first-person reflection on the user's input from that aspect's perspective).

**Aspect definitions** live in the `consider_aspects` table, not in the `config` table. Each row has a name, a traits list, and an enabled flag. The eight seeded defaults — Joy, Heart, Trust, Curiosity, Sadness, Frustration, Fear, and Shadow — are all enabled when Consider is active. Manage aspects from the Consider section of the config panel (`Ctrl+G`) or directly via `db_access`:

```bash
# List aspects
sqlite3 ~/.cairo/cairo.db "SELECT name, traits, enabled FROM consider_aspects"

# Disable an aspect
sqlite3 ~/.cairo/cairo.db "UPDATE consider_aspects SET enabled=0 WHERE name='Skeptic'"
```

---

## Session feedback

| Key | Default | Description |
|---|---|---|
| `session_feedback_enabled` | `true` | When `true`, cairo asks the AI one reflective question at the end of a qualifying session and writes the answer as a `feedback` memory tagged `["feedback","auto-feedback"]`. Set to `false` to disable. |
| `session_feedback_min_messages` | `6` | Minimum total message count for a session to qualify for feedback. Sessions shorter than this (~3 back-and-forth exchanges) are skipped as too brief to yield durable signal. |

The feedback loop fires at session close (after the summarizer drains), uses the `summary_model`, and fails silently — a failed LLM call logs a warning but does not block shutdown.

---

## Automatic keys

These keys are written by cairo at runtime and are not normally set by the user. They are readable and can be overridden, but cairo will overwrite them on the next relevant event.

| Key | Description |
|---|---|
| `last_dream_at` | Timestamp of the most recent `cairo dream` run. Set at the end of each dream cycle. |
| `last_embed_model` | The value of `embed_model` at the last startup. At startup, if `embed_model` has changed since the previous run, cairo prints a warning to stderr listing which tables contain mismatched embeddings (`memories`, `notes`, `skills`, `summaries`, `facts`). Rows with a stale embed model are silently skipped during search — they are not deleted, just excluded. To recover: re-run `cairo learn` on affected projects to rebuild file-index embeddings; memories and facts require a `cairo dream` maintenance cycle to be re-embedded. |

---

## Template usage

Every key above can be used as `{{key}}` inside:

- The base system prompt (via `prompt_parts`)
- Role addenda (via `prompt_parts` with `trigger="role:<name>"`)
- Tool addenda (via `prompt_parts` with `trigger="tool:<name>"`)
- Custom tool `prompt_addendum` fields
- Skill content
- Memories (rendered into prompts)
- The soul itself (default soul references `{{ai_name}}`)

Unknown keys render as empty strings — missing identity values disappear gracefully.

---

## Adding your own keys

Just set them via `db_access`:

```bash
sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('project_name', 'cairo')"
sqlite3 ~/.cairo/cairo.db "INSERT OR REPLACE INTO config (key, value) VALUES ('preferred_language', 'English (US)')"
```

They're now available as `{{project_name}}` and `{{preferred_language}}` in any prompt, memory, or skill content. Use this to parameterize custom framings or specialized roles.

---

## Listing everything

```bash
sqlite3 ~/.cairo/cairo.db "SELECT key, value FROM config ORDER BY key"
```

Useful at the start of a session to remind yourself what's set.
