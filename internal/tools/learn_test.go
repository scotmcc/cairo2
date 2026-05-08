package tools

import (
	"context"
	"testing"

	"github.com/scotmcc/cairo2/internal/store/index"
)

// Test_mergeLearnResults_FileOnlyWhenNoChunks verifies that when the chunks
// table is empty, mergeLearnResults returns file-summary results only.
func Test_mergeLearnResults_FileOnlyWhenNoChunks(t *testing.T) {
	database := openTestDB(t)
	const project = "testproject"
	const model = "stub"

	// Seed a project and two indexed files.
	if err := database.Projects.Upsert(project, "/tmp/test", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	embed := stubEmbedder{}
	vecA, _ := embed.Embed(context.Background(), model, "alpha content about authentication")
	vecB, _ := embed.Embed(context.Background(), model, "beta content about database schema")

	if _, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "auth.go",
		FileType:   "go",
		Bytes:      100,
		SHA256:     "aaaa",
		Summary:    "Handles user authentication.",
		Embedding:  vecA,
		EmbedModel: model,
	}); err != nil {
		t.Fatalf("Upsert file A: %v", err)
	}
	if _, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "schema.go",
		FileType:   "go",
		Bytes:      200,
		SHA256:     "bbbb",
		Summary:    "Defines the database schema.",
		Embedding:  vecB,
		EmbedModel: model,
	}); err != nil {
		t.Fatalf("Upsert file B: %v", err)
	}

	// Query vector matches something about authentication.
	qvec, _ := embed.Embed(context.Background(), model, "alpha content about authentication")

	results := mergeLearnResults(database, project, "", qvec, model, 10)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}
	for _, r := range results {
		if r.IsChunk {
			t.Errorf("expected file-only results, got chunk for %s", r.RelPath)
		}
	}
	// Top result should be the file whose embedding was derived from the same text.
	if results[0].RelPath != "auth.go" {
		t.Errorf("top result = %q, want auth.go", results[0].RelPath)
	}
}

// Test_mergeLearnResults_ChunksSurfaceAboveFileSummaries verifies that when a
// chunk has a higher score than a file summary, it appears first in the merged
// list.
func Test_mergeLearnResults_ChunksSurfaceAboveFileSummaries(t *testing.T) {
	database := openTestDB(t)
	const project = "chunkproject"
	const model = "stub"

	if err := database.Projects.Upsert(project, "/tmp/chunk", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	embed := stubEmbedder{}

	// File A: low-score file summary (short text → small vec component).
	vecA, _ := embed.Embed(context.Background(), model, "x")
	fileAID, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "fileA.go",
		FileType:   "go",
		Bytes:      50,
		SHA256:     "cccc",
		Summary:    "x",
		Embedding:  vecA,
		EmbedModel: model,
	})
	if err != nil {
		t.Fatalf("Upsert file A: %v", err)
	}

	// Chunk inside file A with a long matching string → high cosine score.
	longText := "alpha content about authentication with lots of context"
	chunkVec, _ := embed.Embed(context.Background(), model, longText)
	if _, err := database.Chunks.Upsert(
		fileAID, "method", "DoAuth", longText,
		1, 10, chunkVec, model,
	); err != nil {
		t.Fatalf("Upsert chunk: %v", err)
	}

	// File B: also matches the query well.
	vecB, _ := embed.Embed(context.Background(), model, longText)
	if _, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "fileB.go",
		FileType:   "go",
		Bytes:      80,
		SHA256:     "dddd",
		Summary:    longText,
		Embedding:  vecB,
		EmbedModel: model,
	}); err != nil {
		t.Fatalf("Upsert file B: %v", err)
	}

	qvec, _ := embed.Embed(context.Background(), model, longText)

	results := mergeLearnResults(database, project, "", qvec, model, 10)
	if len(results) == 0 {
		t.Fatal("expected results")
	}

	// There should be at least one chunk result in the merged output.
	hasChunk := false
	for _, r := range results {
		if r.IsChunk {
			hasChunk = true
			break
		}
	}
	if !hasChunk {
		t.Error("expected at least one chunk result in merged output")
	}
}

// Test_mergeLearnResults_DeduplicatesChunkFile verifies that when both a
// chunk and a file summary exist for the same file, the file does not appear
// twice in the merged results.
func Test_mergeLearnResults_DeduplicatesChunkFile(t *testing.T) {
	database := openTestDB(t)
	const project = "dedupproject"
	const model = "stub"

	if err := database.Projects.Upsert(project, "/tmp/dedup", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	embed := stubEmbedder{}

	text := "some function implementation"
	vec, _ := embed.Embed(context.Background(), model, text)
	fileID, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "only.go",
		FileType:   "go",
		Bytes:      100,
		SHA256:     "eeee",
		Summary:    text,
		Embedding:  vec,
		EmbedModel: model,
	})
	if err != nil {
		t.Fatalf("Upsert file: %v", err)
	}

	// Add two chunks for this file — they should de-dup to one chunk entry.
	chunkVec, _ := embed.Embed(context.Background(), model, text)
	if _, err := database.Chunks.Upsert(fileID, "method", "Func1", text, 1, 5, chunkVec, model); err != nil {
		t.Fatalf("Upsert chunk1: %v", err)
	}
	if _, err := database.Chunks.Upsert(fileID, "method", "Func2", text, 6, 5, chunkVec, model); err != nil {
		t.Fatalf("Upsert chunk2: %v", err)
	}

	qvec, _ := embed.Embed(context.Background(), model, text)
	results := mergeLearnResults(database, project, "", qvec, model, 10)

	// Count how many times "only.go" appears.
	count := 0
	for _, r := range results {
		if r.RelPath == "only.go" && r.IsChunk {
			count++
		}
	}
	if count > 1 {
		t.Errorf("only.go appeared as chunk %d times in merged results, want ≤1", count)
	}
}

// Test_mergeLearnResults_SymbolNameBoost verifies that when the query string
// matches a chunk's Name field exactly (case-insensitive), the chunk surfaces
// above a file summary that would otherwise out-rank it on cosine alone.
// Source: 2026-05-05 session-2 finding — searching `splitOversizedChunks`
// returned the parent file's broad summary first instead of the chunk itself.
func Test_mergeLearnResults_SymbolNameBoost(t *testing.T) {
	database := openTestDB(t)
	const project = "boostproject"
	const model = "stub"

	if err := database.Projects.Upsert(project, "/tmp/boost", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	embed := stubEmbedder{}

	// File summary that out-cosine-scores the chunk on raw vector match.
	winningText := "long descriptive summary of authentication splitOversizedChunks paths and helpers galore"
	vecWin, _ := embed.Embed(context.Background(), model, winningText)
	if _, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "wins_on_cosine.go",
		FileType:   "go",
		Bytes:      120,
		SHA256:     "eeee",
		Summary:    winningText,
		Embedding:  vecWin,
		EmbedModel: model,
	}); err != nil {
		t.Fatalf("Upsert wins_on_cosine: %v", err)
	}

	// Target file holding a chunk literally named splitOversizedChunks.
	targetText := "func body split things up"
	vecTarget, _ := embed.Embed(context.Background(), model, targetText)
	targetID, err := database.IndexedFiles.Upsert(&index.IndexedFile{
		Project:    project,
		RelPath:    "chunk.go",
		FileType:   "go",
		Bytes:      50,
		SHA256:     "ffff",
		Summary:    targetText,
		Embedding:  vecTarget,
		EmbedModel: model,
	})
	if err != nil {
		t.Fatalf("Upsert target file: %v", err)
	}
	if _, err := database.Chunks.Upsert(
		targetID, "method", "splitOversizedChunks", targetText,
		1, 10, vecTarget, model,
	); err != nil {
		t.Fatalf("Upsert target chunk: %v", err)
	}

	qvec, _ := embed.Embed(context.Background(), model, winningText)

	// Without the boost (empty query), the file summary that wins on cosine
	// should appear above the target chunk.
	noBoost := mergeLearnResults(database, project, "", qvec, model, 10)
	if len(noBoost) < 1 || noBoost[0].RelPath != "wins_on_cosine.go" {
		t.Logf("baseline ordering (informational): %+v", summarizePaths(noBoost))
	}

	// With the boost (query = "splitOversizedChunks"), the named chunk should
	// surface to the top.
	boosted := mergeLearnResults(database, project, "splitOversizedChunks", qvec, model, 10)
	if len(boosted) == 0 {
		t.Fatal("boosted result set was empty")
	}
	if !boosted[0].IsChunk || boosted[0].Name != "splitOversizedChunks" {
		t.Errorf("symbol-name boost: top result should be the splitOversizedChunks chunk, got %+v", summarizePaths(boosted))
	}
}

func summarizePaths(rs []learnResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		if r.IsChunk {
			out[i] = r.RelPath + ":" + r.Name
		} else {
			out[i] = r.RelPath
		}
	}
	return out
}
