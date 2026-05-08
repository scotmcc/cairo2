package agent

import (
	"fmt"
	"testing"
)

// TestLLMServerErrorClassification (R3) verifies that isLLMServerError correctly
// classifies the literal output of llm.parseOpenAIError. The format strings here
// must match "openai api error (HTTP %d): ..." exactly — any drift between
// parseOpenAIError's format and this classifier is a silent regression.
func TestLLMServerErrorClassification(t *testing.T) {
	// 5xx with full JSON envelope — isLLMServerError must return true
	err5xx := fmt.Errorf("openai api error (HTTP 500): internal server error [type=server_error, code=<nil>]")
	if !isLLMServerError(err5xx) {
		t.Errorf("isLLMServerError(%q) = false, want true for 5xx with envelope", err5xx)
	}

	// 5xx with raw body fallback (no JSON envelope) — still a server error
	err5xxRaw := fmt.Errorf("openai api error (HTTP 503): service unavailable")
	if !isLLMServerError(err5xxRaw) {
		t.Errorf("isLLMServerError(%q) = false, want true for raw 5xx", err5xxRaw)
	}

	// 4xx — must NOT be classified as server error
	err4xx := fmt.Errorf("openai api error (HTTP 400): bad request [type=invalid_request_error, code=invalid_model]")
	if isLLMServerError(err4xx) {
		t.Errorf("isLLMServerError(%q) = true, want false for 4xx", err4xx)
	}

	// 4xx at the boundary — 401 is auth, not server
	err401 := fmt.Errorf("openai api error (HTTP 401): unauthorized [type=auth_error, code=<nil>]")
	if isLLMServerError(err401) {
		t.Errorf("isLLMServerError(%q) = true, want false for 401", err401)
	}

	// Legacy GGML_ASSERT pattern — kept for backends that surface it
	errGGML := fmt.Errorf("GGML_ASSERT failed: context overflow")
	if !isLLMServerError(errGGML) {
		t.Errorf("isLLMServerError(%q) = false, want true for GGML_ASSERT", errGGML)
	}

	// nil — must not panic
	if isLLMServerError(nil) {
		t.Error("isLLMServerError(nil) = true, want false")
	}
}

// stubTool is a minimal Tool implementation for validateRequired tests.
type stubTool struct {
	name   string
	schema map[string]any
}

func (s stubTool) Name() string                                             { return s.name }
func (s stubTool) Description() string                                      { return "" }
func (s stubTool) Parameters() map[string]any                               { return s.schema }
func (s stubTool) Execute(args map[string]any, ctx *ToolContext) ToolResult { return ToolResult{} }

func TestValidateRequired(t *testing.T) {
	tool := stubTool{
		name:   "test_tool",
		schema: map[string]any{"required": []string{"path", "content"}},
	}

	// both required args present — passes
	if r := validateRequired(tool, map[string]any{"path": "/tmp/x", "content": "hello"}); r != nil {
		t.Errorf("expected nil for complete args, got %q", r.Content)
	}

	// required arg missing entirely — error
	r := validateRequired(tool, map[string]any{"content": "hello"})
	if r == nil || !r.IsError {
		t.Error("expected error for missing required arg, got nil")
	}

	// required arg present but empty string — passes (per-tool guards handle empty-string)
	if r := validateRequired(tool, map[string]any{"path": "", "content": "hello"}); r != nil {
		t.Errorf("expected nil for empty-string required arg (per-tool guard handles this), got %q", r.Content)
	}

	// tool with no required array — passes without panic
	noReq := stubTool{name: "noreq", schema: map[string]any{}}
	if r := validateRequired(noReq, map[string]any{}); r != nil {
		t.Errorf("expected nil for tool with no required array, got %q", r.Content)
	}

	// required arg is non-string type (e.g. integer) — present, passes
	intTool := stubTool{
		name:   "int_tool",
		schema: map[string]any{"required": []string{"count"}},
	}
	if r := validateRequired(intTool, map[string]any{"count": float64(5)}); r != nil {
		t.Errorf("expected nil for present non-string required arg, got %q", r.Content)
	}
}
