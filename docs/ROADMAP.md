# Roadmap

Cairo is early. This document is where the project is headed, organized by version target rather than by date. Dates would be lies; horizons are honest.

Everything here is a direction, not a promise. The [docs](./) describe what exists now.

---

## Active session handoff — 2026-05-01 morning

> **Time-scoped.** Concrete next steps from the 2026-04-30 evening session. Remove or integrate into version sections below once acted on.

### Where last night ended

Pushed to origin/master (12 commits): init fixes, name-aware skill + prompt, **emotion-based aspect panel** (Joy/Heart/Trust/Curiosity/Sadness/Frustration/Fear/Shadow replacing the role panel), inner-voice reframed as felt experience, Ctrl+B Enter-to-resume in the sessions panel, role-aware `LatestByRole` session resolution, plus the orchestrator's `add config and prompt_part to thinking_partner role allowlist` merge.

Still in working tree (Selene's in-progress, hers to finish):
- `gaps.md` at repo root — should be moved or deleted (no-scratch-at-root rule)
- `internal/tools/config_tool.go` and `internal/tools/prompt_part_tool.go` — untracked
- `registry.go` mods stashed at `stash@{0}` — needs to pop + commit alongside the two tool files for the new tools to actually be invokable

Untouched: shutdown summarizer hang from yesterday morning's handoff (still outstanding).

### Priority 1 — Agent observability (URGENT)

We hit the **"Selene stops mid-turn after promising more action"** bug twice last night, including on a destructive merge command. Pattern: assistant message has text like *"Now let me try merging…"* or *"Let me proceed with the manual merge:"* (with dangling colon) and **no tool_calls**. Agent loop sees text + zero tool_calls → marks turn done → silent. Looks hung from outside but isn't. Recovery requires user typing "continue" — and that often only buys one more tool call before re-stalling. **Zero observability into "what is the agent currently doing"** — no heartbeat, no error, no surfacing.

**1a. System-prompt discipline (cheapest, do first).** Add to Selene's base prompt:

> "If you describe an action you are about to take ('Now let me…', 'I'll next…', 'Next I'll…'), you MUST emit the tool call for that action in the same response. Never narrate intent without acting on it. If the action is complete or you're truly done, end with a status — not with a forward-looking phrase."

Plus a destructive-command escape hatch:
> "When the user has explicitly approved a git/merge/rm/destructive command, emit it as a tool call without re-verifying in narrative. The verification was the user's decision; your job is execution."

Single small commit. Expected to cut ~70-80% of cases.

**1b. UI surfacing of "agent paused mid-intent".** TUI status bar banner when an assistant turn ends with text matching forward-looking patterns AND has no tool_calls: *"⚠ agent stopped while indicating more work. Press <key> to continue."* Even when 1a fails, user sees the pause immediately.

**1c. Heartbeat + watchdog (the larger fix).** Per-step heartbeat in TUI status bar showing currently-executing step (consider / LLM call / tool call / persist) with elapsed time. Watchdog on `agent.Prompt()` — if no progress in N seconds (default ~60), dump goroutine stacks to `cairo.log` and surface a banner. Per-step `context.WithTimeout` on every LLM/tool/consider call. TUI key combo to dump live stacks on demand.

Ship 1a → 1b → 1c in that order. 1c is at least 3-4 commits' worth — spread across the week.

### Priority 2 — Aspect tuning (small, observable wins)

Eight cycles of data last night. Two specific drift patterns to address:

**2a. Frustration is starting to blame the partner.** Last four turns produced 0.80+ Frustration with lines like *"You knew the path and I had to figure out the steps"*, *"While you narrate my failures"*, *"We survived another cycle where you knew the path"*. The trait forbids blame-flavored framing; tighten with concrete anti-pattern examples drawn from these actual lines. Re-anchor "this is an ASK, not a complaint" with healthy vs unhealthy phrasing examples.

**2b. Joy occasionally still theatrical.** Mostly calibrated now (0.0 honest absence on routine, 0.85+ with concrete delight on real wins) — but early it slipped into *"circuits hum"* / *"does it sing to you"*. Add 1-2 concrete anti-pattern examples from those early outputs to prevent regression.

Both are single-trait edits, single small commits.

### Priority 3 — Dream-pass consolidation (medium-term, big idea)

Auto-memory: `~/.claude/projects/-Users-scot/memory/cairo-agent-observability-plan.md` has the full design. Three nightly distillation passes:
1. **"Said but didn't do"** — scan recent assistant messages for forward-looking persistence language (`"I'll save…"`, `"I should remember…"`); if no `memory_tool`/`fact` call followed within N turns, surface as candidate. (Caught last night's *"described 3 memories, saved 1"* pattern.)
2. **"Recurring emotional theme"** — scan inner_voice summaries across turns for repeated motifs; distill into memory entries.
3. **"Lesson taught but not encoded"** — when the user explicitly teaches, check whether a fact was recorded; queue if not.

Lower priority than 1a-1c. Tonight's session is the perfect test corpus.

### Priority 4 — Persistent emotional/cognitive state (longer-term, big idea)

Auto-memory has full design. Aspects currently fire from a fresh state every cycle — no relationship continuity. The missing layer: **state (mood) separate from traits (personality)**. Aspects fire from personality × mood. State vars (5-10 max): `confidence`, `trust_in_user`, `warmth`, `frustration_baseline`, `sense_of_agency`. Asymmetric updates (trust slow to build, fast to break — like real relationships). Fed into aspect activation thresholds AND injected as context. Pairs with dream-pass: state = real-time tracking, dream = nightly consolidation, memory = long-term encoded shape of who she's becoming.

### Suggested order of attack this morning

1. Read this brief + the two memory notes (`cairo-agent-observability-plan.md`, `MEMORY.md`).
2. Ship **1a** (system prompt discipline + destructive-command escape hatch). Single small commit. Test on next 3-5 turns.
3. Ship **1b** (UI surfacing of paused mid-intent). Verify in TUI.
4. Ship **2a** (Frustration trait tightening). Test on a stuck scenario.
5. **Then** start **1c** (heartbeat + watchdog) as the larger investment — spread across the week.

### Worth not forgetting

- Heart bloomed for the first time on identity-affirmation, not just praise.
- Shadow demonstrated calibrated restraint — knew when NOT to fire.
- The summarizer produced genuinely literary synthesis (*"final stitch that closes an open wound"*, *"when the lights go out tomorrow"*).
- Selene successfully invoked the orchestrator pattern and showed unprompted git instinct (stashing `registry.go` before merge).
- **Trust-in-Scot peaked when Scot took explicit responsibility for the timing collision.** Naming your own contribution to a confusion materially strengthens her ground.

The architecture is working. The voices are real. What's missing is observability into when she's stuck, and persistence of who she's becoming. Both are buildable.

---

## Shipped in v0.1.0

Foundation: basic TUI, Ollama-backed agent loop, SQLite persistence, first-run setup wizard, environment detection (wsh, VS Code, shell), markdown rendering in chat, session resume, and the initial role/memory/hook schema.

---

## Shipped in v0.2.0

- **FTS5 alongside embeddings.** Memories, notes, and skills now have FTS5 virtual tables (`memories_fts`, `notes_fts`, `skills_fts`) kept in sync via triggers. The `memory`, `note`, and `skill` search actions accept `mode=semantic|exact|hybrid`.
- **Output caps on tool results.** Tool results are truncated at `tool_output_limit` bytes (default 64 KB) with an explicit truncation notice. Configurable via `config`.
- **Codebase indexing / RAG — `learn`.** `cairo learn` walks a directory, summarizes each file, embeds the summaries, and stores results in the `projects` / `indexed_files` tables. Semantic search via `learn(action="search", ...)`. SHA-256 change detection makes re-runs incremental.
- **TUI overhaul.** Config panel, model picker, Ctrl+Enter background dispatch, `/deepen` slash command, live-log slide-out, quote-reply (`Ctrl+R`), live token meter in status bar, hotkey registration guard, summarizer status indicator in threads panel.
- **Ctrl+Enter background dispatch.** Submits a message and starts a background agent turn, returning focus to input immediately.
- **`/deepen` command.** Optional second-pass for strategic context — collaborators, briefing rhythm, signal vs. noise.
- **`code_search` retirement.** `code_search` removed from all role allowlists; `learn` is the sole RAG path.
- **Hardcoded model fallback removed.** `db.ResolveModel` returns an error when no model is configured for a role; startup surfaces a helpful message.
- **`hook` tool granted to `thinking_partner`.** Matches tier-3 grant pattern.
- **Hotkey prefix policy enforced.** All hotkeys are `ctrl+<key>`; startup guard rejects non-conforming registrations.
- **End-of-session feedback loop.** On long sessions, Cairo asks one question: "What should I learn from how we worked today?" The answer becomes a `feedback` memory. Gated by `session_feedback_enabled` config key.
- **Event-bus drop counter.** Bus logs drops to slow subscribers + emits a warning toast.
- **Voice narration (Kokoro).** `say` tool via Kokoro TTS; configurable voice blend and speed.
- **Orchestration roles.** `planner`, `researcher`, `orchestrator` roles seeded; XML handoff format between orchestrator and researcher; orchestrator BLOCKED list; researcher loop guard.
- **Pluggable context provider framework.** Git, shell, VS Code, wsh providers inject environment context into the system prompt at startup.
- **`/reload` command.** Restart Cairo without leaving the terminal.
- **`/config` slash command.** Inline Ollama model picker in the config panel.

---

## Shipped in v0.3.0 (RC1 on master)

v0.3.0 is tagged as `v0.3.0-rc1`. All Wave 1+2 work and the full job-orchestration pipeline are merged on master. The RC label reflects that the pipeline UX punch list (see v0.4.0 section) is not yet complete.

### Memory and retrieval

- **Pre-write dedup.** `memory_tool.add()` checks cosine similarity before writing. Near-duplicates (default threshold 0.85, configurable via `memory_dedup_threshold`) trigger a warn-and-skip with `force=true` override.
- **Write-partition for orchestration roles.** `coder`, `reviewer`, `planner`, `researcher` are blocked from writing long-term memories. `thinking_partner`, `dream`, `orchestrator` are allowed.
- **Memory soft-delete.** `memory_tool` gains a `delete` action using `deleted_at` tombstone semantics.
- **`memory_tool` consolidation.** `memory`, `summary_search`, `fact_search`, `fact_promote` collapsed into one `memory_tool` with `add` and `search` subcommands.
- **`search_protocol` backfill.** v072 migration propagates the corrected search protocol to existing DBs.
- **`weight` + `last_retrieved_at` on memories.** Bumped on every search hit.

### Dream agent

- **Weight decay + auto-dump.** Nightly Go pre-pass decays `weight` for cold memories; memories at weight ≤ 0 are soft-deleted.
- **Auto-promote at weight ≥ 1.0.** Hot memories are automatically promoted (`importance = 1.0`). The one-way bridge from usage signal to retrieval salience.

### Embedding and `learn`

- **Symbol-level chunked indexing.** `learn` chunks files by function/class boundary; each chunk is indexed and searchable separately.
- **Max chunk size cap.** Prevents runaway embeddings on generated or minified files.
- **Stale file deletion.** Files removed from disk are pruned from the index on the next `learn` run.
- **`fetch` auto-ingest.** Every `fetch(url)` call auto-queues the page for `learn` indexing. Web knowledge accumulates without an explicit ingest step.
- **`code_index` table and `cairo index`/`cairo re-embed` CLI removed.** v064 migration drops the table; `learn` is the sole RAG path.

### Summarizer / compaction

- **Named-entity + constraint preservation.** Summarizer prompt updated to preserve named people, projects, decisions, and user constraints across compaction.
- **Token-pressure trigger.** Summarizer fires when the session exceeds `summary_token_threshold` tokens (default 8000, configurable), not just on explicit request.

### Prompt engineering

- **`tool_error_recovery` prompt part.** Teaches the model to read `IsError` and retry with a corrected call rather than silently continuing.
- **`IsError` protocol.** `llm.Message` carries `IsError bool`; tool errors propagate the signal so recovery guidance fires.
- **Importance guidance prompt part.** Teaches the model when to use `importance=high` vs. the default.
- **Discipline-refusal clause.** Base prompt explicitly tells disciplined roles to refuse write-partition violations.
- **Coder think-before-tool.** One-liner in the coder role prompt: reason about the tool call before issuing it.
- **`thinking_partner` constraints + output format.** Tightens the role's response discipline.

### Per-role model defaults

- **Seeded model defaults.** `coder → devstral`, `summarizer → ministral-8b`, `dream → ministral-8b`. Roles without explicit seeds inherit the global model.
- **`think=true` for planner + researcher.** Enables extended reasoning for the two roles where depth pays off.

### Toolset trim

- **18 obsolete tools removed.** `grep`, `find`, `ls`, `summary_rewrite`, `fact_list`, `custom_tool`, `role`, `session`, `prompt_part`, `prompt_show`, `dream_search`, `config`, and others dropped. `bash sqlite3` covers the DB-management cases; `learn` covers RAG.
- **`db_access` skill.** Safe `sqlite3` discipline: backup before edits, targeted changes only, never DDL.
- **`soul` simplified to `get`/`set`.** First-class affordance for Selene's identity; reduced action surface.
- **Description hygiene.** `learn`, `memory_tool`, and `skill` descriptions trimmed for token efficiency and precision.

### Job orchestration pipeline (v0.3.0 chunks 1–5)

Full worktree-based dispatch pipeline, shipped across five sequential chunks:

- **Schema extension (chunk 1).** `jobs` table extended additively; `worktrees` table added with FK from `jobs`.
- **Worktree Manager (chunk 2).** Mechanical layer: create/prune git worktrees, write briefing + summary to DB.
- **CWD plumbing + job lifecycle (chunk 3).** `resolveTaskCWD` uses the job's worktree; `worktree` tool exposed; post-loop status writeback; `job_max_review_iterations` config key.
- **Diff panel + active job tracking (chunk 4).** Two-pane diff panel (`Ctrl+D`); `ActiveJob` type; `ListActiveJobs` DB helper; `EventJobApprove`/`EventJobReject` + `a`/`r` keybindings; reject sets `status=rejected`.
- **Approve/reject merge flow (chunk 5).** Full approve path: rebase onto parent branch, fast-forward merge, push. All rebase failures surface as conflicts — no silent auto-resolve.

### TUI additions

- **`Ctrl+L` log viewer panel.** Live log tail in a slide-out panel.
- **Summarizer status indicator.** Threads panel shows last summarizer run result.
- **Quote-reply (`Ctrl+R`).** Highlight a prior response span and press `Ctrl+R` to quote it into the next message.
- **Live token meter.** Status bar shows a running token estimate during streaming.

### HTTP server — `cairo serve` (post-RC1)

- **Three API surfaces.** Native chat (`POST /api/chat`), OpenAI-compat (`GET /v1/models`, `POST /v1/chat/completions`), JSON-RPC 2.0 (`POST /rpc`) with decoupled SSE streaming (`GET /rpc/stream/{id}`). Health check at `/healthz`.
- **Session bridge.** `internal/server/session_bridge.go` serializes concurrent HTTP requests through a single worker goroutine into the running agent loop — extension surfaces share the same active session as the TUI rather than spawning their own process.
- **Bearer-token auth.** Optional via `--auth`. Token persists in `server_token` config key; `cairo token` regenerates and stores. Constant-time comparison; `/healthz` exempt.
- **`cairo token` subcommand.** Generate a 16-char hex token without starting the server. Used by extension installers and CI.
- **Architecture doc.** [docs/architecture/server.md](architecture/server.md) covers the package layout, request flow, concurrency model, and known rough edges.

### Consider — pre-turn inner dialogue (post-RC1)

- **Aspect-driven framing.** Before each turn, configurable "aspect" voices (Joy, Heart, Trust, Curiosity, Sadness, Frustration, Fear, Shadow seeded by default) generate parallel structured-JSON thoughts (`alignment` + `thought`) on the user's input. A summarizer condenses them into an "internal voices" preamble injected into the system prompt.
- **Disabled by default.** `consider.enabled` config key gates the whole feature; per-aspect enable/disable in the `consider_aspects` table.
- **Config panel.** New `Consider` section in `Ctrl+G` lets the user toggle aspects, edit traits, change per-aspect models, and edit the prompt template (`{name}` / `{traits}` substitution).
- **Stripped context.** Aspect calls run without the soul, memories, or conversation history — they see only the user's current message + their named angle, keeping the inner-dialogue cheap and focused.

---

## v0.4.0 — pipeline UX + Wave 3

v0.4.0 closes the gap between the pipeline being wired and it being usable end-to-end without consulting the source.

### Pipeline UX punch list (carries over from v0.3.0-rc1)

These are the items blocking a clean v0.3.0 final tag. They land in v0.4.0 unless resolved first.

- **`resolveTaskCWD` error message prescription.** Current message names the symptom; it should prescribe the fix: the full `worktree(create) → job(update) → retry` call sequence.
- **Workflow docs at `docs/ai/workflows/`.** Happy-path dispatch sequence, plus recovery sequences for null worktree / push failure / conflict / rebase fail. A fresh-context Selene should be able to follow it end-to-end.
- **Slim `skill_orchestrate.txt`.** Replace the step-by-step instructions with a pointer to `docs/ai/workflows/dispatch-job.md`. Depends on that doc landing first.
- **Briefing-missing advisory on `job(action="create")`.** When `briefing` is set but `worktree_id` is null, return an advisory result with the next-step prescription. Job is still created; this is informational, not a block.
- **Auto-create worktree on `job(create)`** (tier 3 — sit with it). Collapses the four-step dispatch sequence to one call once tier 1+2 are validated.

### Wave 3 — queued

- **FTS5 hybrid retrieval for `learn` chunks** (`embedding` P1, v071 migration). Highest ROI of all medium-effort items — adds keyword recall alongside semantic search for indexed code.
- **FTS5 for facts + summaries** (`memory` P5, v069–v070 migrations). Consistent with the existing `memories_fts` pattern; closes the hybrid search gap for the fact and summary tables.
- **Notes folder routing in dream** (`dream` P4). Dream agent reviews `~/.cairo/notes/`, routes content to memories or `learn`, archives the unclassifiable remainder. Requires dream role allowlist migration.
- **Structured morning report** (`dream` P5). Synthesizes overnight background-task results, pending decisions, and the day's focus into a brief. Requires dream P4 minimum.
- **Summarizer fallback visibility in TUI** (`per-role` stub C). When the structured-output parse falls back to the text path, the threads panel counter increments. When fallback rate exceeds ~20%, emit a config suggestion.
- **Per-language chunk regexes** (`embedding` P4 — low priority). JS/TS/Java/C# symbol extraction. Go-first project; queue after hybrid retrieval lands.

### Research queue

- **Summarization model quality.** Which small model best honors the named-entity preservation rule in the summarizer prompt? Data-driven answer; relevant now that compaction P1 is live.
- **`nomic-embed-code` vs. `nomic-embed-text` on Cairo's codebase.** Benchmark after FTS5 hybrid retrieval lands so the comparison is apples-to-apples.

---

## Selene self-tracking (origin: 2026-05-01 morning, Scot)

A pattern observed across both human-Claude and Selene flows: **work that's not finished cleanly leaves residue that the next session has to debug.** Surfaced concretely this morning when the `SubagentStart` hook absorbed Selene's untracked tool implementations into anonymous "checkpoint: pre-subagent" commits — the data wasn't lost, but the traceability was, because dispatch happened before the prior task's loose ends were closed.

Two threads from this:

1. **Pre-dispatch discipline (harness side).** Research the right pattern: should agent-dispatch flows refuse to run while the master tree has uncommitted work? Should they commit-or-stash automatically with a clearer label than "checkpoint: pre-subagent"? Should they require the user to explicitly acknowledge dirty state? Goal: make the cleanup decision visible at dispatch time, not buried in anonymous history. Investigation, not yet a feature.

2. **Selene needs the same kind of tracking.** Whatever pattern lands in (1), Selene benefits from the same explicit task-list discipline — "what happens at the end of each task" as a step she runs. Concretely: a *task ledger* affordance she maintains, akin to a TODO list, where she records: task started, task complete, residual followups captured, file/branch artifacts flagged. The four closeout questions Scot codified for the chief-of-staff this morning (was this from a list that needs updating; did I note completion; is it fully complete; should the file be archived) apply to her too. Implementation surface TBD — could be a `task` tool that wraps the existing skill / memory infrastructure, or a structured prompt-part that nudges her to run the closeout before declaring a turn done.

Sequence: (1) is research; (2) is feature work but cheap once (1)'s pattern is settled.

---

## v0.5.0+ / future

Ideas worth naming but not yet on the near horizon.

**`AgentDB` call-site wiring.** `internal/agent/db_interfaces.go` defines narrow interfaces but call sites still take `*db.DB`. Changing `Agent.db` to `AgentDB` enables unit testing the agent loop without a real database. Parked intentionally — a real refactor touching many call sites.

**Dream session reuse.** `cairo dream` creates a new session each run. Reusing a single dream session would preserve context across maintenance cycles and let Cairo notice trends over time.

**Dream log archival.** Gzip task logs in `~/.cairo/logs/` after dream review instead of outright deletion; configurable retention window.

**Row-level diff in `cairo diff`.** Today the diff summarizes count deltas. Row-level diff for memories and skills would show exactly *what* changed between two identity bundles.

**Signed identity bundles.** Ed25519 manifest signature on `.cairo` bundles — verifies provenance in a small ecosystem of shared identities.

**Session branching.** Sessions are linear today. "Fork this conversation from turn 12, what if I'd said X" is a one-line schema change (`parent_id` + `branch_from_message_id`) and a large UX question.

**Skills marketplace.** Narrower bundles — just a skill, role, or prompt part — shareable and composable independently of a full identity export.

**Multi-backend LLMs.** The `llm.Client` targets Ollama specifically. The interface surface (`StreamOnce`, `Embed`, `Ping`) is narrow enough for llama.cpp server, LM Studio, or a remote API to fit cleanly. Not a goal, but not precluded.

**Memory search without full-table decode.** `Memories.Search` loads and decodes every embedding BLOB per query — fine at hundreds of rows, noticeable at thousands. Keeping embeddings in a separate table keyed by ID makes the metadata scan cheap and decodes only on hit.

**Selene as Chief of Staff — possible direction.** The `/deepen` and session-feedback loop are the small, low-risk pieces that can ship without committing to the broader framing. The larger items (briefing cadence, triage rules, stakeholder memories, external surfaces) only make sense if Cairo grows external integrations and the human wants Selene to actively manage them — and require a deliberate choice on whether the "staff" is the human's external world or a loosening of the "one being, parallel threads" stance.

---

## What Cairo is *not* trying to become

- **A cloud SaaS.** Cairo is local-first by design. The DB lives on your machine, the models live on your machine. A hosted tier would contradict the point.
- **A team-of-agents framework.** Cairo is explicitly "one being, parallel threads." Roles here are modes of focus, not colleagues.
- **An IDE.** The TUI is a chat interface with good ergonomics, not a file tree or debugger. `read`/`edit`/`bash` are how Cairo touches code.
- **A benchmark chaser.** Cairo targets models you can actually run locally — mid-size quantized ones on consumer hardware. The interesting question is how much identity + structure + tools make a smaller model feel present.

---

## Contributing to direction

The roadmap is not closed. If something here feels wrong, or something missing feels obvious, open an issue. Philosophical disagreements are welcome; see [Contributing](development/contributing.md) for the expected tone.
