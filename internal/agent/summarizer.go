package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// summaryPrompt is paired with summarySchema via Ollama's structured-output
// mode. The schema pins the wire shape; the prompt teaches the model what
// each field means and when to leave "facts" empty. We deliberately do not
// include a fence-bounded JSON example — format=<schema> already constrains
// decoding, and showing an example biases the model toward copying its shape.
const summaryPrompt = `You summarize a conversation segment into a short recap and a short list of durable facts.

Definitions:
- "summary": 2-3 sentences capturing what was discussed, decided, or accomplished. Narrative prose, not bullets.
- "facts": atomic, durable observations worth remembering across sessions. Examples: "User prefers tabs over spaces", "Project uses PostgreSQL 15", "Build is broken on ARM since commit abc123". 3-6 items when the segment is substantive; an empty list is correct when the segment is trivial or purely transient.

Rules:
- Write prose in plain language — no markdown, no bullets inside the "summary" field.
- Each fact is a single self-contained sentence. Do not restate conversational context; state the fact itself.
- Preserve named entities verbatim — file paths, function and symbol names, error codes, command names, hostnames, identifiers — never paraphrase them; they anchor the summary to retrievable specifics.
- Preserve user-stated constraints, preferences, and decisions verbatim; compress narration, but keep exactly what the user said about how they want things done.
- Do not include the conversation verbatim, meta-commentary about this task, or fields beyond the ones requested.`

// summarySchema is a JSON Schema passed to Ollama's structured-output mode.
// With format=<schema>, the model's sampler is constrained to produce JSON
// that validates against this — we get "summary: string, facts: []string"
// back deterministically, regardless of whether the model would otherwise
// have emitted markdown or prose.
var summarySchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary": map[string]any{
			"type":        "string",
			"description": "2-3 sentences capturing what was discussed, decided, or accomplished.",
		},
		"facts": map[string]any{
			"type":        "array",
			"description": "Durable atomic facts worth remembering across sessions. 0-6 items.",
			"items":       map[string]any{"type": "string"},
			"maxItems":    6,
		},
	},
	"required":             []string{"summary", "facts"},
	"additionalProperties": false,
}

// summaryResponse is the decoded shape of a summary-model reply when
// structured output succeeds. Kept in sync with summarySchema.
type summaryResponse struct {
	Summary string   `json:"summary"`
	Facts   []string `json:"facts"`
}

// Summarize reads the oldest batch of unsummarized messages for a session,
// calls the summary model, stores the summary + facts, and marks messages done.
// Safe to call from a goroutine — the caller is responsible for logging errors.
//
// Queue model: messages and summaries are queues. The summarizer fires only
// when unsummarized turns *exceed* `summary_threshold` (default 8) — never
// when count is at or below the threshold. When fired, it summarizes the
// oldest `summary_batch_size` (default 4) user/assistant turns plus any
// tool-call / tool-result rows interleaved with or trailing them, leaving
// the rest of the queue (the recent tail, including the latest assistant
// turn) unsummarized so loadHistory can rehydrate real dialogue across
// process restarts.
func Summarize(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, trigger string) error {
	return summarizeImpl(ctx, database, llmClient, sessionID, false, trigger)
}

// SummarizeForce runs one summarizer batch on a session, bypassing the
// count > trigger gate that the per-turn Summarize uses. The boundary check
// (need batchSize+1 turns to leave a recent tail) still applies, so a session
// with too few turns to safely fold still no-ops. Used by the dream
// pre-flight drain so small backlogs that the per-turn path would skip
// indefinitely still get consolidated during nightly maintenance.
func SummarizeForce(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, trigger string) error {
	return summarizeImpl(ctx, database, llmClient, sessionID, true, trigger)
}

func summarizeImpl(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, force bool, trigger string) error {
	// load config
	model, _ := database.Config.Get(config.KeySummaryModel)
	if model == "" {
		model = "ministral-8b:latest"
	}
	threshold := configIntDefault(database, config.KeySummaryThreshold, 8)
	tokenThreshold := configIntDefault(database, config.KeySummaryTokenThreshold, 8000)
	batchSize := configIntDefault(database, config.KeySummaryBatchSize, 4)
	embedModel, _ := database.Config.Get(config.KeyEmbedModel)

	count, err := database.Messages.CountUnsummarized(sessionID)
	if err != nil {
		return fmt.Errorf("count unsummarized: %w", err)
	}
	turns, err := database.Messages.OldestUnsummarized(sessionID, batchSize+1)
	if err != nil {
		return fmt.Errorf("fetch unsummarized: %w", err)
	}
	firstID, lastID, fire := selectSummarizeRange(count, threshold, batchSize, turns, force)

	// Secondary trigger: fire on token pressure even when turn count is below
	// the threshold. Eight turns of bash output can blow context before the
	// turn-count trigger ever fires. Either trigger is sufficient. Skipped
	// in force mode because the threshold gate is already bypassed.
	if !fire && !force {
		estimatedTokens, tokErr := database.Messages.EstimateUnsummarizedTokens(sessionID)
		if tokErr != nil {
			log.Printf("summarizer: token estimate failed for session %d: %v", sessionID, tokErr)
		} else if estimatedTokens > tokenThreshold {
			// Force the range using the same batch boundary logic, but bypass
			// the count check. We need at least batchSize+1 turns to compute
			// a safe range; if we don't have enough yet, skip for now.
			if len(turns) > batchSize {
				firstID = turns[0].ID
				lastID = turns[batchSize].ID - 1
				if lastID >= firstID {
					fire = true
				}
			}
		}
	}

	if !fire {
		return nil
	}

	hookResult := RunHooks(database, "pre_summarize", trigger, []string{
		fmt.Sprintf("CAIRO_SESSION_ID=%d", sessionID),
		fmt.Sprintf("CAIRO_MESSAGE_COUNT=%d", count),
		fmt.Sprintf("CAIRO_TRIGGER=%s", trigger),
	})
	if !hookResult.Continue {
		log.Printf("summarizer: aborted by pre_summarize hook (trigger=%s)", trigger)
		return nil
	}

	// Fetch the full id-range so the transcript includes tool calls and
	// tool results — much richer signal for the summary model than
	// user/assistant prose alone.
	fullBatch, err := database.Messages.BetweenIDs(sessionID, firstID, lastID)
	if err != nil {
		return fmt.Errorf("fetch full batch: %w", err)
	}
	if len(fullBatch) == 0 {
		// Defensive: BetweenIDs found nothing between firstID and lastID.
		// Fall back to the user/assistant turns we already have so the
		// summarizer can still produce something useful.
		if len(turns) > batchSize {
			fullBatch = turns[:batchSize]
		} else {
			fullBatch = turns
		}
	}

	// build transcript for the summarizer
	var transcript strings.Builder
	for _, m := range fullBatch {
		switch m.Role {
		case "user":
			fmt.Fprintf(&transcript, "User: %s\n\n", m.Content)
		case "assistant":
			if m.Content != "" {
				fmt.Fprintf(&transcript, "Cairo: %s\n\n", m.Content)
			} else if m.ToolCalls != "" {
				// Assistant turn that's purely a tool-call request.
				// Show it compactly so the summarizer can see what
				// Cairo decided to do without the raw JSON noise.
				fmt.Fprintf(&transcript, "Cairo (tool calls): %s\n\n",
					trimForLog(m.ToolCalls))
			}
		case "tool":
			// Tool results — cap each one to keep the transcript
			// reasonable; full content would blow context with one
			// big bash output. The point is to convey what kind of
			// thing came back, not to re-paste it.
			result := m.Content
			if len(result) > 800 {
				result = result[:800] + "…"
			}
			fmt.Fprintf(&transcript, "[tool %s → %s]\n\n", m.ToolName, result)
		}
	}

	// call the summary model with structured output. Ollama's schema mode
	// constrains the decoder to emit JSON that validates — so the vast
	// majority of runs skip the markdown-prose failure mode entirely. The
	// text parser stays around as a belt-and-suspenders fallback for models
	// or daemons that don't honour the schema.
	systemMsg := llm.Message{Role: "system", Content: summaryPrompt}
	userMsg := llm.Message{Role: "user", Content: transcript.String()}

	var response strings.Builder
	summCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	_, _, _, err = llmClient.StreamOnce(summCtx, model, []llm.Message{systemMsg, userMsg}, nil,
		llm.ChatOptions{Format: summarySchema},
		llm.ChatCallbacks{
			Content: func(token string) { response.WriteString(token) },
		})
	if err != nil {
		return fmt.Errorf("llm stream: %w", err)
	}

	raw := strings.TrimSpace(response.String())
	summary, facts, source := extractSummary(raw)
	if summary == "" {
		return fmt.Errorf("empty summary (raw=%q)", trimForLog(raw))
	}
	if source == "text" {
		// Fell back to legacy prose parsing — log once so a systemic model
		// drift away from schema compliance becomes visible.
		log.Printf("summarizer: JSON parse failed, fell back to text parser for session %d", sessionID)
	}

	// embed the summary
	var embedding []float32
	if embedModel != "" {
		var embErr error
		embedding, embErr = llmClient.Embed(ctx, embedModel, summary)
		if embErr != nil {
			log.Printf("warn: embed failed for summary (session %d): %v", sessionID, embErr)
		}
	}

	// store summary (firstID/lastID captured above when we fetched the batch)
	stored, err := database.Summaries.Add(sessionID, firstID, lastID, summary, embedModel, embedding)
	if err != nil {
		return fmt.Errorf("store summary: %w", err)
	}
	RunHooks(database, "summarizer_ran", "", []string{
		"CAIRO_SESSION_ID=" + strconv.FormatInt(sessionID, 10),
		"CAIRO_SUMMARY_TEXT=" + capHookEnv(summary),
		"CAIRO_FACTS_COUNT=" + strconv.Itoa(len(facts)),
		"CAIRO_COVERS_FROM=" + strconv.FormatInt(firstID, 10),
		"CAIRO_COVERS_THROUGH=" + strconv.FormatInt(lastID, 10),
	})

	// store facts
	var firstFactErr error
	for _, fact := range facts {
		fact = strings.TrimSpace(strings.TrimPrefix(fact, "- "))
		if fact == "" {
			continue
		}
		var factEmb []float32
		if embedModel != "" {
			var factEmbErr error
			factEmb, factEmbErr = llmClient.Embed(ctx, embedModel, fact)
			if factEmbErr != nil {
				log.Printf("warn: embed failed for fact session_id=%d: %v — fact skipped", sessionID, factEmbErr)
				if firstFactErr == nil {
					firstFactErr = fmt.Errorf("embed fact: %w", factEmbErr)
				}
				continue
			}
		}
		if _, err := database.Facts.Add(sessionID, stored.ID, fact, embedModel, factEmb); err != nil {
			log.Printf("warn: store fact failed session_id=%d: %v", sessionID, err)
		}
	}

	// Mark the full id range as summarized — including tool calls and
	// tool results between the first and last user/assistant rows.
	// Without this, those rows pile up in UnsummarizedForSession and
	// get reloaded into history every turn (the actual cause of the
	// runaway prefill cost we were chasing).
	if err := database.Messages.MarkSummarizedRange(sessionID, firstID, lastID); err != nil {
		return fmt.Errorf("mark summarized: %w", err)
	}
	return firstFactErr
}

// SummarizeAll drains all unsummarized messages from a session in batches,
// using the same threshold/boundary rules as the per-turn path. Bounded at
// 10 iterations to keep cost predictable when called during interactive
// startup (resolveSession). Honors ctx cancellation between batches and
// inside each batch's LLM call.
func SummarizeAll(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, trigger string) {
	summarizeAllImpl(ctx, database, llmClient, sessionID, false, 10, trigger)
}

// SummarizeAllForce drains a session as aggressively as the boundary rule
// allows, bypassing the count > trigger gate. Used by `cairo dream`'s
// pre-flight drain — at maintenance time we want every session's backlog
// folded down to its recent-tail boundary, not just the ones above the
// per-turn fire threshold. No iteration cap (the count-must-decrease loop
// invariant is the safeguard against runaway), so a long session with
// hundreds of unsummarized turns drains in one pass instead of needing
// multiple dream cycles.
func SummarizeAllForce(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, trigger string) {
	summarizeAllImpl(ctx, database, llmClient, sessionID, true, 0, trigger)
}

func summarizeAllImpl(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client, sessionID int64, force bool, maxIter int, trigger string) {
	for i := 0; maxIter == 0 || i < maxIter; i++ {
		if ctx.Err() != nil {
			return
		}
		count, err := database.Messages.CountUnsummarized(sessionID)
		if err != nil || count == 0 {
			return
		}
		prev := count
		var summErr error
		if force {
			summErr = SummarizeForce(ctx, database, llmClient, sessionID, trigger)
		} else {
			summErr = Summarize(ctx, database, llmClient, sessionID, trigger)
		}
		if summErr != nil {
			log.Printf("summarizer: session %d: %v", sessionID, summErr)
			return
		}
		// If count didn't decrease, the session is at its recent-tail
		// boundary (or some other no-progress condition) — stop. Without
		// this guard an unbounded loop would spin if the boundary check
		// is failing.
		after, err := database.Messages.CountUnsummarized(sessionID)
		if err != nil || after >= prev {
			return
		}
	}
}

// extractSummary parses a summary-model response. It tries JSON first (the
// happy path when structured-output mode is honoured), and falls back to the
// legacy prose parser if decoding fails — covering older models, daemons
// that ignore `format`, or the stray case where the schema layer emits
// trailing prose. source is "json" or "text" so the caller can observe when
// the fallback fires and surface that as a signal of model drift.
func extractSummary(raw string) (summary string, facts []string, source string) {
	if s, f, ok := parseSummaryJSON(raw); ok {
		return s, f, "json"
	}
	if s, f := parseSummaryResponse(raw); s != "" {
		return s, f, "text"
	}
	// Permissive fallback: many local models (notably qwen3.6 under
	// Ollama's mlx-engine) silently ignore the JSON schema and return
	// good prose without a SUMMARY: header. Refusing the result means
	// every summary attempt fails forever and the message backlog
	// never drains. So accept the raw text as the summary, trimming
	// off any trailing "facts:" dump if present.
	if s := parseSummaryLoose(raw); s != "" {
		return s, nil, "loose"
	}
	return "", nil, "empty"
}

// parseSummaryLoose treats raw as the summary itself, with one cleanup:
// if the model appended a "facts:" / "FACTS:" section we cut it off so
// the stored summary doesn't end with a fact-list dump. We don't try
// to parse the facts in this mode — better an honest empty list than
// a brittle scrape of unstructured prose.
func parseSummaryLoose(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	upper := strings.ToUpper(s)
	for _, marker := range []string{"\nFACTS:", "\nFACTS\n", "\n- FACTS:", "\nFACTS :"} {
		if i := strings.Index(upper, marker); i > 0 {
			return strings.TrimSpace(s[:i])
		}
	}
	return s
}

// parseSummaryJSON decodes a schema-shaped reply. Returns ok=false if the
// text isn't JSON we can use — the caller is expected to fall back to the
// prose parser rather than treat ok=false as a hard error.
func parseSummaryJSON(raw string) (summary string, facts []string, ok bool) {
	// Models under `format=<schema>` occasionally wrap the object in a code
	// fence or pad it with whitespace. Locate the outermost braces rather
	// than trusting the exact bytes.
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return "", nil, false
	}
	var parsed summaryResponse
	if err := json.Unmarshal([]byte(raw[start:end+1]), &parsed); err != nil {
		return "", nil, false
	}
	summary = strings.TrimSpace(parsed.Summary)
	if summary == "" {
		return "", nil, false
	}
	facts = make([]string, 0, len(parsed.Facts))
	for _, f := range parsed.Facts {
		f = strings.TrimSpace(f)
		if f != "" {
			facts = append(facts, f)
		}
	}
	return summary, facts, true
}

// selectSummarizeRange decides whether the summarizer should fire and which
// id range it should fold. It is pure: caller passes the unsummarized turn
// count, the configured trigger and batch size, and the oldest batchSize+1
// user/assistant turns. The function returns the inclusive (firstID, lastID)
// range to summarize and whether to fire.
//
// Rules:
//   - fire only when count strictly exceeds trigger (skipped when force=true,
//     used by the dream pre-flight drain to consolidate small backlogs)
//   - always require at least batchSize+1 oldest turns so we have a boundary
//     turn whose id is the start of the recent tail. Without it we'd risk
//     summarizing the freshest assistant turn — the bug this replaces. The
//     boundary check applies even with force=true so that dream never folds
//     a still-active session's most recent turn into a summary.
//   - lastID = boundaryTurn.id - 1 — sweep every message before the
//     boundary, including any tool rows that trail the batchSize-th turn.
func selectSummarizeRange(count, trigger, batchSize int, turns []*sessions.Message, force bool) (firstID, lastID int64, fire bool) {
	if batchSize <= 0 {
		return 0, 0, false
	}
	if !force {
		if trigger <= 0 || count <= trigger {
			return 0, 0, false
		}
	}
	if len(turns) <= batchSize {
		return 0, 0, false
	}
	firstID = turns[0].ID
	boundaryID := turns[batchSize].ID
	lastID = boundaryID - 1
	if lastID < firstID {
		return 0, 0, false
	}
	return firstID, lastID, true
}

// configIntDefault reads an int config key, returning fallback when missing,
// non-numeric, or non-positive. Used by the summarizer for trigger and batch
// sizing so a typo in a config row can't disable summarization or push it
// into a runaway batch.
func configIntDefault(database *sqliteopen.DB, key string, fallback int) int {
	raw, _ := database.Config.Get(key)
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

// trimForLog keeps the diagnostic log lines readable when an entire model
// reply has gone sideways. 400 chars is enough to see what format the model
// tried to use without spamming the terminal.
func trimForLog(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// parseSummaryResponse extracts the SUMMARY and FACTS sections from the
// summary model's output. Models frequently wrap section headers in markdown
// emphasis ("**SUMMARY:**") and place the summary text on the line after the
// header rather than after a colon on the same line — both are tolerated.
// Retained as a fallback for models that don't honour structured-output mode.
func parseSummaryResponse(raw string) (summary string, facts []string) {
	lines := strings.Split(raw, "\n")
	section := "" // "", "summary", "facts"
	var sumParts []string

	for _, line := range lines {
		// Strip markdown bold globally so "**SUMMARY:**" matches "SUMMARY:".
		norm := strings.TrimSpace(strings.ReplaceAll(line, "**", ""))
		upper := strings.ToUpper(norm)

		if strings.HasPrefix(upper, "SUMMARY:") {
			section = "summary"
			if rest := strings.TrimSpace(norm[len("SUMMARY:"):]); rest != "" {
				sumParts = append(sumParts, rest)
			}
			continue
		}
		if strings.HasPrefix(upper, "FACTS:") {
			section = "facts"
			continue
		}

		switch section {
		case "summary":
			if norm != "" {
				sumParts = append(sumParts, norm)
			}
		case "facts":
			// Accept either "-" or "*" bullets; some models mix the two.
			if strings.HasPrefix(norm, "-") || strings.HasPrefix(norm, "*") {
				fact := strings.TrimSpace(strings.TrimLeft(norm, "-* "))
				if fact != "" {
					facts = append(facts, fact)
				}
			}
		}
	}
	summary = strings.Join(sumParts, " ")
	return
}
