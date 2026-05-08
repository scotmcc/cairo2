package memory_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// TestDreamsMigration_SchemaColumns verifies that the Phase 1 migration produced
// the correct column set: no embedding or embed_model, and all expected columns present.
func TestDreamsMigration_SchemaColumns(t *testing.T) {
	database := testdb.OpenTestDB(t)

	rows, err := database.SQL().Query(`PRAGMA table_info(dreams)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt, pk interface{}
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan column row: %v", err)
		}
		cols = append(cols, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}

	want := []string{"id", "created_at", "date", "narrative_path", "themes", "mood", "state_daily_ref", "last_edited_at"}
	colSet := make(map[string]bool, len(cols))
	for _, c := range cols {
		colSet[c] = true
	}

	for _, w := range want {
		if !colSet[w] {
			t.Errorf("missing column %q; got %s", w, strings.Join(cols, ", "))
		}
	}
	for _, banned := range []string{"embedding", "embed_model"} {
		if colSet[banned] {
			t.Errorf("banned column %q is present; schema not replaced", banned)
		}
	}
}

// TestDreamsAdd_RoundTrip verifies that Add returns a valid ID and GetByDate
// returns a Dream with all fields matching what was passed to Add.
func TestDreamsAdd_RoundTrip(t *testing.T) {
	database := testdb.OpenTestDB(t)

	id, err := database.Dreams.Add("2026-05-03", "/tmp/test.md", "focus", "calm", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id <= 0 {
		t.Fatalf("Add returned id %d; want > 0", id)
	}

	got, err := database.Dreams.GetByDate("2026-05-03")
	if err != nil {
		t.Fatalf("GetByDate: %v", err)
	}
	if got == nil {
		t.Fatal("GetByDate returned nil; want a record")
	}
	if got.ID != id {
		t.Errorf("ID: want %d, got %d", id, got.ID)
	}
	if got.Date != "2026-05-03" {
		t.Errorf("Date: want 2026-05-03, got %q", got.Date)
	}
	if got.NarrativePath != "/tmp/test.md" {
		t.Errorf("NarrativePath: want /tmp/test.md, got %q", got.NarrativePath)
	}
	if got.Themes != "focus" {
		t.Errorf("Themes: want focus, got %q", got.Themes)
	}
	if got.Mood != "calm" {
		t.Errorf("Mood: want calm, got %q", got.Mood)
	}
	if got.StateDailyRef != nil {
		t.Errorf("StateDailyRef: want nil, got %v", got.StateDailyRef)
	}
}

// TestDreamsAdd_UniqueDate verifies that inserting two dreams with the same date
// returns a UNIQUE constraint error on the second call.
func TestDreamsAdd_UniqueDate(t *testing.T) {
	database := testdb.OpenTestDB(t)

	if _, err := database.Dreams.Add("2026-05-03", "/tmp/a.md", "", "", nil); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	_, err := database.Dreams.Add("2026-05-03", "/tmp/b.md", "", "", nil)
	if err == nil {
		t.Fatal("second Add with same date: want UNIQUE constraint error, got nil")
	}
}

// TestDreamLog_AddAndList verifies that DreamLog.Add persists an entry and
// DreamLog.List returns it with correct fields.
func TestDreamLog_AddAndList(t *testing.T) {
	database := testdb.OpenTestDB(t)

	dreamID, err := database.Dreams.Add("2026-05-03", "/tmp/test.md", "", "", nil)
	if err != nil {
		t.Fatalf("Dreams.Add: %v", err)
	}

	if err := database.DreamLog.Add(dreamID, "merge_memories", "memories", "[1,2]", "merged A into B"); err != nil {
		t.Fatalf("DreamLog.Add: %v", err)
	}

	entries, err := database.DreamLog.List(dreamID)
	if err != nil {
		t.Fatalf("DreamLog.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("DreamLog.List: want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.DreamID != dreamID {
		t.Errorf("DreamID: want %d, got %d", dreamID, e.DreamID)
	}
	if e.Action != "merge_memories" {
		t.Errorf("Action: want merge_memories, got %q", e.Action)
	}
	if e.TargetTable != "memories" {
		t.Errorf("TargetTable: want memories, got %q", e.TargetTable)
	}
	if e.TargetIDs != "[1,2]" {
		t.Errorf("TargetIDs: want [1,2], got %q", e.TargetIDs)
	}
	if e.Note != "merged A into B" {
		t.Errorf("Note: want 'merged A into B', got %q", e.Note)
	}
}

// TestDreamsUpdateMetadata verifies that UpdateMetadata sets narrative_path,
// themes, and mood and is reflected by GetByDate.
func TestDreamsUpdateMetadata(t *testing.T) {
	database := testdb.OpenTestDB(t)

	id, err := database.Dreams.Add("2026-05-03", "<pending>", "", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := database.Dreams.UpdateMetadata(id, "/home/selene/.cairo/dreams/2026-05-03.md", "debugging, planning", "focused"); err != nil {
		t.Fatalf("UpdateMetadata: %v", err)
	}

	got, err := database.Dreams.GetByDate("2026-05-03")
	if err != nil {
		t.Fatalf("GetByDate: %v", err)
	}
	if got.NarrativePath != "/home/selene/.cairo/dreams/2026-05-03.md" {
		t.Errorf("NarrativePath: want /home/selene/.cairo/dreams/2026-05-03.md, got %q", got.NarrativePath)
	}
	if got.Themes != "debugging, planning" {
		t.Errorf("Themes: want \"debugging, planning\", got %q", got.Themes)
	}
	if got.Mood != "focused" {
		t.Errorf("Mood: want focused, got %q", got.Mood)
	}
	if got.LastEditedAt == nil {
		t.Error("LastEditedAt: want non-nil after UpdateMetadata")
	}
}

// TestDreamsDelete_CleansFileAndRow verifies that Delete removes the narrative
// file from disk, the dream_log entries, and the dreams row.
func TestDreamsDelete_CleansFileAndRow(t *testing.T) {
	database := testdb.OpenTestDB(t)

	// Create a real temp file to use as the narrative path.
	f, err := os.CreateTemp("", "cairo-dream-*.md")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	narrativePath := f.Name()
	f.Close()

	id, err := database.Dreams.Add("2026-05-04", narrativePath, "testing", "calm", nil)
	if err != nil {
		t.Fatalf("Dreams.Add: %v", err)
	}

	if err := database.DreamLog.Add(id, "test_action", "memories", "[1]", "test note"); err != nil {
		t.Fatalf("DreamLog.Add: %v", err)
	}

	if err := database.Dreams.Delete(id); err != nil {
		t.Fatalf("Dreams.Delete: %v", err)
	}

	// File must be gone.
	if _, err := os.Stat(narrativePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("narrative file still exists after Delete: %s", narrativePath)
		os.Remove(narrativePath)
	}

	// Row must be gone.
	got, err := database.Dreams.GetByDate("2026-05-04")
	if err != nil {
		t.Fatalf("GetByDate after Delete: %v", err)
	}
	if got != nil {
		t.Errorf("dreams row still exists after Delete; id=%d", got.ID)
	}

	// dream_log entries must be gone.
	entries, err := database.DreamLog.List(id)
	if err != nil {
		t.Fatalf("DreamLog.List after Delete: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dream_log entries still exist after Delete: count=%d", len(entries))
	}
}

// TestDreamLog_FKEnforced verifies that inserting a dream_log entry for a
// nonexistent dream_id is rejected by the FK constraint.
//
// FK enforcement finding: cairo's OpenAt sets PRAGMA foreign_keys = ON via
// both the DSN parameter (_foreign_keys=on) and an explicit PRAGMA call, so
// this test should fail with a real FK constraint error, not silently succeed.
func TestDreamLog_FKEnforced(t *testing.T) {
	database := testdb.OpenTestDB(t)

	err := database.DreamLog.Add(99999, "merge_memories", "memories", "[]", "should fail")
	if err == nil {
		t.Fatal("DreamLog.Add with nonexistent dream_id: want FK constraint error, got nil — " +
			"if this is silent, PRAGMA foreign_keys may not be ON in openTest")
	}
}
