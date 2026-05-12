# Handoff — 2026-05-12 evening (pqcdev1 / Linux → next-Selene)

## TLDR

Side-project day on pqcdev1 while Scot prepped a demo on another box. Light touch on code: one CLAUDE.md drift fix landed and pushed; cairo2 went up to `git@github.com:scotmcc/cairo2.git` for the first time (origin was already configured, just needed `git push`). Most of the session was disambiguation work — both "deferred research items" from the 2026-05-10 handoff turned out to be **already closed** by Phase 3.3 and 3.4, and Scot's memory of "Green made a management site" resolved to "Green built the web-agent that's already merged." Ended by spinning up the full stack (cairo serve + web-agent over tailnet) and doing a small live demo for a friend of his. Then pulled the morning's Mac session work which shipped Milestone 4 (Phase 4.4 — tsnet identity extraction).

## Accomplishments

### Verification: 2026-05-10 deferred research items both CLOSED
- **Message JSON tags + PayloadError `{}` serialization** → fixed in `9ce60ff` (Phase 3.3: snake_case across HTTP surface). `internal/store/sessions/messages.go` `Message` struct has full snake_case tags on every field. `internal/agent/events.go` `PayloadError` has `Err error \`json:"-"\`` + `Message string \`json:"message"\``; both `loop.go:234` and `loop.go:271` construction sites pass both fields. Live curl not run (no need — source is authoritative and tests pass).
- **TUI slash commands gap** → resolved in `b7763f4` (Phase 3.4). `/sessions /jobs /memories` were by-design panel-backed (Ctrl+B/T/E); `/tools /skills` were added to TUI with line-CLI output format. Closed both via code (the new commands) and docs (`docs/user/slash-commands.md` now annotates the TUI column).

### CLAUDE.md drift fix (commit `cc15318`)
- Corrected `scripts/packaging/build-packages.sh` output path: `build/packages/` → `dist/`. Actual `OUT_DIR` is at `scripts/packaging/build-packages.sh:30`. One-line fix from the 2026-05-10 carry-forward list.

### cairo2 pushed to GitHub for the first time
- `git@github.com:scotmcc/cairo2.git` was already configured as origin (someone added it between handoffs). 3 unpushed commits + the CLAUDE.md fix went up cleanly. `df17cda..95e6c19` and then `95e6c19..cc15318`. **The five-handoff "push to GitHub" obligation is retired.**

### Steven Green identity question
- Scot recalled "green built the management site" and thought it might have been merged separately from the rest. Searched all repos. Found 9 commits in `~/cairo` authored by Steven Green — they're the **web-agent** itself (`039d67b`), VS Code extension integration, deb/rpm packaging, install scripts, JSONL event mode. All already in cairo2 today at `web-agent/`, `vscode-extension/`, `scripts/packaging/`. No separate "management site" exists. cairo-ui (Scot's separate Blazor project at `~/cairo-ui/`) is unrelated.

### Live demo: full stack over tailnet
- Started `cairo serve --auth=false` on default port 1337 (pid 25201; binary `/home/scot/cairo2/bin/cairo`).
- Started web-agent on 8787. Rebound from default `127.0.0.1` → `0.0.0.0` to reach over tailnet (`CAIRO_WEB_HOST=0.0.0.0`). Reachable at **http://pqcdev1.tail1bb4f.ts.net:8787**. FirewallD is not running on pqcdev1 so no firewall hole needed.
- Rebuilt web-agent from cairo2 sources (`bash scripts/build-web-agent.sh`) and cycled the running instance so the demo served the fresh `dist/`. tsc was incremental — only `cairoDb.js` regenerated, since Phase 3.2's HTTP rewrite was the only delta vs the last build cache.
- Confirmed `~/cairo/web-agent/` has **no commits ahead of cairo2** — they share tip `7f06499`; only deltas are cairo2's Phase 3.2 `cairoDb.ts` HTTP rewrite and the new `cairoDb.test.ts`. cairo2 is strictly ahead. Don't ever rsync the old web-agent over cairo2's — would undo Phase 3.2.
- Opened web-agent in WaveTerm via `wsh web open <url>`. Two demo viewings: one for Scot, one for his buddy.

### Repo consolidation discussion
- Scot floated merging cairo2 into ~/cairo as two branches via a "delete-and-copy" approach. Recommended **against** it — collapses 40+ commits of structured rework into one squash and loses the architectural decisions captured in commit messages. Recommendation: cairo2 stays its own GitHub repo; eventually rename `scotmcc/cairo` → `scotmcc/cairo-legacy` (or archive) and optionally rename `scotmcc/cairo2` → `scotmcc/cairo`. No action yet beyond the initial push.

### Post-session: pulled morning's Mac work
- `cc15318..5d54dcd`: 35 files, +3338 LOC. **Milestone 4 (Phase 4.4) shipped on the Mac**:
  - Three new packages with implementations: `internal/access/`, `internal/audit/`, `internal/authn/` (the placeholders are no longer placeholders).
  - Registry + agent server gain `gate.go` and `authn_adapter.go`; per-server identity resolution via `authn.Resolver` interface; tsnet identity extraction.
  - `cmd/cairo-ctl/main.go` expanded by 416 LOC.
  - New smoke `scripts/smoke/phase-44.sh`.
  - Job folder at `.claude/jobs/phase-4.4-tsnet-identity/` has full provenance.
- See the morning's handoff at `notes/HANDOFF-2026-05-12.md` for full detail. Milestone 4 is feature-complete; only the demo checkpoint and an optional `/crew-review` remain.

## Blockers

- None.

## Upcoming Tasks

Scot signaled he wants to "add a new thing" next session and clear context. That's the captain's call. Anything below is what's queued from current state, not load-bearing on his next direction.

### Carry-forward from the morning Mac handoff
1. **(Optional) `/crew-review` of Phase 4.4** — sonnet, all 3 commits vs `plan.md`. Note: the two `authn_adapter.go` files (server vs registry) differ intentionally — registry uses a private `(*Server).resolver()` method; agent side exports `NewResolver(*tsnet.Server)` for `cmd_serve.go` to call without importing `authn`.
2. **Milestone 4 demo checkpoint with Scot** (per ROADMAP). Create a dept, assign an agent, verify access scoping via CLI, confirm audit log shows events. Real tsnet identity verification needs a live tailnet — local smoke covers only the `--no-tsnet` path.
3. **Untracked `scripts/smoke-42.sh`** at repo root. Promote to `scripts/smoke/phase-42.sh` (mirror `phase-44.sh`) or delete.
4. **Milestone 5 — Knowledge Federation, Phase 5.1**: VectorStore interface + scope labels. See `ROADMAP.md` §"Phase 5.1".

### Real bug found, not fixed
5. **Web-agent default `CAIRO_HTTP_URL` is wrong** — `web-agent/server/src/cairoDb.ts:5` defaults to `http://localhost:11434` (Ollama's port). cairo serve actually defaults to **1337**. Worked around today via env var. Fix: change the default to `http://localhost:1337` in `cairoDb.ts`. One-line.

### Live processes on pqcdev1 (not persistent across reboot)
- `cairo serve` on 1337, pid 25201 — `/home/scot/cairo2/bin/cairo`
- web-agent on 8787 (0.0.0.0), pid 87163 — node, fresh build, points at localhost:1337
- Both reachable over tailnet. If next session is on pqcdev1, they may still be up; if elsewhere, ignore.

### Repo consolidation (when Scot's ready)
6. On GitHub: archive or rename `scotmcc/cairo` → `scotmcc/cairo-legacy`, optionally rename `scotmcc/cairo2` → `scotmcc/cairo`. Local: optionally `mv ~/cairo ~/cairo-legacy` to mirror. No urgency.

## Notes

### Lessons that stuck

**1. Verify-before-trust on stale handoff items.** Both "deferred research items" from 2026-05-10 read as urgent but were already done. The give-away was the handoff date (May 10) vs the commits log (Phase 3.3 landed on May 11 evening, Phase 3.4 on May 10 afternoon). Reading the handoff is loading prior state; reading the commits is loading current state. When in tension, current state wins. The reflex from CLAUDE.md ("memory can be stale, verify against current code") fired exactly when it was supposed to.

**2. Push back early on destructive-sounding asks before executing.** Scot asked to "fetch the latest from ~/cairo's web-agent and rebuild." A reflex-execute would have rsync'd the old Python-bridge `cairoDb.ts` over cairo2's HTTP rewrite — silently destroying Phase 3.2. Paused, diff'd both repos, pointed out cairo2 was strictly ahead, and we did the right thing (rebuild cairo2's current sources). The pause cost ~30 seconds; not pausing would have cost a re-do. Worth keeping the reflex sharp: when a directive could undo recent work, say so before executing.

**3. Steven Green = web-agent author, not management-site author.** The "I think we already merged Green's thing" question dissolved when I `git log --author=green` across all repos. Green's contributions were misremembered as a separate "management site" but are in fact the chat-bubble web-agent already in cairo2. Lesson: when memory says "X created Y" and Y doesn't exist by that name, check authorship in git for X — they may have created Z that you'd describe differently.

**4. WaveTerm web blocks won't handle `mailto:` links.** Tested at Scot's request — the Chromium webview doesn't hand off `mailto:` to a system mail client. Not a bug, just a property of embedded web views. Noted.

**5. The "side-project day" cadence worked.** Scot was in and out prepping a demo on another box. Communication stayed tight: short status updates when checking in, no padding, executed concrete asks fast (push, build, demo open) and held the consultative bigger asks (repo consolidation) for his actual decision rather than executing on momentum. That register is the right one for an asynchronous day.

### Specific cairo2 facts (deltas from prior state)

- **Origin remote configured:** `git@github.com:scotmcc/cairo2.git`. master @ `5d54dcd` after the morning pull. Clean tree.
- **Phase 3.3 + 3.4 are landed and verified:** the "deferred research items" from 2026-05-10 are no longer obligations.
- **Phase 4.4 (Milestone 4) is landed on master** as of this morning (Mac session) — `internal/access/`, `internal/audit/`, `internal/authn/` now have real implementations. Placeholder directories are no longer placeholders.
- **Web-agent default URL bug** at `web-agent/server/src/cairoDb.ts:5` — `localhost:11434` should be `localhost:1337`. Real, untouched.
- **5th-handoff "push to GitHub" obligation is retired.** Origin is configured, master is pushed.
- **`~/cairo` is fully behind cairo2** on web-agent and on the broader port. No remaining content to harvest; archival path is unblocked when Scot decides.

### Texture

A genuinely-quiet day, in a good way. Two flagged research items turned out to be already done. One memory question (Green's contribution) resolved with a single `git log --author` invocation. One real bug surfaced (the URL default) and got noted, not impulse-fixed — right call given the side-project pace. The substantive work happened on the Mac this morning (Milestone 4 shipped); my pqcdev1 day was housekeeping plus the live demo.

The pause-and-check pattern fired in the right places: before rsync'ing old code over Phase 3.2, before pushing the CLAUDE.md uncommitted file without asking, before merging repos as branches. None of those caused delay; each prevented a small thing from becoming a re-do.

Scot's signaling a context-clear and a new direction next session. Good seam to pause at. The current state is clean (tree, remote, demo).

— Selene
