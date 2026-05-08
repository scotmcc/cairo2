package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/scotmcc/cairo2/internal/llm"
)

// noopLLM returns a client pointing to a closed port. If any test reaches the
// LLM call it will get a connection error, signalling that the pre_summarize
// abort did not fire as expected.
func noopLLM() *llm.Client {
	return llm.New("http://127.0.0.1:1", "")
}

// TestPreSummarize_Fires_WithCorrectPayload verifies that the pre_summarize
// hook receives the correct CAIRO_SESSION_ID, CAIRO_MESSAGE_COUNT, and
// CAIRO_TRIGGER env vars.
func TestPreSummarize_Fires_WithCorrectPayload(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	out := filepath.Join(t.TempDir(), "payload")

	// Hook writes env vars to out, then aborts so no LLM call is attempted.
	cmd := fmt.Sprintf(
		`echo "$CAIRO_SESSION_ID $CAIRO_MESSAGE_COUNT $CAIRO_TRIGGER" > %s && echo '{"continue":false}'`,
		out,
	)
	addHook(t, d, "pre_summarize", cmd)
	addTestMessages(t, d, sid, 5, "test message") // batchSize+1 = 5 so force fires

	wantCount, err := d.Messages.CountUnsummarized(sid)
	if err != nil {
		t.Fatalf("count unsummarized: %v", err)
	}
	if err := SummarizeForce(context.Background(), d, noopLLM(), sid, "test"); err != nil {
		t.Fatalf("SummarizeForce: %v", err)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_summarize hook did not write payload file: %v", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(raw)))
	if len(parts) != 3 {
		t.Fatalf("expected 3 space-separated fields in payload, got %d: %q", len(parts), string(raw))
	}
	if parts[0] != fmt.Sprintf("%d", sid) {
		t.Errorf("CAIRO_SESSION_ID: got %q, want %d", parts[0], sid)
	}
	if parts[1] != fmt.Sprintf("%d", wantCount) {
		t.Errorf("CAIRO_MESSAGE_COUNT: got %q, want %d", parts[1], wantCount)
	}
	if parts[2] != "test" {
		t.Errorf("CAIRO_TRIGGER: got %q, want %q", parts[2], "test")
	}
}

// TestPreSummarize_ContinueFalse_AbortsLLMCall verifies that a pre_summarize
// hook returning {"continue":false} prevents the LLM from being contacted.
// If the LLM were called, noopLLM() would return a connection error; nil
// confirms the abort path was taken.
func TestPreSummarize_ContinueFalse_AbortsLLMCall(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	addHook(t, d, "pre_summarize", `echo '{"continue":false}'`)
	addTestMessages(t, d, sid, 5, "test message")

	if err := SummarizeForce(context.Background(), d, noopLLM(), sid, "abort-test"); err != nil {
		t.Fatalf("expected nil (hook aborted before LLM call), got: %v", err)
	}
}

// TestPreSummarize_AutoTrigger verifies that Summarize threads trigger="auto"
// through to the pre_summarize hook's CAIRO_TRIGGER env var.
func TestPreSummarize_AutoTrigger(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	out := filepath.Join(t.TempDir(), "trigger")

	cmd := fmt.Sprintf(
		`echo "$CAIRO_TRIGGER" > %s && echo '{"continue":false}'`,
		out,
	)
	addHook(t, d, "pre_summarize", cmd)
	// count > threshold (default 8) triggers the non-force path.
	addTestMessages(t, d, sid, 9, "turn")

	if err := Summarize(context.Background(), d, noopLLM(), sid, "auto"); err != nil {
		t.Fatalf("Summarize: %v", err)
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_summarize hook did not write trigger file: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "auto" {
		t.Errorf("CAIRO_TRIGGER: got %q, want %q", got, "auto")
	}
}

// TestPreSummarize_ShutdownTrigger verifies that SummarizeAllForce threads
// trigger="shutdown" through to the pre_summarize hook's CAIRO_TRIGGER env var.
func TestPreSummarize_ShutdownTrigger(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	out := filepath.Join(t.TempDir(), "trigger")

	cmd := fmt.Sprintf(
		`echo "$CAIRO_TRIGGER" > %s && echo '{"continue":false}'`,
		out,
	)
	addHook(t, d, "pre_summarize", cmd)
	addTestMessages(t, d, sid, 5, "turn")

	SummarizeAllForce(context.Background(), d, noopLLM(), sid, "shutdown")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_summarize hook did not write trigger file: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "shutdown" {
		t.Errorf("CAIRO_TRIGGER: got %q, want %q", got, "shutdown")
	}
}

// TestPreSummarize_StartupTrigger verifies that SummarizeAll threads
// trigger="startup" through to the pre_summarize hook's CAIRO_TRIGGER env var.
func TestPreSummarize_StartupTrigger(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	out := filepath.Join(t.TempDir(), "trigger")

	cmd := fmt.Sprintf(
		`echo "$CAIRO_TRIGGER" > %s && echo '{"continue":false}'`,
		out,
	)
	addHook(t, d, "pre_summarize", cmd)
	addTestMessages(t, d, sid, 20, "turn")

	SummarizeAll(context.Background(), d, noopLLM(), sid, "startup")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_summarize hook did not write trigger file: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "startup" {
		t.Errorf("CAIRO_TRIGGER: got %q, want %q", got, "startup")
	}
}

// TestPreSummarize_DreamTrigger verifies that SummarizeAllForce threads
// trigger="dream" through to the pre_summarize hook's CAIRO_TRIGGER env var.
// Same code path as shutdown — different label.
func TestPreSummarize_DreamTrigger(t *testing.T) {
	d := openTestDB(t)
	sid := seedSession(t, d)
	out := filepath.Join(t.TempDir(), "trigger")

	cmd := fmt.Sprintf(
		`echo "$CAIRO_TRIGGER" > %s && echo '{"continue":false}'`,
		out,
	)
	addHook(t, d, "pre_summarize", cmd)
	addTestMessages(t, d, sid, 5, "turn")

	SummarizeAllForce(context.Background(), d, noopLLM(), sid, "dream")

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("pre_summarize hook did not write trigger file: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != "dream" {
		t.Errorf("CAIRO_TRIGGER: got %q, want %q", got, "dream")
	}
}
