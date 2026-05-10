# Memory

Cairo remembers things across sessions using a layered persistence system. This document covers what persists, how it's used, and how to manage it.

---

## The five layers

| Layer | What it stores | Auto-injected into prompt? | Survives session end? |
|---|---|---|---|
| **Memories** | Stable facts about you, your preferences, and your projects | Yes — top 15 per turn | Yes |
| **Summaries** | Compressed turn history | Yes — top 4 per turn | Yes |
| **Facts** | Atomic observations extracted during summarization | No — searchable on demand | Yes |
| **Notes** | Ephemeral scratch space | No | Yes |
| **Learn index** | Per-project file summaries and embeddings | Partial (project list) | Yes |

All five live in `~/.cairo/cairo.db`.

---

## Memories — what cairo always knows

Memories are the most direct form of persistence. The 15 most recent memories are pasted into every system prompt under `## Memories`. This means cairo *starts knowing* these things — it doesn't need to search for them.

Cairo creates memories during `/init` and adds them over time when it learns something worth keeping. You can also ask it to remember something explicitly:

```
> remember that I prefer single-file changes over scattered edits
```

### Listing memories

In the line CLI:
```
> /memories
```

Pinned memories are marked `[P]`. Each row shows an id and content.

### Pinning memories

A pinned memory is protected from automatic removal by the dream maintenance cycle. Pin anything you want to keep indefinitely:

```
/pin 7
```

To remove the pin:
```
/unpin 7
```

Pinned memories also show `[P]` in `/pinned`.

### Memory retrieval

The default retrieval (the 15 most-recent) is ordered by recency, not relevance. For older or topic-specific memories, cairo can search by semantic similarity:

```
> what do you remember about my database preferences?
```
(Cairo calls `memory_tool(action="search", query="database preferences")` internally.)

Retrieval ranking is `cosine similarity × decayed importance`. Importance is a 0–1 score assigned when a memory is created (default 0.5); it decays slowly over 180 days but never below 0.6× the base. Pinned memories are never auto-removed regardless of their importance score.

**Note:** importance is the retrieval-relevance score. It is distinct from an internal lifecycle signal called `weight` that governs auto-promotion and auto-removal during dream cycles. Don't expect them to behave the same way — they measure different things.

---

## Summaries — compressed conversation history

After enough turns accumulate in a session (configurable via `summary_threshold`, default 4 unsummarized messages), the summarizer condenses them into a paragraph-sized summary and writes it to the `summaries` table.

Summaries are **global** — they aren't scoped to a session. A summary written in last week's planning session is findable from today's coding session. This is how "we already talked about this" actually works across context boundaries.

The 4 most recent summaries are injected into every prompt under `## Conversation context`. Older summaries are reachable via semantic search.

The summarizer runs automatically in the background after each turn. You can trigger it manually with `/dream` (which runs the full maintenance cycle).

---

## Facts — atomic observations

When the summarizer writes a summary, it also extracts atomic facts — one-sentence observations like "user's name is Alex" or "project uses Go 1.25 modules." Facts are stored in the `facts` table.

Facts are not injected automatically — they'd add too much noise. Cairo searches them on demand via `memory_tool(action="search")`. If a fact is important enough to always be present, ask cairo to promote it to a memory:

```
> take fact 12 and add it to your memories
```

---

## The dream cycle

`cairo dream` is a maintenance pass that runs over the memory system. It has three roles:

1. **Writer** — scans recent messages for expressed intent to remember something that wasn't followed up with a `memory_tool` call, and writes the missing memories.
2. **Curator** — finds near-duplicate memories and facts (by cosine similarity) and merges them. Pinned memories are never the losing side of a merge.
3. **Dreamer** — writes a narrative of the day's sessions to `~/.cairo/dreams/<YYYY-MM-DD>.md`.

You can trigger a dream manually:

```bash
cairo dream
```

Or from inside a session:
```
/dream
```

Dream runs are logged in the `dreams` table. To see recent runs:
```
/dreams
```

To read a narrative:
```
/dreams 2026-05-09
```

---

## The learn project index

The learn index is separate from the four text-based layers above. It's a per-project map of your codebase: each indexed file gets a model-generated 1–2 sentence summary plus a semantic embedding.

Index a project:
```
/learn ~/myproject
```

Or from outside a session:
```bash
cairo learn ~/myproject
```

Once indexed, cairo can answer "where does X live?" questions efficiently without reading every file. The index is incremental — re-running `/learn` on an already-indexed project only processes changed files.

---

## Honest limits

- The 15-memory auto-injection is by recency, not relevance. If you have many memories, recent-but-irrelevant ones can crowd out older-but-relevant ones. Use explicit search when this matters.
- Semantic search decodes every embedding BLOB in the table. At hundreds of rows this is fast; at thousands it becomes noticeable. The ROADMAP tracks a fix.
- If you change the `embed_model` config key, old embeddings won't match new queries — semantic search silently skips mismatched rows. Cairo warns at startup if this affects any tables.
- Facts and summaries are generated by a small, fast model (`ministral-8b` by default). Quality is bounded by that model's capability. Change `summary_model` if you want heavier distillation.

---

## Config keys that affect memory

| Key | Default | Effect |
|---|---|---|
| `memory_limit` | 15 | How many memories are auto-injected per turn |
| `summary_threshold` | 4 | Unsummarized messages before a summary is written |
| `summary_context` | 4 | How many recent summaries go into the prompt |
| `summary_model` | `ministral-8b:latest` | Model used for summarization and fact extraction |
| `memory_dedup_threshold` | 0.85 | Cosine similarity above which memories are considered duplicates at write time |
| `dream_curator_similarity_threshold` | 0.92 | Cosine similarity threshold for dream-pass merges |

Change any of these with:
```bash
cairo config set memory_limit 20
```
Or ask cairo:
```
> set my memory limit to 20
```
