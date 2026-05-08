package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTool_Name(t *testing.T) {
	if Read().Name() != "read" {
		t.Errorf("expected name 'read', got %q", Read().Name())
	}
}

func TestReadBasicFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	content := "line1\nline2\nline3"
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Should contain line numbers and content
	if !containsStr(result.Content, "1\tline1") {
		t.Errorf("expected '1\\tline1' in output, got: %q", result.Content)
	}
	if !containsStr(result.Content, "2\tline2") {
		t.Errorf("expected '2\\tline2' in output, got: %q", result.Content)
	}
	if !containsStr(result.Content, "3\tline3") {
		t.Errorf("expected '3\\tline3' in output, got: %q", result.Content)
	}
}

func TestReadWithOffsetAndLimit(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	content := "a\nb\nc\nd\ne"
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path, "offset", 2, "limit", 2), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "2\tb") || !containsStr(result.Content, "3\tc") {
		t.Errorf("expected lines 2-3, got: %q", result.Content)
	}
	if containsStr(result.Content, "1\ta") {
		t.Errorf("unexpected line 1 in truncated output: %q", result.Content)
	}
	if containsStr(result.Content, "4\td") || containsStr(result.Content, "5\te") {
		t.Errorf("unexpected lines 4-5 in truncated output: %q", result.Content)
	}
}

func TestReadTruncationMessage(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	content := "a\nb\nc\nd\ne\nf\ng\nh"
	err := os.WriteFile(path, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path, "limit", 3), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "truncated") || !containsStr(result.Content, "8 lines total") {
		t.Errorf("expected truncation message with line count, got: %q", result.Content)
	}
}

func TestReadNonExistentFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "nonexistent.txt")

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path), ctx)
	if !result.IsError {
		t.Fatal("expected error for non-existent file")
	}
	if !containsStr(result.Content, "no such file") && !containsStr(result.Content, "not found") {
		t.Errorf("expected 'no such file' or 'not found', got: %q", result.Content)
	}
}

func TestReadEmptyFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "empty.txt")
	err := os.WriteFile(path, []byte(""), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if containsStr(result.Content, "truncated") {
		t.Errorf("empty file should not show truncation message")
	}
}

func TestReadOffsetPastEnd(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := os.WriteFile(path, []byte("only line"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	// Offset past end should not panic
	result := Read().Execute(argm("path", path, "offset", 100, "limit", 10), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
}

func TestReadSubdirectory(t *testing.T) {
	tmp := t.TempDir()
	subdir := filepath.Join(tmp, "sub")
	err := os.MkdirAll(subdir, 0755)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(subdir, "nested.txt")
	err = os.WriteFile(path, []byte("nested content"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !containsStr(result.Content, "nested content") {
		t.Errorf("expected 'nested content', got: %q", result.Content)
	}
}

func TestReadBinaryFile(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "binary.bin")
	binary := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD}
	err := os.WriteFile(path, binary, 0644)
	if err != nil {
		t.Fatal(err)
	}

	ctx := setupEditCtx(t, tmp)
	result := Read().Execute(argm("path", path), ctx)
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	// Binary content should still be readable (just displayed as text)
	if !containsStr(result.Content, "0") { // at least some bytes will display
		t.Logf("binary read output: %q", result.Content)
	}
}
