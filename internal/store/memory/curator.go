package memory

import (
	"database/sql"
	"fmt"
	"os"
	"time"
)

// MergeOutcome describes the result of a merge-decision call.
type MergeOutcome int

const (
	MergeOutcomeArchive        MergeOutcome = iota // loser should be archived
	MergeOutcomeSkip                               // both pinned; log conflict, do nothing
	MergeOutcomeBelowThreshold                     // similarity < threshold; no-op, no log
)

// MemoryCandidatePair is a pair of memories whose cosine similarity meets or
// exceeds the curator threshold.
type MemoryCandidatePair struct {
	A, B *Memory
	Sim  float32
}

// FactCandidatePair is a pair of facts whose cosine similarity meets or exceeds
// the curator threshold.
type FactCandidatePair struct {
	A, B *Fact
	Sim  float32
}

// MergeMemoryDecision determines which memory survives when two unreviewed
// memories exceed the similarity threshold. Returns winner, loser, and outcome.
// Both inputs must be non-nil. Threshold is the minimum cosine similarity to
// trigger a merge.
//
// Rules:
//   - Similarity < threshold → MergeOutcomeBelowThreshold (winner/loser are nil)
//   - Both pinned → MergeOutcomeSkip (winner/loser are both inputs, no archive)
//   - Exactly one pinned → pinned is winner, unpinned is loser → MergeOutcomeArchive
//   - Neither pinned → higher Importance wins; tie → lower ID (older) wins → MergeOutcomeArchive
func MergeMemoryDecision(a, b *Memory, sim float32, threshold float64) (winner, loser *Memory, outcome MergeOutcome) {
	if float64(sim) < threshold {
		return nil, nil, MergeOutcomeBelowThreshold
	}
	aPinned := a.PinnedAt != nil
	bPinned := b.PinnedAt != nil
	if aPinned && bPinned {
		return a, b, MergeOutcomeSkip
	}
	if aPinned {
		return a, b, MergeOutcomeArchive
	}
	if bPinned {
		return b, a, MergeOutcomeArchive
	}
	// Neither pinned: higher importance wins; tie → lower ID (older) wins.
	if a.Importance >= b.Importance {
		if a.Importance == b.Importance && b.ID < a.ID {
			return b, a, MergeOutcomeArchive
		}
		return a, b, MergeOutcomeArchive
	}
	return b, a, MergeOutcomeArchive
}

// MergeFactDecision determines which fact survives. Facts have no pinned_at
// field, so pinning logic does not apply.
//
// Rules:
//   - Similarity < threshold → MergeOutcomeBelowThreshold
//   - Higher Importance wins; tie → lower ID (older) wins → MergeOutcomeArchive
func MergeFactDecision(a, b *Fact, sim float32, threshold float64) (winner, loser *Fact, outcome MergeOutcome) {
	if float64(sim) < threshold {
		return nil, nil, MergeOutcomeBelowThreshold
	}
	if a.Importance >= b.Importance {
		if a.Importance == b.Importance && b.ID < a.ID {
			return b, a, MergeOutcomeArchive
		}
		return a, b, MergeOutcomeArchive
	}
	return b, a, MergeOutcomeArchive
}

// MemoryMergeCandidates performs an O(n²) pairwise cosine scan over the input
// slice and returns all pairs whose cosine similarity meets or exceeds threshold.
// Skips entries with nil or zero-length embeddings. Skips pairs where EmbedModel
// differs.
//
// The caller is responsible for capping the input slice at n=200 before calling.
func MemoryMergeCandidates(memories []*Memory, threshold float64) []MemoryCandidatePair {
	var pairs []MemoryCandidatePair
	for i := 0; i < len(memories); i++ {
		a := memories[i]
		if len(a.Embedding) == 0 {
			continue
		}
		for j := i + 1; j < len(memories); j++ {
			b := memories[j]
			if len(b.Embedding) == 0 {
				continue
			}
			if a.EmbedModel != b.EmbedModel {
				continue
			}
			sim := Cosine(a.Embedding, b.Embedding)
			if float64(sim) >= threshold {
				pairs = append(pairs, MemoryCandidatePair{A: a, B: b, Sim: sim})
			}
		}
	}
	return pairs
}

// FactMergeCandidates is the same as MemoryMergeCandidates but for facts.
func FactMergeCandidates(facts []*Fact, threshold float64) []FactCandidatePair {
	var pairs []FactCandidatePair
	for i := 0; i < len(facts); i++ {
		a := facts[i]
		if len(a.Embedding) == 0 {
			continue
		}
		for j := i + 1; j < len(facts); j++ {
			b := facts[j]
			if len(b.Embedding) == 0 {
				continue
			}
			if a.EmbedModel != b.EmbedModel {
				continue
			}
			sim := Cosine(a.Embedding, b.Embedding)
			if float64(sim) >= threshold {
				pairs = append(pairs, FactCandidatePair{A: a, B: b, Sim: sim})
			}
		}
	}
	return pairs
}

// CurateMemories performs the pairwise cosine deduplication pass for memories.
// It loads unreviewed memories with embeddings, caps at 200, finds merge
// candidates, and applies MergeMemoryDecision atomically per merge.
//
// Note: Phase 4d (mark_reviewed) is responsible for stamping reviewed_at on
// both winner and loser. The curator does NOT call MarkReviewed — that's the
// dream-pass closing step.
// TxRunner runs a function inside a database transaction. It is satisfied
// by *sqliteopen.DB.WithTx, but declared here as an interface to keep
// memory/ free of an upward import.
type TxRunner interface {
	WithTx(fn func(*sql.Tx) error) error
}

func CurateMemories(memoriesQ *MemoryQ, tx TxRunner, dreamID int64, threshold float64) error {
	memories, err := memoriesQ.UnreviewedWithEmbeddings()
	if err != nil {
		return fmt.Errorf("load unreviewed memories: %w", err)
	}

	// Cap at 200. UnreviewedWithEmbeddings returns ORDER BY created_at DESC,
	// so we keep the most recent 200 and defer older rows to the next cycle.
	if len(memories) > 200 {
		fmt.Fprintf(os.Stderr, "curator: unreviewed memories capped at 200 (total: %d); older rows deferred\n", len(memories))
		memories = memories[:200]
	}

	candidates := MemoryMergeCandidates(memories, threshold)

	// Track IDs archived in this pass to avoid double-processing a loser that
	// appears in multiple candidate pairs.
	archived := make(map[int64]bool)

	for _, pair := range candidates {
		if archived[pair.A.ID] || archived[pair.B.ID] {
			continue
		}
		winner, loser, outcome := MergeMemoryDecision(pair.A, pair.B, pair.Sim, threshold)
		switch outcome {
		case MergeOutcomeBelowThreshold:
			// Candidates are already filtered above threshold; guard for safety.
			continue
		case MergeOutcomeSkip:
			// Both pinned: log conflict and move on.
			if logErr := logMemoryConflict(tx, dreamID, pair.A, pair.B); logErr != nil {
				fmt.Fprintf(os.Stderr, "curator: log conflict: %v\n", logErr)
			}
		case MergeOutcomeArchive:
			if err := mergeMemory(tx, dreamID, winner, loser); err != nil {
				fmt.Fprintf(os.Stderr, "curator: merge memory %d→%d: %v\n", loser.ID, winner.ID, err)
				continue
			}
			archived[loser.ID] = true
		}
	}
	return nil
}

// CurateFacts performs the pairwise cosine deduplication pass for facts.
// Facts have no pinned_at field, so the both-pinned conflict path does not apply.
//
// Note: Phase 4d (mark_reviewed) is responsible for stamping reviewed_at on
// both winner and loser. The curator does NOT call MarkReviewed — that's the
// dream-pass closing step.
func CurateFacts(factsQ *FactQ, tx TxRunner, dreamID int64, threshold float64) error {
	facts, err := factsQ.UnreviewedWithEmbeddings()
	if err != nil {
		return fmt.Errorf("load unreviewed facts: %w", err)
	}

	if len(facts) > 200 {
		fmt.Fprintf(os.Stderr, "curator: unreviewed facts capped at 200 (total: %d); older rows deferred\n", len(facts))
		facts = facts[:200]
	}

	candidates := FactMergeCandidates(facts, threshold)
	archived := make(map[int64]bool)

	for _, pair := range candidates {
		if archived[pair.A.ID] || archived[pair.B.ID] {
			continue
		}
		winner, loser, outcome := MergeFactDecision(pair.A, pair.B, pair.Sim, threshold)
		if outcome != MergeOutcomeArchive {
			continue
		}
		if err := mergeFact(tx, dreamID, winner, loser); err != nil {
			fmt.Fprintf(os.Stderr, "curator: merge fact %d→%d: %v\n", loser.ID, winner.ID, err)
			continue
		}
		archived[loser.ID] = true
	}
	return nil
}

// mergeMemory archives the loser and writes an audit entry in a single transaction.
func mergeMemory(database TxRunner, dreamID int64, winner, loser *Memory) error {
	now := time.Now()
	nowStr := now.Format("2006-01-02 15:04:05")
	reversalSQL := fmt.Sprintf("UPDATE memories SET archived_at = NULL WHERE id = %d", loser.ID)
	targetIDs := fmt.Sprintf("[%d]", loser.ID)
	note := fmt.Sprintf("merged into memory %d (importance=%.2f); reversal: %s", winner.ID, winner.Importance, reversalSQL)

	return database.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE memories SET archived_at = ? WHERE id = ?`,
			nowStr, loser.ID,
		); err != nil {
			return fmt.Errorf("archive loser: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO dream_log(dream_id, action, target_table, target_ids, note, created_at)
			 VALUES(?, ?, ?, ?, ?, datetime('now'))`,
			dreamID, "merge_memories", "memories", targetIDs, note,
		); err != nil {
			return fmt.Errorf("dream_log: %w", err)
		}
		return nil
	})
}

// mergeFact archives the loser fact and writes an audit entry in a single transaction.
func mergeFact(database TxRunner, dreamID int64, winner, loser *Fact) error {
	now := time.Now()
	nowStr := now.Format("2006-01-02 15:04:05")
	reversalSQL := fmt.Sprintf("UPDATE facts SET archived_at = NULL WHERE id = %d", loser.ID)
	targetIDs := fmt.Sprintf("[%d]", loser.ID)
	note := fmt.Sprintf("merged into fact %d (importance=%.2f); reversal: %s", winner.ID, winner.Importance, reversalSQL)

	return database.WithTx(func(tx *sql.Tx) error {
		if _, err := tx.Exec(
			`UPDATE facts SET archived_at = ? WHERE id = ?`,
			nowStr, loser.ID,
		); err != nil {
			return fmt.Errorf("archive loser: %w", err)
		}
		if _, err := tx.Exec(
			`INSERT INTO dream_log(dream_id, action, target_table, target_ids, note, created_at)
			 VALUES(?, ?, ?, ?, ?, datetime('now'))`,
			dreamID, "merge_facts", "facts", targetIDs, note,
		); err != nil {
			return fmt.Errorf("dream_log: %w", err)
		}
		return nil
	})
}

// logMemoryConflict writes a dream_log entry for a both-pinned conflict pair.
// Neither memory is archived.
func logMemoryConflict(database TxRunner, dreamID int64, a, b *Memory) error {
	targetIDs := fmt.Sprintf("[%d,%d]", a.ID, b.ID)
	note := fmt.Sprintf("both memories are pinned; merge skipped. IDs: %d and %d", a.ID, b.ID)
	return database.WithTx(func(tx *sql.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO dream_log(dream_id, action, target_table, target_ids, note, created_at)
			 VALUES(?, ?, ?, ?, ?, datetime('now'))`,
			dreamID, "merge_conflict_both_pinned", "memories", targetIDs, note,
		)
		return err
	})
}
