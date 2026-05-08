package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/llm"
)

// TestRunLoop_PersistPaths verifies the persist contract for a single
// outer turn that contains a tool-call inner iteration followed by a
// final-text response:
//
//   - cfg.persist("assistant", "", toolCallsJSON, ...) once with both tool
//     calls represented in the JSON
//   - cfg.persistTool called once per tool call (twice), status="ok"
//   - cfg.persist("assistant", "final", ...) for the final text
func TestRunLoop_PersistPaths(t *testing.T) {
	ft := &fakeTool{}
	script := []scriptedResponse{
		{toolCalls: []toolCallSpec{
			{id: "t1", name: "fake_tool", args: "{}"},
			{id: "t2", name: "fake_tool", args: "{}"},
		}},
		{text: "final"},
	}
	cfg, _, rec, _ := newLoopHarness(t, script, harnessOpts{
		tools: []Tool{ft},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runLoop(ctx, cfg); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	msgs := rec.Messages()

	// Find the assistant tool-call persist (Content="" and ToolCallsJSON non-empty).
	var toolCallPersist *persistCall
	for i := range msgs {
		if msgs[i].Role == "assistant" && msgs[i].Content == "" && msgs[i].ToolCallsJSON != "" {
			toolCallPersist = &msgs[i]
			break
		}
	}
	if toolCallPersist == nil {
		t.Fatalf("expected one assistant persist with empty content and tool_calls JSON; got %+v", msgs)
	}
	if !strings.Contains(toolCallPersist.ToolCallsJSON, "\"t1\"") ||
		!strings.Contains(toolCallPersist.ToolCallsJSON, "\"t2\"") {
		t.Errorf("toolCallsJSON missing one of t1/t2: %q", toolCallPersist.ToolCallsJSON)
	}

	// Find the final assistant text persist.
	var finalPersist *persistCall
	for i := range msgs {
		if msgs[i].Role == "assistant" && msgs[i].Content == "final" {
			finalPersist = &msgs[i]
			break
		}
	}
	if finalPersist == nil {
		t.Errorf("expected final assistant persist with Content=\"final\"; got %+v", msgs)
	}

	tools := rec.Tools()
	if len(tools) != 2 {
		t.Fatalf("expected exactly 2 persistTool calls (one per tool call), got %d: %+v", len(tools), tools)
	}
	for _, tc := range tools {
		if tc.ToolName != "fake_tool" {
			t.Errorf("expected ToolName=fake_tool, got %q", tc.ToolName)
		}
		if tc.Status != "ok" {
			t.Errorf("expected status=ok, got %q", tc.Status)
		}
		if tc.Content != "ok" {
			t.Errorf("expected tool content=ok, got %q", tc.Content)
		}
	}
	if ft.calls.Load() != 2 {
		t.Errorf("expected fakeTool.Execute called 2x, got %d", ft.calls.Load())
	}
}

// TestRunLoop_TurnSignals verifies that ApplyTurnSignals fires at end of
// turn by observing a state delta. We pre-seed history with a user message
// containing an explicit-love signal (raises warmth). After runLoop, today's
// warmth must exceed the row's prior value.
func TestRunLoop_TurnSignals(t *testing.T) {
	script := []scriptedResponse{
		{text: "thanks for letting me know"},
	}
	history := []llm.Message{
		{Role: "user", Content: "i love you, you matter to me"},
	}
	cfg, _, _, _ := newLoopHarness(t, script, harnessOpts{
		history: history,
	})

	// Capture warmth before.
	before, err := cfg.db.State.Today()
	if err != nil {
		t.Fatalf("State.Today(before): %v", err)
	}
	warmthBefore := before.Warmth

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runLoop(ctx, cfg); err != nil {
		t.Fatalf("runLoop: %v", err)
	}

	after, err := cfg.db.State.Today()
	if err != nil {
		t.Fatalf("State.Today(after): %v", err)
	}
	if after.Warmth <= warmthBefore {
		t.Errorf("expected warmth to increase from explicit-love user signal; before=%v after=%v",
			warmthBefore, after.Warmth)
	}
}
