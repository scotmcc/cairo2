# Memory model

Cairo has five kinds of persistent text — memories, summaries, facts, notes, and the learn project index. Each has a job. Understanding which is which is most of what you need to know about how Cairo "remembers."

All five live in the same SQLite database. All five are available to every role. Four of them carry embedding vectors for semantic search.

---

## Memories — stable, curated identity knowledge

**Table:** `memories` · **Tool:** `memory_tool`

A memory is a fact about the user, the project, or the being's preferences that should persist indefinitely. Things like "the user prefers blunt, terse responses" or "this project uses modernc.org/sqlite, not mattn."

Memories are added deliberately — by the being during `/init`, or whenever it learns something worth keeping. They're the equivalent of notes a thoughtful colleague would take in a notebook they'd reread for months.

**Shape:**
- `content` — free-form text
- `tags` — JSON array, optional
- `embedding` — vector for semantic search, optional

**Access patterns:**
- On every turn, the `memory_limit` most-recent memories (default 15) are pasted into the system prompt under `## Memories`.
- The model can call `memory_tool(action="search", query=...)` to pull older ones by semantic similarity, across memories, facts, and summaries in one call.
- The model can add memories via `memory_tool(action="add")`; updates and deletes require the `db_access` skill with `bash sqlite3`.

Each memory has an `importance` score (0.0–1.0, default 0.5). Search results are ranked by `cosine_similarity × decayed_importance`. Importance decays linearly from the base value to 0.6× over 180 days, then stays there. Updating a memory (via `db_access`) resets the decay clock.

**When to prefer memories:** things you want the being to *start every turn already knowing*, not just *be able to recall on demand*.

**Pinning:** a memory can be pinned (`pinned_at IS NOT NULL`) to protect it from lifecycle removal. Pinned memories survive auto-dump regardless of `weight`, and are never used as the archived source in a dream-pass merge. Pinning is how you mark a memory as permanently load-bearing. Use `/pin <id>` and `/unpin <id>` in the TUI or line CLI, or `memory_tool(action="pin", id=<int>)` from the agent. Pinned memories show a `[P]` indicator in listings.

---

## Summaries — compressed conversation history

**Table:** `summaries` · **Search:** `memory_tool(action="search")` (unified across memories, facts, and summaries)

A summary is a paragraph-sized distillation of a range of conversation turns. The summarizer goroutine (`internal/agent/summarizer.go`) runs after each turn if enough new messages have accumulated, and writes a single row covering messages `covers_from`…`covers_through`.

Summaries are **global** — they live across sessions. A summary written in one session is findable from another, which lets "oh, we figured this out last Thursday in the other terminal" actually work.

**Shape:**
- `content` — the summary paragraph
- `session_id` — provenance, not a scope filter
- `covers_from`, `covers_through` — message id range
- `embedding` — for semantic search

**Access patterns:**
- On every turn the most recent `summary_context` rows (default 4) are pasted into the system prompt under `## Conversation context`.
- The model can call `memory_tool(action="search", query=...)` to pull relevant older summaries (included alongside memories and facts).

**When summaries are created:** automatically, after a turn, when the number of unsummarized messages in the current session exceeds `summary_threshold` (default 4). The summarizer model is configurable via `summary_model`; it defaults to `ministral-8b:latest` — small and fast, because this runs often.

---

## Facts — atomic observations extracted during summarization

**Table:** `facts` · **Search:** `memory_tool(action="search")` (unified); promotion via `memory_tool(action="add")`

When the summarizer writes a summary, it also extracts atomic facts — single observations like "user's name is Scot" or "project uses Go 1.25." Facts are the raw material that could, if judged durable, become memories.

Facts are durable, specific domain truths — observations like "bubbletea v3 has a known vulnerability" or "Scot prefers files around 100 lines". They differ from memories in permanence intent: facts are things you expect to stay true indefinitely; memories are contextual impressions that may age out of relevance. Facts also carry importance scores with decay, but use `created_at` (not updated_at) since facts are immutable.

**Shape:**
- `content` — one-sentence observation
- `session_id`, `summary_id` — provenance
- `embedding` — for search

**Access patterns:**
- Facts are not injected into the prompt automatically — they'd be too noisy.
- To promote a fact to a memory, add a new memory via `memory_tool(action="add", content="...")` with the fact's content.

**When facts matter:** they're the bridge between "we talked about X last session" and "the being *always knows* X." Summaries preserve the gist; facts preserve the atoms; promotion preserves them forever.

---

## Notes — ephemeral scratch space

**Table:** `notes` · **Tool:** `note`

Notes are free-form scratch text. A draft, a working plan for a multi-turn job, a list of things to come back to. They don't go into the prompt automatically; the being reads them when it decides to.

**Shape:**
- `title`
- `content`
- `tags` — JSON array

**When to prefer notes over memories:** for work-product, drafts, or context that isn't a "fact about the world." A note saying "plan for refactor X: step 1..., step 2..." doesn't belong in memories (it's not identity-level) but also shouldn't disappear when the session ends.

---

## Learn project index — per-project file map

**Tables:** `projects`, `indexed_files` · **Tool:** `learn` · **Command:** `/learn [path]`

The learn index is a fifth persistence layer distinct from the four text stores above. It is project-namespaced (one `projects` row per indexed directory) and summary-first (each `indexed_files` row stores a model-generated 1–2 sentence summary plus its embedding, not the raw file content).

**Purpose:** answer "where does X live in this codebase?" questions efficiently. The model calls `learn(action="search", project=..., query=...)` to get ranked file-location results with summaries.

**How it replaced `code_index` / `code_search` (removed in v0.2.1):**
- `code_index` embedded raw file content into a single pool; `learn` embeds model-generated *summaries* into per-project namespaces.
- `learn` is intentional — the user triggers it with `/learn` or `learn(action="add")`. `cairo index` was more like a batch job.
- `learn` stores a project description (auto-generated from the file summaries), file-level metadata (type, bytes, SHA-256), and change-detection for incremental re-runs.

**When to use:** whenever the question is about a codebase the user has explicitly mapped. The `## Where to look for things` section of the base prompt directs the model here first for code questions. See also [Architecture: learn](../architecture/learn.md).

---

## How it fits together

Here's a rough picture of a turn's interaction with the memory model:

```
Turn N:
  incoming user message
  → system prompt composed:
      + ## Steering (user_steering config key, if set)
      + ## Memories (top 15, always loaded for thinking_partner)
      + ## Conversation context (top 4 summaries, always loaded)
      + ## Indexed projects (learn project list, when any projects exist)
      + ## Where to look for things (search_protocol base prompt part)
  → LLM streams response, may call:
      learn(search|list|...)                — codebase/file location questions
      memory_tool(add|search)               — permanent identity knowledge; search includes facts + summaries
      skill(create|read|...)                — reusable workflow instructions
      bash sqlite3 ...                      — direct DB access for notes, roles, config, dream records
  → turn completes
  → background summarizer runs:
      if unsummarized messages ≥ summary_threshold:
          write a summary row
          write any extracted fact rows
```

Every rough corner of this has a note in [ROADMAP.md](../../ROADMAP.md) — row-level diff, leaner search on large memory tables. The model as described is simple; the implementation has known scale corners.

---

## Dream pass

`cairo dream` runs a nightly (or on-demand) maintenance cycle over the memory system. Each run is a sequenced set of roles.

**Roles — all four shipped:**

| Role | Status | Job |
|------|--------|-----|
| **writer** | live | Scans unreviewed messages for expressed intent to remember ("I should remember X", "note that Y") that wasn't followed by a `memory_tool` call. Writes the missing memories and logs each one to `dream_log`. |
| **curator** | live | Pairwise cosine-similarity scan over unreviewed memories and facts; merge near-duplicates above the configured threshold (default `0.92`). For memories: pinned wins, else higher importance, else lower ID; both-pinned conflicts are logged but not merged. For facts: higher importance wins (no pinning). Each merge is one atomic transaction — `archived_at` UPDATE + `dream_log` INSERT. |
| **dreamer** | live | Synthesises the day's sessions and dream-pass mutations into narrative prose. Writes to `~/.cairo/dreams/<YYYY-MM-DD>.md` with YAML frontmatter (`themes:`, `mood:`) at the top; body is creative Markdown shaped by the day's ritual mood. |
| **reviewed_at marking** | live | At the end of each dream cycle, stamps `reviewed_at` on every memory, fact, summary, and message that was scanned. The next dream-pass uses these stamps to scope its work and avoid re-processing. Pre-dream ID snapshots ensure new memories the writer added during this run aren't incorrectly marked. |

**Fail-soft:** a role error is logged to stderr but does not abort the dream. Partial runs are acceptable.

**Audit trail:** every dream-pass mutation produces a `dream_log` row. Writer entries use `action='wrote_missing_memory'`. Curator entries use `action IN ('merge_memories', 'merge_facts', 'merge_conflict_both_pinned')`.

**Grace-period archive:** when the curator archives a loser, it sets `archived_at`. The row stays for one full dream cycle before hard-delete. The `dream_log` `note` column for each merge contains the verbatim reversal SQL (e.g. `UPDATE memories SET archived_at = NULL WHERE id = 42`) — execute within 24h to undo a bad merge.

**Dream session records:** one `dreams` row per run, keyed by date. Query via `db_access`: `bash sqlite3 ~/.cairo/cairo.db 'SELECT id, date, narrative_path, themes, mood FROM dreams ORDER BY id DESC LIMIT 5'`.

---

## Exact-match search alongside semantic

Memories, notes, and skills also have FTS5 (`memories_fts`, `notes_fts`, `skills_fts`) virtual tables backed by triggers on the source rows. The `memory_tool` and `skill` search actions take a `mode` argument:

- `semantic` (default) — embedding cosine similarity, ranked by `cosine × decayed_importance`
- `exact` — FTS5 keyword/phrase match; useful when you remember the words but not the meaning
- `hybrid` — both, deduplicated by id; semantic results first, then any FTS hits not already in the set

Hybrid is the right default for "find anything related to X." Exact is useful for recall by specific terms (e.g. searching for an exact filename or error string in a memory). Semantic is the cheap, narrow-by-meaning option.

---

## Known rough edges

- `memory_tool(action="search")` does a full-table decode of every embedding BLOB per query. Fine at hundreds of rows, noticeable at thousands. See ROADMAP.
- All embeddable tables carry an `embed_model TEXT` column. `SearchTopK` skips rows whose `embed_model` doesn't match the current query's model, preventing cross-dimension cosine comparisons. If you change `embed_model`, old embeddings are silently excluded from semantic search. At startup cairo prints a warning listing which tables are affected (memories, notes, skills, summaries, facts). To rebuild: re-run `cairo learn` on affected projects to regenerate file-index embeddings; memories and facts must be re-embedded via a `cairo dream` maintenance cycle or direct DB tooling.
- Summaries and facts are written by a small fast model (ministral-8b by default). The quality of the distillation is bounded by that model. If you'd rather spend tokens on stronger summaries, set `summary_model` to something heavier.
