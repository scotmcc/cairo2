package cli

import (
	"strings"
	"testing"
)

func TestFormatToolArgs(t *testing.T) {
	if got := formatToolArgs(nil, 100); got != "" {
		t.Errorf("nil args: want empty, got %q", got)
	}
	if got := formatToolArgs(map[string]any{}, 100); got != "" {
		t.Errorf("empty args: want empty, got %q", got)
	}
	got := formatToolArgs(map[string]any{"q": "hello"}, 100)
	if got != `{"q":"hello"}` {
		t.Errorf("normal args: got %q", got)
	}
	long := strings.Repeat("a", 500)
	got = formatToolArgs(map[string]any{"q": long}, 30)
	if !strings.HasSuffix(got, "…") || len([]rune(got)) != 31 {
		t.Errorf("truncated args wrong: %q (runes=%d)", got, len([]rune(got)))
	}
	got = formatToolArgs(map[string]any{"q": "héllo→世界"}, 100)
	if !strings.Contains(got, "世界") {
		t.Errorf("unicode args: got %q", got)
	}
}

func TestFormatToolPreview(t *testing.T) {
	if got := formatToolPreview("", 100); got != "" {
		t.Errorf("empty: got %q", got)
	}
	got := formatToolPreview("  line one\nline\ttwo\n\nline three  ", 100)
	if got != "line one line two line three" {
		t.Errorf("multiline collapse: got %q", got)
	}
	long := strings.Repeat("世", 500)
	got = formatToolPreview(long, 50)
	if len([]rune(got)) != 51 || !strings.HasSuffix(got, "…") {
		t.Errorf("truncated preview wrong: runes=%d suffix-ok=%v", len([]rune(got)), strings.HasSuffix(got, "…"))
	}
}
