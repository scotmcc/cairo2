# Docs — Findings

**Reviewed:** docs/, CLAUDE.md, README.md
**Date:** 2026-05-02
**Counts:** major: 3, medium: 2, small: 1

## Summary

The Cairo docs are well-organized with clear audience separation (concepts/ for contributors, ai/ for the agent). However, broken internal links to a non-existent `/guides/` directory affect usability, a version mismatch between CLAUDE.md and documentation content creates drift, and some content redundancy across concepts/ and ai/ folders suggests opportunity for consolidation.

---

## Findings

### [major] Broken links to `/guides/` directory
- **Where:** 8 files reference paths like `../guides/background-work.md`, `../guides/custom-tools.md`, `../guides/skills.md`, `../guides/portable-identity.md`
- **Files affected:** `docs/ai/skills.md:31`, `docs/architecture/overview.md:11,15`, `docs/architecture/database.md:14`, `docs/getting-started/first-run.md:18`, `docs/getting-started/quickstart.md:27`, `docs/reference/cli.md:50`, `docs/reference/config-keys.md:13`, `docs/reference/tools.md:7,69,114`
- **What:** Links reference `../guides/` but actual files live in `../development/` (e.g., `background-work.md`, `custom-tools.md` are at `docs/development/`, not `docs/guides/`)
- **Why it matters:** New contributors or automated tools following these links will encounter 404s; frustrates navigation
- **Action:** Replace all `../guides/` with `../development/` in the 8 affected files

### [major] Version mismatch: CLAUDE.md vs. docs
- **Where:** `/Users/scot/cairo/CLAUDE.md:3` states "Current release: v0.2.0" but docs extensively document v0.3.0 features
- **What:** CLAUDE.md lists current release as v0.2.0; docs/FEATURES.md, docs/ROADMAP.md, and docs/releases/v0.3.0-rc-internal.md all reference v0.3.0 as the active development/pre-release target with multiple "New in v0.3.0" callouts
- **Why it matters:** Newcomers reading CLAUDE.md will think the project is at v0.2.0 when docs indicate v0.3.0-rc is the current working version; creates false impression of staleness
- **Action:** Either (a) update CLAUDE.md to reflect current dev version, or (b) clarify that v0.2.0 is the last stable release and v0.3.0-rc is pre-release. Include version context in opening line.

### [major] ROADMAP.md link path error
- **Where:** `/Users/scot/cairo/docs/ROADMAP.md:5` and several locations
- **What:** Link formatted as `[Contributing](docs/development/contributing.md)` — relative path assumes reading from repo root, but when ROADMAP.md is read from docs/ subdirectory, the path resolves to `docs/docs/development/contributing.md`
- **Why it matters:** Link breaks when ROADMAP is read in docs context (e.g., via `docs/README.md` reference to `../ROADMAP.md`); relative paths should be `development/contributing.md` instead
- **Action:** Change root-level links in ROADMAP.md from `docs/` prefix to relative paths (e.g., `development/contributing.md`, `../../ROADMAP.md` from within subdirs)

### [medium] Redundant documentation across concepts/ and ai/ folders
- **Where:** `docs/concepts/identity.md`, `docs/ai/identity.md`; `docs/concepts/memory-model.md`, `docs/ai/memory-and-facts.md`; `docs/concepts/sessions-and-steering.md`, `docs/ai/sessions.md`
- **What:** Three pairs of near-duplicate docs explaining the same concepts in different styles. `concepts/` targets developers (architecture/reasoning), `ai/` targets the agent (usage/how-to). `ai/memory-and-facts.md:5` explicitly notes "Authoritative reference: `docs/concepts/memory-model.md`" but the relationship isn't consistently documented in other pairs.
- **Why it matters:** Maintainers must keep both versions in sync; readers unsure which to consult; medium-term maintenance burden
- **Action:** Add disclaimer headers in each ai/ doc pointing to the authoritative concepts/ version (model what ai/memory-and-facts.md does). Consider whether separate docs are necessary or if one version with dual audience prose would suffice.

### [medium] Missing/incomplete TOC for large docs/
- **Where:** `/Users/scot/cairo/docs/` (no master index file)
- **What:** `docs/README.md` provides a good high-level organizational TOC, but large individual docs (e.g., `FEATURES.md` 352 lines, `architecture/database.md` 438 lines, `development/adding-a-tool.md` 413 lines) lack internal anchor links or section tables of contents
- **Why it matters:** Readers searching for specific topics within large files must scroll/search manually; no way to jump to a section via link
- **Action:** For files >400 lines, add a top-level section index with `[Section name](#section-anchor)` links. Consider splitting the largest reference docs if they truly cover multiple disjoint topics.

### [small] Dangling reference in ai/skills.md
- **Where:** `/Users/scot/cairo/docs/ai/skills.md:31`
- **What:** Text reads "See the comparison table in `docs/guides/skills.md`." But file lives at `docs/development/skills.md` and comparison table is in `docs/getting-started/skills.md`
- **Why it matters:** Points reader to non-existent file
- **Action:** Remove line or correct path to `../getting-started/skills.md`
