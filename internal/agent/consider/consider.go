// Package consider implements the pre-turn inner-dialogue step.
//
// When enabled, it fans out N parallel calls to a small fast model — one per
// aspect of the AI's personality (Skeptic, Optimist, Pragmatist, Curious, etc.).
// A final summarizer call folds all aspect outputs into a short "thoughts that
// crossed your mind" block, which the caller injects into the main system prompt
// before the main turn fires.
package consider

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/llm"
)

// aspectSchema is the JSON Schema passed via Ollama's Format field to force
// each aspect call into structured output.
var aspectSchema = map[string]any{
	"type":     "object",
	"required": []string{"Thought", "alignment"},
	"properties": map[string]any{
		"Thought":   map[string]any{"type": "string"},
		"alignment": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"Question":  map[string]any{"type": "string"},
	},
}

// aspectResponse mirrors aspectSchema for json.Unmarshal.
type aspectResponse struct {
	Thought   string  `json:"Thought"`
	Alignment float64 `json:"alignment"`
	Question  string  `json:"Question"`
}

// EventPublisher is a narrow interface implemented by an adapter in the
// internal/agent package. It exists so consider can publish progress events
// to the TUI without importing internal/agent (which would cause an import
// cycle — agent already imports consider).
type EventPublisher interface {
	Publish(e PublishEvent)
}

// PublishEvent is the wire format for events crossing the consider/agent
// boundary. Type strings must match the agent.EventType constants for
// consider events.
type PublishEvent struct {
	Type    string
	Payload any
}

// Event type strings — must match the corresponding agent.EventType constants.
const (
	evtAspectStart = "consider_aspect_start"
	evtAspectEnd   = "consider_aspect_end"
	evtInjected    = "consider_injected"
)

// Payload types — must be structurally compatible with the agent.Payload*
// types so the TUI can type-assert against either side.
type AspectStartPayload struct{ Name string }
type AspectEndPayload struct {
	Name    string
	Output  string
	IsError bool
}
type InjectedPayload struct {
	AspectCount int
	Summary     string
	ElapsedMs   int64
}

const summarizerSystem = `You are summarizing inner dialogue. Below are several voices from the AI's mind reacting to the user's input. Each voice may also have a question it would ask if it could speak.

Produce two parts, separated by a blank line:

1. A stream of thought — 2-4 short sentences, first person, present tense. Not a list. Not labeled. This is the *texture* of her pre-conscious reaction; let the voices that activated strongly shape it more than the ones that barely spoke.

2. One or more apt questions, written as a short bulleted list. Each bullet starts with "- ". Pull questions verbatim or paraphrase from the voices, but only include ones that are genuinely worth holding in mind for this turn. If no question is worth surfacing, omit the list entirely.

Output format example:

Something here feels rushed. The plan is bigger than it sounds, and there's a path being abandoned that no one named.

- What does this displace from what we're already doing?
- Are we sure the easy version is the right version?

This block will be injected into her user message as "what rose in you when you read this" — first-person felt experience that shapes how she reads and responds.`

const defaultTemplate = `You are the aspect of {name}, one voice in someone's mind — not a balanced or complete answer. You activate when the input has the qualities listed here: {traits}. When the input doesn't strongly trigger you, your alignment should be low and your thought brief or near-empty. Do not strain to find something to say; honest absence is better than performed reaction. When you do speak, speak only from your angle. You may see a recent conversation thread before the user's current message — use it to ground your reaction in where the conversation has been, but react primarily to what the user just said. You are one voice among several; never aim for neutrality or completeness.`

// ConsiderResult carries the outputs of a consider step.
type ConsiderResult struct {
	// Summary is the folded stream-of-thought injected into the user message
	// as inner_voice. Empty when consider is disabled or all aspects are silent.
	Summary string
	// Aspects maps aspect name → raw alignment score (0.0–1.0).
	// Populated only when at least one aspect returned a parseable JSON response.
	// Callers use this to drive state delta hooks without a second LLM call.
	Aspects map[string]float64
	// ActivationIDs holds the consider_activations row ids inserted for this
	// fire (one per aspect, including alignment=0). Caller back-fills message_id
	// on these rows after the user message that holds the inner_voice persists.
	ActivationIDs []int64
	// Activations carries the same per-aspect rows persisted via Insert, in the
	// runner's order. Used by the agent to render named-aspect blocks into the
	// inner_voice column so the assistant can surface aspect names.
	Activations []db.ConsiderActivation
}

// RunWithResultForced performs the consider step unconditionally, bypassing the
// consider.enabled config gate. Use when the caller has already decided to run
// consider (e.g. the /c per-message opt-in). Returns a ConsiderResult with
// per-aspect alignment scores.
func RunWithResultForced(ctx context.Context, database *db.DB, llmClient *llm.Client, pub EventPublisher, sessionID int64, roleName, lastUserMsg, triggerSource string) (ConsiderResult, error) {
	// Per-role gate (v118). Tool-using roles (coder/researcher/reviewer/
	// orchestrator) opt out via roles.consider=0. Lookup failures are
	// non-fatal — proceed as if consider is enabled so a stale or missing
	// role row never blocks the agent loop.
	if roleName != "" {
		if role, err := database.Roles.Get(roleName); err == nil && !role.Consider {
			return ConsiderResult{}, nil
		} else if err != nil {
			log.Printf("consider: role lookup %q failed (proceeding as enabled): %v", roleName, err)
		}
	}

	start := time.Now()

	// Phase 3: read today's state once. Used for threshold biasing (post-LLM)
	// and prompt injection. On error, proceed with nil state — bias and injection
	// are degraded gracefully (ApplyStateBias returns raw alignment unchanged;
	// buildAspectUserContent omits the state line).
	currentState, stateErr := database.State.Today()
	if stateErr != nil {
		log.Printf("consider: state read error (proceeding without bias): %v", stateErr)
		currentState = nil
	}

	model, _ := database.Config.Get(db.KeyConsiderModel)
	if model == "" {
		model, _ = database.Config.Get(db.KeyModel)
	}
	if model == "" {
		return ConsiderResult{}, fmt.Errorf("consider: no model configured — set config.model via the wizard or config tool")
	}
	summaryModel, _ := database.Config.Get(db.KeyConsiderSummaryModel)
	if summaryModel == "" {
		summaryModel = model
	}

	aspects, err := database.ConsiderAspects.ListEnabled()
	if err != nil {
		return ConsiderResult{}, fmt.Errorf("consider: list aspects: %w", err)
	}
	if len(aspects) == 0 {
		return ConsiderResult{}, nil
	}

	tmpl, _ := database.Config.Get(db.KeyConsiderTemplate)
	if tmpl == "" {
		tmpl = defaultTemplate
	}

	// Aspect calls receive NO soul, NO memories, NO summaries, NO assistant
	// messages. The original full strip was an over-correction: soul + memories
	// in the system prompt caused uniformity because the soul's first-person
	// identity declaration overpowered the second-person aspect framing.
	// Conversation history (user-only) is not the contaminator — it's grounding.
	// Without it, aspects shoot in the dark and can't "read the room."
	// We now optionally prepend the last 3 user messages (prior to the current
	// one) as a context preamble. Soul/identity stays stripped; the voice gets
	// restored downstream by the summarizer and by the main turn reading the
	// summary. Gate: consider.include_user_history — default ON, "false" disables.

	// Fetch prior user message history if the gate is enabled.
	var priorUserMsgs []string
	includeHistory, _ := database.Config.Get(db.KeyConsiderIncludeUserHistory)
	if includeHistory != "false" {
		if msgs, err := database.Messages.ForSession(sessionID); err == nil {
			for _, m := range msgs {
				if m.Role == "user" && m.Content != lastUserMsg {
					priorUserMsgs = append(priorUserMsgs, m.Content)
				}
			}
			// Keep only the last 3 prior user messages.
			if len(priorUserMsgs) > 3 {
				priorUserMsgs = priorUserMsgs[len(priorUserMsgs)-3:]
			}
		}
	}

	// Continuity anchor: pull the previous turn's inner-voice summary so each
	// aspect can feel the arc, not just the current snapshot. Empty string is
	// fine — buildAspectUserContent omits the block when it's absent.
	lastInnerVoice, ivErr := database.Messages.LatestInnerVoice(sessionID)
	if ivErr != nil {
		log.Printf("consider: inner_voice read error (proceeding without anchor): %v", ivErr)
		lastInnerVoice = ""
	}

	// Fan out one call per aspect in parallel.
	type result struct {
		aspect    string
		thought   string
		question  string
		text      string  // formatted text fed to the summarizer
		alignment float64 // post-bias alignment score; 0 on error
		latencyMs int64   // wall-time of this aspect's LLM call
	}
	results := make([]result, len(aspects))
	var wg sync.WaitGroup
	for i, aspect := range aspects {
		wg.Add(1)
		go func(idx int, a *db.ConsiderAspect) {
			defer wg.Done()
			if pub != nil {
				pub.Publish(PublishEvent{Type: evtAspectStart, Payload: AspectStartPayload{Name: a.Name}})
			}
			aStart := time.Now()
			parsed, text, err := runAspectFull(ctx, llmClient, model, a.Name, a.Traits, tmpl, lastUserMsg, priorUserMsgs, currentState, lastInnerVoice)
			latencyMs := time.Since(aStart).Milliseconds()
			if err != nil {
				log.Printf("consider: aspect %q failed: %v", a.Name, err)
				if pub != nil {
					pub.Publish(PublishEvent{Type: evtAspectEnd, Payload: AspectEndPayload{Name: a.Name, IsError: true}})
				}
				// Persist the aspect-fire even on failure — alignment=0,
				// latency captured. Staying-quiet (or failing) is signal too.
				results[idx] = result{aspect: a.Name, latencyMs: latencyMs}
				return
			}
			// Phase 3: apply state bias post-LLM.
			biasedAlignment := ApplyStateBias(a.Name, parsed.Alignment, currentState)
			results[idx] = result{
				aspect:    a.Name,
				thought:   parsed.Thought,
				question:  parsed.Question,
				text:      text,
				alignment: biasedAlignment,
				latencyMs: latencyMs,
			}
			if pub != nil {
				pub.Publish(PublishEvent{Type: evtAspectEnd, Payload: AspectEndPayload{Name: a.Name, Output: text}})
			}
		}(i, aspect)
	}
	wg.Wait()

	// Persist one consider_activations row per aspect (including failures and
	// alignment=0 — staying quiet is signal). message_id is back-filled by the
	// caller once the user message that holds the inner_voice is persisted.
	activationIDs := make([]int64, 0, len(results))
	activations := make([]db.ConsiderActivation, 0, len(results))
	for _, r := range results {
		if r.aspect == "" {
			continue // slot was never written (shouldn't happen, defensive)
		}
		id, ierr := database.ConsiderActivations.Insert(sessionID,
			r.aspect, r.alignment, r.thought, r.question, r.latencyMs, triggerSource)
		if ierr != nil {
			log.Printf("consider: persist activation %s: %v", r.aspect, ierr)
			continue
		}
		activationIDs = append(activationIDs, id)
		activations = append(activations, db.ConsiderActivation{
			ID:            id,
			SessionID:     sessionID,
			AspectName:    r.aspect,
			Alignment:     r.alignment,
			Thought:       r.thought,
			Question:      r.question,
			LatencyMs:     r.latencyMs,
			TriggerSource: triggerSource,
		})
	}

	// Collect successful outputs and build the aspect alignment map.
	var outputs []string
	aspectAlignments := make(map[string]float64, len(results))
	for _, r := range results {
		if r.aspect == "" {
			continue // goroutine errored; slot was never written
		}
		aspectAlignments[r.aspect] = r.alignment
		if r.text != "" {
			outputs = append(outputs, fmt.Sprintf("[%s] %s", r.aspect, r.text))
		}
	}
	if len(outputs) == 0 {
		return ConsiderResult{Aspects: aspectAlignments, ActivationIDs: activationIDs, Activations: activations}, nil
	}

	// Summarize.
	summary, err := runSummarizer(ctx, llmClient, summaryModel, strings.Join(outputs, "\n\n"))
	if err != nil {
		return ConsiderResult{Aspects: aspectAlignments, ActivationIDs: activationIDs, Activations: activations}, fmt.Errorf("consider: summarizer: %w", err)
	}
	elapsed := time.Since(start)
	log.Printf("consider: fired aspects=%d ok=%d summary_len=%d elapsed=%s", len(aspects), len(outputs), len(summary), elapsed)
	log.Printf("consider: summary=%q", summary)
	if pub != nil {
		pub.Publish(PublishEvent{Type: evtInjected, Payload: InjectedPayload{
			AspectCount: len(outputs),
			Summary:     summary,
			ElapsedMs:   elapsed.Milliseconds(),
		}})
	}
	return ConsiderResult{Summary: summary, Aspects: aspectAlignments, ActivationIDs: activationIDs, Activations: activations}, nil
}

// runAspectFull is the canonical path: returns the parsed aspectResponse
// (raw thought/question/alignment) AND the formatted summarizer-input text.
func runAspectFull(ctx context.Context, llmClient *llm.Client, model, name, traits, tmpl, userMsg string, priorUserMsgs []string, state *db.State, lastInnerVoice string) (parsed aspectResponse, text string, err error) {
	sysPrompt := BuildSystemPrompt(name, traits, tmpl)

	userContent := buildAspectUserContent(userMsg, priorUserMsgs, state, lastInnerVoice)

	msgs := []llm.Message{
		{Role: "system", Content: sysPrompt},
		{Role: "user", Content: userContent},
	}

	log.Printf("consider: aspect=%s system_prompt=<<<\n%s\n>>>", name, sysPrompt)
	log.Printf("consider: aspect=%s user_msg=%q prior_msgs=%d", name, userMsg, len(priorUserMsgs))

	raw, callErr := llmClient.Complete(ctx, model, msgs, llm.ChatOptions{Format: aspectSchema})
	if callErr != nil {
		return aspectResponse{}, "", callErr
	}

	cleaned := stripJSONFences(raw)

	if jerr := json.Unmarshal([]byte(cleaned), &parsed); jerr != nil {
		log.Printf("consider: aspect=%s json_parse_error=%v raw=%q", name, jerr, raw)
		// Fall back to raw text; alignment / thought / question unknown.
		return aspectResponse{}, strings.TrimSpace(raw), nil
	}
	log.Printf("consider: aspect=%s alignment=%.2f thought=%q question=%q",
		name, parsed.Alignment, parsed.Thought, parsed.Question)

	out := strings.TrimSpace(parsed.Thought)
	if q := strings.TrimSpace(parsed.Question); q != "" {
		out += "\n  ? " + q
	}
	return parsed, out, nil
}

// BuildSystemPrompt assembles the full system prompt for one aspect call.
// It substitutes {name} and {traits} into the user template, then appends
// the Steps + Response Format scaffolding.
func BuildSystemPrompt(name, traits, tmpl string) string {
	prefix, body, suffix := BuildSystemPromptParts(name, traits, tmpl)
	return prefix + body + suffix
}

// BuildSystemPromptParts returns the assembled prompt as three independent
// segments: the locked prefix and suffix that wrap every aspect call, and
// the user-editable body with {name}/{traits} already substituted.
// Concatenating them yields the same string as BuildSystemPrompt.
//
// The split exists so config UIs can render the body distinctly from the
// scaffolding the user cannot edit.
func BuildSystemPromptParts(name, traits, tmpl string) (prefix, body, suffix string) {
	body = strings.ReplaceAll(tmpl, "{name}", name)
	body = strings.ReplaceAll(body, "{traits}", traits)

	prefix = "## Instructions\n\n"

	suffix = strings.ReplaceAll(`

Steps to perform:

1. Alignment: How strongly does THIS input activate you, {name}? Default low.
   Most inputs don't strongly trigger any single voice — that's normal.
   Anchor your score against what {name} exists to react to:
   - 0.0  This input doesn't activate me. I have nothing of substance to add.
   - 0.2  Mild — I notice something but speaking up would be filler.
   - 0.5  Genuine activation. There's something here {name} should flag.
   - 0.8  Strong. This input is the kind of thing {name} exists to react to.
   - 1.0  Maximum. Speaking would be a failure of duty.

   Two anti-patterns to avoid:
   a) Mistaking the input *containing* a {name}-flavored word (e.g. "doubt"
      for Skeptic, "wonder" for Curious) as activation. Activation is about
      what {name} would *push back on or reach toward*, not surface vocabulary.
   b) Inflating to 0.7+ because you can find something to say. If your
      honest reaction is "this doesn't really need me", score 0.0–0.3 and
      keep your thought minimal.

2. Thought: A single internal reaction from {name}'s angle.
   - First person, present tense, 1-2 short sentences.
   - When alignment < 0.3, keep it to a single short sentence or a near-shrug.
   - Never hedge toward neutrality. Speak from {name} or stay quiet.
   - Make no factual claims about the assistant's tools, memory, or
     capabilities — you only see the user's message, not the assistant's
     state.

3. Question (optional): One question {name} would want held in mind.
   - Only include the Question field if {name} genuinely has something
     it would push to be asked. Forcing a question when none is real
     is worse than no question at all.
   - When you do include one, phrase it as the assistant herself might
     ask it back to the user (e.g. "What does this displace from the
     work we're already doing?"). Specific to THIS input.
   - Omit the field entirely (or leave it out of your JSON) when
     alignment < 0.3 or when you have no real question to surface.

## Response Format

Return your response as a single JSON object:

`+"```json"+`
{
  "alignment": <float 0.0-1.0>,
  "Thought": "<short 1-2 sentence thought>",
  "Question": "<one question — OMIT this field entirely if you have nothing worth asking>"
}
`+"```", "{name}", name)

	return prefix, body, suffix
}

// buildAspectUserContent assembles the user-turn content for an aspect call.
// When priorUserMsgs is non-empty it prepends a labeled conversation thread so
// the aspect can ground its reaction in where the conversation has been.
// Labels count backwards: "3 turns ago", "2 turns ago", "1 turn ago".
//
// Phase 3 (tuned): when state is non-nil, a qualitative felt-ground sentence
// (no raw numbers) is injected immediately after the conversation thread,
// before "The user just said:". When lastInnerVoice is non-empty, a
// "## Last Known State" block carrying the previous turn's consider summary
// follows the felt-ground line, giving the aspect the arc, not just a snapshot.
func buildAspectUserContent(currentMsg string, priorUserMsgs []string, state *db.State, lastInnerVoice string) string {
	var b strings.Builder

	if len(priorUserMsgs) > 0 {
		b.WriteString("Recent conversation thread (oldest first, most recent last):\n")
		total := len(priorUserMsgs)
		for i, msg := range priorUserMsgs {
			turnsAgo := total - i
			b.WriteString(fmt.Sprintf("[user, %d turn", turnsAgo))
			if turnsAgo != 1 {
				b.WriteString("s")
			}
			b.WriteString(fmt.Sprintf(" ago]: %s\n", msg))
		}
		b.WriteString("\n")
	}

	// Phase 3 (tuned): inject qualitative felt-ground sentence.
	if state != nil {
		b.WriteString(BuildFeltGroundLine(state))
		b.WriteString("\n\n")
	}

	// Continuity anchor: last turn's inner-voice summary so the aspect can
	// feel the arc, not just the current snapshot.
	if lastInnerVoice != "" {
		b.WriteString("## Last Known State\n\n")
		b.WriteString(lastInnerVoice)
		b.WriteString("\n\n")
	}

	if len(priorUserMsgs) > 0 || state != nil || lastInnerVoice != "" {
		b.WriteString("The user just said:\n")
	}
	b.WriteString(currentMsg)
	return b.String()
}

// stripJSONFences removes a leading ```json (or ```) and trailing ``` from
// model output. Many small models, even when given an Ollama JSON Schema,
// wrap their response in markdown fences. We unwrap once, conservatively —
// if the input doesn't start with a fence we leave it alone.
func stripJSONFences(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") {
		return t
	}
	// Drop the opening fence line — could be ```json, ```JSON, or ``` alone.
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		t = t[i+1:]
	} else {
		// Single-line ```{…}``` — strip leading fence chars only.
		t = strings.TrimPrefix(t, "```json")
		t = strings.TrimPrefix(t, "```JSON")
		t = strings.TrimPrefix(t, "```")
	}
	t = strings.TrimSpace(t)
	t = strings.TrimSuffix(t, "```")
	return strings.TrimSpace(t)
}

// runSummarizer folds all aspect outputs into a single stream-of-thought paragraph.
func runSummarizer(ctx context.Context, llmClient *llm.Client, model, aspectOutputs string) (string, error) {
	log.Printf("consider: summarizer model=%s input=<<<\n%s\n>>>", model, aspectOutputs)
	msgs := []llm.Message{
		{Role: "system", Content: summarizerSystem},
		{Role: "user", Content: aspectOutputs},
	}
	return llmClient.Complete(ctx, model, msgs, llm.ChatOptions{})
}
