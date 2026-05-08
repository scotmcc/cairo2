package agent

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/scotmcc/cairo2/internal/db"
	"github.com/scotmcc/cairo2/internal/llm"
)

// RunCurator performs the Phase 4b curation pass:
//  1. Load similarity threshold from config.
//  2. Scan unreviewed memories with embeddings, cap at 200, find merge candidates,
//     apply MergeMemoryDecision, execute merges atomically.
//  3. Scan unreviewed facts with embeddings, same flow.
//  4. Run hard-delete rotation for previously-archived memories and facts.
//
// Errors are logged to stderr and do not abort the dream. The caller should
// treat a non-nil return as informational.
func RunCurator(ctx context.Context, database *db.DB, dreamID int64, _ *llm.Client) error {
	threshStr := database.Config.GetWithDefault(db.KeyDreamCuratorSimilarityThreshold, "0.92")
	threshold, err := strconv.ParseFloat(threshStr, 64)
	if err != nil || threshold <= 0 || threshold > 1 {
		fmt.Fprintf(os.Stderr, "curator: invalid threshold %q, using 0.92\n", threshStr)
		threshold = 0.92
	}

	if err := db.CurateMemories(database, dreamID, threshold); err != nil {
		fmt.Fprintf(os.Stderr, "curator: memories: %v\n", err)
	}
	if err := db.CurateFacts(database, dreamID, threshold); err != nil {
		fmt.Fprintf(os.Stderr, "curator: facts: %v\n", err)
	}
	if _, err := database.Memories.DeleteArchived(); err != nil {
		fmt.Fprintf(os.Stderr, "curator: delete archived memories: %v\n", err)
	}
	if _, err := database.Facts.DeleteArchived(); err != nil {
		fmt.Fprintf(os.Stderr, "curator: delete archived facts: %v\n", err)
	}
	return nil
}
