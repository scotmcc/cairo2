# Code Review Playbook (for a future Claude instance)

You are about to run a deep, READ-ONLY code review of a codebase. The user has done this before on another project (cairo, Go) and wants the same shape of output. This file is your operating manual. Follow it, adapt the area split to the new codebase, dispatch agents in parallel, do not read the code yourself in the main session.

## The contract with the user

- **Read-only.** No code edits. The only writes are findings markdown files.
- **You don't read the code.** Dispatch parallel Explore subagents. Each agent owns one area and writes one findings file. You write the index from their reports.
- **Output is for AI consumption, not human prose.** Terse. Every finding cites `path/file.ext:line`. No padding.
- **Severity is load-bearing.** Three labels, used exactly:
  - **major** — breaking: bug, race, data loss, security issue, broken abstraction the rest of the system relies on, doc that actively misleads
  - **medium** — doesn't work as designed: SRP violation, file/function clearly too long, duplicated logic across ≥2 sites, partial migration / inconsistent interface adoption, leaky abstraction
  - **small** — nit; not an issue today, will become one if built on (3rd near-duplicate emerging, function trending toward 100 lines, magic number used twice)
- **What NOT to flag:** naming preferences, comment style, micro-DRY of 2-line helpers, "could use generics," anything purely aesthetic. Style nits are explicit non-goals.

## Step 1 — Confirm scope with the user before launching

Ask, in one short message:
1. Output directory for findings (they will tell you where).
2. Whether this replaces, extends, or is independent of any prior review.
3. Approve your proposed area split (see Step 2).
4. Any areas to skip (vendored code, generated code, test fixtures).

Don't dispatch until they answer. They prefer one round of alignment over speculative work.

## Step 2 — Pick area split

For Go (cairo) I split by `internal/<package>` groups, ~7 areas, one agent each. For a **C# project**:

- Default to **one agent per `.csproj`** (project-level isolation), unless a project is huge — then split that project by namespace or top-level folder.
- Treat `Tests/` projects as their own area (or skip entirely if the user says so).
- Treat solution-level docs (`README.md`, `docs/`, ADRs) as a separate "docs" area, just like cairo had.
- For an ASP.NET app, sensible groups are: API/Controllers, Domain/Models, Infrastructure/Persistence, Background services, Auth/Middleware, Client/UI, Shared/Common.
- Aim for **6–10 agents**. Fewer = each one is too broad and skims. More = coordination tax + repeated context (every agent re-reads the project's CLAUDE.md / README).

For a "much bigger" codebase the user mentioned: **don't be tempted to give one agent two areas to keep the count down.** Add agents instead. Each Explore agent has its own context window — splitting work is free, doubling work per agent is not.

## Step 3 — Dispatch in parallel

All agents go in **one message with multiple Agent tool calls**, `subagent_type: "Explore"`, `run_in_background: true`. Cairo's seven launched cleanly in parallel and finished within ~2.5 minutes total.

Each agent gets a **self-contained brief**. Agents have zero conversation context — no shared memory of what you told another agent. Repeat the rubric, repeat the format spec, repeat the output path, in every brief.

### Brief template (copy and adapt per area)

```
You are doing a READ-ONLY code review of <repo path>. **Do not edit, write,
or change ANY code or non-findings files.** Your only writes are to your
assigned findings file.

## Scope (yours)
- <list of folders / projects / namespaces>

## Goal
Find issues. Not style nits, not naming bikesheds. Real problems.

## Severity rubric (use these exact labels)
- **major** — breaking: <area-specific examples>
- **medium** — doesn't work as designed: <area-specific examples>
- **small** — nit; trending toward an issue

Skip: naming preferences, comment style, micro-DRY of 2-line helpers,
anything purely aesthetic.

## What to look for
- Single Responsibility violations
- Overcomplexity / over-engineering
- Files > <threshold> lines
- Functions > <threshold> lines or with deep nesting
- DRY/SOLID violations that have actually duplicated
- Dead code, unreachable branches, vestigial flags
- <2-4 area-specific things — pull from the project's CLAUDE.md
  "landmines" section or equivalent>

## Output
Write to `<output-dir>/<area-name>.md`. Create the directory if needed.

Format — written FOR AN AI to act on later, not for human prose:

\`\`\`
# <Area> — Findings

**Reviewed:** <paths>
**Date:** <today>
**Counts:** major: N, medium: N, small: N

## Summary (2-4 sentences)

## Findings

### [major] Short title
- **Where:** `path/file.ext:line` (or range)
- **What:** one-sentence problem statement
- **Why it matters:** concrete consequence
- **Action:** specific direction (no code)
\`\`\`

Order findings by severity. Every finding MUST cite file:line. If you can't
cite a line, the finding isn't ready — drop it or sharpen it. Be terse.

Read <repo>/CLAUDE.md (or README.md) for project context. Do not read
<list of dirs to skip — sandboxes, vendored, generated>.

Report path + counts in under 100 words.
```

### Per-area customization that mattered for cairo

For each area, after the generic checklist, **add 2–4 bullets pulled from the project's CLAUDE.md "landmines" or equivalent doctrine notes.** Examples that produced strong findings:

- DB area: "Memory scoring doctrine: the retrieval score at `internal/db/memories.go:224` should stay `cosine * decayImportance(importance)`. Verify this is intact." → agent confirmed it, eliminated a worry.
- Walker area: "`.claude/worktrees/` MUST be skipped — landmine." → agent found the walker excludes `.cairo/` but NOT `.claude/worktrees/`, the headline finding of the whole review.
- TUI area: "TUI hotkeys MUST be ctrl+<key>. Bare vim-style hotkeys are forbidden." → agent found ~9 panels violating this.

**Lesson: the project's own documented landmines are where the best findings come from.** Read the project's CLAUDE.md / docs first, harvest landmine claims, and ask each agent to verify the relevant ones for their area.

For C# specifically, harvest things like:
- "We don't use AutoMapper in the X layer."
- "DI is composition root only — no service-locator calls anywhere."
- "Entity X is the aggregate root; child Y must not be saved directly."
- "Migrations live in project Z; project W has frozen schema."
- Async/sync boundaries, ConfigureAwait conventions.
- Nullability annotations: is the project nullable-enabled? Inconsistencies?

If the project has no doctrine doc, **ask the user** for 3–5 invariants worth verifying before you launch. Do not invent invariants — agents will hallucinate findings to "satisfy" them.

## Step 4 — Watch the notifications, do NOT poll

Background agents send notifications when they complete. Do not sleep, do not check files mid-flight. Acknowledge each completion in one terse line to the user (`"Agent N/M complete (area): X major, Y medium, Z small. K still running."`) and wait for the rest. Cairo's seven completed in 64–148 seconds each.

## Step 5 — Verify, then write the index

After all notifications fire:

1. **`ls` the output directory.** **Trust but verify** — at least one cairo agent reported "findings written" but the file was never saved. The agent's summary is what it *intended*, not what it did.
2. If a file is missing AND the agent's summary contains the full content inline, write it yourself from the report. Don't re-dispatch unless the content is also missing.
3. Write `00-index.md` (or whatever sorts first) with:
   - Severity rubric (repeated, so the index is self-contained)
   - Counts table (one row per area + total row)
   - One TL;DR paragraph per area
   - **Cross-cutting themes** — patterns that appeared in 3+ areas. This is where the index adds value beyond a directory listing. For cairo: silent error suppression in 3 areas, function-size sprawl in 3 areas, partial migrations in 3 areas, hotkey discipline not uniformly enforced.
   - **Recommended triage order** — your suggestion, framed as a suggestion, not a plan.

## Lessons learned from the cairo run

1. **One agent failed to actually write its file.** Always `ls` the output dir before declaring success. If you're lucky, the agent's summary has the content inline and you can salvage it without re-running. **Same applies to commit verification:** subagents that report "I committed X to file Y, Z" can lie about what was actually staged. Always `git show --stat <hash>` after a subagent commit to confirm the file list matches the report. (Hit this in a separate work session when a Haiku silently included `.claude/settings.json` in its commit and never mentioned it in the self-report.)
2. **Memory-flagged regressions weren't always found.** Cairo's memory had two known regressions (`/init` on resume, shutdown hang). The commands+cli agent didn't surface them — they live in another area. **Don't assume an agent will find what memory says exists.** If the user has named specific bugs, brief whichever agent owns the suspected code path with that bug name and the suspect commits/files.
3. **Counts produce useful signal.** A clean area (cairo's `internal/db/`: 0 major, 1 medium, 1 small) is itself a finding — it tells the user where they don't need to look. Surface this in the TL;DR.
4. **Reusing the rubric verbatim across briefs is fine, even encouraged.** Agents have no shared context; consistency comes from the briefs being near-identical.
5. **The user wants alignment before launch.** They explicitly said "I want to make sure we know where the new code review is" and asked about the severity bar. Don't skip Step 1.
6. **Don't nest orchestrators.** Each Explore agent does its own search. Don't have an agent dispatch sub-agents.
7. **Models:** `subagent_type: "Explore"` is the right choice. It's read-only, has Read+Grep+Bash, and is fast. Don't use `general-purpose` for this — too much capability, no read-only guarantee.
8. **Briefs were ~70–100 lines each.** That's fine. Verbose briefs produce focused findings; terse briefs produce generic findings.
9. **Cross-component round-trip bugs are invisible to per-area agents.** Per-area review by definition reads one slice; bugs that live BETWEEN components (DB write → DB read → reuse, serialization → deserialization symmetry, interface adoption gaps where producers and consumers drift) won't be flagged because no single agent owns both sides. Worth dispatching a separate "boundaries and round-trips" agent in addition to the per-area ones. That agent's brief: enumerate the data shapes that cross component boundaries, then verify symmetry between every writer and reader of each shape. (Discovered this when a separate session found three related bugs — ID dropped on DB read, ToolCallID missing in two message-construction sites, Arguments-as-object instead of JSON-string — none of which any per-area agent would have caught.)

## C#-specific tweaks for the next run

- **File size threshold:** C# tolerates longer files than Go. Bump the "flag long file" threshold to ~500 lines (vs. cairo's ~300). Bump function/method to ~80 (vs. ~60).
- **Look for:** synchronous-over-async (`.Result`, `.Wait()`), missing `ConfigureAwait(false)` in library code, `IDisposable` not honored, EF Core change-tracker misuse, DI registration drift between `Startup`/`Program` and consumers, controller actions doing business logic, fat constructors (>5 deps = SRP smell).
- **Skip by default:** `bin/`, `obj/`, generated `*.Designer.cs`, EF migration snapshot files (only flag the migration `Up`/`Down` themselves if relevant), packed/decompiled refs.
- **Tests:** ask user whether to review test projects. They often have their own SRP/duplication issues but the user may not care about those for this pass.
- **Solution structure first:** before splitting into agents, run `find . -name "*.csproj" -not -path "*/bin/*" -not -path "*/obj/*"` to see the project layout. Group small leaf projects together; split the biggest project internally.

## Final shape the user expects

```
<output-dir>/
  00-index.md            ← rubric, counts table, TL;DRs, cross-cutting themes, triage order
  <area-1>.md
  <area-2>.md
  ...
  playbook.md            ← optional; this file, if they want it preserved
```

That's it. Confirm scope, dispatch in parallel, verify the files, write the index, surface cross-cutting themes. Don't read the code yourself.
