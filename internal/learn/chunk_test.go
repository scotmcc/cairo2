package learn

import (
	"strings"
	"testing"
)

// Test_ChunkContent_GoFile verifies that a Go source file with multiple
// funcs and a type definition is split into the expected symbol chunks.
func Test_ChunkContent_GoFile(t *testing.T) {
	src := `package foo

import "fmt"

func Alpha(x int) string {
	return fmt.Sprintf("%d", x)
}

func Beta() {
	// nothing
}

type Gamma struct {
	Name string
}
`
	chunks := ChunkContent(src, "go")
	if len(chunks) != 3 {
		t.Fatalf("want 3 chunks (Alpha, Beta, Gamma), got %d: %+v", len(chunks), chunks)
	}

	// Alpha
	if chunks[0].Name != "Alpha" {
		t.Errorf("chunk[0].Name = %q, want Alpha", chunks[0].Name)
	}
	if chunks[0].StartLine != 5 {
		t.Errorf("chunk[0].StartLine = %d, want 5", chunks[0].StartLine)
	}

	// Beta
	if chunks[1].Name != "Beta" {
		t.Errorf("chunk[1].Name = %q, want Beta", chunks[1].Name)
	}

	// Gamma — chunkGoSymbols matches `type X struct`, so Gamma is a separate chunk.
	if chunks[2].Name != "Gamma" {
		t.Errorf("chunk[2].Name = %q, want Gamma", chunks[2].Name)
	}
	if chunks[2].Label != "type" {
		t.Errorf("chunk[2].Label = %q, want type", chunks[2].Label)
	}
}

// Test_ChunkContent_GoFile_SingleFunc ensures a file with one function returns
// exactly one chunk with the correct name.
func Test_ChunkContent_GoFile_SingleFunc(t *testing.T) {
	src := `package bar

func OnlyOne() int {
	return 42
}
`
	chunks := ChunkContent(src, "go")
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Name != "OnlyOne" {
		t.Errorf("Name = %q, want OnlyOne", chunks[0].Name)
	}
	if chunks[0].Label != "method" {
		t.Errorf("Label = %q, want method", chunks[0].Label)
	}
}

// Test_ChunkContent_NoFuncs_ReturnsWholeFile verifies fallback for Go files
// with no func signatures — the whole file becomes one chunk.
func Test_ChunkContent_NoFuncs_ReturnsWholeFile(t *testing.T) {
	src := `package consts

const Foo = 1
const Bar = 2
`
	chunks := ChunkContent(src, "go")
	if len(chunks) != 1 {
		t.Fatalf("want 1 chunk (whole-file fallback), got %d", len(chunks))
	}
	if chunks[0].StartLine != 1 {
		t.Errorf("StartLine = %d, want 1", chunks[0].StartLine)
	}
}

// Test_ChunkContent_NonGoFile verifies that an unsupported file type falls
// through to paragraph-based chunking (the default).
func Test_ChunkContent_NonGoFile(t *testing.T) {
	src := "line one\nline two\n\nline four\nline five\n"
	chunks := ChunkContent(src, "txt")
	// Two paragraphs separated by blank line.
	if len(chunks) != 2 {
		t.Fatalf("want 2 paragraph chunks, got %d", len(chunks))
	}
	if chunks[0].Label != "paragraph" {
		t.Errorf("Label = %q, want paragraph", chunks[0].Label)
	}
}

// Test_AugmentedText verifies the format produced for embedding inputs.
func Test_AugmentedText(t *testing.T) {
	chunk := Chunk{
		StartLine: 10,
		Length:    5,
		Content:   "func Foo() {}",
		Label:     "method",
		Name:      "Foo",
	}
	got := AugmentedText("myproject", "internal/foo/foo.go", chunk)
	want := "project=myproject file=internal/foo/foo.go line=10 func=Foo · func Foo() {}"
	if got != want {
		t.Errorf("AugmentedText =\n  %q\nwant\n  %q", got, want)
	}
}

// Test_AugmentedText_NoName verifies the format when the chunk has no name
// (e.g. a whole-file fallback chunk).
func Test_AugmentedText_NoName(t *testing.T) {
	chunk := Chunk{
		StartLine: 1,
		Length:    3,
		Content:   "package main",
		Label:     "method",
		Name:      "",
	}
	got := AugmentedText("proj", "main.go", chunk)
	if strings.Contains(got, "func=") {
		t.Errorf("unexpected func= in AugmentedText with empty name: %q", got)
	}
}

// Test_SplitOversizedChunks_SplitsAtLineBoundary verifies that a chunk
// exceeding maxTokens is split at a line boundary and no content is lost.
func Test_SplitOversizedChunks_SplitsAtLineBoundary(t *testing.T) {
	// Build a chunk whose content is ~600 chars (150 tokens * 4) with 30
	// lines of 20 chars each. maxTokens=10 (40 chars) forces multiple splits.
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, strings.Repeat("x", 19)) // 19 chars + implicit newline = 20
	}
	content := strings.Join(lines, "\n")
	input := []Chunk{{
		StartLine: 1,
		Length:    30,
		Content:   content,
		Label:     "method",
		Name:      "BigFunc",
	}}

	got := splitOversizedChunks(input, 10) // 10 tokens = 40 chars max

	if len(got) < 2 {
		t.Fatalf("expected multiple chunks from oversized input, got %d", len(got))
	}

	// No individual chunk should exceed maxTokens*4 chars (except a single
	// line that alone exceeds the cap, which is emitted as-is).
	maxChars := 10 * 4
	for i, c := range got {
		if len(c.Content) > maxChars {
			t.Errorf("chunk[%d] content len=%d exceeds maxChars=%d", i, len(c.Content), maxChars)
		}
	}

	// Reassembled content must equal original (split preserves all content).
	var parts []string
	for _, c := range got {
		parts = append(parts, c.Content)
	}
	reassembled := strings.Join(parts, "\n")
	if reassembled != content {
		t.Errorf("reassembled content does not match original\ngot:  %q\nwant: %q", reassembled, content)
	}

	// Names should include _part1, _part2 suffix.
	if !strings.HasSuffix(got[0].Name, "_part1") {
		t.Errorf("first sub-chunk Name = %q, want suffix _part1", got[0].Name)
	}
	if !strings.HasSuffix(got[1].Name, "_part2") {
		t.Errorf("second sub-chunk Name = %q, want suffix _part2", got[1].Name)
	}
}

// Test_SplitOversizedChunks_SmallChunkUnchanged verifies that chunks under
// the cap pass through without modification.
func Test_SplitOversizedChunks_SmallChunkUnchanged(t *testing.T) {
	input := []Chunk{{
		StartLine: 5,
		Length:    3,
		Content:   "func Foo() {}",
		Label:     "method",
		Name:      "Foo",
	}}
	got := splitOversizedChunks(input, 400)
	if len(got) != 1 {
		t.Fatalf("want 1 chunk (no split), got %d", len(got))
	}
	if got[0].Name != "Foo" || got[0].StartLine != 5 {
		t.Errorf("chunk mutated unexpectedly: %+v", got[0])
	}
}

// Test_SplitOversizedChunks_NoNameChunk verifies that splitting a chunk with
// no name does not produce _partN suffixes (name stays empty).
func Test_SplitOversizedChunks_NoNameChunk(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, strings.Repeat("y", 19))
	}
	content := strings.Join(lines, "\n")
	input := []Chunk{{
		StartLine: 1,
		Length:    20,
		Content:   content,
		Label:     "paragraph",
		Name:      "", // no name
	}}

	got := splitOversizedChunks(input, 5) // force split
	if len(got) < 2 {
		t.Fatalf("expected split, got %d chunks", len(got))
	}
	for i, c := range got {
		if c.Name != "" {
			t.Errorf("chunk[%d].Name = %q, want empty for unnamed source chunk", i, c.Name)
		}
	}
}

// Test_ChunkContent_LargeGoFunc verifies that a Go function exceeding the
// default MaxChunkTokens cap is split into multiple chunks, and that the
// existing symbol-level chunking is preserved for smaller functions.
func Test_ChunkContent_LargeGoFunc(t *testing.T) {
	// Save and restore MaxChunkTokens so this test doesn't affect others.
	orig := MaxChunkTokens
	defer func() { MaxChunkTokens = orig }()
	MaxChunkTokens = 10 // 40 chars — forces split on any real function body

	// A function with enough lines/chars to exceed 10 tokens (40 chars).
	src := "package p\n\nfunc BigFn() {\n" + strings.Repeat("\tx := 1\n", 20) + "}\n"
	chunks := ChunkContent(src, "go")

	if len(chunks) < 2 {
		t.Fatalf("expected BigFn to be split into 2+ chunks at cap=10, got %d", len(chunks))
	}
	// Each chunk's content should be within the cap.
	maxChars := MaxChunkTokens * 4
	for i, c := range chunks {
		if len(c.Content) > maxChars {
			t.Errorf("chunk[%d] len=%d exceeds cap=%d", i, len(c.Content), maxChars)
		}
	}
}
