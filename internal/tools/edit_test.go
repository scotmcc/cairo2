package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scotmcc/cairo2/internal/agent"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// setupEditCtx creates a real DB for unsafe_mode checks and returns a ToolContext.
func setupEditCtx(t *testing.T, workDir string) *agent.ToolContext {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := sqliteopen.OpenAt(path)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return &agent.ToolContext{DB: d, WorkDir: workDir}
}

func argm(kvs ...any) map[string]any {
	m := make(map[string]any)
	for i := 0; i+1 < len(kvs); i += 2 {
		key, ok := kvs[i].(string)
		if !ok {
			continue
		}
		m[key] = kvs[i+1]
	}
	return m
}

func TestEditTool_Name(t *testing.T) {
	if Edit().Name() != "edit" {
		t.Errorf("expected name 'edit', got %q", Edit().Name())
	}
}

func TestEditTool_ReplaceSuccess(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("hello world"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "world", "new_text", "universe",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "edited") || !containsStr(result.Content, path) {
		t.Errorf("expected 'edited <path>', got %q", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello universe" {
		t.Errorf("file content = %q, want %q", string(data), "hello universe")
	}
}

func TestEditTool_NotFound(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("hello world"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "nonexistent", "new_text", "nothing",
	), ctx)

	if !result.IsError {
		t.Fatal("expected error for missing old_text")
	}
	if !containsStr(result.Content, "not found") {
		t.Errorf("expected 'not found' in error, got %q", result.Content)
	}
}

func TestEditTool_MultipleMatches(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("foo bar foo baz foo"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "foo", "new_text", "bar",
	), ctx)

	if !result.IsError {
		t.Fatal("expected error for multiple matches")
	}
	if !containsStr(result.Content, "3 locations") {
		t.Errorf("expected '3 locations' in error, got %q", result.Content)
	}
}

func TestEditTool_ExactlyOneMatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("foo bar foo"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "bar", "new_text", "baz",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "foo baz foo" {
		t.Errorf("file content = %q, want %q", string(data), "foo baz foo")
	}
}

func TestEditTool_PermissionDenied(t *testing.T) {
	tmp := t.TempDir()
	outsidePath := filepath.Join(tmp, "..", "outside.txt")

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", outsidePath, "old_text", "hello", "new_text", "world",
	), ctx)

	if !result.IsError {
		t.Fatal("expected permission denied error")
	}
	if !containsStr(result.Content, "unsafe_mode") && !containsStr(result.Content, "outside") {
		t.Errorf("expected safety message in %q", result.Content)
	}
}

func TestEditTool_PermissionAllowedWithUnsafeMode(t *testing.T) {
	tmp := t.TempDir()
	outsidePath := filepath.Join(tmp, "..", "outside.txt")
	err := os.WriteFile(outsidePath, []byte("hello"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	if err := ctx.DB.Config.Set("unsafe_mode", "true"); err != nil {
		t.Fatalf("set unsafe_mode: %v", err)
	}

	result := Edit().Execute(argm(
		"path", outsidePath, "old_text", "hello", "new_text", "world",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error with unsafe_mode: %s", result.Content)
	}
	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "world" {
		t.Errorf("file content = %q, want %q", string(data), "world")
	}
}

func TestEditTool_MultilineContent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	original := "line1\nline2\nline3"
	err := os.WriteFile(path, []byte(original), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "line2", "new_text", "replaced",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := "line1\nreplaced\nline3"
	if string(data) != expected {
		t.Errorf("file content =\n%q\nwant\n%q", string(data), expected)
	}
}

func TestEditTool_LastLineReplacement(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("content here"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "here", "new_text", "there",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "content there" {
		t.Errorf("file content = %q, want %q", string(data), "content there")
	}
}

func TestEditTool_NewlineInMatch(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("hello\nworld"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Edit().Execute(argm(
		"path", path, "old_text", "\n", "new_text", "\n\n",
	), ctx)

	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := "hello\n\nworld"
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

func containsStr(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
