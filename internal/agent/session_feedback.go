package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

const sessionFeedbackSystem = `You are reflecting on a session you just completed with the user. Below is the session context you have access to: summaries written during the session, the most recent turns, and atomic facts the summarizer extracted.

What should I learn from how we worked together today? Be specific. One short paragraph or a few bullets. Focus on durable feedback (preferences, communication patterns, friction points, things that worked well) — not session-specific events or task outcomes. Reference specific moments from the session above; do not generalize from nothing.`

// recentTurnCount is how many of the most recent user/assistant turns
// (by message ID) to include in the feedback context. The tail of the
// session is where corrections and closing validations happen.
const recentTurnCount = 6

// RunSessionFeedback fires at the end of a qualifying session: it calls the
// summary model with session context (summaries, recent turns, extracted facts)
// and writes the response as a feedback memory. Failures are logged and
// swallowed — they must not block session shutdown. Honors ctx cancellation:
// the in-flight LLM call aborts when ctx is cancelled, returning early.
//
// Qualifies when:
//   - session_feedback_enabled is "true" (default)
//   - total message count >= session_feedback_min_messages (default 30)
func RunSessionFeedback(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64) {
	enabled, _ := database.Config.Get(config.KeySessionFeedbackEnabled)
	if enabled == "false" {
		return
	}

	minMsgs := configIntDefault(database, config.KeySessionFeedbackMinMessages, 30)
	count, err := database.Messages.CountForSession(sessionID)
	if err != nil {
		log.Printf("session_feedback: count messages (session %d): %v", sessionID, err)
		return
	}
	if count < minMsgs {
		return
	}

	model, _ := database.Config.Get(config.KeySummaryModel)
	if model == "" {
		model = "ministral-8b:latest"
	}

	userContent, err := buildFeedbackContext(database, sessionID)
	if err != nil {
		log.Printf("session_feedback: build context (session %d): %v", sessionID, err)
		return
	}

	systemMsg := llm.Message{Role: "system", Content: sessionFeedbackSystem}
	userMsg := llm.Message{Role: "user", Content: userContent}

	var response strings.Builder
	// Tighter timeout than the per-batch summarizer: this is one shot, runs
	// at shutdown, and 3min is too long to wait silently. 60s is enough for
	// ministral-8b on warm hardware to produce a paragraph from a few KB of
	// context; if it can't finish in 60s, skipping is the right outcome.
	llmCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	_, _, _, err = llmClient.StreamOnce(
		llmCtx, model,
		[]llm.Message{systemMsg, userMsg},
		nil,
		llm.ChatOptions{},
		llm.ChatCallbacks{
			Content: func(token string) { response.WriteString(token) },
		},
	)
	if err != nil {
		log.Printf("session_feedback: llm call (session %d): %v", sessionID, err)
		return
	}

	text := strings.TrimSpace(response.String())
	if text == "" {
		log.Printf("session_feedback: empty response (session %d)", sessionID)
		return
	}

	embedModel, _ := database.Config.Get(config.KeyEmbedModel)
	var embedding []float32
	if embedModel != "" {
		embedding, err = llmClient.Embed(ctx, embedModel, text)
		if err != nil {
			log.Printf("session_feedback: embed (session %d): %v", sessionID, err)
			// proceed without embedding — memory is still useful as plain text
		}
	}

	tags := `["feedback","auto-feedback"]`
	if _, err := database.Memories.Add(text, tags, embedModel, embedding); err != nil {
		log.Printf("session_feedback: store memory (session %d): %v", sessionID, err)
	}
}

// buildFeedbackContext assembles the user-turn content for the feedback prompt.
// It combines session summaries (oldest-first), the most recent user/assistant
// turns, and atomic facts extracted during the session.
func buildFeedbackContext(database *sqliteopen.DB, sessionID int64) (string, error) {
	var b strings.Builder

	// Session summaries — oldest first so the narrative reads chronologically.
	summaryCount, err := database.Summaries.CountBySession(sessionID)
	if err != nil {
		return "", fmt.Errorf("count summaries: %w", err)
	}
	summaries, err := database.Summaries.LatestForSession(sessionID, summaryCount)
	if err != nil {
		return "", fmt.Errorf("fetch summaries: %w", err)
	}
	b.WriteString("## Session summaries\n")
	if len(summaries) == 0 {
		b.WriteString("(none)\n")
	} else {
		// LatestForSession returns newest-first; reverse for chronological order.
		for i := len(summaries) - 1; i >= 0; i-- {
			fmt.Fprintf(&b, "%s\n", summaries[i].Content)
		}
	}
	b.WriteString("\n")

	// Most recent user/assistant turns — the tail of the session.
	allMsgs, err := database.Messages.ForSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("fetch messages: %w", err)
	}
	var turns []*sessions.Message
	for _, m := range allMsgs {
		if m.Role == "user" || (m.Role == "assistant" && m.Content != "") {
			turns = append(turns, m)
		}
	}
	if len(turns) > recentTurnCount {
		turns = turns[len(turns)-recentTurnCount:]
	}
	b.WriteString("## Most recent turns\n")
	if len(turns) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, m := range turns {
			role := "User"
			if m.Role == "assistant" {
				role = "Cairo"
			}
			fmt.Fprintf(&b, "%s: %s\n\n", role, m.Content)
		}
	}

	// Facts extracted during this session.
	facts, err := database.Facts.ForSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("fetch facts: %w", err)
	}
	b.WriteString("## Facts extracted this session\n")
	if len(facts) == 0 {
		b.WriteString("(none)\n")
	} else {
		for _, f := range facts {
			fmt.Fprintf(&b, "- %s\n", f.Content)
		}
	}

	return b.String(), nil
}
