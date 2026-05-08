package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTool_Name(t *testing.T) {
	if Write().Name() != "write" {
		t.Errorf("expected name 'write', got %q", Write().Name())
	}
}

func TestWriteCreateNewFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "new.txt")

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", "hello world"), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "wrote") && !containsStr(result.Content, "bytes") {
		t.Errorf("expected 'wrote N bytes', got: %q", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello world" {
		t.Errorf("file content = %q, want %q", string(data), "hello world")
	}
}

func TestWriteOverwriteExistingFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "existing.txt")
	err := os.WriteFile(path, []byte("old content"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", "new content"), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new content" {
		t.Errorf("file content = %q, want %q", string(data), "new content")
	}
}

func TestWriteCreatesSubdirectories(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "deep/nested/dir/file.txt")

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", "deep content"), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "deep content" {
		t.Errorf("file content = %q, want %q", string(data), "deep content")
	}
}

func TestWriteEmptyContent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "empty.txt")

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", ""), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("expected empty file, got %q", string(data))
	}
}

func TestWriteMultilineContent(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "multiline.txt")
	content := "line1\nline2\nline3"

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", content), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file content = %q, want %q", string(data), content)
	}
}

func TestWritePermissionDenied(t *testing.T) {
	tmp := t.TempDir()
	outsidePath := filepath.Join(tmp, "..", "outside.txt")

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", outsidePath, "content", "should fail"), ctx)
	if !result.IsError {
		t.Fatal("expected permission denied error")
	}
	if !containsStr(result.Content, "unsafe_mode") && !containsStr(result.Content, "outside") {
		t.Errorf("expected safety message in %q", result.Content)
	}
}

func TestWritePermissionAllowedWithUnsafeMode(t *testing.T) {
	tmp := t.TempDir()
	outsidePath := filepath.Join(tmp, "..", "outside.txt")

	ctx := setupEditCtx(t, tmp)
	if err := ctx.DB.Config.Set("unsafe_mode", "true"); err != nil {
		t.Fatalf("set unsafe_mode: %v", err)
	}

	result := Write().Execute(argm("path", outsidePath, "content", "allowed"), ctx)
	if result.IsError {
		t.Fatalf("unexpected error with unsafe_mode: %s", result.Content)
	}

	data, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "allowed" {
		t.Errorf("file content = %q, want %q", string(data), "allowed")
	}
}

func TestWriteSpecialCharacters(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "special.txt")
	content := "UTF-8: 你好世界 🌍\nSpecial: <>&\"'\nBackslash: \\ paths"

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", content), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("file content mismatch:\ngot:  %q\nwant: %q", string(data), content)
	}
}

func TestWriteReturnsByteCount(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "counted.txt")

	ctx := setupEditCtx(t, tmp)
	result := Write().Execute(argm("path", path, "content", "12345"), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "5 bytes") {
		t.Errorf("expected '5 bytes' in output, got: %q", result.Content)
	}
}
