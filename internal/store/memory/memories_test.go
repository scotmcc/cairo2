package memory_test

import (
	"database/sql"
	"testing"
	"time"

	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// TestMemoryAdd_ImportanceSentinel verifies that Memories.Add sets importance=0.5
// as the unrated sentinel — keeps the memory findable in retrieval until the
// dream pass scores it via the LLM rater.
func TestMemoryAdd_ImportanceSentinel(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("some fresh memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if m.Importance != 0.5 {
		t.Errorf("importance after Add: want 0.5 (unrated sentinel), got %v", m.Importance)
	}
}

// TestMemoryContentHashDedup_SameContent verifies that inserting the same content
// twice within the 7-day window returns the existing memory (same ID).
func TestMemoryContentHashDedup_SameContent(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, err := database.Memories.Add("prefer small Go files", "[]", "", nil)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	m2, err := database.Memories.Add("prefer small Go files", "[]", "", nil)
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if m1.ID != m2.ID {
		t.Errorf("dedup: want same ID on duplicate add (got %d and %d)", m1.ID, m2.ID)
	}
}

// TestMemoryContentHashDedup_Whitespace verifies that content differing only in
// leading/trailing whitespace is deduped (same normalized hash).
func TestMemoryContentHashDedup_Whitespace(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, err := database.Memories.Add("prefer small Go files", "[]", "", nil)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	m2, err := database.Memories.Add("  prefer small Go files  ", "[]", "", nil)
	if err != nil {
		t.Fatalf("second Add (whitespace variant): %v", err)
	}
	if m1.ID != m2.ID {
		t.Errorf("dedup: want same ID for whitespace-only diff (got %d and %d)", m1.ID, m2.ID)
	}
}

// TestMemoryContentHashDedup_OutsideWindow verifies that content inserted more
// than 7 days apart creates a new row (outside the dedup window).
func TestMemoryContentHashDedup_OutsideWindow(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, err := database.Memories.Add("prefer small Go files", "[]", "", nil)
	if err != nil {
		t.Fatalf("first Add: %v", err)
	}
	// Backdate the first memory to 8 days ago so the next insert falls outside the 7-day window.
	_, err = database.SQL().Exec(
		`UPDATE memories SET created_at = unixepoch() - 8*24*3600 WHERE id = ?`, m1.ID)
	if err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}

	m2, err := database.Memories.Add("prefer small Go files", "[]", "", nil)
	if err != nil {
		t.Fatalf("second Add (outside window): %v", err)
	}
	if m1.ID == m2.ID {
		t.Errorf("dedup: want NEW ID after 8-day gap, got same ID %d", m1.ID)
	}
}

// TestMemoryUnrated verifies Unrated returns only importance=0.5 memories
// (the unrated sentinel). Memories that have been LLM-rated to a score in [0.2, 1.0]
// no longer match the sentinel and are excluded.
func TestMemoryUnrated(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, err := database.Memories.Add("unrated memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add unrated: %v", err)
	}
	m2, err := database.Memories.Add("rated memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add rated: %v", err)
	}
	if err := database.Memories.SetImportance(m2.ID, 0.6); err != nil {
		t.Fatalf("SetImportance: %v", err)
	}

	list, err := database.Memories.Unrated(10)
	if err != nil {
		t.Fatalf("Unrated: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unrated: want 1, got %d", len(list))
	}
	if list[0].ID != m1.ID {
		t.Errorf("Unrated: want id %d, got %d", m1.ID, list[0].ID)
	}
}

// TestMemoryUnrated_ExcludesDeleted verifies Unrated skips soft-deleted memories.
func TestMemoryUnrated_ExcludesDeleted(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("deleted unrated memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := database.Memories.Delete(m.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	list, err := database.Memories.Unrated(10)
	if err != nil {
		t.Fatalf("Unrated: %v", err)
	}
	for _, row := range list {
		if row.ID == m.ID {
			t.Errorf("Unrated: deleted memory %d should not appear", m.ID)
		}
	}
}

// TestMemoryUnrated_Limit verifies the limit parameter is respected.
func TestMemoryUnrated_Limit(t *testing.T) {
	database := testdb.OpenTestDB(t)

	for i := 0; i < 5; i++ {
		if _, err := database.Memories.Add("unrated content", "[]", "model", []float32{float32(i), 0}); err != nil {
			// dedup kicks in for identical nil embeddings; use distinct embeddings
			t.Logf("Add[%d] skipped (dedup): %v", i, err)
		}
	}
	// Add enough distinct memories
	for i := 0; i < 5; i++ {
		if _, err := database.Memories.Add(
			"unrated content number "+string(rune('a'+i)),
			"[]", "", nil,
		); err != nil {
			t.Fatalf("Add[%d]: %v", i, err)
		}
	}

	list, err := database.Memories.Unrated(3)
	if err != nil {
		t.Fatalf("Unrated(3): %v", err)
	}
	if len(list) > 3 {
		t.Errorf("Unrated(3): want at most 3, got %d", len(list))
	}
}

// TestMemoryWeightDefaults verifies that new memories start with weight=0.5
// and last_retrieved_at=nil, matching the v066 migration defaults.
func TestMemoryWeightDefaults(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("test content", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Weight != 0.5 {
		t.Errorf("initial weight: want 0.5, got %v", got.Weight)
	}
	if got.LastRetrievedAt != nil {
		t.Errorf("initial last_retrieved_at: want nil, got %v", got.LastRetrievedAt)
	}
}

// TestBumpRetrieval verifies that BumpRetrieval increments weight by 0.001
// and sets last_retrieved_at.
func TestBumpRetrieval(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("bump test content", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := database.Memories.BumpRetrieval([]int64{m.ID}); err != nil {
		t.Fatalf("BumpRetrieval: %v", err)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after bump: %v", err)
	}

	const want = 0.5 + 0.001
	const epsilon = 1e-9
	if diff := got.Weight - want; diff < -epsilon || diff > epsilon {
		t.Errorf("weight after bump: want ~%v, got %v", want, got.Weight)
	}
	if got.LastRetrievedAt == nil {
		t.Error("last_retrieved_at: want non-nil after bump, got nil")
	}
}

// TestBumpRetrievalMultiple verifies that bumping accumulates correctly
// across multiple calls.
func TestBumpRetrievalMultiple(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("multi-bump content", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := database.Memories.BumpRetrieval([]int64{m.ID}); err != nil {
			t.Fatalf("BumpRetrieval[%d]: %v", i, err)
		}
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after bumps: %v", err)
	}

	const want = 0.5 + 3*0.001
	const epsilon = 1e-9
	if diff := got.Weight - want; diff < -epsilon || diff > epsilon {
		t.Errorf("weight after 3 bumps: want ~%v, got %v", want, got.Weight)
	}
}

// TestBumpRetrievalEmpty verifies that calling BumpRetrieval with an empty
// slice is a no-op (no error, no crash).
func TestBumpRetrievalEmpty(t *testing.T) {
	database := testdb.OpenTestDB(t)
	if err := database.Memories.BumpRetrieval(nil); err != nil {
		t.Errorf("BumpRetrieval(nil): want no error, got %v", err)
	}
	if err := database.Memories.BumpRetrieval([]int64{}); err != nil {
		t.Errorf("BumpRetrieval([]): want no error, got %v", err)
	}
}

// TestRunNightlyDecay_DecaysOldMemories verifies that a memory not retrieved
// in the past 24h has its weight decremented by 0.001.
func TestRunNightlyDecay_DecaysOldMemories(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("old memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Stamp last_retrieved_at well in the past (100000s ago).
	_, err = database.SQL().Exec(
		`UPDATE memories SET last_retrieved_at = unixepoch() - 100000 WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("stamp last_retrieved_at: %v", err)
	}

	decayed, dumped, _, err := database.Memories.RunNightlyDecay()
	if err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}
	if decayed < 1 {
		t.Errorf("decayed: want >= 1, got %d", decayed)
	}
	if dumped != 0 {
		t.Errorf("dumped: want 0, got %d", dumped)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	const want = 0.5 - 0.001
	const epsilon = 1e-9
	if diff := got.Weight - want; diff < -epsilon || diff > epsilon {
		t.Errorf("weight after decay: want ~%v, got %v", want, got.Weight)
	}
}

// TestRunNightlyDecay_SkipsRecentlyRetrieved verifies that a memory retrieved
// within the past 24h is not decayed.
func TestRunNightlyDecay_SkipsRecentlyRetrieved(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("recent memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Stamp last_retrieved_at to now (within the 24h window).
	_, err = database.SQL().Exec(
		`UPDATE memories SET last_retrieved_at = unixepoch() WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("stamp last_retrieved_at: %v", err)
	}

	if _, _, _, err := database.Memories.RunNightlyDecay(); err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	const want = 0.5
	const epsilon = 1e-9
	if diff := got.Weight - want; diff < -epsilon || diff > epsilon {
		t.Errorf("weight after decay (recently retrieved): want ~%v, got %v", want, got.Weight)
	}
}

// TestRunNightlyDecay_AutoDumpsAtZero verifies that a memory whose weight
// decays to 0 is soft-deleted and excluded from subsequent queries.
func TestRunNightlyDecay_AutoDumpsAtZero(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("near-zero memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Set weight to 0.0005 — will decay below 0 and be clamped to 0.0, then dumped.
	_, err = database.SQL().Exec(
		`UPDATE memories SET weight = 0.0005, last_retrieved_at = unixepoch() - 100000 WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("set low weight: %v", err)
	}

	_, dumped, _, err := database.Memories.RunNightlyDecay()
	if err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}
	if dumped < 1 {
		t.Errorf("dumped: want >= 1, got %d", dumped)
	}

	// Get should return nil (deleted_at is set, filtered by Get's WHERE clause).
	got, err := database.Memories.Get(m.ID)
	if err == nil && got != nil {
		t.Error("Get: expected nil memory after auto-dump, got a result")
	}
}

// TestRunNightlyDecay_AutoPromotesHotMemories verifies that a memory with
// weight >= 1.0 has its importance set to 1.0 after RunNightlyDecay.
// Auto-promote is the one-way bridge from weight to importance — the retrieval
// scoring formula (cosine * decayImportance(importance)) is not changed.
func TestRunNightlyDecay_AutoPromotesHotMemories(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("hot memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Set weight to 1.0 and importance to 0.5 (below the promotion threshold).
	// Stamp last_retrieved_at to now so the decay pass doesn't drop weight below 1.0
	// before the promote pass runs.
	_, err = database.SQL().Exec(
		`UPDATE memories SET weight = 1.0, importance = 0.5, last_retrieved_at = unixepoch() WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("set weight/importance: %v", err)
	}

	_, _, promoted, err := database.Memories.RunNightlyDecay()
	if err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}
	if promoted < 1 {
		t.Errorf("promoted: want >= 1, got %d", promoted)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Importance != 1.0 {
		t.Errorf("importance after auto-promote: want 1.0, got %v", got.Importance)
	}
}

// TestRunNightlyDecay_NoDoublePromote verifies that a memory already at
// importance=1.0 is not re-promoted (WHERE importance < 1.0 clamp).
func TestRunNightlyDecay_NoDoublePromote(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("already promoted", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	_, err = database.SQL().Exec(
		`UPDATE memories SET weight = 1.5, importance = 1.0 WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("set weight/importance: %v", err)
	}

	_, _, promoted, err := database.Memories.RunNightlyDecay()
	if err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}
	if promoted != 0 {
		t.Errorf("promoted: want 0 (already at 1.0), got %d", promoted)
	}
}

// TestMigrationV066Columns verifies that the weight and last_retrieved_at
// columns exist with correct defaults after migration.
func TestMigrationV066Columns(t *testing.T) {
	database := testdb.OpenTestDB(t)

	// Insert a row via raw SQL simulating a pre-migration row (no weight/last_retrieved_at).
	// After migration these columns exist with defaults, so even old rows have them.
	_, err := database.SQL().Exec(`INSERT INTO memories(content, tags) VALUES('pre-migration row', '[]')`)
	if err != nil {
		t.Fatalf("raw insert: %v", err)
	}

	var weight float64
	var lastRetrievedAt *int64
	err = database.SQL().QueryRow(
		`SELECT weight, last_retrieved_at FROM memories WHERE content = 'pre-migration row'`,
	).Scan(&weight, &lastRetrievedAt)
	if err != nil {
		t.Fatalf("select new columns: %v", err)
	}
	if weight != 0.5 {
		t.Errorf("default weight: want 0.5, got %v", weight)
	}
	if lastRetrievedAt != nil {
		t.Errorf("default last_retrieved_at: want nil, got %v", lastRetrievedAt)
	}
}

// --- Phase 2: lifecycle column tests ---

// TestMemoryPinUnpin_RoundTrip verifies Pin sets pinned_at and Unpin clears it.
func TestMemoryPinUnpin_RoundTrip(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("pinnable memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := database.Memories.Pin(m.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after pin: %v", err)
	}
	if got.PinnedAt == nil {
		t.Fatal("PinnedAt: want non-nil after Pin, got nil")
	}

	if err := database.Memories.Unpin(m.ID); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	got, err = database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after unpin: %v", err)
	}
	if got.PinnedAt != nil {
		t.Errorf("PinnedAt: want nil after Unpin, got %v", got.PinnedAt)
	}
}

// TestMemoryListPinned verifies ListPinned returns only pinned memories.
func TestMemoryListPinned(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, _ := database.Memories.Add("memory one", "[]", "", nil)
	m2, _ := database.Memories.Add("memory two", "[]", "", nil)
	m3, _ := database.Memories.Add("memory three", "[]", "", nil)

	if err := database.Memories.Pin(m1.ID); err != nil {
		t.Fatalf("Pin m1: %v", err)
	}
	if err := database.Memories.Pin(m2.ID); err != nil {
		t.Fatalf("Pin m2: %v", err)
	}

	pinned, err := database.Memories.ListPinned()
	if err != nil {
		t.Fatalf("ListPinned: %v", err)
	}
	if len(pinned) != 2 {
		t.Fatalf("ListPinned: want 2, got %d", len(pinned))
	}
	for _, p := range pinned {
		if p.ID == m3.ID {
			t.Errorf("ListPinned: unpinned memory %d should not appear", m3.ID)
		}
	}
}

// TestMemoryArchivedAt_SetAndClear verifies SetArchivedAt sets and clears archived_at.
func TestMemoryArchivedAt_SetAndClear(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("archivable memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	now := time.Now().Truncate(time.Second)
	if err := database.Memories.SetArchivedAt(m.ID, &now); err != nil {
		t.Fatalf("SetArchivedAt(set): %v", err)
	}
	// Get filters archived rows out by contract — read archived_at via raw SQL.
	var archivedAt sql.NullString
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, m.ID).Scan(&archivedAt); err != nil {
		t.Fatalf("read archived_at after set: %v", err)
	}
	if !archivedAt.Valid {
		t.Fatal("ArchivedAt: want non-nil after set, got NULL")
	}

	if err := database.Memories.SetArchivedAt(m.ID, nil); err != nil {
		t.Fatalf("SetArchivedAt(clear): %v", err)
	}
	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if got.ArchivedAt != nil {
		t.Errorf("ArchivedAt: want nil after clear, got %v", got.ArchivedAt)
	}
}

// TestMemoryUnreviewed verifies Unreviewed returns only memories with reviewed_at IS NULL.
func TestMemoryUnreviewed(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, _ := database.Memories.Add("unreviewed memory", "[]", "", nil)
	m2, _ := database.Memories.Add("reviewed memory", "[]", "", nil)

	// Stamp reviewed_at on m2 via raw SQL.
	_, err := database.SQL().Exec(`UPDATE memories SET reviewed_at = datetime('now') WHERE id = ?`, m2.ID)
	if err != nil {
		t.Fatalf("stamp reviewed_at: %v", err)
	}

	list, err := database.Memories.Unreviewed()
	if err != nil {
		t.Fatalf("Unreviewed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unreviewed: want 1, got %d", len(list))
	}
	if list[0].ID != m1.ID {
		t.Errorf("Unreviewed: want id %d, got %d", m1.ID, list[0].ID)
	}
}

// TestMemoryUnreviewed_ExcludesArchived verifies Unreviewed skips memories that
// were archived by the dream-pass curator (archived_at IS NOT NULL).
func TestMemoryUnreviewed_ExcludesArchived(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, _ := database.Memories.Add("not archived", "[]", "", nil)
	m2, _ := database.Memories.Add("archived", "[]", "", nil)

	now := time.Now()
	if err := database.Memories.SetArchivedAt(m2.ID, &now); err != nil {
		t.Fatalf("SetArchivedAt: %v", err)
	}

	list, err := database.Memories.Unreviewed()
	if err != nil {
		t.Fatalf("Unreviewed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unreviewed: want 1, got %d", len(list))
	}
	if list[0].ID != m1.ID {
		t.Errorf("Unreviewed: want id %d, got %d", m1.ID, list[0].ID)
	}
}

// TestMemoryUnreviewedWithEmbeddings verifies the curator-flavored query
// returns the embedding BLOB (which Unreviewed elides).
func TestMemoryUnreviewedWithEmbeddings(t *testing.T) {
	database := testdb.OpenTestDB(t)

	emb := []float32{1.0, 0.0, 0.0}
	m, err := database.Memories.Add("with embedding", "[]", "test-model", emb)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	list, err := database.Memories.UnreviewedWithEmbeddings()
	if err != nil {
		t.Fatalf("UnreviewedWithEmbeddings: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("UnreviewedWithEmbeddings: want 1, got %d", len(list))
	}
	if list[0].ID != m.ID {
		t.Errorf("UnreviewedWithEmbeddings: want id %d, got %d", m.ID, list[0].ID)
	}
	if len(list[0].Embedding) != 3 {
		t.Fatalf("Embedding: want len 3, got %d", len(list[0].Embedding))
	}
	if list[0].Embedding[0] != 1.0 || list[0].Embedding[1] != 0.0 || list[0].Embedding[2] != 0.0 {
		t.Errorf("Embedding: want [1,0,0], got %v", list[0].Embedding)
	}
}

// TestMemoryMarkReviewed verifies MarkReviewed stamps reviewed_at on the given IDs.
func TestMemoryMarkReviewed(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m1, _ := database.Memories.Add("memory a", "[]", "", nil)
	m2, _ := database.Memories.Add("memory b", "[]", "", nil)
	m3, _ := database.Memories.Add("memory c", "[]", "", nil)

	if err := database.Memories.MarkReviewed([]int64{m1.ID, m2.ID}); err != nil {
		t.Fatalf("MarkReviewed: %v", err)
	}

	list, err := database.Memories.Unreviewed()
	if err != nil {
		t.Fatalf("Unreviewed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("Unreviewed after MarkReviewed: want 1, got %d", len(list))
	}
	if list[0].ID != m3.ID {
		t.Errorf("Unreviewed: want id %d (unreviewed), got %d", m3.ID, list[0].ID)
	}
}

// TestMemoryDeleteArchived_GracePeriod verifies DeleteArchived hard-deletes
// memories archived more than 1 day ago.
func TestMemoryDeleteArchived_GracePeriod(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("old archived memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Set archived_at to 2 days ago — beyond the 1-day grace period.
	_, err = database.SQL().Exec(
		`UPDATE memories SET archived_at = datetime('now', '-2 days') WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("stamp archived_at: %v", err)
	}

	count, err := database.Memories.DeleteArchived()
	if err != nil {
		t.Fatalf("DeleteArchived: %v", err)
	}
	if count != 1 {
		t.Errorf("DeleteArchived count: want 1, got %d", count)
	}

	got, err := database.Memories.Get(m.ID)
	if err == nil && got != nil {
		t.Error("Get: expected nil after hard-delete, got a result")
	}
}

// TestMemoryDeleteArchived_SparesFresh verifies DeleteArchived does not delete
// memories archived less than 1 day ago.
func TestMemoryDeleteArchived_SparesFresh(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("freshly archived memory", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Set archived_at to just now — within the 1-day grace period.
	_, err = database.SQL().Exec(
		`UPDATE memories SET archived_at = datetime('now') WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("stamp archived_at: %v", err)
	}

	count, err := database.Memories.DeleteArchived()
	if err != nil {
		t.Fatalf("DeleteArchived: %v", err)
	}
	if count != 0 {
		t.Errorf("DeleteArchived count: want 0 (within grace period), got %d", count)
	}

	// Get filters archived rows out by contract — verify presence via raw SQL.
	var foundID int64
	err = database.SQL().QueryRow(`SELECT id FROM memories WHERE id = ?`, m.ID).Scan(&foundID)
	if err != nil {
		t.Errorf("memory should still exist within grace period (not hard-deleted), got err=%v", err)
	}
}

// TestAutoDump_SkipsPinnedMemory is the highest-stakes Phase 2 test.
// It verifies that RunNightlyDecay does NOT soft-delete a pinned memory even
// when weight and last_retrieved_at would otherwise trigger auto-dump.
// This is the sole guard protecting [P] memories from lifecycle eviction.
func TestAutoDump_SkipsPinnedMemory(t *testing.T) {
	database := testdb.OpenTestDB(t)

	m, err := database.Memories.Add("pinned memory to spare", "[]", "", nil)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := database.Memories.Pin(m.ID); err != nil {
		t.Fatalf("Pin: %v", err)
	}

	// Drive weight to 0 and last_retrieved_at far in the past so the auto-dump
	// predicate would fire for any unpinned memory.
	_, err = database.SQL().Exec(
		`UPDATE memories SET weight = 0.0, last_retrieved_at = unixepoch() - 100000 WHERE id = ?`, m.ID)
	if err != nil {
		t.Fatalf("set weight/last_retrieved_at: %v", err)
	}

	_, dumped, _, err := database.Memories.RunNightlyDecay()
	if err != nil {
		t.Fatalf("RunNightlyDecay: %v", err)
	}
	if dumped != 0 {
		t.Errorf("dumped: want 0 (pinned memory must be spared), got %d", dumped)
	}

	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Get after RunNightlyDecay: %v", err)
	}
	if got == nil {
		t.Fatal("pinned memory was auto-dumped — [P] safety guarantee violated")
	}
}

// TestMigration_NewColumnsExist verifies that the Phase 2 lifecycle columns
// exist on memories, facts, summaries, and messages after migration.
func TestMigration_NewColumnsExist(t *testing.T) {
	database := testdb.OpenTestDB(t)

	tables := []struct {
		name    string
		columns []string
	}{
		{"memories", []string{"pinned_at", "archived_at", "reviewed_at"}},
		{"facts", []string{"archived_at", "reviewed_at"}},
		{"summaries", []string{"reviewed_at"}},
		{"messages", []string{"reviewed_at"}},
	}

	for _, tc := range tables {
		rows, err := database.SQL().Query(`PRAGMA table_info(` + tc.name + `)`)
		if err != nil {
			t.Fatalf("PRAGMA table_info(%s): %v", tc.name, err)
		}
		found := make(map[string]bool)
		for rows.Next() {
			var cid int
			var name, colType string
			var notNull int
			var dflt, pk interface{}
			if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				rows.Close()
				t.Fatalf("scan table_info(%s): %v", tc.name, err)
			}
			found[name] = true
		}
		rows.Close()
		for _, col := range tc.columns {
			if !found[col] {
				t.Errorf("table %s: missing column %q", tc.name, col)
			}
		}
	}
}
