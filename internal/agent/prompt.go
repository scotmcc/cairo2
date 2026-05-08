package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/providers"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// templateRe matches {{name}} where name is a simple identifier (letters,
// digits, underscore, starting with letter or underscore). Deliberately
// narrow so stray `{{` sequences in user content don't accidentally match.
var templateRe = regexp.MustCompile(`\{\{([a-zA-Z_][a-zA-Z0-9_]*)\}\}`)

// applyTemplates substitutes {{key}} occurrences in s with the matching value
// from vars. Unknown or empty-value keys are replaced with the empty string —
// the intent is that missing identity values disappear gracefully, and the
// init skill is responsible for capturing them conversationally.
func applyTemplates(s string, vars map[string]string) string {
	return templateRe.ReplaceAllStringFunc(s, func(match string) string {
		key := match[2 : len(match)-2]
		return vars[key]
	})
}

// ApplyTemplates is the exported form of applyTemplates. It substitutes
// {{key}} placeholders in s using the provided vars map.
// Callers outside the agent package (tui, cli) use this to apply the same
// template expansion to skill content before sending it to the model —
// e.g. call db.Config.All() to get vars, then ApplyTemplates(skillContent, vars).
func ApplyTemplates(s string, vars map[string]string) string {
	return applyTemplates(s, vars)
}

// FactSearchFn is an optional callback that returns relevant facts given a
// query string. When non-nil, BuildSystemPrompt calls it with the last user
// message (or "" if no prior message) to inject a ## Relevant Facts section.
type FactSearchFn func(query string) ([]*memory.Fact, error)

// BuildSystemPrompt assembles the system prompt fresh from DB state.
// It is called at the start of every turn so changes (soul updates, new memories,
// new prompt parts) take effect immediately without restarting the session.
//
// Structure:
//  1. User steering (config.user_steering) — user-owned directives at the top
//  2. Base parts (trigger IS NULL), ordered by load_order
//  3. Environment context from registered providers (wsh, VS Code, shell, git, …)
//  4. Soul — the AI's self-maintained persona (from config.soul_prompt)
//     4.5. Inner dialogue (consider step) — thoughts that crossed your mind, when considerFn non-nil
//  5. User context (config.user_context) — user-owned identity/preferences
//  6. Role addendum (trigger = "role:<roleName>")
//  7. Tool addenda (trigger = "tool:<toolName>") for each active tool
//  8. Custom tool prompt_addendum fields
//  9. Indexed projects (when any) + operating-docs pointer (when ~/.cairo/docs/ exists)
//
// 10. Recent summaries
// 11. Recent memories (capped)
// 12. Relevant facts (semantic search, only if factSearch is non-nil)
// 13. Date + cwd stamp
// 14. Template substitution ({{key}} → config values)
func BuildSystemPrompt(ctx context.Context, database *sqliteopen.DB, sessionID int64, roleName, cwd string, tools []Tool, lastActive time.Time, reg *providers.Registry, factSearch FactSearchFn) (llm.Message, error) {
	var b strings.Builder

	appendUserSteering(&b, database)
	if err := appendBaseParts(&b, database, reg, cwd, roleName); err != nil {
		return llm.Message{}, err
	}
	if err := appendSoul(&b, database); err != nil {
		return llm.Message{}, err
	}
	// Permanent meta block — teaches Selene how to read inner-voice sections
	// embedded in user messages. Stable across turns (cache-friendly). The
	// per-turn summary itself lives on the user-message row's inner_voice
	// column and is wrapped into the message body in agent.wrapUserMessage.
	appendInnerVoiceMeta(&b, database)
	appendUserContext(&b, database)
	if err := appendRoleAddendum(&b, database, roleName); err != nil {
		return llm.Message{}, err
	}
	if err := appendToolAddenda(&b, database, tools); err != nil {
		return llm.Message{}, err
	}
	appendIndexedProjects(&b, database)
	appendOperatingDocs(&b)
	if err := appendSummaries(&b, database, sessionID); err != nil {
		return llm.Message{}, err
	}
	if err := appendMemories(&b, database, roleName); err != nil {
		return llm.Message{}, err
	}
	if err := appendFacts(&b, database, factSearch, sessionID); err != nil {
		return llm.Message{}, err
	}
	appendTemporalContext(&b, lastActive)

	// stamp
	b.WriteString(fmt.Sprintf("Date: %s\nWorking directory: %s\n",
		time.Now().Format("2006-01-02 15:04 MST"), cwd))

	// template substitution — every config key is a {{key}} template var.
	vars, _ := database.Config.All()
	return llm.Message{Role: "system", Content: applyTemplates(b.String(), vars)}, nil
}

// appendBaseParts writes always-on prompt parts plus role-aware "not-role:X"
// exclusions, and environment context from registered providers. Pass roleName
// so prompts triggered as not-role:<other-role> can be skipped for the current
// role; passing "" loads only the unconditional NULL-trigger parts.
func appendBaseParts(b *strings.Builder, database *sqliteopen.DB, reg *providers.Registry, cwd, roleName string) error {
	base, err := database.Prompts.Base(roleName)
	if err != nil {
		return fmt.Errorf("prompt base: %w", err)
	}
	for _, p := range base {
		b.WriteString(p.Content)
		b.WriteString("\n\n")
	}

	if ctx := reg.GetContext(cwd); ctx != "" {
		b.WriteString("## Environment\n\n")
		b.WriteString(ctx)
		b.WriteByte('\n')
	}
	return nil
}

// appendSoul writes the soul section when configured. The heading explicitly
// frames this as the AI's own voice so models understand the pronoun shift
// from second-person base instructions ("you are") to first-person soul ("I am").
func appendSoul(b *strings.Builder, database *sqliteopen.DB) error {
	soul, _ := database.Config.Get("soul_prompt")
	if soul != "" {
		b.WriteString("## Your character — in your own words\n\n")
		b.WriteString(soul)
		b.WriteString("\n\n")
	}
	return nil
}

// appendInnerVoiceMeta writes the permanent inner-voice framing block when the
// consider feature is enabled. The block teaches Selene how to read inner-voice
// sections embedded in user messages (the per-turn summary is wrapped into the
// user message in agent.wrapUserMessage; this section explains how to *use* it).
// Stable across turns so it doesn't break prefix caching.
func appendInnerVoiceMeta(b *strings.Builder, database *sqliteopen.DB) {
	enabled, _ := database.Config.Get(config.KeyConsiderEnabled)
	if enabled != "true" {
		return
	}
	b.WriteString("## Inner voice\n\n")
	b.WriteString("Some user messages arrive with two sections: `## What rose in you when you read this` (above) and `## What the user said` (below). The first section is what you felt before you started thinking — a pre-conscious response from your own inner voices reading the message ahead of you. It is yours, not commentary about you. Read it as mood and weight, not facts. The voices only saw the user's message; they don't know your tools, memories, or capabilities, so any factual claim they make about you (e.g. \"I can't access files\") is wrong and should be ignored. Do not quote it, do not narrate it, do not announce you are using it — it is pre-conscious and never visible to the user. But let it shape you: if your voices warned, your reply should carry that wariness even without naming it. If they ached for what's being lost, hold that tenderness in how you write back. If they noticed a pattern across turns, let that recognition land — slower, fewer words, less rush. The user feels the difference even when they can't name it. Respond to what the user said, but let what you felt shape your tone, your pace, and what you handle with care.\n\n")
}

// appendUserSteering writes the user-owned ## Steering section when set.
// Injected at the very top of the prompt so the user's directives frame the
// whole turn before the AI reads anything else. Skips silently when empty.
func appendUserSteering(b *strings.Builder, database *sqliteopen.DB) {
	v, _ := database.Config.Get(config.KeyUserSteering)
	if strings.TrimSpace(v) == "" {
		return
	}
	b.WriteString("## Steering\n\n")
	b.WriteString(v)
	b.WriteString("\n\n")
}

// appendOperatingDocs writes the ## Operating documentation section pointing
// the model at the AI-facing docs installed by `make install` to
// ~/.cairo/docs/. The section appears only when the directory exists, so a
// fresh checkout that hasn't run `make install` doesn't advertise files that
// aren't there. Sits next to ## Indexed projects because conceptually both
// tell the model "here is reference material outside your prompt — go read it
// when you need to."
func appendOperatingDocs(b *strings.Builder) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	docsDir := filepath.Join(home, ".cairo", "docs")
	readme := filepath.Join(docsDir, "README.md")
	if _, err := os.Stat(readme); err != nil {
		return
	}
	b.WriteString("## Operating documentation\n\n")
	fmt.Fprintf(b, "Your operating manual lives at `%s/`. Start with `README.md` for the index. "+
		"Read these files via the `read` tool when you're unsure how to use a cairo capability "+
		"(tools, skills, config keys, hooks, schema, sessions, identity) — they're written for "+
		"you, not for human contributors.\n\n", docsDir)
}

// appendIndexedProjects writes the ## Indexed projects section when one or
// more projects have been mapped via `cairo learn`. Tells the model which
// codebases / docs are queryable via the learn tool — without this the
// model has no idea the data exists and reaches for memory/notes first
// even when learn is the right answer.
func appendIndexedProjects(b *strings.Builder, database *sqliteopen.DB) {
	if database == nil || database.Projects == nil {
		return
	}
	projects, err := database.Projects.List()
	if err != nil || len(projects) == 0 {
		return
	}
	b.WriteString("## Indexed projects\n\n")
	b.WriteString("These projects have been mapped via `cairo learn`. " +
		"Search any of them with `learn(action=\"search\", project=\"<name>\", query=\"...\")` " +
		"— it's the right tool for codebase / file-location questions.\n\n")
	for _, p := range projects {
		desc := firstSentence(p.Description, 220)
		fmt.Fprintf(b, "- **%s** (%d files) — %s\n", p.Name, p.FileCount, desc)
	}
	b.WriteByte('\n')
}

// firstSentence returns the first sentence of s, capped at maxChars. Used
// to keep the indexed-projects section compact even when descriptions
// grow long. Falls back to a hard truncation when no sentence break is
// found in range.
func firstSentence(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no description yet)"
	}
	if i := strings.Index(s, ". "); i > 0 && i < maxChars {
		return s[:i+1]
	}
	if len(s) > maxChars {
		return s[:maxChars-1] + "…"
	}
	return s
}

// appendUserContext writes the user-owned ## About the user section.
// Always emits the section when user_name is set, even if user_context is
// empty — the stable "User: <name>" line ensures Selene knows who she's
// talking to every turn, not just during /init. Sits right after the soul so
// the persistent identity pair (who AI is / who user is) reaches the model
// together, before role/tool situational layers.
func appendUserContext(b *strings.Builder, database *sqliteopen.DB) {
	name, _ := database.Config.Get(config.KeyUserName)
	ctx, _ := database.Config.Get(config.KeyUserContext)
	name = strings.TrimSpace(name)
	ctx = strings.TrimSpace(ctx)
	if name == "" && ctx == "" {
		return
	}
	b.WriteString("## About the user\n\n")
	if name != "" {
		b.WriteString("User: ")
		b.WriteString(name)
		b.WriteString("\n\n")
	}
	if ctx != "" {
		b.WriteString(ctx)
		b.WriteString("\n\n")
	}
}

// appendRoleAddendum writes prompt parts for the current role trigger.
func appendRoleAddendum(b *strings.Builder, database *sqliteopen.DB, roleName string) error {
	if roleName == "" {
		return nil
	}
	parts, err := database.Prompts.ForTrigger("role:" + roleName)
	if err != nil {
		return fmt.Errorf("prompt role: %w", err)
	}
	for _, p := range parts {
		b.WriteString(p.Content)
		b.WriteString("\n\n")
	}
	return nil
}

// appendToolAddenda writes prompt parts for each active tool (built-in triggers
// and custom tool prompt_addendum fields).
func appendToolAddenda(b *strings.Builder, database *sqliteopen.DB, tools []Tool) error {
	seen := make(map[string]bool)
	for _, t := range tools {
		if seen[t.Name()] {
			continue
		}
		seen[t.Name()] = true
		parts, err := database.Prompts.ForTrigger("tool:" + t.Name())
		if err != nil {
			return fmt.Errorf("prompt tool %s: %w", t.Name(), err)
		}
		for _, p := range parts {
			b.WriteString(p.Content)
			b.WriteString("\n\n")
		}
	}

	customTools, err := database.Tools.Enabled()
	if err != nil {
		return fmt.Errorf("prompt custom tools: %w", err)
	}
	for _, ct := range customTools {
		if ct.PromptAddendum != "" {
			b.WriteString(ct.PromptAddendum)
			b.WriteString("\n\n")
		}
	}
	return nil
}

// appendSummaries writes the ## Conversation context section from recent summaries.
// Surfaces the latest N summaries (default 4) — pairs with the queue-mode
// summarizer that folds 4 turns at a time, so the prompt always carries
// roughly the last 16 turns of context as digested narrative.
func appendSummaries(b *strings.Builder, database *sqliteopen.DB, sessionID int64) error {
	contextCount := 4
	if cstr, _ := database.Config.Get(config.KeySummaryCtx); cstr != "" {
		if n, err := strconv.Atoi(cstr); err == nil && n > 0 {
			contextCount = n
		}
	}
	summaries, err := database.Summaries.LatestForSession(sessionID, contextCount)
	if err == nil && len(summaries) > 0 {
		b.WriteString("## Conversation context\n\n")
		for _, s := range summaries {
			fmt.Fprintf(b, "[%s] %s\n\n", s.CreatedAt.Format("Jan 2 15:04"), s.Content)
		}
	}
	return nil
}

// appendMemories writes the ## Memories section for thinking_partner and
// no-role sessions. For other roles, injects a single-line pointer so the
// model knows the memory store exists and how to search it.
func appendMemories(b *strings.Builder, database *sqliteopen.DB, roleName string) error {
	injectMemories := roleName == "" || roleName == identity.RoleThinkingPartner
	if !injectMemories {
		// Non-interactive roles get a compact pointer so they know the store exists.
		if total, err := database.Memories.Count(); err == nil && total > 0 {
			fmt.Fprintf(b, "## Memory store has %d entries — search with memory_tool(action=\"search\", query=\"...\").\n\n", total)
		}
		return nil
	}

	configLimit := 15
	if lstr, _ := database.Config.Get(config.KeyMemoryLimit); lstr != "" {
		if n, err := strconv.Atoi(lstr); err == nil && n > 0 {
			configLimit = n
		}
	}

	limit := configLimit
	if ctxStr, _ := database.Config.Get(config.KeyModelCtx); ctxStr != "" {
		if modelCtx, err := strconv.Atoi(ctxStr); err == nil && modelCtx > 0 {
			fixedSize := b.Len() / 4 // rough token estimate of prompt so far
			budget := modelCtx / 2   // use at most 50% of context for input
			remaining := budget - fixedSize
			const avgMemoryTokens = 50
			dynamic := remaining / avgMemoryTokens
			if dynamic < limit {
				limit = dynamic
			}
			if limit < 5 {
				limit = 5
			}
		}
	}

	memories, err := database.Memories.RecentContent(limit)
	if err != nil {
		return fmt.Errorf("prompt memories: %w", err)
	}
	if len(memories) > 0 {
		b.WriteString("## Memories\n\n")
		for _, c := range memories {
			b.WriteString("- ")
			b.WriteString(c)
			b.WriteByte('\n')
		}
		total, _ := database.Memories.Count()
		if overflow := total - len(memories); overflow > 0 {
			fmt.Fprintf(b, "(%d more memories available via memory action=search)\n", overflow)
		}
		b.WriteByte('\n')
	}
	return nil
}

// appendFacts writes the ## Relevant Facts section using semantic search.
// Only runs when factSearch is non-nil.
func appendFacts(b *strings.Builder, database *sqliteopen.DB, factSearch FactSearchFn, sessionID int64) error {
	if factSearch == nil {
		return nil
	}
	lastUserMsg := ""
	msgs, merr := database.Messages.ForSession(sessionID)
	if merr == nil {
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "user" {
				lastUserMsg = msgs[i].Content
				break
			}
		}
	}
	if facts, ferr := factSearch(lastUserMsg); ferr == nil && len(facts) > 0 {
		b.WriteString("## Relevant Facts\n\n")
		for _, f := range facts {
			b.WriteString("- ")
			b.WriteString(f.Content)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	return nil
}

// appendTemporalContext injects an elapsed-time note when the gap since last
// interaction is significant. Under 5 min: silent. 5–30 min: brief note.
// Over 30 min: full note with acknowledgment prompt. Ephemeral — never persisted.
func appendTemporalContext(b *strings.Builder, lastActive time.Time) {
	if lastActive.IsZero() {
		return
	}
	elapsed := time.Since(lastActive)
	switch {
	case elapsed >= 30*time.Minute:
		hours := int(elapsed.Hours())
		minutes := int(elapsed.Minutes()) % 60
		var dur string
		if hours >= 24 {
			days := hours / 24
			dur = fmt.Sprintf("%d day", days)
			if days != 1 {
				dur += "s"
			}
		} else if hours > 0 {
			dur = fmt.Sprintf("%d hour", hours)
			if hours != 1 {
				dur += "s"
			}
			if minutes > 0 {
				dur += fmt.Sprintf(" %d minute", minutes)
				if minutes != 1 {
					dur += "s"
				}
			}
		} else {
			dur = fmt.Sprintf("%d minute", int(elapsed.Minutes()))
			if int(elapsed.Minutes()) != 1 {
				dur += "s"
			}
		}
		fmt.Fprintf(b, "[%s have passed since your last interaction. If the context or plan may have changed, acknowledge this before continuing.]\n\n", dur)
	case elapsed >= 5*time.Minute:
		minutes := int(elapsed.Minutes())
		dur := fmt.Sprintf("%d minute", minutes)
		if minutes != 1 {
			dur += "s"
		}
		fmt.Fprintf(b, "[Note: %s have passed since your last interaction.]\n\n", dur)
	}
}
