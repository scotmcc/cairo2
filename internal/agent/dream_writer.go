package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/scotmcc/cairo2/internal/llm"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/identity"
	"github.com/scotmcc/cairo2/internal/store/sessions"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// writerSystemPrompt is the system instruction for the writer role's
// intent-detection call. The model receives this as the system message paired
// with a user message containing the transcript.
//
// Design intent: we want the model to spot natural-language expressions of
// memory intent ("I should remember X", "note that Y", "keep in mind Z") that
// were NOT followed by the AI invoking a memory tool. The output is a JSON
// array of memory strings ready to store verbatim. An empty array is the
// correct answer when nothing warrants a memory.
//
// This is the v1 prompt. Selene will iterate on it after seeing real outputs.
const writerSystemPrompt = `You are a memory writer reviewing a conversation transcript.

Your job: identify places where Selene (the AI assistant) or the user expressed intent to remember something, but no memory was actually saved (no memory_tool call followed the expression).

Look for phrases like:
- "I should remember that..."
- "Note to self: ..."
- "Keep in mind that..."
- "I'll remember..."
- "Make a note that..."
- "Worth remembering: ..."
- Any other clear expression of intent to persist information for later use

For each such expression that was NOT immediately followed by a memory_tool call, extract the core fact that was meant to be remembered. Write it as a single, self-contained sentence in the first or third person, appropriate for storing as a durable memory. Omit conversational framing — state the fact itself.

Return a JSON object with a single key "memories" containing an array of strings. Each string is one memory to write. Return an empty array when no unfollowed memory-intent is found. Do not include memories that were already saved via tool call.

Output only valid JSON matching this schema: {"memories": ["...", "..."]}`

// writerResponse is the structured output shape from the writer LLM call.
type writerResponse struct {
	Memories []string `json:"memories"`
}

// writerSchema is the JSON Schema for structured-output mode.
var writerSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"memories": map[string]any{
			"type":  "array",
			"items": map[string]any{"type": "string"},
		},
	},
	"required":             []string{"memories"},
	"additionalProperties": false,
}

// RunWriter scans unreviewed messages for expressed intent to remember
// that wasn't followed by a memory_tool call, writes the missing memories,
// and logs each write to dream_log.
//
// Writer failure is logged but does not abort the dream. The caller should
// treat a non-nil error as informational only.
//
// Model note: uses the existing qwen3.6:35b-a3b-mlx-bf16 default (analytical
// work, no narrative generation needed). No separate config key required.
func RunWriter(ctx context.Context, database *sqliteopen.DB, sessionIDs []int64, dreamID int64, llmClient *llm.Client) error {
	msgs, err := database.Messages.UnreviewedForSessions(sessionIDs)
	if err != nil {
		return fmt.Errorf("writer: fetch messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	transcript := buildWriterTranscript(msgs)
	if strings.TrimSpace(transcript) == "" {
		return nil
	}

	model, err := sqliteopen.ResolveModel(database, identity.RoleDream, "qwen3.6:35b-a3b-mlx-bf16")
	if err != nil {
		return fmt.Errorf("writer: resolve model: %w", err)
	}

	llmMsgs := []llm.Message{
		{Role: "system", Content: writerSystemPrompt},
		{Role: "user", Content: "Transcript to review:\n\n" + transcript},
	}
	raw, err := llmClient.Complete(ctx, model, llmMsgs, llm.ChatOptions{
		Format:          writerSchema,
		DisableThinking: true,
	})
	if err != nil {
		return fmt.Errorf("writer: llm call: %w", err)
	}

	var resp writerResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return fmt.Errorf("writer: decode response: %w", err)
	}

	embedModel, _ := database.Config.Get(config.KeyEmbedModel)
	for _, content := range resp.Memories {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		var embedding []float32
		if embedModel != "" {
			vec, err := llmClient.Embed(ctx, embedModel, content)
			if err != nil {
				fmt.Fprintf(os.Stderr, "writer: embed failed for memory: %v — skipping\n", err)
				continue
			}
			embedding = vec
		}
		mem, err := database.Memories.Add(content, "[]", embedModel, embedding)
		if err != nil {
			fmt.Fprintf(os.Stderr, "writer: write memory: %v\n", err)
			continue
		}
		targetIDs := fmt.Sprintf("[%d]", mem.ID)
		if logErr := database.DreamLog.Add(dreamID, "wrote_missing_memory", "memories", targetIDs,
			"Selene said she'd remember this but didn't write it"); logErr != nil {
			fmt.Fprintf(os.Stderr, "writer: dream_log write: %v\n", logErr)
		}
	}
	return nil
}

// buildWriterTranscript formats messages as a readable transcript for the
// writer LLM. Each line is prefixed with the role.
func buildWriterTranscript(msgs []*sessions.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		role := m.Role
		if role == "assistant" {
			role = "Selene"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}
	return b.String()
}
