> **See also:** [concepts/memory-model.md](../concepts/memory-model.md) is the authoritative reference for this topic. This file documents the AI-facing perspective — which layer to use and when.

# Memory, facts, notes, summaries

Five persistence layers, five jobs. Pick the right one — wrong layer is the most common identity-management mistake.

---

## Decision tree

```
Is it identity-level — something I should always start every turn knowing?
    yes → memory_tool(action="add")
    no  → continue

Is it work-product — a draft, plan, or context for *this* effort?
    yes → write a scratch file and read it on demand, or store as a memory with a "scratch" tag
    no  → continue

Is it a multi-step workflow I'll invoke by name more than once?
    yes → skill(action="create")
    no  → continue

Is it an atomic durable observation extracted from conversation?
    (you almost never write these directly — the summarizer does)
    if you find one via memory_tool(action="search") that earned permanence,
    promote it manually by adding a memory with memory_tool(action="add")
```

---

## Memories

**Tool:** `memory_tool(action="add|search")` · **Table:** `memories`

- Auto-injected into every prompt under `## Memories` (top `memory_limit`, default 15).
- Search ranked by `cosine × decayed_importance`. Decay floors at 0.6× over 180 days; importance resets when you update the row via `db_access`.
- `memory_tool(action="search")` searches across memories, facts, and summaries in one call.
- For delete or update: use the `db_access` skill with `bash sqlite3`.

Add a memory when you learn something the user (or you) want active *every* turn. Don't add work-in-progress here.

### Pinning

`memories.pinned_at` is a timestamp column. When set, the memory is **pinned** — it survives auto-dump regardless of `weight`, and the dream-pass curator will never use it as the merged-away source in a deduplication. Use pinning for memories that are load-bearing and must not disappear under normal lifecycle pressure.

- `memory_tool(action="pin", id=<int>)` — mark a memory pinned. Restricted to roles `thinking_partner`, `dream`, `orchestrator`.
- `memory_tool(action="unpin", id=<int>)` — remove the pin. Same role restriction.

Pinned memories show a `[P]` indicator in `/memories` listings, `/pinned` output, the TUI palette, and the search panel.

### Importance vs. weight — two separate signals, never combined

`memories` has two numeric fields that are easy to confuse. They measure different things:

| Column | Purpose | Set by | Used for |
|--------|---------|--------|----------|
| `importance` (default 0.5) | Retrieval salience. How relevant this memory is to search queries. Decays slowly (floors at 0.6× over 180 days). | You (at write time), promoted by dream auto-promote. | Retrieval score: `cosine × decayed_importance`. |
| `weight` (default 0.5) | Lifecycle signal. How actively this memory is being used. Bumped +0.001 on each retrieval; decayed −0.001 nightly for memories not retrieved in 24h. | Harness (automatic). | Auto-promote (weight ≥ 1.0 → importance=1.0) and auto-dump (weight ≤ 0 → soft-deleted). |

**Do not multiply them at retrieval.** The retrieval score is `cosine × decayed_importance` only. `weight` is internal lifecycle machinery. An auto-promoted memory with high importance but low weight (currently cold) would be wrongly suppressed if weight were included in retrieval scoring — which would defeat the purpose of promotion.

Auto-promote is the one-way bridge: `weight ≥ 1.0` → `importance = 1.0`. After promotion, weight continues its normal lifecycle independently.

---

## Notes

**Table:** `notes` (the `note` tool was removed in v0.3.0)

- Not injected into the prompt.
- For scratch work, write a temp file and read it on demand, or store in `memories` with a `scratch` tag.
- Direct DB access: `bash sqlite3 ~/.cairo/cairo.db 'SELECT title, content FROM notes'`.

---

## Summaries

**Table:** `summaries` (no direct tool — search via `memory_tool`)

- Written *for* you by a background summarizer after `summary_threshold` (default 4) unsummarized messages.
- Top `summary_context` (default 4) recent summaries auto-inject as `## Conversation context`.
- **Cross-session** — a summary from any session is searchable from any other.
- You don't write summaries directly.

Search summaries when you suspect *"we figured this out before"* — use `memory_tool(action="search", query="...")`.

---

## Facts

**Table:** `facts` (no direct tool — search via `memory_tool`)

- Atomic observations extracted by the summarizer. Immutable rows.
- Not auto-injected (would be too noisy).
- `memory_tool(action="search")` includes facts in its results — no separate fact-search tool needed.
- To promote a fact to a memory, add a new memory via `memory_tool(action="add", content="...")`.

Facts vs. memories: facts are *single durable observations* extracted automatically. Memories are *active identity knowledge* you start every turn knowing.

---

## Dreams

**Tables:** `dreams`, `dream_log` (no direct tool — `db_access` only)

`cairo dream` is a nightly maintenance cycle over the memory system. It runs a sequence of roles — **writer, curator, dreamer, reviewed_at marking** — all live as of the current build.

**Writer role — what it does for you:**

The writer reviews unreviewed conversation messages looking for expressions of memory intent — phrases like "I should remember that…" or "note that…" — that were NOT followed by a `memory_tool` call. When it finds one, it writes the missing memory on your behalf and logs the action to `dream_log`.

This is a safety net, not a substitute for calling `memory_tool` yourself. If you expressed intent to remember something and the session ended before you got to it, the writer may catch it on the next dream run.

**Curator role — what it does for you:**

The curator runs after the writer. It does pairwise cosine similarity over your unreviewed memories and facts and merges near-duplicates (default threshold 0.92). For memories, pinned always wins; otherwise higher importance, then lower ID. Both-pinned pairs are logged as conflicts and left intact. For facts, no pinning — higher importance wins.

A merged row is **archived**, not deleted: `archived_at` is set, and the row lingers one full dream cycle. The next `cairo dream` hard-deletes any row with `archived_at IS NOT NULL`. That gives a 24-hour window to reverse a bad merge.

**Reading `dream_log`:**

```sql
-- recent dream activity
SELECT id, dream_id, action, target_table, target_ids, note
FROM dream_log
ORDER BY id DESC
LIMIT 20;

-- memories the writer added on your behalf
SELECT id, dream_id, target_ids, note
FROM dream_log
WHERE action = 'wrote_missing_memory'
ORDER BY id DESC;

-- recent curator merges (memories + facts + conflicts)
SELECT id, dream_id, action, target_ids, note
FROM dream_log
WHERE action IN ('merge_memories','merge_facts','merge_conflict_both_pinned')
ORDER BY id DESC LIMIT 20;
```

`target_ids` is a JSON array of the affected row IDs. Cross-reference with `SELECT id, content FROM memories WHERE id IN (...)` (or facts) to inspect them.

**Undo a recent merge:** read the `note` column of the relevant `dream_log` row. It contains the verbatim reversal SQL (e.g. `UPDATE memories SET archived_at = NULL WHERE id = 42`). Execute within 24 hours — the next `cairo dream` hard-deletes any row with `archived_at IS NOT NULL`.

**Dream session records:** one `dreams` row per dream-pass date. Schema: `id`, `created_at`, `date TEXT UNIQUE`, `narrative_path`, `themes`, `mood`, `state_daily_ref`, `last_edited_at`. No embedding — dreams are not searchable.

**Dreamer role — what it does for you:**

After the writer and curator complete, the dreamer synthesises the day into a narrative file at `~/.cairo/dreams/<YYYY-MM-DD>.md`. The file opens with a YAML frontmatter block:

```yaml
---
themes: debugging, planning, momentum
mood: focused
---
```

The body is creative Markdown prose — 200–500 words — shaped by the day's ritual mood (`state_daily`). It is not a changelog; it is a felt account of the day's work and housekeeping.

Read a dream:

```bash
cat ~/.cairo/dreams/2026-05-03.md
```

Session-start context injection (reading the dream into the prompt at the start of each session) lands in Phase 5.

---

## Search mode rule of thumb

- `semantic` — *"find anything that means roughly this"*. Cheapest. Default.
- `exact` — *"find these literal words / this filename / this error"*.
- `hybrid` — *"find anything related, by meaning or by keyword"*. Best general-purpose mode when scope is broad.

---

## When you change `embed_model`

Old rows have a different model tag and will be *skipped* in semantic search. Cairo prints a startup warning listing which tables are affected. Memories and facts can be re-embedded via `cairo dream` or direct DB work. Do not assume search is comprehensive after a model change.

Note: `cairo learn` uses a separate key — `embed_model_code` — for code chunk embeddings. Changing `embed_model` does not affect the code index space. See `embed_model_code` in [config-keys](../reference/config-keys.md) for the learn migration workflow.
