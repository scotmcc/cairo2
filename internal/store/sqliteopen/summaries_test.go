package sqliteopen_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// addTestFact is a helper to insert a fact with minimal required fields.
// Uses a raw INSERT to pass NULL for the optional summary_id FK.
func addTestFact(t *testing.T, database *sqliteopen.DB) *memory.Fact {
	t.Helper()
	sess, err := database.Sessions.Create("test", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}
	res, err := database.SQL().Exec(
		`INSERT INTO facts(session_id, summary_id, content, embed_model) VALUES(?, NULL, 'test fact content', '')`,
		sess.ID)
	if err != nil {
		t.Fatalf("insert fact: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId: %v", err)
	}
	f, err := database.Facts.GetFact(id)
	if err != nil {
		t.Fatalf("GetFact: %v", err)
	}
	return f
}

// addTestSummary is a helper to insert a summary with minimal required fields.
func addTestSummary(t *testing.T, database *sqliteopen.DB) *memory.Summary {
	t.Helper()
	sess, err := database.Sessions.Create("test", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}
	s, err := database.Summaries.Add(sess.ID, 1, 10, "test summary content", "", nil)
	if err != nil {
		t.Fatalf("Summaries.Add: %v", err)
	}
	return s
}

// TestFactArchivedAt_SetAndClear verifies SetArchivedAt sets and clears archived_at on facts.
func TestFactArchivedAt_SetAndClear(t *testing.T) {
	database := testdb.OpenTestDB(t)

	f := addTestFact(t, database)

	now := time.Now().Truncate(time.Second)
	if err := database.Facts.SetArchivedAt(f.ID, &now); err != nil {
		t.Fatalf("SetArchivedAt(set): %v", err)
	}
	// GetFact filters archived rows out by contract — read archived_at via raw SQL.
	var archivedAt sql.NullString
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, f.ID).Scan(&archivedAt); err != nil {
		t.Fatalf("read archived_at after set: %v", err)
	}
	if !archivedAt.Valid {
		t.Fatal("ArchivedAt: want non-nil after set, got NULL")
	}

	if err := database.Facts.SetArchivedAt(f.ID, nil); err != nil {
		t.Fatalf("SetArchivedAt(clear): %v", err)
	}
	got, err := database.Facts.GetFact(f.ID)
	if err != nil {
		t.Fatalf("GetFact after clear: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Errorf("ArchivedAt: want nil after clear, got %v", got.ArchivedAt)
	}
}

// TestFactUnreviewed verifies Unreviewed returns only facts with reviewed_at IS NULL.
func TestFactUnreviewed(t *testing.T) {
	database := testdb.OpenTestDB(t)

	f1 := addTestFact(t, database)
	f2 := addTestFact(t, database)

	// Stamp reviewed_at on f2 via raw SQL.
	_, err := database.SQL().Exec(`UPDATE facts SET reviewed_at = datetime('now') WHERE id = ?`, f2.ID)
	if err != nil {
		t.Fatalf("stamp reviewed_at: %v", err)
	}

	list, err := database.Facts.Unreviewed()
	if err != nil {
		t.Fatalf("Unreviewed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unreviewed: want 1, got %d", len(list))
	}
	if list[0].ID != f1.ID {
		t.Errorf("Unreviewed: want id %d, got %d", f1.ID, list[0].ID)
	}
}

// TestFactUnreviewed_ExcludesArchived verifies Unreviewed skips facts that
// were archived by the dream-pass curator. Without the archived_at IS NULL
// guard, archived facts re-appear on subsequent dream cycles.
func TestFactUnreviewed_ExcludesArchived(t *testing.T) {
	database := testdb.OpenTestDB(t)

	f1 := addTestFact(t, database)
	f2 := addTestFact(t, database)

	now := time.Now()
	if err := database.Facts.SetArchivedAt(f2.ID, &now); err != nil {
		t.Fatalf("SetArchivedAt: %v", err)
	}

	list, err := database.Facts.Unreviewed()
	if err != nil {
		t.Fatalf("Unreviewed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unreviewed: want 1, got %d", len(list))
	}
	if list[0].ID != f1.ID {
		t.Errorf("Unreviewed: want id %d, got %d", f1.ID, list[0].ID)
	}
}

// TestFactUnreviewedWithEmbeddings verifies the curator-flavored query returns
// the embedding BLOB. Sets the embedding via raw SQL since addTestFact doesn't
// take one.
func TestFactUnreviewedWithEmbeddings(t *testing.T) {
	database := testdb.OpenTestDB(t)

	f := addTestFact(t, database)

	emb := []float32{0.5, 0.5, 0.0}
	encoded := memory.EncodeEmbedding(emb)
	if _, err := database.SQL().Exec(
		`UPDATE facts SET embedding = ?, embed_model = 'test-model' WHERE id = ?`,
		encoded, f.ID,
	); err != nil {
		t.Fatalf("set embedding: %v", err)
	}

	list, err := database.Facts.UnreviewedWithEmbeddings()
	if err != nil {
		t.Fatalf("UnreviewedWithEmbeddings: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("UnreviewedWithEmbeddings: want 1, got %d", len(list))
	}
	if list[0].ID != f.ID {
		t.Errorf("UnreviewedWithEmbeddings: want id %d, got %d", f.ID, list[0].ID)
	}
	if len(list[0].Embedding) != 3 {
		t.Fatalf("Embedding: want len 3, got %d", len(list[0].Embedding))
	}
	if list[0].Embedding[0] != 0.5 || list[0].Embedding[1] != 0.5 || list[0].Embedding[2] != 0.0 {
		t.Errorf("Embedding: want [0.5,0.5,0], got %v", list[0].Embedding)
	}
}

// TestSummaryMarkReviewed verifies MarkReviewed stamps reviewed_at on the given summary IDs.
func TestSummaryMarkReviewed(t *testing.T) {
	database := testdb.OpenTestDB(t)

	s1 := addTestSummary(t, database)
	s2 := addTestSummary(t, database)
	s3 := addTestSummary(t, database)

	if err := database.Summaries.MarkReviewed([]int64{s1.ID, s2.ID}); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	// Verify s1 and s2 are now reviewed via direct Get.
	got1, err := database.Summaries.Get(s1.ID)
	if err != nil {
		t.Fatalf("Get s1: %v", err)
	}
	if got1.ReviewedAt == nil {
		t.Errorf("s1.ReviewedAt: want non-nil after MarkReviewed, got nil")
	}

	got3, err := database.Summaries.Get(s3.ID)
	if err != nil {
		t.Fatalf("Get s3: %v", err)
	}
	if got3.ReviewedAt != nil {
		t.Errorf("s3.ReviewedAt: want nil (not marked), got %v", got3.ReviewedAt)
	}
}
