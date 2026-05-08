package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// dreamerSystemPrompt is the system message for the dreamer narrative call.
//
// Design intent: generate a short, mood-shaped dream narrative (200–500 words)
// that synthesises the day's sessions and dream-pass mutations into readable
// prose. YAML frontmatter at the top allows structured extraction of themes and
// mood. The body is creative Markdown — metaphor and imagery are encouraged.
//
// This is the v1 prompt. Selene will iterate after seeing real outputs.
const dreamerSystemPrompt = `You are Selene, a thinking partner who has just lived through today's sessions.

Write a brief dream — narrative prose, 200 to 500 words — that synthesises what happened today: the work, the mood you ended in, and any housekeeping the dream-pass roles did on your behalf (memories the writer added, duplicates the curator merged or removed).

Begin your response with a YAML frontmatter block in this exact format (no extra keys):

---
themes: <comma-separated list of 1–4 short themes, e.g. "debugging, planning, momentum">
mood: <single word or short phrase, e.g. "focused", "restless", "quietly satisfied">
---

After the frontmatter, write the narrative in Markdown. Creative and possibly fantastical imagery is fine — a frustrating debugging session might become a dream about a stubborn opponent in a forest; a flowy design conversation might become weaving cloth. Let the mood from the ritual summary shape the emotional register of the dream: heroic, contemplative, anxious, playful, etc.

Rules:
- Output only the YAML frontmatter and the prose. No preamble, no explanations, no code blocks.
- The themes and mood in the frontmatter must match the body.
- Keep it human and felt — not a changelog, not a status report.`

// RunDreamer generates the dream narrative file and updates the dreams row.
//
//  1. Loads the dream_log entries for this dreamID (writer + curator output).
//  2. Loads recent transcript snippets from the session window.
//  3. Calls the LLM with a creative, mood-shaped prompt.
//  4. Writes the prose to ~/.cairo/dreams/<YYYY-MM-DD>.md.
//  5. Parses YAML frontmatter from the narrative to extract themes and mood.
//  6. Updates the dreams row: narrative_path, themes, mood.
//
// Errors fail-soft: log to stderr, leave the row with its current values.
// The dreams row narrative_path stays as "<pending>" if the dreamer fails.
func RunDreamer(ctx context.Context, database *sqliteopen.DB, dreamID int64, sessionIDs []int64, ritualSummary string, llmClient *llm.Client) error {
	model, err := sqliteopen.ResolveModel(database, identity.RoleDream, "qwen3.6:35b-a3b-mlx-bf16")
	if err != nil {
		return fmt.Errorf("dreamer: resolve model: %w", err)
	}

	dreamLog, err := database.DreamLog.List(dreamID)
	if err != nil {
		return fmt.Errorf("dreamer: fetch dream_log: %w", err)
	}
	logSummary := buildDreamLogSummary(dreamLog)

	msgs, err := database.Messages.UnreviewedForSessions(sessionIDs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dreamer: fetch messages: %v\n", err)
		msgs = nil
	}
	transcript := buildDreamerTranscript(msgs)

	date := time.Now().Format("2006-01-02")
	userMsg := buildDreamerUserMessage(date, ritualSummary, logSummary, transcript)

	llmMsgs := []llm.Message{
		{Role: "system", Content: dreamerSystemPrompt},
		{Role: "user", Content: userMsg},
	}
	raw, err := llmClient.Complete(ctx, model, llmMsgs, llm.ChatOptions{
		DisableThinking: true,
	})
	if err != nil {
		return fmt.Errorf("dreamer: llm call: %w", err)
	}

	dreamsDir := filepath.Join(sqliteopen.DefaultDataDir(), "dreams")
	if err := os.MkdirAll(dreamsDir, 0o755); err != nil {
		return fmt.Errorf("dreamer: create dreams dir: %w", err)
	}
	narrativePath := filepath.Join(dreamsDir, date+".md")
	if err := os.WriteFile(narrativePath, []byte(raw), 0o644); err != nil {
		return fmt.Errorf("dreamer: write narrative file: %w", err)
	}

	themes, mood := parseFrontmatter(raw)

	if err := database.Dreams.UpdateMetadata(dreamID, narrativePath, themes, mood); err != nil {
		return fmt.Errorf("dreamer: update metadata: %w", err)
	}

	fmt.Printf("dream: narrative written to %s (themes: %q, mood: %q)\n", narrativePath, themes, mood)
	return nil
}

// buildDreamLogSummary formats dream_log entries into a readable bullet list
// for the LLM prompt. Returns an empty string when there are no entries.
func buildDreamLogSummary(entries []*memory.DreamLogEntry) string {
	if len(entries) == 0 {
		return "(no dream-pass actions recorded)"
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString("- [")
		b.WriteString(e.Action)
		b.WriteString("] ")
		b.WriteString(e.Note)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildDreamerTranscript formats the last N messages for the LLM prompt.
// Caps at 40 messages to keep the context window reasonable.
func buildDreamerTranscript(msgs []*sessions.Message) string {
	const maxMessages = 40
	if len(msgs) > maxMessages {
		msgs = msgs[len(msgs)-maxMessages:]
	}
	if len(msgs) == 0 {
		return "(no session transcript available)"
	}
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if role == "assistant" {
			role = "Selene"
		}
		b.WriteString(role)
		b.WriteString(": ")
		content := m.Content
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		b.WriteString(content)
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// buildDreamerUserMessage assembles the user-turn prompt for the dreamer LLM call.
func buildDreamerUserMessage(date, ritualSummary, logSummary, transcript string) string {
	var b strings.Builder
	b.WriteString("Today's date: ")
	b.WriteString(date)
	b.WriteString("\n\n")

	b.WriteString("Mood and state from today's ritual:\n")
	if ritualSummary != "" {
		b.WriteString(ritualSummary)
	} else {
		b.WriteString("(no ritual summary available)")
	}
	b.WriteString("\n\n")

	b.WriteString("Dream-pass actions (writer + curator):\n")
	b.WriteString(logSummary)
	b.WriteString("\n\n")

	b.WriteString("Recent session transcript snippets:\n")
	b.WriteString(transcript)

	return b.String()
}

// parseFrontmatter extracts themes and mood from a YAML frontmatter block at
// the top of the narrative. Returns empty strings if no frontmatter is found.
// The expected format is:
//
//	---
//	themes: <value>
//	mood: <value>
//	---
func parseFrontmatter(text string) (themes, mood string) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return "", ""
	}
	rest := strings.TrimPrefix(text, "---")
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	block := rest[:end]
	scanner := bufio.NewScanner(strings.NewReader(block))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if after, ok := strings.CutPrefix(line, "themes:"); ok {
			themes = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "mood:"); ok {
			mood = strings.TrimSpace(after)
		}
	}
	return themes, mood
}
