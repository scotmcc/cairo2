package db

import (
	"testing"
)

// Test_IndexedFileQ_DeleteMissing_RemovesStaleRows seeds two indexed_files
// rows for a project, then calls DeleteMissing with only one of the rel-paths
// present. Verifies the absent row is deleted and the present row survives.
func Test_IndexedFileQ_DeleteMissing_RemovesStaleRows(t *testing.T) {
	database := openTest(t)

	// Ensure a project row exists (indexed_files has a FK to projects).
	if err := database.Projects.Upsert("testproj", "/tmp/testproj", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}

	// Insert two files with minimal data (no real embedding needed for this test).
	for _, relPath := range []string{"a.go", "b.go"} {
		_, err := database.IndexedFiles.Upsert(&IndexedFile{
			Project:    "testproj",
			RelPath:    relPath,
			FileType:   "go",
			Bytes:      100,
			SHA256:     relPath + "-sha",
			Summary:    "summary of " + relPath,
			Embedding:  []float32{0.1, 0.2},
			EmbedModel: "nomic-embed-text",
		})
		if err != nil {
			t.Fatalf("Upsert %s: %v", relPath, err)
		}
	}

	// Simulate a walk that only found a.go (b.go was deleted).
	deleted, err := database.IndexedFiles.DeleteMissing("testproj", []string{"a.go"})
	if err != nil {
		t.Fatalf("DeleteMissing: %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteMissing returned %d, want 1", deleted)
	}

	// a.go must still be present.
	sha, err := database.IndexedFiles.GetSHA("testproj", "a.go")
	if err != nil || sha == "" {
		t.Errorf("a.go missing after DeleteMissing (sha=%q, err=%v)", sha, err)
	}

	// b.go must be gone.
	sha, err = database.IndexedFiles.GetSHA("testproj", "b.go")
	if err != nil || sha != "" {
		t.Errorf("b.go still present after DeleteMissing (sha=%q, err=%v)", sha, err)
	}
}

// Test_IndexedFileQ_DeleteMissing_EmptyPresent removes all rows for a project
// when the present slice is empty (walker found nothing).
func Test_IndexedFileQ_DeleteMissing_EmptyPresent(t *testing.T) {
	database := openTest(t)

	if err := database.Projects.Upsert("emptyproj", "/tmp/emptyproj", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	_, err := database.IndexedFiles.Upsert(&IndexedFile{
		Project:    "emptyproj",
		RelPath:    "z.go",
		FileType:   "go",
		Bytes:      50,
		SHA256:     "z-sha",
		Summary:    "summary",
		Embedding:  []float32{0.5},
		EmbedModel: "nomic-embed-text",
	})
	if err != nil {
		t.Fatalf("Upsert z.go: %v", err)
	}

	deleted, err := database.IndexedFiles.DeleteMissing("emptyproj", nil)
	if err != nil {
		t.Fatalf("DeleteMissing(nil): %v", err)
	}
	if deleted != 1 {
		t.Errorf("DeleteMissing(nil) returned %d, want 1", deleted)
	}

	sha, _ := database.IndexedFiles.GetSHA("emptyproj", "z.go")
	if sha != "" {
		t.Errorf("z.go still present after DeleteMissing with empty present list")
	}
}

// Test_IndexedFileQ_DeleteMissing_NothingToDelete is a no-op case: all indexed
// files are also in the present slice — zero rows should be deleted.
func Test_IndexedFileQ_DeleteMissing_NothingToDelete(t *testing.T) {
	database := openTest(t)

	if err := database.Projects.Upsert("fullproj", "/tmp/fullproj", ""); err != nil {
		t.Fatalf("Upsert project: %v", err)
	}
	_, err := database.IndexedFiles.Upsert(&IndexedFile{
		Project:    "fullproj",
		RelPath:    "c.go",
		FileType:   "go",
		Bytes:      50,
		SHA256:     "c-sha",
		Summary:    "summary",
		Embedding:  []float32{0.5},
		EmbedModel: "nomic-embed-text",
	})
	if err != nil {
		t.Fatalf("Upsert c.go: %v", err)
	}

	deleted, err := database.IndexedFiles.DeleteMissing("fullproj", []string{"c.go"})
	if err != nil {
		t.Fatalf("DeleteMissing: %v", err)
	}
	if deleted != 0 {
		t.Errorf("DeleteMissing returned %d, want 0 (no stale files)", deleted)
	}
}
