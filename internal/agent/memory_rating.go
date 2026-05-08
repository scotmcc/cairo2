package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// memoryRatingPrompt is adapted from Cursor's Memory Prompt rubric (Memory Prompt.txt).
// Default-low bias: 99% of memories should score 1–3. Only assign 4–5 for
// clearly valuable, actionable, general preferences.
const memoryRatingPrompt = `You are judging whether a memory is worth remembering for future AI-assisted software engineering sessions.
If a memory is remembered, the AI assistant will be able to use it to make a better response in future sessions.

Here is the conversation that led to the memory suggestion:
<conversation_context>
{context}
</conversation_context>

Here is a memory that was captured:
"{memory}"

Please review this memory and decide how worthy it is of being remembered, assigning a score from 1 to 5.

A memory is worthy of being remembered if it is:
- Relevant to the domain of programming and software engineering
- Important for understanding the user, project, or context
- SPECIFIC and ACTIONABLE — vague preferences or observations should be scored low (Score: 1-2)
- Not a specific task detail, one-off request, or implementation specifics (Score: 1)
- CRUCIALLY, it MUST NOT be tied *only* to the specific files or code snippets discussed in the current conversation. It must represent a general preference or rule.

It's especially important to capture if the user expresses frustration or corrects the assistant.

<examples_rated_negatively>
Examples of memories that should NOT be remembered (Score: 1 — often tied to specific code or one-off details):
session-was-productive: The session was productive. (Vague, non-actionable — Score: 1)
refactor-target: The calculateTotal function needs refactoring. (Specific to current task — Score: 1)
variable-name-choice: Use 'userData' for this specific function. (Implementation detail — Score: 1)
specific-bug-fixed: Fixed a nil pointer in cmd/cairo/dream.go line 42. (One-off fix, not a general rule — Score: 1)

Examples of VAGUE or OBVIOUS memories (Score: 1-2):
code-organization: User likes well-organized code. (Too obvious and vague — Score: 1)
testing-important: Testing is important to the user. (Too obvious — Score: 1)
error-handling: User wants good error handling. (Too obvious — Score: 1)
go-best-practices: User follows Go best practices. (Too vague — Score: 2)
</examples_rated_negatively>

<examples_rated_neutral>
Examples of memories with MIDDLE-RANGE scores (Score: 3):
user-name: User's name is Scot. (Mildly useful context — Score: 3)
project-context: User is building a Go TUI harness with SQLite and Bubble Tea. (Specific project context, helpful but not critical — Score: 3)
</examples_rated_neutral>

<examples_rated_positively>
Examples of memories that SHOULD be remembered (Score: 4-5):
file-size-preference: User prefers small focused Go files (~100 lines per file). (Specific and actionable — Score: 4)
prefer-go-interfaces: User prefers Go interfaces over concrete types for testability. (Clear preference that affects code — Score: 4)
hotkey-prefix: TUI hotkeys must use ctrl+<key> prefix; bare vim-style keys are forbidden. (Specific constraint with history of breakage — Score: 5)
test-before-commit: Always run 'go test ./...' before committing. (Explicit workflow rule — Score: 5)
</examples_rated_positively>

Err on the side of rating things POORLY. The user gets extremely annoyed when memories are graded too highly.
Especially focus on rating VAGUE or OBVIOUS memories as 1 or 2. Those are the most likely to be wrong.
Assign score 3 if you are uncertain or if the memory is borderline. Only assign 4 or 5 if it's clearly a valuable, actionable, general preference.
Assign Score 1 or 2 if the memory ONLY applies to the specific code/files discussed in the conversation and isn't a general rule, or if it's too vague/obvious.
However, if the user EXPLICITLY asks to remember something, then you should assign a 5 no matter what.
Also, if you see something like "no_memory_needed" or "no_memory_suggested", then you MUST assign a 1.

Provide a brief justification for your score, focused on why this memory is or isn't part of the 99% that should score 1–3.
Then on a new line return the score in the format "SCORE: N" where N is an integer between 1 and 5.`

// RateImportance calls the LLM to score memory importance (1-5).
// Returns 0.5 on LLM error (low-but-not-zero default; dream will retry on next pass).
func RateImportance(ctx context.Context, client *llm.Client, model, conversationContext, memory string) (float64, error) {
	prompt := strings.NewReplacer(
		"{context}", conversationContext,
		"{memory}", memory,
	).Replace(memoryRatingPrompt)

	msgs := []llm.Message{
		{Role: "user", Content: prompt},
	}
	response, err := client.Complete(ctx, model, msgs, llm.ChatOptions{DisableThinking: true})
	if err != nil {
		return 0.5, err
	}
	return parseScore(response)
}

// parseScore extracts the integer N from "SCORE: N" in the LLM response and
// maps it onto cairo's existing [0, 1] importance scale: N/5 → [0.2, 1.0].
// Score 1 maps to 0.2 (still findable, just deboosted) rather than 0 to keep
// rated low-priority memories visible in retrieval. Accepts only integers 1–5.
func parseScore(response string) (float64, error) {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SCORE:") {
			continue
		}
		parts := strings.Fields(strings.TrimPrefix(line, "SCORE:"))
		if len(parts) == 0 {
			continue
		}
		n, err := strconv.Atoi(parts[0])
		if err != nil || n < 1 || n > 5 {
			return 0, fmt.Errorf("invalid SCORE value %q (want integer 1-5)", parts[0])
		}
		return float64(n) / 5.0, nil
	}
	return 0, fmt.Errorf("no SCORE: N line found in response")
}

// RunRater finds importance=0 memories and scores them via LLM.
// Errors are logged to stderr and do not abort the dream.
func RunRater(ctx context.Context, database *sqliteopen.DB, llmClient *llm.Client) error {
	unrated, err := database.Memories.Unrated(50)
	if err != nil {
		return fmt.Errorf("rater: fetch unrated: %w", err)
	}
	if len(unrated) == 0 {
		return nil
	}

	model, err := sqliteopen.ResolveModel(database, identity.RoleDream, "qwen3.6:35b-a3b-mlx-bf16")
	if err != nil {
		return fmt.Errorf("rater: resolve model: %w", err)
	}

	for _, m := range unrated {
		score, err := RateImportance(ctx, llmClient, model, "", m.Content)
		if err != nil {
			fmt.Fprintf(os.Stderr, "rater: rating failed for memory %d: %v\n", m.ID, err)
			continue
		}
		if err := database.Memories.SetImportance(m.ID, score); err != nil {
			fmt.Fprintf(os.Stderr, "rater: SetImportance failed for memory %d: %v\n", m.ID, err)
		}
	}
	return nil
}
