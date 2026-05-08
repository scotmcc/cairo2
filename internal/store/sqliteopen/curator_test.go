package sqliteopen_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/scotmcc/cairo2/internal/store/memory"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
	testdb "github.com/scotmcc/cairo2/internal/store/testing"
)

// vecA, vecB are 2D unit-ish vectors with cosine ≈ 0.995 (above 0.92 threshold).
// vecC is orthogonal to vecA: cosine(vecA, vecC) = 0.0.
var (
	vecA = []float32{1.0, 0.0}
	vecB = []float32{0.99503719, 0.09950372}
	vecC = []float32{0.0, 1.0}
)

func addTestFactWithEmbedding(t *testing.T, database *sqliteopen.DB, importance float64, embedding []float32) *memory.Fact {
	t.Helper()
	sess, err := database.Sessions.Create("test", "/tmp", "thinking_partner")
	if err != nil {
		t.Fatalf("Sessions.Create: %v", err)
	}
	res, err := database.SQL().Exec(
		`INSERT INTO facts(session_id, summary_id, content, embed_model, importance, embedding) VALUES(?, NULL, 'test fact', 'testmodel', ?, ?)`,
		sess.ID, importance, memory.EncodeEmbedding(embedding))
	if err != nil {
		t.Fatalf("insert fact with embedding: %v", err)
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

func addTestMemoryWithEmbedding(t *testing.T, database *sqliteopen.DB, importance float64, embedding []float32, pinned bool) *memory.Memory {
	t.Helper()
	m, err := database.Memories.Add("test memory", "[]", "testmodel", embedding)
	if err != nil {
		t.Fatalf("Memories.Add: %v", err)
	}
	if err := database.Memories.SetImportance(m.ID, importance); err != nil {
		t.Fatalf("SetImportance: %v", err)
	}
	if pinned {
		if err := database.Memories.Pin(m.ID); err != nil {
			t.Fatalf("Pin: %v", err)
		}
	}
	got, err := database.Memories.Get(m.ID)
	if err != nil {
		t.Fatalf("Memories.Get: %v", err)
	}
	return got
}

func addTestDreamID(t *testing.T, database *sqliteopen.DB) int64 {
	t.Helper()
	id, err := database.Dreams.Add("2026-05-03", "/tmp/test.md", "", "", nil)
	if err != nil {
		t.Fatalf("Dreams.Add: %v", err)
	}
	return id
}

func TestMergeMemoryDecision_BelowThreshold(t *testing.T) {
	now := time.Now()
	a := &memory.Memory{ID: 1, Importance: 0.8, PinnedAt: &now}
	b := &memory.Memory{ID: 2, Importance: 0.6}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.90, 0.92)
	if outcome != memory.MergeOutcomeBelowThreshold {
		t.Errorf("outcome: want MergeOutcomeBelowThreshold, got %d", outcome)
	}
	if winner != nil || loser != nil {
		t.Errorf("want nil winner/loser below threshold")
	}
}

func TestMergeMemoryDecision_NeitherPinned_AHigherImportance(t *testing.T) {
	a := &memory.Memory{ID: 1, Importance: 0.8}
	b := &memory.Memory{ID: 2, Importance: 0.6}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != a {
		t.Errorf("winner: want a (higher importance)")
	}
	if loser != b {
		t.Errorf("loser: want b")
	}
}

func TestMergeMemoryDecision_NeitherPinned_BHigherImportance(t *testing.T) {
	a := &memory.Memory{ID: 1, Importance: 0.6}
	b := &memory.Memory{ID: 2, Importance: 0.8}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != b {
		t.Errorf("winner: want b (higher importance)")
	}
	if loser != a {
		t.Errorf("loser: want a")
	}
}

func TestMergeMemoryDecision_NeitherPinned_TieBreakByID(t *testing.T) {
	a := &memory.Memory{ID: 10, Importance: 0.7}
	b := &memory.Memory{ID: 5, Importance: 0.7}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != b {
		t.Errorf("winner: want b (ID=5, lower/older)")
	}
	if loser != a {
		t.Errorf("loser: want a (ID=10)")
	}
}

func TestMergeMemoryDecision_PinnedWins(t *testing.T) {
	now := time.Now()
	a := &memory.Memory{ID: 1, Importance: 0.6, PinnedAt: &now}
	b := &memory.Memory{ID: 2, Importance: 0.9}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != a {
		t.Errorf("winner: want a (pinned)")
	}
	if loser != b {
		t.Errorf("loser: want b (unpinned)")
	}
}

func TestMergeMemoryDecision_BothPinned(t *testing.T) {
	now := time.Now()
	a := &memory.Memory{ID: 1, Importance: 0.7, PinnedAt: &now}
	b := &memory.Memory{ID: 2, Importance: 0.8, PinnedAt: &now}

	winner, loser, outcome := memory.MergeMemoryDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeSkip {
		t.Errorf("outcome: want MergeOutcomeSkip, got %d", outcome)
	}
	if winner == nil || loser == nil {
		t.Errorf("both-pinned: want non-nil winner and loser")
	}
}

func TestMergeFactDecision_BelowThreshold(t *testing.T) {
	a := &memory.Fact{ID: 1, Importance: 0.9}
	b := &memory.Fact{ID: 2, Importance: 0.7}

	winner, loser, outcome := memory.MergeFactDecision(a, b, 0.80, 0.92)
	if outcome != memory.MergeOutcomeBelowThreshold {
		t.Errorf("outcome: want MergeOutcomeBelowThreshold, got %d", outcome)
	}
	if winner != nil || loser != nil {
		t.Errorf("want nil winner/loser below threshold")
	}
}

func TestMergeFactDecision_HigherImportanceWins(t *testing.T) {
	a := &memory.Fact{ID: 1, Importance: 0.9}
	b := &memory.Fact{ID: 2, Importance: 0.7}

	winner, loser, outcome := memory.MergeFactDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != a {
		t.Errorf("winner: want a (higher importance)")
	}
	if loser != b {
		t.Errorf("loser: want b")
	}
}

func TestMergeFactDecision_TieBreakByID(t *testing.T) {
	a := &memory.Fact{ID: 20, Importance: 0.5}
	b := &memory.Fact{ID: 3, Importance: 0.5}

	winner, loser, outcome := memory.MergeFactDecision(a, b, 0.95, 0.92)
	if outcome != memory.MergeOutcomeArchive {
		t.Errorf("outcome: want MergeOutcomeArchive, got %d", outcome)
	}
	if winner != b {
		t.Errorf("winner: want b (ID=3, lower/older)")
	}
	if loser != a {
		t.Errorf("loser: want a (ID=20)")
	}
}

func TestCuratorMemories_StandardMerge(t *testing.T) {
	database := testdb.OpenTestDB(t)
	dreamID := addTestDreamID(t, database)

	mA := addTestMemoryWithEmbedding(t, database, 0.8, vecA, false)
	mB := addTestMemoryWithEmbedding(t, database, 0.6, vecB, false)
	mC := addTestMemoryWithEmbedding(t, database, 0.5, vecC, false)

	if err := memory.CurateMemories(database.Memories, database, dreamID, 0.92); err != nil {
		t.Fatalf("CurateMemories: %v", err)
	}

	gotA, err := database.Memories.Get(mA.ID)
	if err != nil {
		t.Fatalf("Get(A): %v", err)
	}
	if gotA == nil {
		t.Fatal("A was deleted; expected survival")
	}
	if gotA.ArchivedAt != nil {
		t.Errorf("A should not be archived; ArchivedAt=%v", gotA.ArchivedAt)
	}

	var bArchiveStr *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, mB.ID).Scan(&bArchiveStr); err != nil {
		t.Fatalf("query B archived_at: %v", err)
	}
	if bArchiveStr == nil {
		t.Errorf("B: expected archived_at to be set, got NULL")
	}

	gotC, err := database.Memories.Get(mC.ID)
	if err != nil {
		t.Fatalf("Get(C): %v", err)
	}
	if gotC == nil {
		t.Fatal("C was deleted; expected survival")
	}
	if gotC.ArchivedAt != nil {
		t.Errorf("C should not be archived; ArchivedAt=%v", gotC.ArchivedAt)
	}

	entries, err := database.DreamLog.List(dreamID)
	if err != nil {
		t.Fatalf("DreamLog.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dream_log: want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != "merge_memories" {
		t.Errorf("action: want merge_memories, got %q", e.Action)
	}
	expectedTargetIDs := fmt.Sprintf("[%d]", mB.ID)
	if e.TargetIDs != expectedTargetIDs {
		t.Errorf("target_ids: want %q, got %q", expectedTargetIDs, e.TargetIDs)
	}
	reversalSnippet := "archived_at = NULL WHERE id ="
	if !strings.Contains(e.Note, reversalSnippet) {
		t.Errorf("note %q should contain reversal SQL snippet %q", e.Note, reversalSnippet)
	}

	_, err = database.SQL().Exec(
		`UPDATE memories SET archived_at = datetime('now', '-2 days') WHERE id = ?`, mB.ID)
	if err != nil {
		t.Fatalf("backdate archived_at: %v", err)
	}
	n, err := database.Memories.DeleteArchived()
	if err != nil {
		t.Fatalf("DeleteArchived: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteArchived: want 1, got %d", n)
	}

	var count int
	if err := database.SQL().QueryRow(`SELECT COUNT(*) FROM memories WHERE id = ?`, mB.ID).Scan(&count); err != nil {
		t.Fatalf("count B: %v", err)
	}
	if count != 0 {
		t.Errorf("B should be hard-deleted; got count=%d", count)
	}

	gotA2, err := database.Memories.Get(mA.ID)
	if err != nil || gotA2 == nil {
		t.Errorf("A should still exist after DeleteArchived")
	}
}

func TestCuratorMemories_PinnedSurvives(t *testing.T) {
	database := testdb.OpenTestDB(t)
	dreamID := addTestDreamID(t, database)

	mD := addTestMemoryWithEmbedding(t, database, 0.6, vecA, true)
	mE := addTestMemoryWithEmbedding(t, database, 0.9, vecB, false)

	if err := memory.CurateMemories(database.Memories, database, dreamID, 0.92); err != nil {
		t.Fatalf("CurateMemories: %v", err)
	}

	var dArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, mD.ID).Scan(&dArchived); err != nil {
		t.Fatalf("query D: %v", err)
	}
	if dArchived != nil {
		t.Errorf("D (pinned) should not be archived; archived_at=%v", *dArchived)
	}

	var eArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, mE.ID).Scan(&eArchived); err != nil {
		t.Fatalf("query E: %v", err)
	}
	if eArchived == nil {
		t.Errorf("E (unpinned, high-importance) should be archived as loser")
	}

	entries, err := database.DreamLog.List(dreamID)
	if err != nil {
		t.Fatalf("DreamLog.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dream_log: want 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "merge_memories" {
		t.Errorf("action: want merge_memories, got %q", entries[0].Action)
	}
	winnerRef := "merged into memory"
	if !strings.Contains(entries[0].Note, winnerRef) {
		t.Errorf("note %q should contain %q", entries[0].Note, winnerRef)
	}
}

func TestCuratorMemories_BothPinnedConflict(t *testing.T) {
	database := testdb.OpenTestDB(t)
	dreamID := addTestDreamID(t, database)

	mF := addTestMemoryWithEmbedding(t, database, 0.7, vecA, true)
	mG := addTestMemoryWithEmbedding(t, database, 0.8, vecB, true)

	if err := memory.CurateMemories(database.Memories, database, dreamID, 0.92); err != nil {
		t.Fatalf("CurateMemories: %v", err)
	}

	var fArchived, gArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, mF.ID).Scan(&fArchived); err != nil {
		t.Fatalf("query F: %v", err)
	}
	if err := database.SQL().QueryRow(`SELECT archived_at FROM memories WHERE id = ?`, mG.ID).Scan(&gArchived); err != nil {
		t.Fatalf("query G: %v", err)
	}
	if fArchived != nil {
		t.Errorf("F should not be archived (both pinned)")
	}
	if gArchived != nil {
		t.Errorf("G should not be archived (both pinned)")
	}

	entries, err := database.DreamLog.List(dreamID)
	if err != nil {
		t.Fatalf("DreamLog.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dream_log: want 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Action != "merge_conflict_both_pinned" {
		t.Errorf("action: want merge_conflict_both_pinned, got %q", e.Action)
	}
	if !strings.Contains(e.TargetIDs, "[") {
		t.Errorf("target_ids %q should be JSON array", e.TargetIDs)
	}
}

func TestCuratorFacts_StandardMerge(t *testing.T) {
	database := testdb.OpenTestDB(t)
	dreamID := addTestDreamID(t, database)

	fA := addTestFactWithEmbedding(t, database, 0.8, vecA)
	fB := addTestFactWithEmbedding(t, database, 0.6, vecB)
	fC := addTestFactWithEmbedding(t, database, 0.5, vecC)

	if err := memory.CurateFacts(database.Facts, database, dreamID, 0.92); err != nil {
		t.Fatalf("CurateFacts: %v", err)
	}

	var aArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, fA.ID).Scan(&aArchived); err != nil {
		t.Fatalf("query fA: %v", err)
	}
	if aArchived != nil {
		t.Errorf("fA should not be archived")
	}

	var bArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, fB.ID).Scan(&bArchived); err != nil {
		t.Fatalf("query fB: %v", err)
	}
	if bArchived == nil {
		t.Errorf("fB should be archived (loser)")
	}

	var cArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, fC.ID).Scan(&cArchived); err != nil {
		t.Fatalf("query fC: %v", err)
	}
	if cArchived != nil {
		t.Errorf("fC should not be archived (below threshold)")
	}

	entries, err := database.DreamLog.List(dreamID)
	if err != nil {
		t.Fatalf("DreamLog.List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("dream_log: want 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "merge_facts" {
		t.Errorf("action: want merge_facts, got %q", entries[0].Action)
	}
}

func TestCuratorFacts_HigherImportanceWins(t *testing.T) {
	database := testdb.OpenTestDB(t)
	dreamID := addTestDreamID(t, database)

	fLow := addTestFactWithEmbedding(t, database, 0.4, vecA)
	fHigh := addTestFactWithEmbedding(t, database, 0.9, vecB)

	if err := memory.CurateFacts(database.Facts, database, dreamID, 0.92); err != nil {
		t.Fatalf("CurateFacts: %v", err)
	}

	var highArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, fHigh.ID).Scan(&highArchived); err != nil {
		t.Fatalf("query fHigh: %v", err)
	}
	if highArchived != nil {
		t.Errorf("fHigh should not be archived (higher importance wins)")
	}

	var lowArchived *string
	if err := database.SQL().QueryRow(`SELECT archived_at FROM facts WHERE id = ?`, fLow.ID).Scan(&lowArchived); err != nil {
		t.Fatalf("query fLow: %v", err)
	}
	if lowArchived == nil {
		t.Errorf("fLow should be archived (lower importance)")
	}
}
